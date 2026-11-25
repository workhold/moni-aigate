package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/tasks"
)

func TestTaskAbsenceAndForeignAccessLookTheSame(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{}
	respond := func(err error) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/00000000-0000-4000-8000-000000000001", nil)
		server.error(context, err)
		return recorder
	}
	missing := respond(tasks.ErrNotFound)
	foreign := respond(tasks.ErrForbidden)
	if missing.Code != http.StatusNotFound || foreign.Code != http.StatusNotFound || !bytes.Equal(missing.Body.Bytes(), foreign.Body.Bytes()) {
		t.Fatalf("task existence differs: missing=%d %s foreign=%d %s", missing.Code, missing.Body.String(), foreign.Code, foreign.Body.String())
	}
}
