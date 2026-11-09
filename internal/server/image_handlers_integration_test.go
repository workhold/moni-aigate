package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
)

func TestImageGenerationHTTPUsesCurrentMediaWalletFacts(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	body := imageInputJSON(imageProfileVersion, "billing image")
	req, err := http.NewRequest(http.MethodPost, f.http.URL+"/v1/images/generations", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "image-wallet-1")
	response, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, raw)
	}
	var value map[string]any
	if err = json.Unmarshal(raw, &value); err != nil || value["status"] != "completed" {
		t.Fatalf("unexpected image response=%s err=%v", raw, err)
	}
	var reservations, settlements, charged int
	if err = f.pool.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM media_reservations),
 (SELECT count(*) FROM media_settlements),
 (SELECT coalesce(sum(charged_amount),0) FROM media_settlements)`).Scan(&reservations, &settlements, &charged); err != nil {
		t.Fatal(err)
	}
	if reservations != 1 || settlements != 1 || charged != 1 {
		t.Fatalf("media financial facts reservations=%d settlements=%d charged=%d", reservations, settlements, charged)
	}
}

func TestSpeechHTTPUsesCurrentMediaWalletFacts(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	if _, err := f.pool.Exec(context.Background(), `UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE tenant_id=$1 AND id=$3`, f.tenant, "mock-tts", f.key); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"mock-tts","input":"hello media","voice":"alloy","response_format":"wav"}`
	req, err := http.NewRequest(http.MethodPost, f.http.URL+"/v1/audio/speech", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "tts-wallet-1")
	response, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || len(raw) < 4 || !bytes.Equal(raw[:4], []byte("RIFF")) {
		t.Fatalf("status=%d content-type=%q body-prefix=%q", response.StatusCode, response.Header.Get("Content-Type"), raw)
	}
	var reservations, settlements, charged int
	if err = f.pool.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM media_reservations),
 (SELECT count(*) FROM media_settlements),
 (SELECT coalesce(sum(charged_amount),0) FROM media_settlements)`).Scan(&reservations, &settlements, &charged); err != nil {
		t.Fatal(err)
	}
	if reservations != 1 || settlements != 1 || charged != len([]rune("hello media")) {
		t.Fatalf("media speech financial facts reservations=%d settlements=%d charged=%d", reservations, settlements, charged)
	}
}

func TestMediaResultsSurviveProcessCacheReset(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	body := imageInputJSON(imageProfileVersion, "persistent image")
	request, err := http.NewRequest(http.MethodPost, f.http.URL+"/v1/images/generations", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "persistent-image-1")
	response, err := f.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("image status=%d body=%s", response.StatusCode, raw)
	}
	var image map[string]any
	if err = json.Unmarshal(raw, &image); err != nil {
		t.Fatal(err)
	}
	requestID, ok := image["id"].(string)
	if !ok || requestID == "" {
		t.Fatalf("missing image id in %s", raw)
	}
	f.server.imageMu.Lock()
	f.server.imageByID = map[string]*imageRequestRecord{}
	f.server.imageByIdempotency = map[string]*imageRequestRecord{}
	f.server.imageMu.Unlock()
	get, err := http.NewRequest(http.MethodGet, f.http.URL+"/v1/images/generations/"+requestID, nil)
	if err != nil {
		t.Fatal(err)
	}
	get.Header.Set("Authorization", "Bearer "+f.token)
	response, err = f.http.Client().Do(get)
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(persisted) != string(raw) {
		t.Fatalf("persisted image status=%d body=%s", response.StatusCode, persisted)
	}
	replay, err := http.NewRequest(http.MethodPost, f.http.URL+"/v1/images/generations", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	replay.Header.Set("Authorization", "Bearer "+f.token)
	replay.Header.Set("Content-Type", "application/json")
	replay.Header.Set("Idempotency-Key", "persistent-image-1")
	replayResponse, err := f.http.Client().Do(replay)
	if err != nil {
		t.Fatal(err)
	}
	replayed, _ := io.ReadAll(replayResponse.Body)
	replayResponse.Body.Close()
	if replayResponse.StatusCode != http.StatusOK || string(replayed) != string(raw) {
		t.Fatalf("persisted image POST replay status=%d body=%s", replayResponse.StatusCode, replayed)
	}
	var imageSettlements int
	if err = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM media_settlements`).Scan(&imageSettlements); err != nil {
		t.Fatal(err)
	}
	if imageSettlements != 1 {
		t.Fatalf("persisted image replay created duplicate settlement: %d", imageSettlements)
	}

	if _, err := f.pool.Exec(context.Background(), `UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE tenant_id=$1 AND id=$3`, f.tenant, "mock-tts", f.key); err != nil {
		t.Fatal(err)
	}
	speech := `{"model":"mock-tts","input":"persistent speech","voice":"alloy","response_format":"wav"}`
	postSpeech := func() (*http.Response, []byte) {
		req, reqErr := http.NewRequest(http.MethodPost, f.http.URL+"/v1/audio/speech", bytes.NewBufferString(speech))
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "persistent-tts-1")
		res, reqErr := f.http.Client().Do(req)
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		data, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		return res, data
	}
	first, firstBody := postSpeech()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("speech status=%d body=%s", first.StatusCode, firstBody)
	}
	f.server.MediaIdempotency = sync.Map{}
	second, secondBody := postSpeech()
	if second.StatusCode != http.StatusOK || string(secondBody) != string(firstBody) {
		t.Fatalf("persisted speech status=%d body prefix=%q", second.StatusCode, secondBody[:minInt(len(secondBody), 4)])
	}
	var settlements int
	if err = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM media_settlements`).Scan(&settlements); err != nil {
		t.Fatal(err)
	}
	if settlements != 2 {
		t.Fatalf("cache reset created duplicate settlement: %d", settlements)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
