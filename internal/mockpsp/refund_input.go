package mockpsp

import (
	"bytes"

	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/paymentrefundproto"
)

// CanonicalRefundCommand is the repository boundary for a v2 refund command.
// The request is parsed and canonicalized before it can reach a repository;
// callers cannot provide a digest or raw body that disagrees with it.
type CanonicalRefundCommand struct {
	raw     []byte
	digest  string
	env     paymentrefundproto.Environment
	request paymentrefundproto.RefundRequest
}

// CanonicalRefundQuery is the repository boundary for a v2 refund query.
type CanonicalRefundQuery struct {
	raw     []byte
	digest  string
	env     paymentrefundproto.Environment
	request paymentrefundproto.QueryRequest
}

func ParseRefundCommandInput(env paymentproto.Environment, raw []byte) (CanonicalRefundCommand, error) {
	parsed, err := paymentrefundproto.ParseRefundRequest(raw)
	if err != nil || parsed.EnvironmentID != env.ID || parsed.MerchantID != env.MerchantID {
		return CanonicalRefundCommand{}, fail("invalid_request")
	}
	canonical, err := paymentrefundproto.CanonicalRefundRequest(parsed)
	if err != nil {
		return CanonicalRefundCommand{}, fail("invalid_request")
	}
	digest, err := paymentrefundproto.ParameterDigest(parsed)
	if err != nil {
		return CanonicalRefundCommand{}, fail("invalid_request")
	}
	return CanonicalRefundCommand{raw: canonical, digest: digest, env: paymentrefundproto.Environment{ID: env.ID, MerchantID: env.MerchantID}, request: parsed}, nil
}

func ParseRefundQueryInput(env paymentproto.Environment, raw []byte) (CanonicalRefundQuery, error) {
	parsed, err := paymentrefundproto.ParseQueryRequest(raw)
	if err != nil || parsed.Command.EnvironmentID != env.ID || parsed.Command.MerchantID != env.MerchantID {
		return CanonicalRefundQuery{}, fail("invalid_request")
	}
	canonical, err := paymentrefundproto.CanonicalQueryRequest(parsed)
	if err != nil {
		return CanonicalRefundQuery{}, fail("invalid_request")
	}
	digest, err := paymentrefundproto.QueryDigest(parsed)
	if err != nil {
		return CanonicalRefundQuery{}, fail("invalid_request")
	}
	return CanonicalRefundQuery{raw: canonical, digest: digest, env: paymentrefundproto.Environment{ID: env.ID, MerchantID: env.MerchantID}, request: parsed}, nil
}

func (v CanonicalRefundCommand) Bytes() ([]byte, error) {
	if len(v.raw) == 0 || v.digest == "" || !bytes.Equal(v.raw, mustRefundCommandCanonical(v.request)) {
		return nil, fail("invalid_request")
	}
	return append([]byte(nil), v.raw...), nil
}

func (v CanonicalRefundQuery) Bytes() ([]byte, error) {
	if len(v.raw) == 0 || v.digest == "" || !bytes.Equal(v.raw, mustRefundQueryCanonical(v.request)) {
		return nil, fail("invalid_request")
	}
	return append([]byte(nil), v.raw...), nil
}

func mustRefundCommandCanonical(v paymentrefundproto.RefundRequest) []byte {
	b, _ := paymentrefundproto.CanonicalRefundRequest(v)
	return b
}

func mustRefundQueryCanonical(v paymentrefundproto.QueryRequest) []byte {
	b, _ := paymentrefundproto.CanonicalQueryRequest(v)
	return b
}
