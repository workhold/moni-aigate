package payments

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type frozenPaymentOrder struct {
	id, environment, merchant, merchantOrder, amount, currency string
	created, expires                                           time.Time
}

func (s *Service) policyTx(ctx context.Context, tx pgx.Tx, out *PolicyView) error {
	e := tx.QueryRow(ctx, `SELECT enabled,revision::text FROM payment_policy WHERE singleton`).Scan(&out.Enabled, &out.Revision)
	out.RuntimeAllowed = s.runtime.hasEnvironment() && string(s.runtime.mode()) == "sandbox"
	out.EffectiveEnabled = out.Enabled && out.RuntimeAllowed
	return e
}
func (s *Service) GetPolicy(ctx context.Context, actor string) (PolicyView, error) {
	var out PolicyView
	e := s.read(ctx, Reader{ActorID: actor}, true, true, func(ctx context.Context, tx pgx.Tx) error { return s.policyTx(ctx, tx, &out) })
	if e != nil {
		return PolicyView{}, e
	}
	return out, nil
}
func (s *Service) capabilities(ctx context.Context, r Reader, platform bool) (PaymentCapabilitiesView, error) {
	out := PaymentCapabilitiesView{Mode: string(s.runtime.mode())}
	e := s.read(ctx, r, platform, false, func(ctx context.Context, tx pgx.Tx) error {
		var policy PolicyView
		if e := s.policyTx(ctx, tx, &policy); e != nil {
			return e
		}
		if s.runtime.hasEnvironment() {
			env := "sandbox"
			out.Environment = &env
			out.LocalSimulation = true
		}
		out.PaymentsEnabled = policy.EffectiveEnabled
		return nil
	})
	if e != nil {
		return PaymentCapabilitiesView{}, e
	}
	return out, nil
}
func (s *Service) PlatformCapabilities(ctx context.Context, actor string) (PaymentCapabilitiesView, error) {
	return s.capabilities(ctx, Reader{ActorID: actor}, true)
}
func (s *Service) WalletCapabilities(ctx context.Context, r Reader) (PaymentCapabilitiesView, error) {
	return s.capabilities(ctx, r, false)
}

const paymentVersionColumns = `id,sku_id,version::text,display_name,amount,wallet_credit_amount,currency,merchant_id,payee_label,checkout_ttl_seconds,created_at`

func scanPaymentVersion(row pgx.Row) (v SKUVersionView, e error) {
	e = row.Scan(&v.ID, &v.SKUID, &v.Version, &v.DisplayName, &v.Amount, &v.WalletCreditAmount, &v.Currency, &v.MerchantID, &v.PayeeLabel, &v.CheckoutTTLSeconds, &v.CreatedAt)
	v.Environment = "sandbox"
	return
}
func paymentSKUTx(ctx context.Context, tx pgx.Tx, id string) (PlatformSKUView, error) {
	var out PlatformSKUView
	e := tx.QueryRow(ctx, `SELECT id,revision::text,enabled,current_version_id,created_at FROM payment_skus WHERE id=$1`, id).Scan(&out.ID, &out.Revision, &out.Enabled, &out.CurrentVersionID, &out.CreatedAt)
	if e != nil {
		return out, e
	}
	if out.CurrentVersionID != nil {
		v, e := scanPaymentVersion(tx.QueryRow(ctx, `SELECT `+paymentVersionColumns+` FROM payment_sku_versions WHERE id=$1 AND sku_id=$2`, *out.CurrentVersionID, id))
		if errors.Is(e, pgx.ErrNoRows) {
			return PlatformSKUView{}, ErrPaymentsIntegrity
		}
		if e != nil {
			return PlatformSKUView{}, e
		}
		out.CurrentVersion = &v
	} else if out.Enabled {
		return PlatformSKUView{}, ErrPaymentsIntegrity
	}
	return out, nil
}
func (s *Service) GetPlatformSKU(ctx context.Context, actor, id string) (PlatformSKUView, error) {
	var out PlatformSKUView
	if !paymentID(id) {
		return out, paymentFail("invalid_request")
	}
	e := s.read(ctx, Reader{ActorID: actor}, true, false, func(ctx context.Context, tx pgx.Tx) error { var e error; out, e = paymentSKUTx(ctx, tx, id); return e })
	if e != nil {
		return PlatformSKUView{}, e
	}
	return out, nil
}

type paymentRowKey struct {
	id string
	at time.Time
}

func paymentPageKeys(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]paymentRowKey, error) {
	rows, e := tx.Query(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var keys []paymentRowKey
	for rows.Next() {
		var k paymentRowKey
		if e = rows.Scan(&k.id, &k.at); e != nil {
			return nil, e
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}
func (s *Service) ListPlatformSKUs(ctx context.Context, actor string, page Page) (PageResult[PlatformSKUView], error) {
	out := PageResult[PlatformSKUView]{Data: []PlatformSKUView{}}
	filter := paymentFilter("platform", "sku", "")
	before, id, e := decodePaymentCursor(page.Cursor, filter)
	if e != nil {
		return out, e
	}
	e = s.read(ctx, Reader{ActorID: actor}, true, false, func(ctx context.Context, tx pgx.Tx) error {
		keys, e := paymentPageKeys(ctx, tx, `SELECT id,created_at FROM payment_skus WHERE ($1::timestamptz IS NULL OR created_at<$1 OR (created_at=$1 AND id COLLATE "C"<$2)) ORDER BY created_at DESC,id COLLATE "C" DESC LIMIT 51`, before, id)
		if e != nil {
			return e
		}
		if len(keys) > 50 {
			out.NextCursor = encodePaymentCursor(keys[49].at, keys[49].id, filter)
			keys = keys[:50]
		}
		for _, k := range keys {
			v, e := paymentSKUTx(ctx, tx, k.id)
			if e != nil {
				return e
			}
			out.Data = append(out.Data, v)
		}
		return nil
	})
	if e != nil {
		return PageResult[PlatformSKUView]{Data: []PlatformSKUView{}}, e
	}
	return out, nil
}
func (s *Service) ListWalletSKUs(ctx context.Context, r Reader, page Page) (PageResult[SKUVersionView], error) {
	out := PageResult[SKUVersionView]{Data: []SKUVersionView{}}
	filter := paymentFilter("tenant:"+r.TenantID, "sku", "")
	before, id, e := decodePaymentCursor(page.Cursor, filter)
	if e != nil {
		return out, e
	}
	e = s.read(ctx, r, false, false, func(ctx context.Context, tx pgx.Tx) error {
		var policy PolicyView
		if e := s.policyTx(ctx, tx, &policy); e != nil {
			return e
		}
		if !policy.EffectiveEnabled {
			return nil
		}
		keys, e := paymentPageKeys(ctx, tx, `SELECT v.id,v.created_at FROM payment_sku_versions v JOIN payment_skus s ON s.current_version_id=v.id AND s.id=v.sku_id WHERE s.enabled AND ($1::timestamptz IS NULL OR v.created_at<$1 OR (v.created_at=$1 AND v.id COLLATE "C"<$2)) ORDER BY v.created_at DESC,v.id COLLATE "C" DESC LIMIT 51`, before, id)
		if e != nil {
			return e
		}
		if len(keys) > 50 {
			out.NextCursor = encodePaymentCursor(keys[49].at, keys[49].id, filter)
			keys = keys[:50]
		}
		for _, k := range keys {
			v, e := scanPaymentVersion(tx.QueryRow(ctx, `SELECT `+paymentVersionColumns+` FROM payment_sku_versions WHERE id=$1`, k.id))
			if e != nil {
				return e
			}
			out.Data = append(out.Data, v)
		}
		return nil
	})
	if e != nil {
		return PageResult[SKUVersionView]{Data: []SKUVersionView{}}, e
	}
	return out, nil
}

// Safe views read source, receipt, effect and journal validity in the same RR
// snapshot; a fragment or an invalid graph is never presented as a credit.
func paymentOrderTx(ctx context.Context, tx pgx.Tx, id, tenant string) (OrderView, error) {
	var o OrderView
	var env, merchantOrder string
	e := tx.QueryRow(ctx, `SELECT id,tenant_id,creator,sku_version_id,display_name,amount,wallet_credit_amount,currency,environment_id,merchant_id,payee_label,created_at,expires_at,merchant_order_id,clock_timestamp() FROM payment_orders WHERE id=$1 AND ($2='' OR tenant_id=$2)`, id, tenant).Scan(&o.ID, &o.TenantID, &o.Creator, &o.SKUVersionID, &o.DisplayName, &o.Amount, &o.WalletCreditAmount, &o.Currency, &env, &o.MerchantID, &o.PayeeLabel, &o.CreatedAt, &o.ExpiresAt, &merchantOrder, &o.ObservedAt)
	if e != nil {
		return o, e
	}
	o.Environment = "sandbox"
	o.FundingState = "unconfirmed"
	var selection, capture bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_order_capture_selections WHERE order_id=$1),EXISTS(SELECT 1 FROM payment_captures WHERE order_id=$1)`, id).Scan(&selection, &capture)
	if e != nil {
		return OrderView{}, e
	}
	if selection {
		o.FundingState = "verified"
	}
	if capture {
		var valid bool
		var receipt, journal string
		var at time.Time
		e = tx.QueryRow(ctx, `SELECT f.id,f.journal_id,f.created_at,payment_capture_source_valid(c.id) AND payment_sandbox_journal_valid(f.journal_id) AND (f.tenant_id,f.source_kind,f.source_id,f.amount,f.currency)=(c.tenant_id,'sandbox_capture',c.id,c.wallet_credit_amount,'COIN') FROM payment_captures c JOIN funding_receipts f ON f.source_kind='sandbox_capture' AND f.source_id=c.id WHERE c.order_id=$1`, id).Scan(&receipt, &journal, &at, &valid)
		if e != nil || !valid || !selection {
			return OrderView{}, ErrPaymentsIntegrity
		}
		o.FundingState = "credited"
		o.FundingReceiptID = &receipt
		o.JournalID = &journal
		o.CreditedAt = &at
	}
	var outbox, state, external, digest string
	e = tx.QueryRow(ctx, `SELECT id,state,external_command_id,parameter_digest FROM payment_command_outbox WHERE order_id=$1 AND kind='checkout'`, id).Scan(&outbox, &state, &external, &digest)
	if e != nil {
		return OrderView{}, ErrPaymentsIntegrity
	}
	switch state {
	case "pending", "retry_wait":
		o.CreationState = "queued"
	case "leased":
		o.CreationState = "submitting"
	case "submission_unknown":
		o.CreationState = state
	case "review_required":
		o.CreationState = state
	case "completed":
		o.CreationState = "confirmed"
	default:
		return OrderView{}, ErrPaymentsIntegrity
	}
	status, checkoutRef, found, e := paymentCheckoutResultTx(ctx, tx, outbox, env, o.MerchantID, merchantOrder, external, digest)
	if e != nil {
		return OrderView{}, e
	}
	ready := found && status == "ready"
	if ready {
		o.CheckoutRef = checkoutRef
	}
	if state == "completed" {
		if !found {
			return OrderView{}, ErrPaymentsIntegrity
		}
		if status == "rejected" {
			o.CreationState = "failed_no_effect"
		}
	}

	var closed, cancel bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_provider_revisions WHERE order_id=$1 AND kind='closed'),EXISTS(SELECT 1 FROM payment_cancel_intents WHERE order_id=$1)`, id).Scan(&closed, &cancel)
	if e != nil {
		return OrderView{}, e
	}
	switch {
	case closed:
		o.CheckoutState = "closed"
	case cancel:
		o.CheckoutState = "cancel_requested"
	case !o.ObservedAt.Before(o.ExpiresAt):
		o.CheckoutState = "expired"
	case ready:
		o.CheckoutState = "ready"
	default:
		o.CheckoutState = "pending"
	}
	var reason string
	e = tx.QueryRow(ctx, paymentReviewQuery, id).Scan(&reason)
	if e == nil {
		o.ReviewRequired = true
		o.ReviewReason = &reason
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return OrderView{}, e
	}
	return o, nil
}

const paymentReviewQuery = `SELECT reason FROM (
 SELECT reason,created_at,id,0 priority FROM payment_review_facts WHERE order_id=$1
 UNION ALL SELECT CASE WHEN kind='query' AND last_error_code='attempts_exhausted' THEN 'query_attempts_exhausted' WHEN kind<>'query' AND last_error_code='attempts_exhausted' THEN 'command_attempts_exhausted' ELSE 'provider_protocol_invalid' END,created_at,id,1 priority FROM payment_command_outbox WHERE order_id=$1 AND state='review_required'
 UNION ALL SELECT CASE WHEN p.last_error_code='attempts_exhausted' THEN 'processing_attempts_exhausted' ELSE 'provider_protocol_invalid' END,e.verified_at,e.id,1 priority FROM payment_verified_evidence e JOIN payment_evidence_processing p ON p.evidence_id=e.id WHERE e.order_id=$1 AND p.state='processing_review'
 ) review ORDER BY priority,created_at,id COLLATE "C" LIMIT 1`

func (s *Service) getOrder(ctx context.Context, r Reader, id string, platform bool) (OrderView, error) {
	var out OrderView
	if !paymentID(id) {
		return out, paymentFail("invalid_request")
	}
	e := s.read(ctx, r, platform, false, func(ctx context.Context, tx pgx.Tx) error {
		var e error
		out, e = paymentOrderTx(ctx, tx, id, r.TenantID)
		return e
	})
	if e != nil {
		return OrderView{}, e
	}
	return out, nil
}
func (s *Service) GetPlatformOrder(ctx context.Context, actor, id string) (OrderView, error) {
	return s.getOrder(ctx, Reader{ActorID: actor}, id, true)
}
func (s *Service) GetWalletOrder(ctx context.Context, r Reader, id string) (OrderView, error) {
	return s.getOrder(ctx, r, id, false)
}
func (s *Service) listOrders(ctx context.Context, r Reader, page Page, platform bool) (PageResult[OrderView], error) {
	out := PageResult[OrderView]{Data: []OrderView{}}
	scope := "platform"
	if !platform {
		scope = "tenant:" + r.TenantID
	}
	filter := paymentFilter(scope, "order", "")
	before, id, e := decodePaymentCursor(page.Cursor, filter)
	if e != nil {
		return out, e
	}
	e = s.read(ctx, r, platform, false, func(ctx context.Context, tx pgx.Tx) error {
		keys, e := paymentPageKeys(ctx, tx, `SELECT id,created_at FROM payment_orders WHERE ($3='' OR tenant_id=$3) AND ($1::timestamptz IS NULL OR created_at<$1 OR (created_at=$1 AND id COLLATE "C"<$2)) ORDER BY created_at DESC,id COLLATE "C" DESC LIMIT 51`, before, id, r.TenantID)
		if e != nil {
			return e
		}
		if len(keys) > 50 {
			out.NextCursor = encodePaymentCursor(keys[49].at, keys[49].id, filter)
			keys = keys[:50]
		}
		for _, k := range keys {
			o, e := paymentOrderTx(ctx, tx, k.id, r.TenantID)
			if e != nil {
				return e
			}
			out.Data = append(out.Data, o)
		}
		return nil
	})
	if e != nil {
		return PageResult[OrderView]{Data: []OrderView{}}, e
	}
	return out, nil
}
func (s *Service) ListPlatformOrders(ctx context.Context, actor string, page Page) (PageResult[OrderView], error) {
	return s.listOrders(ctx, Reader{ActorID: actor}, page, true)
}
func (s *Service) ListWalletOrders(ctx context.Context, r Reader, page Page) (PageResult[OrderView], error) {
	return s.listOrders(ctx, r, page, false)
}
func (s *Service) ListPlatformObservations(ctx context.Context, actor, order string, page Page) (PageResult[ObservationView], error) {
	out := PageResult[ObservationView]{Data: []ObservationView{}}
	if !paymentID(order) {
		return out, paymentFail("invalid_request")
	}
	filter := paymentFilter("platform", "observation", order)
	before, id, e := decodePaymentCursor(page.Cursor, filter)
	if e != nil {
		return out, e
	}
	e = s.read(ctx, Reader{ActorID: actor}, true, false, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT e.id,e.order_id,e.kind,p.state,coalesce((SELECT r.reason FROM payment_review_facts r WHERE r.evidence_id=e.id ORDER BY r.created_at,r.id COLLATE "C" LIMIT 1),CASE WHEN p.state='processing_review' THEN CASE WHEN p.last_error_code='attempts_exhausted' THEN 'processing_attempts_exhausted' ELSE 'provider_protocol_invalid' END END),e.happened_at,e.verified_at FROM payment_verified_evidence e JOIN payment_evidence_processing p ON p.evidence_id=e.id WHERE e.order_id=$3 AND ($1::timestamptz IS NULL OR e.verified_at<$1 OR (e.verified_at=$1 AND e.id COLLATE "C"<$2)) ORDER BY e.verified_at DESC,e.id COLLATE "C" DESC LIMIT 51`, before, id, order)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var v ObservationView
			if e = rows.Scan(&v.ID, &v.OrderID, &v.Kind, &v.Status, &v.Reason, &v.OccurredAt, &v.ReceivedAt); e != nil {
				return e
			}
			out.Data = append(out.Data, v)
		}
		return rows.Err()
	})
	if e != nil {
		return PageResult[ObservationView]{Data: []ObservationView{}}, e
	}
	if len(out.Data) > 50 {
		v := out.Data[49]
		out.NextCursor = encodePaymentCursor(v.ReceivedAt, v.ID, filter)
		out.Data = out.Data[:50]
	}
	return out, nil
}

// A signed query can confirm a previously ambiguous mutation without creating
// a transport observation. Both persisted proof forms feed this safe view.
func paymentCheckoutResultTx(ctx context.Context, tx pgx.Tx, outbox, env, merchant, order, command, digest string) (string, *string, bool, error) {
	var source string
	var raw []byte
	var request []byte
	e := tx.QueryRow(ctx, `SELECT source,raw,request FROM (
 SELECT 'command' source,ob.canonical_reply raw,NULL::bytea request,ob.generation,ob.observed_at at,ob.id FROM payment_command_observations ob WHERE ob.outbox_id=$1 AND convert_from(ob.canonical_reply,'UTF8')::jsonb->>'command_state'='completed'
 UNION ALL SELECT 'query',v.canonical_body,a.canonical_request,a.mutation_generation,v.received_at,v.id FROM payment_verified_queries v JOIN payment_query_attempts a ON a.query_id=v.query_id JOIN payment_command_outbox s ON s.id=a.supervisor_id WHERE s.original_outbox_id=$1 AND convert_from(v.canonical_body,'UTF8')::jsonb->>'command_state'='completed'
 ) facts ORDER BY generation DESC,at DESC,id COLLATE "C" DESC LIMIT 1`, outbox).Scan(&source, &raw, &request)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", nil, false, nil
	}
	if e != nil {
		return "", nil, false, e
	}
	var state string
	var result *p.CommandResult
	if source == "command" {
		r, e := p.ParseCommandReply(raw)
		canonical, ce := p.CanonicalCommandReply(r)
		if e != nil || ce != nil || !bytes.Equal(raw, canonical) || r.EnvironmentID != env || r.MerchantID != merchant || r.MerchantOrderID != order || r.CommandID != command || r.CommandKind != "checkout" || r.ParameterDigest != digest {
			return "", nil, false, ErrPaymentsIntegrity
		}
		state, result = r.CommandState, r.Result
	} else if source == "query" {
		q, e := p.ParseQuery(request)
		cq, qe := p.CanonicalQuery(q)
		r, re := p.ParseQueryReply(raw)
		cr, ce := p.CanonicalQueryReply(r)
		if e != nil || qe != nil || re != nil || ce != nil || !bytes.Equal(request, cq) || !bytes.Equal(raw, cr) || q.EnvironmentID != env || q.MerchantID != merchant || q.MerchantOrderID != order || q.CommandID != command || q.CommandKind != "checkout" || q.ParameterDigest != digest || q.QueryID != r.QueryID || r.EnvironmentID != q.EnvironmentID || r.MerchantID != q.MerchantID || r.MerchantOrderID != q.MerchantOrderID || r.CommandID != q.CommandID || r.CommandKind != q.CommandKind || r.ParameterDigest != q.ParameterDigest {
			return "", nil, false, ErrPaymentsIntegrity
		}
		state, result = r.CommandState, r.Result
	} else {
		return "", nil, false, ErrPaymentsIntegrity
	}
	if state != "completed" || result == nil {
		return "", nil, false, ErrPaymentsIntegrity
	}
	return result.Status, result.CheckoutRef, true, nil
}
