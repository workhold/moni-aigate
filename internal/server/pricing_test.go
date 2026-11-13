package server

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/identity"
)

func TestPricingCompileBodyAcceptsOptionalBaseCASAndRejectsNestedDuplicates(t *testing.T) {
	model := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	version := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	raw := `{"operation_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","multiplier":"0.85","model_ids":["` + model + `"],"effective_at":"2026-12-01T00:00:00Z","expected_base_version_ids":{"` + model + `":"` + version + `"}}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(raw))
	s := &Server{}
	command, ok := s.compileGroupPriceBody(c)
	if !ok || command.Multiplier != "0.85" || command.ExpectedBaseVersionIDs[model] != version || len(command.ModelIDs) != 1 {
		t.Fatalf("compile body: %#v/%v", command, ok)
	}
	if _, ok = strictPricingRawObject(json.RawMessage(`{"x":"1","x":"2"}`)); ok {
		t.Fatal("accepted duplicate expected base key")
	}
}

func TestPricingStrictBodyAndUnicode(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"operation_id":"a","label":"商业客户"}`, true}, {`{"operation_id":"a","label":"\ud83d\ude00"}`, true}, {`{"operation_id":"a","label":"\\ud800"}`, true},
		{`{"operation_id":"a","label":"\ud800"}`, false}, {`{"operation_id":"a","label":"\udc00"}`, false}, {`{"operation_id":"a","Label":"x"}`, false}, {`{"operation_id":"a","label":"a","label":"b"}`, false}, {`{"operation_id":"a","label":"x","unknown":0}`, false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(tc.raw))
		obj, ok := pricingBody(c, "operation_id", "label")
		if ok != tc.valid {
			t.Errorf("%s: %v", tc.raw, ok)
		}
		if ok && len(obj) != 2 {
			t.Fatal("decoder lost body")
		}
	}
}
func TestPricingAdmissionIndependentBudgetsAndRelease(t *testing.T) {
	s := &Server{}
	newContext := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Set("session", &identity.Session{User: identity.User{ID: "actor"}})
		return c
	}
	done, ok := s.admitPricing(newContext(), "write", "")
	if !ok {
		t.Fatal("first write")
	}
	if _, ok = s.admitPricing(newContext(), "write", ""); ok {
		t.Fatal("same actor concurrent write admitted")
	}
	read, ok := s.admitPricing(newContext(), "read", "")
	if !ok {
		t.Fatal("write blocked independent read")
	}
	read()
	done()
	done()
	next, ok := s.admitPricing(newContext(), "write", "")
	if !ok {
		t.Fatal("permit not released")
	}
	next()
	if len(s.commerce.buckets) != 0 {
		t.Fatal("shared commerce limiter")
	}
}
func TestPricingErrorsNeverExposeRawFailure(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	s := &Server{}
	s.pricingError(c, errors.New("secret database failure"))
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), "pricing_busy") {
		t.Fatal(w.Code, w.Body.String())
	}
}
