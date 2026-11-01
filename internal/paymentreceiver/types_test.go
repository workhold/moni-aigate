package paymentreceiver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReceiptHistoryRejectsBrokenCanonicalBindings(t *testing.T) {
	d := ReceiptData{ReceiptID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", EventID: "evt:synthetic", Status: "received", Environment: "sandbox", ReceivedAt: "2026-11-01T00:00:00.000000Z"}
	raw, e := marshalReceipt(d)
	if e != nil {
		t.Fatal(e)
	}
	if got, e := parseReceipt(raw, d.EventID); e != nil || got != d {
		t.Fatal("valid immutable receipt rejected")
	}
	for _, bad := range []string{
		strings.Replace(string(raw), `"status":"received"`, `"status":"received","status":"received"`, 1),
		strings.Replace(string(raw), `"status"`, `"Status"`, 1),
		strings.Replace(string(raw), `"environment":"sandbox"`, `"environment":"live"`, 1),
		strings.Replace(string(raw), `"receipt_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"`, `"receipt_id":null`, 1),
		strings.Replace(string(raw), "2026-11-01T00:00:00.000000Z", "0000-11-01T00:00:00.000000Z", 1),
		strings.Replace(string(raw), "2026-11-01T00:00:00.000000Z", "2026-02-30T00:00:00.000000Z", 1),
		string(raw) + ` {}`, " " + string(raw),
	} {
		if _, e := parseReceipt([]byte(bad), d.EventID); e == nil {
			t.Fatal("invalid durable receipt accepted")
		}
	}
	if _, e := parseReceipt(raw, "evt:other"); e == nil {
		t.Fatal("wrong event receipt accepted")
	}
	if _, e := json.Marshal(RuntimeConfig{}); e != nil {
		t.Fatal(e)
	}
}
