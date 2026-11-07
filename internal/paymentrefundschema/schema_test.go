package paymentrefundschema

import (
	"strings"
	"testing"
)

func TestC1MSQLIsStableAndIsolated(t *testing.T) {
	sql, err := SQL()
	if err != nil || sql == "" {
		t.Fatal("missing C1-M SQL")
	}
	if !strings.Contains(sql, "CREATE TABLE mock_refund_commands") ||
		!strings.Contains(sql, "CREATE TABLE mock_refund_facts") ||
		!strings.Contains(sql, "CREATE TABLE mock_refund_queries") {
		t.Fatal("incomplete C1-M schema")
	}
	if strings.Contains(sql, "mock_v1") || strings.Contains(sql, "ALTER TABLE mock_commands") {
		t.Fatal("C1-M extension must not mutate mock v1")
	}
	digest, err := Digest()
	if err != nil || len(digest) != 64 {
		t.Fatalf("invalid digest: %v", err)
	}
	if Version != 1 {
		t.Fatalf("unexpected extension version %d", Version)
	}
}
