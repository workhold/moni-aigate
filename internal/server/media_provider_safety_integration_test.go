package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/tasks"
	"golang.org/x/net/websocket"
)

func TestInnerTestMediaIsUnavailableBeforeAnyBillingFact(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	f.server.Config.MediaProviderProfile = config.MediaProviderProfileInnerTest
	f.server.Config.MediaDeterministicMock = false
	f.server.ImageAdapter = nil
	f.server.ImageService = nil
	f.server.MediaAdapter = nil
	f.server.TaskAdapter = nil
	f.server.Realtime = nil
	f.server.RealtimeAdapter = nil

	cases := []struct {
		name, method, path, body, idempotency string
		contentType                           bool
	}{
		{name: "image", method: http.MethodPost, path: "/v1/images/generations", body: imageInputJSON(imageProfileVersion, "closed-image"), idempotency: "closed-image", contentType: true},
		{name: "tts", method: http.MethodPost, path: "/v1/audio/speech", body: `{"model":"mock-tts","input":"closed","voice":"alloy","response_format":"wav"}`, idempotency: "closed-tts", contentType: true},
		{name: "video", method: http.MethodPost, path: "/v1/tasks", body: `{}`, idempotency: "closed-video"},
		{name: "realtime", method: http.MethodGet, path: "/v1/realtime?model=retail-model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, f.http.URL+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+f.token)
			if tc.idempotency != "" {
				req.Header.Set("Idempotency-Key", tc.idempotency)
			}
			if tc.contentType {
				req.Header.Set("Content-Type", "application/json")
			}
			response, err := f.http.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusServiceUnavailable || !bytes.Contains(body, []byte(`"code":"provider_unavailable"`)) {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
		})
	}

	var requests, reservations, attempts, observations, settlements, tasksCount, sessions, turns int
	err := f.pool.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM media_requests),
 (SELECT count(*) FROM media_reservations),
 (SELECT count(*) FROM media_attempts),
 (SELECT count(*) FROM media_observations),
 (SELECT count(*) FROM media_settlements),
 (SELECT count(*) FROM media_tasks),
 (SELECT count(*) FROM realtime_sessions),
 (SELECT count(*) FROM realtime_turns)`).Scan(&requests, &reservations, &attempts, &observations, &settlements, &tasksCount, &sessions, &turns)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 0 || reservations != 0 || attempts != 0 || observations != 0 || settlements != 0 || tasksCount != 0 || sessions != 0 || turns != 0 {
		t.Fatalf("disabled media entered current facts: requests=%d reservations=%d attempts=%d observations=%d settlements=%d tasks=%d sessions=%d turns=%d", requests, reservations, attempts, observations, settlements, tasksCount, sessions, turns)
	}
}

func assertMockReleasedSettlement(t *testing.T, f *gatewayFixture, operation string) {
	t.Helper()
	var status, usageState, supplierStatus string
	var charged, supplierAmount int64
	err := f.pool.QueryRow(context.Background(), `SELECT s.status,o.usage_state,s.supplier_cost_status,s.charged_amount,coalesce(s.supplier_cost_amount,0)
FROM media_settlements s
JOIN media_requests q ON q.tenant_id=s.tenant_id AND q.id=s.request_id
JOIN media_observations o ON o.tenant_id=s.tenant_id AND o.request_id=q.id
WHERE q.tenant_id=$1 AND q.operation=$2`, f.tenant, operation).Scan(&status, &usageState, &supplierStatus, &charged, &supplierAmount)
	if err != nil {
		t.Fatal(err)
	}
	if status != "released" || usageState != "missing" || supplierStatus != "unknown" || charged != 0 || supplierAmount != 0 {
		t.Fatalf("mock %s created charge-bearing facts: status=%s usage=%s supplier=%s charged=%d supplier=%d", operation, status, usageState, supplierStatus, charged, supplierAmount)
	}
}

func walletTotals(t *testing.T, f *gatewayFixture) (held, spent int64) {
	t.Helper()
	if err := f.pool.QueryRow(context.Background(), `SELECT held,spent FROM api_keys WHERE tenant_id=$1 AND id=$2`, f.tenant, f.key).Scan(&held, &spent); err != nil {
		t.Fatal(err)
	}
	return held, spent
}

func TestExplicitMockMediaMarksResultsAndDoesNotSpendWallet(t *testing.T) {
	t.Run("image", func(t *testing.T) {
		f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
		beforeHeld, beforeSpent := walletTotals(t, f)
		req, err := http.NewRequest(http.MethodPost, f.http.URL+"/v1/images/generations", strings.NewReader(imageInputJSON(imageProfileVersion, "mock-image")))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "mock-image-safety")
		response, err := f.http.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		var value map[string]any
		if response.StatusCode != http.StatusOK || json.Unmarshal(body, &value) != nil || value["provider_mode"] != "mock" {
			t.Fatalf("mock image status=%d body=%s", response.StatusCode, body)
		}
		assertMockReleasedSettlement(t, f, "image_generation")
		afterHeld, afterSpent := walletTotals(t, f)
		if beforeHeld != afterHeld || beforeSpent != afterSpent {
			t.Fatalf("mock image changed wallet held/spent: before=%d/%d after=%d/%d", beforeHeld, beforeSpent, afterHeld, afterSpent)
		}
	})

	t.Run("tts", func(t *testing.T) {
		f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
		if _, err := f.pool.Exec(context.Background(), `UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE tenant_id=$1 AND id=$3`, f.tenant, "mock-tts", f.key); err != nil {
			t.Fatal(err)
		}
		beforeHeld, beforeSpent := walletTotals(t, f)
		req, err := http.NewRequest(http.MethodPost, f.http.URL+"/v1/audio/speech", strings.NewReader(`{"model":"mock-tts","input":"mock speech","voice":"alloy","response_format":"wav"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "mock-tts-safety")
		response, err := f.http.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || response.Header.Get("X-Moni-Provider-Mode") != "mock" || len(body) < 4 || !bytes.Equal(body[:4], []byte("RIFF")) {
			t.Fatalf("mock TTS status=%d mode=%q body-prefix=%q", response.StatusCode, response.Header.Get("X-Moni-Provider-Mode"), body)
		}
		assertMockReleasedSettlement(t, f, "tts")
		afterHeld, afterSpent := walletTotals(t, f)
		if beforeHeld != afterHeld || beforeSpent != afterSpent {
			t.Fatalf("mock TTS changed wallet held/spent: before=%d/%d after=%d/%d", beforeHeld, beforeSpent, afterHeld, afterSpent)
		}
	})

	t.Run("video", func(t *testing.T) {
		f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
		fixture := createVideoTask(t, f, "mock-video-safety")
		beforeHeld, beforeSpent := walletTotals(t, f)
		service, err := f.server.taskService(taskPrincipal(t, f))
		if err != nil {
			t.Fatal(err)
		}
		taskID, err := tasks.ParseID(fixture.taskID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = service.Submit(context.Background(), taskID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if _, err = service.Observe(context.Background(), taskID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		assertMockReleasedSettlement(t, f, "video_generation")
		afterHeld, afterSpent := walletTotals(t, f)
		if beforeHeld != 1 || afterHeld != 0 || beforeSpent != afterSpent {
			t.Fatalf("mock video did not release its reservation without spending: before=%d/%d after=%d/%d", beforeHeld, beforeSpent, afterHeld, afterSpent)
		}
	})

	t.Run("realtime", func(t *testing.T) {
		f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
		beforeHeld, beforeSpent := walletTotals(t, f)
		wsURL := "ws" + strings.TrimPrefix(f.http.URL, "http") + "/v1/realtime?model=retail-model"
		wsConfig, err := websocket.NewConfig(wsURL, "http://console.test")
		if err != nil {
			t.Fatal(err)
		}
		wsConfig.Header = http.Header{"Authorization": []string{"Bearer " + f.token}}
		conn, err := websocket.DialConfig(wsConfig)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		created := readRealtimeEvent(t, conn)
		if created.Type != "session.created" || eventDataString(t, created, "provider_mode") != "mock" {
			t.Fatalf("created event=%+v", created)
		}
		sendRealtimeEvent(t, conn, `{"event_id":"22222222-2222-4222-8222-222222222201","type":"input.text","data":{"text":"mock realtime"}}`)
		if readRealtimeEvent(t, conn).Type != "event.accepted" {
			t.Fatal("realtime input was not accepted")
		}
		sendRealtimeEvent(t, conn, `{"event_id":"22222222-2222-4222-8222-222222222202","type":"response.create","data":{}}`)
		if readRealtimeEvent(t, conn).Type != "event.accepted" || readRealtimeEvent(t, conn).Type != "response.created" || readRealtimeEvent(t, conn).Type != "response.delta" || readRealtimeEvent(t, conn).Type != "response.done" {
			t.Fatal("realtime mock turn did not complete")
		}
		assertMockReleasedSettlement(t, f, "realtime_turn")
		afterHeld, afterSpent := walletTotals(t, f)
		if beforeHeld != afterHeld || beforeSpent != afterSpent {
			t.Fatalf("mock realtime changed wallet held/spent: before=%d/%d after=%d/%d", beforeHeld, beforeSpent, afterHeld, afterSpent)
		}
	})
}
