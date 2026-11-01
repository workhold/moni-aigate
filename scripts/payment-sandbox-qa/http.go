package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/mockpsp"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type response struct {
	status  int
	header  http.Header
	body    []byte
	dropped bool
}

func (s *session) post(origin, path, token string, body []byte, signed *paymentproto.SignedHeaders) (response, error) {
	if s.client == nil {
		s.transport = &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, ForceAttemptHTTP2: false}
		s.client = &http.Client{Transport: s.transport, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	r, err := http.NewRequestWithContext(s.ctx, http.MethodPost, origin+path, bytes.NewReader(body))
	if err != nil {
		return response{}, failure("request_invalid")
	}
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if signed != nil {
		r.Header.Set("X-Moni-Key-Id", signed.KeyID)
		r.Header.Set("X-Moni-Delivery-Id", signed.DeliveryID)
		r.Header.Set("X-Moni-Delivery-Time", signed.DeliveryTime)
		r.Header.Set("X-Moni-Signature", signed.Signature)
	}
	res, err := s.client.Do(r)
	if err != nil {
		// A deliberate wire drop has no safe response to parse. The caller must
		// independently prove the planned durable before/after state.
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) {
			return response{dropped: true}, nil
		}
		return response{}, failure("http_transport_failed")
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(b) > 65536 || res.ProtoMajor != 1 || res.ProtoMinor != 1 || res.Header.Get("Content-Type") != "application/json" || res.Header.Get("Cache-Control") != "no-store" {
		return response{}, failure("response_invalid")
	}
	return response{status: res.StatusCode, header: res.Header.Clone(), body: b}, nil
}

func (s *session) checkoutInput() (paymentproto.CheckoutRequest, []byte, error) {
	var now time.Time
	if s.mockDB.QueryRow(s.ctx, "SELECT clock_timestamp()").Scan(&now) != nil {
		return paymentproto.CheckoutRequest{}, nil, failure("database_clock_failed")
	}
	order, err := paymentproto.MerchantOrderID(s.env.Protocol.ID, uuid.NewString())
	if err != nil {
		return paymentproto.CheckoutRequest{}, nil, failure("checkout_fixture_invalid")
	}
	created := now.Add(-time.Second).UTC().Truncate(time.Microsecond)
	q := paymentproto.CheckoutRequest{SchemaVersion: 1, EnvironmentID: s.env.Protocol.ID, MerchantID: s.env.Protocol.MerchantID, CommandID: uuid.NewString(), MerchantOrderID: order, Amount: "100", Currency: "CNY", CreatedAt: created.Format(utc6), ExpiresAt: created.Add(15 * time.Minute).Format(utc6)}
	b, err := paymentproto.CanonicalCheckout(q)
	if err != nil {
		return q, nil, failure("checkout_fixture_invalid")
	}
	return q, b, nil
}

const utc6 = "2006-01-02T15:04:05.000000Z"

func (s *session) checkout(raw []byte) (paymentproto.CommandReply, []byte, error) {
	res, err := s.post(s.env.APIOrigin, "/v1/checkout", s.env.APIToken(), raw, nil)
	if err != nil || res.status != 200 || res.dropped {
		return paymentproto.CommandReply{}, nil, failure("checkout_http_failed")
	}
	out, err := paymentproto.ParseCommandReply(res.body)
	in, inputErr := paymentproto.ParseCheckout(raw)
	if err != nil || inputErr != nil || out.CommandID != in.CommandID || out.MerchantOrderID != in.MerchantOrderID || out.EnvironmentID != in.EnvironmentID || out.MerchantID != in.MerchantID || out.ParameterDigest != paymentproto.RequestDigest(raw) || out.CommandKind != "checkout" {
		return paymentproto.CommandReply{}, nil, failure("checkout_binding_failed")
	}
	return out, res.body, nil
}

func (s *session) queryInput(q paymentproto.CheckoutRequest, checkout []byte) (paymentproto.QueryRequest, []byte, error) {
	in := paymentproto.QueryRequest{SchemaVersion: 1, EnvironmentID: q.EnvironmentID, MerchantID: q.MerchantID, QueryID: uuid.NewString(), CommandID: q.CommandID, MerchantOrderID: q.MerchantOrderID, CommandKind: "checkout", ParameterDigest: paymentproto.RequestDigest(checkout)}
	b, err := paymentproto.CanonicalQuery(in)
	if err != nil {
		return in, nil, failure("query_fixture_invalid")
	}
	return in, b, nil
}

func (s *session) query(q paymentproto.QueryRequest, raw []byte) (paymentproto.QueryReply, []byte, error) {
	res, err := s.post(s.env.APIOrigin, "/v1/query", s.env.APIToken(), raw, nil)
	if err != nil || res.status != 200 || res.dropped {
		return paymentproto.QueryReply{}, nil, failure("query_http_failed")
	}
	h, err := paymentproto.ReadSignedHeaders(res.header)
	if err != nil {
		return paymentproto.QueryReply{}, nil, failure("query_signature_failed")
	}
	var now time.Time
	if s.mockDB.QueryRow(s.ctx, "SELECT clock_timestamp()").Scan(&now) != nil {
		return paymentproto.QueryReply{}, nil, failure("database_clock_failed")
	}
	keys, err := s.env.Keyring()
	if err != nil {
		return paymentproto.QueryReply{}, nil, failure("query_signature_failed")
	}
	verified, err := keys.VerifyQuery(s.env.Protocol, q, now.UTC(), h, res.body)
	if err != nil {
		return paymentproto.QueryReply{}, nil, failure("query_signature_failed")
	}
	b, err := verified.Canonical()
	if err != nil || !bytes.Equal(b, res.body) {
		return paymentproto.QueryReply{}, nil, failure("query_canonical_failed")
	}
	changed := q
	changed.QueryID = uuid.NewString()
	if _, err = keys.VerifyQuery(s.env.Protocol, changed, now.UTC(), h, res.body); err == nil {
		return paymentproto.QueryReply{}, nil, failure("query_signature_binding_failed")
	}
	out, err := paymentproto.ParseQueryReply(res.body)
	if err != nil {
		return out, nil, failure("query_reply_invalid")
	}
	return out, res.body, nil
}

func (s *session) control(in mockpsp.ControlInput) (mockpsp.ControlReply, []byte, error) {
	if in.OperationID == "" {
		in.OperationID = uuid.NewString()
	}
	b, err := json.Marshal(in)
	if err != nil {
		return mockpsp.ControlReply{}, nil, failure("control_fixture_invalid")
	}
	res, err := s.post(s.env.ControlOrigin, "/_qa/v1/commands", s.env.ControlToken(), b, nil)
	var reply mockpsp.ControlReply
	if err != nil || res.status != 200 || res.dropped || json.Unmarshal(res.body, &reply) != nil || reply.OperationID != in.OperationID || reply.Status != "accepted" || reply.Reason != "accepted" || reply.ResourceID == nil {
		return reply, nil, failure("control_not_accepted")
	}
	return reply, res.body, nil
}

func (s *session) signedNotification(body []byte) (paymentproto.SignedHeaders, error) {
	var now time.Time
	if s.receiverDB.QueryRow(s.ctx, "SELECT clock_timestamp()").Scan(&now) != nil {
		return paymentproto.SignedHeaders{}, failure("database_clock_failed")
	}
	k, err := s.env.Keyring()
	if err != nil {
		return paymentproto.SignedHeaders{}, failure("notification_fixture_invalid")
	}
	h, err := k.SignNotification(s.env.Protocol, paymentproto.SignedHeaders{DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, body)
	if err != nil {
		return paymentproto.SignedHeaders{}, failure("notification_fixture_invalid")
	}
	return h, nil
}
