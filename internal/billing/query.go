package billing

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

const ReportConsumer = "reports-v1"

func limitQuery(q Query) int {
	if q.Limit <= 0 || q.Limit > 200 {
		return 100
	}
	return q.Limit
}
func (s *Store) Wallet(ctx context.Context, q Query) (Wallet, error) {
	w := Wallet{TenantID: q.TenantID}
	if !validID(q.TenantID) {
		return w, fail("invalid_tenant", "tenant scope required")
	}
	err := s.pool.QueryRow(ctx, `SELECT coalesce(sum(balance) FILTER(WHERE kind='AVAILABLE'),0)::bigint,coalesce(sum(balance) FILTER(WHERE kind='HELD'),0)::bigint FROM ledger_accounts WHERE tenant_id=$1`, q.TenantID).Scan(&w.Available, &w.Held)
	return w, err
}
func (s *Store) Journals(ctx context.Context, q Query) ([]Journal, error) {
	if !validID(q.TenantID) {
		return nil, fail("invalid_tenant", "tenant scope required")
	}
	before := q.Before
	if before.IsZero() {
		before = time.Now().UTC().Add(time.Second)
	}
	rows, err := s.pool.Query(ctx, `SELECT j.id,j.operation_id,j.kind,j.reference,j.created_at,
 coalesce((SELECT jsonb_agg(jsonb_build_object('account_kind',a.kind,'delta',l.signed_delta::text) ORDER BY a.kind) FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id),'[]'::jsonb)
 FROM journals j WHERE j.tenant_id=$1 AND j.created_at<$2 ORDER BY j.created_at DESC,j.id LIMIT $3`, q.TenantID, before, limitQuery(q))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Journal{}
	for rows.Next() {
		var j Journal
		var b []byte
		if err = rows.Scan(&j.ID, &j.OperationID, &j.Kind, &j.Reference, &j.CreatedAt, &b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &j.Lines); err != nil {
			return nil, err
		}
		result = append(result, j)
	}
	return result, rows.Err()
}
func (s *Store) Requests(ctx context.Context, q Query) ([]RequestRecord, error) {
	if !validID(q.TenantID) {
		return nil, fail("invalid_tenant", "tenant scope required")
	}
	before := q.Before
	if before.IsZero() {
		before = time.Now().UTC().Add(time.Second)
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id,r.model_id,r.status,r.billing_status,r.created_at,coalesce(s.charged_amount,0),v.amount FROM request_records r JOIN reservations v ON v.request_id=r.id LEFT JOIN settlements s ON s.request_id=r.id WHERE r.tenant_id=$1 AND r.created_at<$2 ORDER BY r.created_at DESC,r.id LIMIT $3`, q.TenantID, before, limitQuery(q))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RequestRecord{}
	for rows.Next() {
		var r RequestRecord
		if err = rows.Scan(&r.ID, &r.ModelID, &r.Status, &r.BillingStatus, &r.CreatedAt, &r.ChargedAmount, &r.ReservationAmount); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
func (s *Store) Report(ctx context.Context, q Query) (Report, error) {
	r := Report{TenantID: q.TenantID}
	if !validID(q.TenantID) {
		return r, fail("invalid_tenant", "tenant scope required")
	}
	err := s.pool.QueryRow(ctx, `SELECT
 coalesce((SELECT credited FROM report_totals WHERE tenant_id=$1 AND consumer=(SELECT consumer FROM active_report_projection WHERE singleton)),0)::text,
 coalesce((SELECT charged FROM report_totals WHERE tenant_id=$1 AND consumer=(SELECT consumer FROM active_report_projection WHERE singleton)),0)::text,
 coalesce((SELECT refunded FROM report_totals WHERE tenant_id=$1 AND consumer=(SELECT consumer FROM active_report_projection WHERE singleton)),0)::text,
 coalesce((SELECT released FROM report_totals WHERE tenant_id=$1 AND consumer=(SELECT consumer FROM active_report_projection WHERE singleton)),0)::text,
 coalesce((SELECT requests FROM report_totals WHERE tenant_id=$1 AND consumer=(SELECT consumer FROM active_report_projection WHERE singleton)),0),
 coalesce((SELECT sum(cost_amount) FROM upstream_attempts WHERE tenant_id=$1 AND cost_status<>'unknown'),0)::text,
 (SELECT count(*) FROM upstream_attempts WHERE tenant_id=$1 AND cost_status='unknown')`, q.TenantID).Scan(&r.Credited, &r.Charged, &r.Refunded, &r.Released, &r.Requests, &r.KnownProviderCost, &r.UnknownProviderCosts)
	return r, err
}

func (s *Store) Project(ctx context.Context, in ProjectInput) (int, error) {
	if in.Consumer == "" {
		if err := s.pool.QueryRow(ctx, "SELECT consumer FROM active_report_projection WHERE singleton").Scan(&in.Consumer); err != nil {
			return 0, err
		}
	}
	if !boundedString(in.Consumer, 80) {
		return 0, fail("invalid_consumer", "consumer name required")
	}
	limit := in.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	processed := 0
	type event struct {
		ID, Tenant, Kind string
		Payload          []byte
	}
	var events []event
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		processed = 0
		events = nil
		rows, err := tx.Query(ctx, `SELECT event_id,tenant_id,kind,sanitized_payload FROM projection_outbox o WHERE NOT EXISTS(SELECT 1 FROM projection_receipts r WHERE r.consumer=$1 AND r.event_id=o.event_id) ORDER BY created_at,event_id LIMIT $2`, in.Consumer, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var e event
			if err = rows.Scan(&e.ID, &e.Tenant, &e.Kind, &e.Payload); err != nil {
				rows.Close()
				return err
			}
			events = append(events, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range events {
			tag, err := tx.Exec(ctx, `INSERT INTO projection_receipts(consumer,event_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, in.Consumer, e.ID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				continue
			}
			if !oneOf(e.Kind, "credited", "reserved", "settled", "released", "refunded") {
				processed++
				continue
			}
			var payload struct {
				Amount   Money `json:"amount"`
				Released Money `json:"released"`
			}
			if err = json.Unmarshal(e.Payload, &payload); err != nil {
				return err
			}
			if payload.Amount < 0 || payload.Released < 0 {
				return fail("invalid_event", "negative projection amount")
			}
			var credited, charged, refunded, released Money
			var requests int64
			switch e.Kind {
			case "credited":
				credited = payload.Amount
			case "reserved":
				requests = 1
			case "settled":
				charged = payload.Amount
				released = payload.Released
			case "released":
				released = payload.Released
			case "refunded":
				refunded = payload.Amount
			}
			if _, err = tx.Exec(ctx, `INSERT INTO report_totals(consumer,tenant_id,credited,charged,refunded,released,requests) VALUES($1,$2,$3::text::numeric,$4::text::numeric,$5::text::numeric,$6::text::numeric,$7) ON CONFLICT(consumer,tenant_id) DO UPDATE SET credited=report_totals.credited+excluded.credited,charged=report_totals.charged+excluded.charged,refunded=report_totals.refunded+excluded.refunded,released=report_totals.released+excluded.released,requests=report_totals.requests+excluded.requests`, in.Consumer, e.Tenant, moneyText(credited), moneyText(charged), moneyText(refunded), moneyText(released), requests); err != nil {
				return err
			}
			processed++
		}
		return nil
	})
	return processed, err
}
func (s *Store) VerifyLedger(ctx context.Context, q Query) (LedgerCheck, error) {
	var result LedgerCheck
	if q.TenantID != "" && !validID(q.TenantID) {
		return result, fail("invalid_tenant", "invalid scope")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM ledger_accounts a WHERE ($1='' OR a.tenant_id=$1) AND a.balance::numeric<>coalesce((SELECT sum(l.signed_delta::numeric) FROM journal_lines l WHERE l.account_id=a.id),0)),
 (SELECT count(*) FROM journals j WHERE ($1='' OR j.tenant_id=$1) AND ((SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)<2 OR (SELECT coalesce(sum(l.signed_delta::numeric),0) FROM journal_lines l WHERE l.journal_id=j.id)<>0))`, q.TenantID).Scan(&result.AccountMismatches, &result.UnbalancedJournals)
	if err != nil {
		return result, err
	}
	funding, err := verifyFundingTx(ctx, tx, q.TenantID)
	if err != nil {
		return result, err
	}
	result.Balanced = result.AccountMismatches == 0 && result.UnbalancedJournals == 0 && funding.Balanced
	if err = tx.Commit(ctx); err != nil {
		return LedgerCheck{}, err
	}
	return result, nil
}

func moneyText(m Money) string { return strconv.FormatInt(int64(m), 10) }
