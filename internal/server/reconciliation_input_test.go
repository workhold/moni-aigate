package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBindReconciliationResolve(t *testing.T) {
	gin.SetMode(gin.TestMode)
	valid := `{"action":"settle","reason":"verified","evidence_ref":"invoice:1","retail_quantities":{"schema_id":"10000000-0000-4000-8000-000000000002","version":"1","side":"retail","quantities":[]}}`
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{name: "complete shape", raw: valid, want: true},
		{name: "old scalar fields", raw: `{"action":"settle","reason":"verified","evidence_ref":"invoice:1","input_tokens":10,"output_tokens":5}`, want: false},
		{name: "unknown field", raw: strings.Replace(valid, `,"evidence_ref"`, `,"unexpected":true,"evidence_ref"`, 1), want: false},
		{name: "duplicate outer field", raw: strings.Replace(valid, `,"evidence_ref"`, `,"action":"release","evidence_ref"`, 1), want: false},
		{name: "duplicate nested field", raw: strings.Replace(valid, `"quantities":[]`, `"quantities":[],"quantities":[]`, 1), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(tc.raw))
			var got reconciliationResolveRequest
			if ok := bindReconciliationResolve(c, &got); ok != tc.want {
				t.Fatalf("bind=%v want %v", ok, tc.want)
			}
		})
	}
}
