package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

func newImageHTTPTestServer(t *testing.T, response provider.ImageResponse) (*Server, *provider.MockMediaAdapter) {
	t.Helper()
	adapter := &provider.MockMediaAdapter{ImageResponse: response}
	profile, service := newImageRuntime(adapter)
	return &Server{
		ImageAdapter:       adapter,
		ImageProfile:       profile,
		ImageService:       service,
		imageByIdempotency: make(map[string]*imageRequestRecord),
		imageByID:          make(map[string]*imageRequestRecord),
	}, adapter
}

func imageTestRouter(s *Server, p identity.Principal) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withPrincipal := func(handler gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Set("principal", p)
			c.Set("request_id", uuid.NewString())
			handler(c)
		}
	}
	r.POST("/v1/images/generations", withPrincipal(s.imageGeneration))
	r.GET("/v1/images/generations/:id", withPrincipal(s.imageGenerationResult))
	return r
}

func imagePNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	value := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			value.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 31, A: 255})
		}
	}
	if err := png.Encode(&out, value); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func imageInputJSON(profileVersion, prompt string) string {
	return `{"version":"1","profile_version":"` + profileVersion + `","variant_key":"default","prompt":"` + prompt + `","size":"256x256","format":"png","n":"1"}`
}

func imageRequest(method, path, body, key string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return req
}

func imageResponseJSON(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("response=%s: %v", response.Body.String(), err)
	}
	return value
}

func imageErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	value := imageResponseJSON(t, response)
	errValue, ok := value["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no error: %s", response.Body.String())
	}
	code, _ := errValue["code"].(string)
	return code
}

func TestImageRoutesAreRegistered(t *testing.T) {
	s, _ := newImageHTTPTestServer(t, provider.ImageResponse{})
	engine := s.Engine()
	want := map[string]bool{
		"POST /v1/images/generations":    false,
		"GET /v1/images/generations/:id": false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("missing route %s", route)
		}
	}
}

func TestImageGenerationStrictInputAndInlineResult(t *testing.T) {
	s, adapter := newImageHTTPTestServer(t, provider.ImageResponse{Sent: true, MediaType: "image/png", Data: imagePNG(t)})
	p := identity.Principal{TenantID: "tenant-a", UserID: "user-a", KeyID: "key-a"}
	router := imageTestRouter(s, p)
	raw := imageInputJSON(imageProfileVersion, "a red kite")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, imageRequest(http.MethodPost, "/v1/images/generations", raw, "image-1"))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	value := imageResponseJSON(t, response)
	if value["object"] != imageResultType || value["status"] != "completed" || value["profile_version"] != imageProfileVersion {
		t.Fatalf("unexpected response=%s", response.Body.String())
	}
	data, ok := value["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("unexpected data=%#v", value["data"])
	}
	item := data[0].(map[string]any)
	if item["mime_type"] != "image/png" || item["width"] != float64(256) || item["height"] != float64(256) || item["b64_json"] == "" {
		t.Fatalf("unexpected result item=%#v", item)
	}
	if len(adapter.ImageRequests()) != 1 {
		t.Fatalf("adapter calls=%d", len(adapter.ImageRequests()))
	}
	requests := adapter.ImageRequests()
	if requests[0].Model != imageMockModel || requests[0].Prompt != "a red kite" || requests[0].N != 1 || requests[0].Size != "256x256" || requests[0].ResponseFormat != "png" {
		t.Fatalf("unexpected adapter request=%+v", requests[0])
	}
	requestID, _ := value["id"].(string)
	get := httptest.NewRecorder()
	router.ServeHTTP(get, imageRequest(http.MethodGet, "/v1/images/generations/"+requestID, "", "image-1"))
	if get.Code != http.StatusOK || get.Body.String() != response.Body.String() {
		t.Fatalf("result read status=%d body=%s", get.Code, get.Body.String())
	}
}

func TestImageGenerationRejectsStrictInputBeforeAdapter(t *testing.T) {
	s, adapter := newImageHTTPTestServer(t, provider.ImageResponse{Sent: true, MediaType: "image/png", Data: imagePNG(t)})
	p := identity.Principal{TenantID: "tenant-a", UserID: "user-a", KeyID: "key-a"}
	router := imageTestRouter(s, p)
	profile := imageProfileVersion
	tests := []struct {
		name string
		body string
		key  string
		want int
	}{
		{"unknown_field", strings.TrimSuffix(imageInputJSON(profile, "x"), "}") + `,"extra":"x"}`, "strict-1", http.StatusBadRequest},
		{"duplicate_field", strings.Replace(imageInputJSON(profile, "x"), `,"n":"1"}`, `,"n":"1","n":"1"}`, 1), "strict-2", http.StatusBadRequest},
		{"wrong_profile", imageInputJSON("22222222-2222-4222-8222-222222222222", "x"), "strict-3", http.StatusBadRequest},
		{"missing_key", imageInputJSON(profile, "x"), "", http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			req := imageRequest(http.MethodPost, "/v1/images/generations", test.body, test.key)
			if test.name == "missing_key" {
				req.Header.Del("Idempotency-Key")
			}
			router.ServeHTTP(response, req)
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if len(adapter.ImageRequests()) != 0 {
		t.Fatalf("invalid requests reached adapter: %d", len(adapter.ImageRequests()))
	}
}

func TestImageGenerationIdempotencyDoesNotRegenerate(t *testing.T) {
	s, adapter := newImageHTTPTestServer(t, provider.ImageResponse{Sent: true, MediaType: "image/png", Data: imagePNG(t)})
	p := identity.Principal{TenantID: "tenant-a", UserID: "user-a", KeyID: "key-a"}
	router := imageTestRouter(s, p)
	raw := imageInputJSON(imageProfileVersion, "same")
	first := httptest.NewRecorder()
	router.ServeHTTP(first, imageRequest(http.MethodPost, "/v1/images/generations", raw, "same-key"))
	second := httptest.NewRecorder()
	router.ServeHTTP(second, imageRequest(http.MethodPost, "/v1/images/generations", raw, "same-key"))
	if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Body.String() != second.Body.String() {
		t.Fatalf("replay first=%d second=%d first_body=%s second_body=%s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if len(adapter.ImageRequests()) != 1 {
		t.Fatalf("duplicate regenerated image, calls=%d", len(adapter.ImageRequests()))
	}
	conflict := httptest.NewRecorder()
	router.ServeHTTP(conflict, imageRequest(http.MethodPost, "/v1/images/generations", imageInputJSON(imageProfileVersion, "different"), "same-key"))
	if conflict.Code != http.StatusConflict || imageErrorCode(t, conflict) != "idempotency_conflict" || len(adapter.ImageRequests()) != 1 {
		t.Fatalf("idempotency conflict status=%d body=%s calls=%d", conflict.Code, conflict.Body.String(), len(adapter.ImageRequests()))
	}
}

func TestImageGenerationResultIsBoundToOriginalCredential(t *testing.T) {
	s, _ := newImageHTTPTestServer(t, provider.ImageResponse{Sent: true, MediaType: "image/png", Data: imagePNG(t)})
	owner := identity.Principal{TenantID: "tenant-a", UserID: "user-a", KeyID: "key-a"}
	router := imageTestRouter(s, owner)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, imageRequest(http.MethodPost, "/v1/images/generations", imageInputJSON(imageProfileVersion, "private"), "private-key"))
	id := imageResponseJSON(t, response)["id"].(string)
	for name, principal := range map[string]identity.Principal{
		"other key":    {TenantID: "tenant-a", UserID: "user-a", KeyID: "key-b"},
		"other user":   {TenantID: "tenant-a", UserID: "user-b", KeyID: "key-a"},
		"other tenant": {TenantID: "tenant-b", UserID: "user-a", KeyID: "key-a"},
	} {
		t.Run(name, func(t *testing.T) {
			other := imageTestRouter(s, principal)
			denied := httptest.NewRecorder()
			other.ServeHTTP(denied, imageRequest(http.MethodGet, "/v1/images/generations/"+id, "", "other-key"))
			if denied.Code != http.StatusNotFound || imageErrorCode(t, denied) != "not_found" {
				t.Fatalf("cross-principal result access status=%d body=%s", denied.Code, denied.Body.String())
			}
		})
	}
}

func TestImageGenerationNonDispatchFailureIsTerminalAndDoesNotBecomeUnknown(t *testing.T) {
	s, adapter := newImageHTTPTestServer(t, provider.ImageResponse{})
	p := identity.Principal{TenantID: "tenant-a", UserID: "user-a", KeyID: "key-a"}
	router := imageTestRouter(s, p)
	raw := imageInputJSON(imageProfileVersion, "rejected before dispatch")
	first := httptest.NewRecorder()
	router.ServeHTTP(first, imageRequest(http.MethodPost, "/v1/images/generations", raw, "not-sent"))
	second := httptest.NewRecorder()
	router.ServeHTTP(second, imageRequest(http.MethodPost, "/v1/images/generations", raw, "not-sent"))
	if first.Code != http.StatusBadGateway || second.Code != http.StatusBadGateway || imageErrorCode(t, first) != "media_provider_error" || imageErrorCode(t, second) != "media_provider_error" {
		t.Fatalf("non-dispatch failure status first=%d second=%d first_body=%s second_body=%s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if len(adapter.ImageRequests()) != 1 {
		t.Fatalf("non-dispatch failure regenerated, calls=%d", len(adapter.ImageRequests()))
	}
}

func TestImageGenerationUnknownConsumptionIsTerminalAndNeverRegenerates(t *testing.T) {
	s, adapter := newImageHTTPTestServer(t, provider.ImageResponse{})
	var calls atomic.Int32
	adapter.GenerateImageFunc = func(_ context.Context, _ provider.ImageRequest) (provider.ImageResponse, error) {
		calls.Add(1)
		return provider.ImageResponse{Sent: true}, provider.NewMediaError("provider_timeout", http.StatusGatewayTimeout, true, true)
	}
	p := identity.Principal{TenantID: "tenant-a", UserID: "user-a", KeyID: "key-a"}
	router := imageTestRouter(s, p)
	raw := imageInputJSON(imageProfileVersion, "may be consumed")
	first := httptest.NewRecorder()
	router.ServeHTTP(first, imageRequest(http.MethodPost, "/v1/images/generations", raw, "unknown-key"))
	second := httptest.NewRecorder()
	router.ServeHTTP(second, imageRequest(http.MethodPost, "/v1/images/generations", raw, "unknown-key"))
	if first.Code != http.StatusBadGateway || second.Code != http.StatusBadGateway || imageErrorCode(t, first) != "media_consumption_unknown" || imageErrorCode(t, second) != "media_consumption_unknown" {
		t.Fatalf("unknown status first=%d second=%d first_body=%s second_body=%s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("unknown request regenerated, calls=%d", calls.Load())
	}
}

func TestImageGenerationInvalidResultAfterDispatchIsUnknown(t *testing.T) {
	s, _ := newImageHTTPTestServer(t, provider.ImageResponse{Sent: true, MediaType: "image/png", Data: []byte("not-a-png")})
	p := identity.Principal{TenantID: "tenant-a", UserID: "user-a", KeyID: "key-a"}
	router := imageTestRouter(s, p)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, imageRequest(http.MethodPost, "/v1/images/generations", imageInputJSON(imageProfileVersion, "bad result"), "bad-result"))
	if response.Code != http.StatusBadGateway || imageErrorCode(t, response) != "media_consumption_unknown" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
