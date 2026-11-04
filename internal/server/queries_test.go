package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestQueryTimestampJSONIsUTCWithoutLosingPrecision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset int
		nanos  int
	}{
		{"local_east_microseconds", 8 * 60 * 60, 123456000},
		{"local_west_nanoseconds", -5 * 60 * 60, 123456789},
		{"utc_whole_seconds", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := time.Date(2026, 11, 4, 14, 5, 6, tc.nanos, time.FixedZone(tc.name, tc.offset))
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.JSON(200, gin.H{"created_at": queryValue("created_at", at), "amount": queryValue("amount", int64(9007199254740993)), "expires_at": queryValue("expires_at", nil), "name": queryValue("name", "unchanged")})
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			want := at.UTC().Format(time.RFC3339Nano)
			if w.Code != 200 || body["created_at"] != want || !strings.HasSuffix(want, "Z") || body["amount"] != "9007199254740993" || body["expires_at"] != nil || body["name"] != "unchanged" {
				t.Fatal("query JSON changed its instant, precision, amount or nullable fields", w.Body.String())
			}
		})
	}
}

func TestQueryTimestampCursorPreservesInstantAndPriorOffset(t *testing.T) {
	at := time.Date(2026, 11, 4, 14, 5, 6, 123456000, time.FixedZone("local", 8*60*60))
	normalized, ok := queryValue("created_at", at).(time.Time)
	if !ok || normalized.Location() != time.UTC || !normalized.Equal(at) {
		t.Fatal("query timestamps must remain time.Time for pagination")
	}
	for _, createdAt := range []time.Time{at, normalized} {
		raw, err := json.Marshal(cursor{CreatedAt: createdAt, ID: "same-record"})
		if err != nil {
			t.Fatal(err)
		}
		token := base64.RawURLEncoding.EncodeToString(raw)
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		var restored cursor
		if err != nil || json.Unmarshal(decoded, &restored) != nil || restored.ID != "same-record" || !restored.CreatedAt.Equal(at) {
			t.Fatal("current UTC or previously issued offset cursor changed pagination instant")
		}
	}
}
