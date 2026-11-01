package paymentrefundproto_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	v1 "github.com/moni-ai/moni-aigate/internal/paymentproto"
	p "github.com/moni-ai/moni-aigate/internal/paymentrefundproto"
)

// This checked-in artifact is produced by a separately authored Python program
// with explicit ordered JSON and frames. No Go implementation function supplies
// the expected canonical, digest, signing-input bytes, or signature.
type goldenHeaders struct {
	KeyID        string `json:"key_id"`
	DeliveryID   string `json:"delivery_id"`
	DeliveryTime string `json:"delivery_time"`
	Signature    string `json:"signature"`
}

func (h goldenHeaders) v2() p.SignedHeaders {
	return p.SignedHeaders{KeyID: h.KeyID, DeliveryID: h.DeliveryID, DeliveryTime: h.DeliveryTime, Signature: h.Signature}
}
func (h goldenHeaders) v1() v1.SignedHeaders {
	return v1.SignedHeaders{KeyID: h.KeyID, DeliveryID: h.DeliveryID, DeliveryTime: h.DeliveryTime, Signature: h.Signature}
}

type goldenValue struct {
	Canonical    string        `json:"canonical"`
	UTF8Bytes    int           `json:"utf8_bytes"`
	RawSHA256    string        `json:"raw_sha256"`
	DigestDomain string        `json:"digest_domain"`
	FramedDigest string        `json:"framed_digest"`
	Headers      goldenHeaders `json:"headers"`
}
type goldenEnvelope struct {
	Name                  string        `json:"name"`
	Kind                  string        `json:"kind"`
	CommandVector         *string       `json:"command_vector"`
	QueryVector           *string       `json:"query_vector"`
	Canonical             string        `json:"canonical"`
	RawBody               string        `json:"raw_body"`
	RawUTF8Bytes          int           `json:"raw_utf8_bytes"`
	RawSHA256             string        `json:"raw_sha256"`
	ContextDigest         string        `json:"context_digest"`
	SignatureDomain       string        `json:"signature_domain"`
	FramedSigningInputHex string        `json:"framed_signing_input_hex"`
	Headers               goldenHeaders `json:"headers"`
	ExpectedFactDigest    *string       `json:"expected_fact_digest"`
}
type goldenArtifact struct {
	Version       int  `json:"artifact_schema_version"`
	SyntheticOnly bool `json:"synthetic_only"`
	Keys          struct {
		V1ASCII   string `json:"v1_ascii"`
		V2ASCII   string `json:"v2_ascii"`
		V1KeyID   string `json:"v1_key_id"`
		V2KeyID   string `json:"v2_key_id"`
		NotBefore string `json:"not_before"`
		NotAfter  string `json:"not_after"`
	} `json:"keys"`
	Environment struct {
		ID         string `json:"id"`
		MerchantID string `json:"merchant_id"`
	} `json:"environment"`
	ReceivedAt    string                     `json:"received_at"`
	V1Bridge      map[string]json.RawMessage `json:"v1_bridge"`
	Commands      map[string]goldenValue     `json:"commands"`
	Queries       map[string]goldenValue     `json:"queries"`
	Facts         map[string]goldenValue     `json:"facts"`
	SignedVectors []goldenEnvelope           `json:"signed_vectors"`
}

func golden(t *testing.T) goldenArtifact {
	t.Helper()
	raw, e := os.ReadFile("../../docs/verification/payment-refund-v2-golden.json")
	if e != nil {
		t.Fatal(e)
	}
	var a goldenArtifact
	if json.Unmarshal(raw, &a) != nil || a.Version != 1 || !a.SyntheticOnly || len(a.SignedVectors) != 9 {
		t.Fatal("invalid independent golden artifact")
	}
	return a
}
func goldenTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, e := time.Parse("2006-01-02T15:04:05.000000Z", s)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func bridgeVector(t *testing.T, a goldenArtifact, name string) goldenValue {
	t.Helper()
	var v goldenValue
	if json.Unmarshal(a.V1Bridge[name], &v) != nil || v.Canonical == "" {
		t.Fatal("missing bridge vector")
	}
	return v
}
func assertGoldenValue(t *testing.T, v goldenValue, b []byte, d string) {
	t.Helper()
	sum := sha256.Sum256(b)
	if string(b) != v.Canonical || len(b) != v.UTF8Bytes || hex.EncodeToString(sum[:]) != v.RawSHA256 {
		t.Fatal("Python canonical/raw SHA vector differs")
	}
	if v.FramedDigest != "" && (d != v.FramedDigest || independentDigest(v.DigestDomain, b) != v.FramedDigest) {
		t.Fatal("Python framed digest differs")
	}
}

func TestIndependentPythonBridgeDigestAndSignatureVectors(t *testing.T) {
	a := golden(t)
	before, after := goldenTime(t, a.Keys.NotBefore), goldenTime(t, a.Keys.NotAfter)
	received := goldenTime(t, a.ReceivedAt)
	e := p.Environment{ID: a.Environment.ID, MerchantID: a.Environment.MerchantID}
	oldEnv := v1.Environment{ID: e.ID, MerchantID: e.MerchantID, PayeeLabel: "synthetic payee"}
	oldKeys, err := v1.NewKeyring(a.Keys.V1KeyID, []v1.KeySpec{{ID: a.Keys.V1KeyID, Key: []byte(a.Keys.V1ASCII), NotBefore: before, NotAfter: after}})
	if err != nil {
		t.Fatal(err)
	}
	nv := bridgeVector(t, a, "notification")
	qv := bridgeVector(t, a, "query_reply")
	qr := bridgeVector(t, a, "query_request")
	expected := bridgeVector(t, a, "expected_capture")
	oldN, err := oldKeys.VerifyNotification(oldEnv, received, nv.Headers.v1(), []byte(nv.Canonical))
	if err != nil {
		t.Fatal("independent v1 notification MAC", err)
	}
	oldRequest, err := v1.ParseQuery([]byte(qr.Canonical))
	if err != nil {
		t.Fatal(err)
	}
	oldQ, err := oldKeys.VerifyQuery(oldEnv, oldRequest, received, qv.Headers.v1(), []byte(qv.Canonical))
	if err != nil {
		t.Fatal("independent v1 query MAC", err)
	}
	cn, err := p.CaptureFromV1Notification(oldN)
	if err != nil {
		t.Fatal(err)
	}
	cq, err := p.CaptureFromV1Query(oldQ)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []p.CaptureBinding{cn, cq} {
		b, err := c.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		d, err := c.Digest()
		if err != nil {
			t.Fatal(err)
		}
		assertGoldenValue(t, expected, b, d)
	}
	commands := map[string]p.RefundRequest{}
	queries := map[string]p.QueryRequest{}
	for name, v := range a.Commands {
		r, err := p.ParseRefundRequest([]byte(v.Canonical))
		if err != nil {
			t.Fatal(err)
		}
		constructed, err := p.NewRefundRequest(cn, r.CommandID, r.LocalRefundID, r.Amount)
		if err != nil || constructed != r {
			t.Fatal("bridge/request constructor differs", err)
		}
		b, err := p.CanonicalRefundRequest(r)
		if err != nil {
			t.Fatal(err)
		}
		d, err := p.ParameterDigest(r)
		if err != nil {
			t.Fatal(err)
		}
		assertGoldenValue(t, v, b, d)
		commands[name] = r
	}
	for name, v := range a.Queries {
		q, err := p.ParseQueryRequest([]byte(v.Canonical))
		if err != nil {
			t.Fatal(err)
		}
		constructed, err := p.NewQueryRequest(q.Command, q.QueryID, q.MutationGeneration)
		if err != nil || constructed != q {
			t.Fatal("query constructor differs", err)
		}
		b, err := p.CanonicalQueryRequest(q)
		if err != nil {
			t.Fatal(err)
		}
		d, err := p.QueryDigest(q)
		if err != nil {
			t.Fatal(err)
		}
		assertGoldenValue(t, v, b, d)
		queries[name] = q
	}
	for _, v := range a.Facts {
		f, err := p.ParseRefundFact([]byte(v.Canonical))
		if err != nil {
			t.Fatal(err)
		}
		b, err := p.CanonicalRefundFact(f)
		if err != nil {
			t.Fatal(err)
		}
		d, err := p.FactDigest(f)
		if err != nil {
			t.Fatal(err)
		}
		assertGoldenValue(t, v, b, d)
	}
	k, err := p.NewKeyring(a.Keys.V2KeyID, []p.KeySpec{{ID: a.Keys.V2KeyID, Key: []byte(a.Keys.V2ASCII), NotBefore: before, NotAfter: after}})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range a.SignedVectors {
		t.Run(v.Name, func(t *testing.T) {
			raw := []byte(v.RawBody)
			h := v.Headers.v2()
			unsigned := h
			unsigned.Signature = ""
			sum := sha256.Sum256(raw)
			if len(raw) != v.RawUTF8Bytes || hex.EncodeToString(sum[:]) != v.RawSHA256 {
				t.Fatal("Python raw provenance differs")
			}
			framed := independentFrame([]byte(v.SignatureDomain), []byte(h.KeyID), []byte(e.ID), []byte(e.MerchantID), []byte(v.ContextDigest), []byte(h.DeliveryID), []byte(h.DeliveryTime), []byte(v.RawSHA256))
			if hex.EncodeToString(framed) != v.FramedSigningInputHex {
				t.Fatal("Python signing frame differs")
			}
			var signed p.SignedHeaders
			var canonical []byte
			var fact p.VerifiedRefundFact
			var factErr error
			switch v.Kind {
			case "command_reply":
				if v.CommandVector == nil {
					t.Fatal("missing command pin")
				}
				command := commands[*v.CommandVector]
				signed, err = k.SignCommandReply(e, command, unsigned, raw)
				if err != nil {
					t.Fatal(err)
				}
				verified, err := k.VerifyCommandReply(e, command, received, h, raw)
				if err != nil {
					t.Fatal(err)
				}
				canonical, err = verified.Canonical()
				if err != nil {
					t.Fatal(err)
				}
				fact, factErr = verified.Fact()
			case "query_reply":
				if v.QueryVector == nil {
					t.Fatal("missing query pin")
				}
				query := queries[*v.QueryVector]
				signed, err = k.SignQueryReply(e, query, unsigned, raw)
				if err != nil {
					t.Fatal(err)
				}
				verified, err := k.VerifyQueryReply(e, query, received, h, raw)
				if err != nil {
					t.Fatal(err)
				}
				canonical, err = verified.Canonical()
				if err != nil {
					t.Fatal(err)
				}
				fact, factErr = verified.Fact()
			case "notification":
				signed, err = k.SignNotification(e, unsigned, raw)
				if err != nil {
					t.Fatal(err)
				}
				verified, err := k.VerifyNotification(e, received, h, raw)
				if err != nil {
					t.Fatal(err)
				}
				canonical, err = verified.Canonical()
				if err != nil {
					t.Fatal(err)
				}
				fact, factErr = verified.Fact()
			default:
				t.Fatal("unknown golden purpose")
			}
			if signed != h || string(canonical) != v.Canonical {
				t.Fatal("Python signature/canonical differs")
			}
			if v.ExpectedFactDigest == nil {
				if factErr != p.ErrInvalid {
					t.Fatal("nonterminal fact")
				}
				if d, err := fact.Digest(); d != "" || err != p.ErrInvalid {
					t.Fatal("nonterminal returned fact")
				}
			} else {
				if factErr != nil {
					t.Fatal(factErr)
				}
				d, err := fact.Digest()
				if err != nil || d != *v.ExpectedFactDigest {
					t.Fatal("Python fact identity differs", err)
				}
			}
		})
	}
}
