package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
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
