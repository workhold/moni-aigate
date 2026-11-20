package catalog

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRoutePublishCacheTTL(t *testing.T) {
	var input RoutePublishInput
	if err := json.Unmarshal([]byte(`{"cache_ttl_seconds":60}`), &input); err != nil || input.CacheTTL() != 60 {
		t.Fatalf("valid cache TTL rejected: %v", err)
	}
	if (RoutePublishInput{}).CacheTTL() != 0 {
		t.Fatal("cache must default off")
	}
}

func TestRoutePublishBreakerDefaultsAndBounds(t *testing.T) {
	var defaults RoutePublishInput
	if err := json.Unmarshal([]byte(`{"expected_revision":0,"bindings":[]}`), &defaults); err != nil {
		t.Fatal(err)
	}
	breaker, err := defaults.BreakerConfig()
	if err != nil || breaker.FailureThreshold != 3 || breaker.OpenDuration != 30*time.Second || breaker.HalfOpenTokens != 1 {
		t.Fatalf("incorrect defaults: %+v %v", breaker, err)
	}
	var custom RoutePublishInput
	if err = json.Unmarshal([]byte(`{"failure_threshold":2,"open_ms":1000,"half_open_tokens":4}`), &custom); err != nil {
		t.Fatal(err)
	}
	breaker, err = custom.BreakerConfig()
	if err != nil || breaker.FailureThreshold != 2 || breaker.OpenDuration != time.Second || breaker.HalfOpenTokens != 4 {
		t.Fatalf("custom breaker lost: %+v %v", breaker, err)
	}
	for _, body := range []string{
		`{"failure_threshold":1}`, `{"failure_threshold":21}`,
		`{"open_ms":999}`, `{"open_ms":600001}`,
		`{"half_open_tokens":0}`, `{"half_open_tokens":5}`,
		`{"cache_ttl_seconds":59}`, `{"cache_ttl_seconds":86401}`,
		`{"failure_threshold":null}`, `{"open_ms":1.5}`, `{"cache_ttl_seconds":null}`,
	} {
		var input RoutePublishInput
		if err = json.Unmarshal([]byte(body), &input); err == nil {
			t.Fatalf("invalid breaker JSON accepted: %s", body)
		}
	}
}
