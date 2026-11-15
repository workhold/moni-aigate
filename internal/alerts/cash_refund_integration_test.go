package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func TestPostgresCashRefundPendingAlertUsesApprovedAtAndRecovers(t *testing.T) {
	f := integrationWithCashRefundHistory(t, true)
	var valid bool
	if err := f.owner.QueryRow(f.ctx, `SELECT wallet_cash_refund_order_valid($1)`, f.cashOrder).Scan(&valid); err != nil || !valid {
		t.Fatalf("backfilled cash-refund history is invalid: %v valid=%v", err, valid)
	}
	if err := f.app.QueryRow(f.ctx, cashRefundSourceSQL, uuid.NewString(), int64(600)).Scan(&valid, new(time.Time), new(string), new(bool), new(*time.Time), new(*string)); err != nil || valid {
		t.Fatalf("missing tenant was treated as an existing alert source: %v", err)
	}
	created := f.create(CashRefundPendingAged, "tenant", f.tenant, AgingParameters{MinimumAgeSeconds: "600", TriggerCount: "1", RecoveryCount: "0"}, "enabled")
	f.due(*created.ResourceID)
	if outcome := f.evaluate(); outcome.Outcome != "evaluated" {
		t.Fatalf("cash-refund alert evaluation=%+v", outcome)
	}
	page, err := f.store.Episodes(f.ctx, f.reader(), EpisodeFilter{})
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("cash-refund alert episodes=%+v err=%v", page, err)
	}
	episode := page.Data[0]
	sample, ok := episode.LatestSample.(AgingSample)
	if !ok || sample.Kind != CashRefundPendingAged || sample.ObservedCount != "1" || sample.CountIsLowerBound || sample.OldestReferenceID == nil || *sample.OldestReferenceID != f.cashOrder || sample.OldestCreatedAt == nil || time.Since(sample.OldestCreatedAt.Time) < 9*time.Minute {
		t.Fatalf("cash-refund alert sample mismatch: %+v", episode.LatestSample)
	}
	var state string
	if err = f.app.QueryRow(f.ctx, `SELECT state FROM cash_refund_orders WHERE id=$1`, f.cashOrder).Scan(&state); err != nil || state != "approved" {
		t.Fatalf("alert evaluation changed cash-refund state: %s %v", state, err)
	}

	root, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = root.Exec(f.ctx, `SELECT id FROM cash_refund_orders WHERE id=$1 FOR NO KEY UPDATE`, f.cashOrder); err != nil {
		t.Fatal(err)
	}
	observeCtx, cancel := context.WithTimeout(f.ctx, time.Second)
	var exists, lower bool
	var at time.Time
	var count string
	var oldest *time.Time
	var reference *string
	if err = f.app.QueryRow(observeCtx, cashRefundSourceSQL, f.tenant, int64(600)).Scan(&exists, &at, &count, &lower, &oldest, &reference); err != nil || !exists || count != "1" {
		t.Fatalf("cash-refund source took a business lock: exists=%v count=%s err=%v", exists, count, err)
	}
	cancel()
	_ = root.Rollback(f.ctx)

	key := uuid.NewString()
	cancelled, err := billing.New(f.owner).CancelCashRefund(f.ctx, billing.CashRefundActionInput{IdempotencyKey: key, RequestHMAC: key, TenantID: f.tenant, OrderID: f.cashOrder, ActorID: f.finance, Reason: "customer resolved offline transfer issue"})
	if err != nil || cancelled.State != "cancelled" {
		t.Fatalf("cash refund could not be released for recovery: %+v %v", cancelled, err)
	}
	f.due(*created.ResourceID)
	if outcome := f.evaluate(); outcome.Outcome != "evaluated" {
		t.Fatalf("terminal recovery evaluation=%+v", outcome)
	}
	episode, err = f.store.Episode(f.ctx, f.reader(), episode.ID)
	if err != nil || episode.Status != "resolved" || episode.ResolveReason == nil || *episode.ResolveReason != "recovered" {
		t.Fatalf("terminal cash refund did not recover alert: %+v %v", episode, err)
	}
}

func TestPostgresCashRefundAlertSourceUpperBoundAndIndex(t *testing.T) {
	f := integrationWithCashRefundHistory(t, true, 1001)
	var exists, lower bool
	var at time.Time
	var count string
	var oldest *time.Time
	var reference *string
	if err := f.app.QueryRow(f.ctx, cashRefundSourceSQL, f.tenant, int64(600)).Scan(&exists, &at, &count, &lower, &oldest, &reference); err != nil || !exists || count != "1001" || !lower || oldest == nil || reference == nil {
		t.Fatalf("cash-refund upper-bound source exists=%v count=%s lower=%v oldest=%v reference=%v err=%v", exists, count, lower, oldest, reference, err)
	}
	var invalid int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM cash_refund_orders WHERE NOT wallet_cash_refund_order_valid(id)`).Scan(&invalid); err != nil || invalid != 0 {
		t.Fatalf("migrated cash-refund sources are invalid: %d %v", invalid, err)
	}
	var raw []byte
	if err := f.app.QueryRow(f.ctx, `EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) `+cashRefundSourceSQL, f.tenant, int64(600)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plan []map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	scans := 0
	walkAlertPlan(plan[0]["Plan"].(map[string]any), func(node map[string]any) {
		if node["Relation Name"] != "cash_refund_orders" {
			return
		}
		scans++
		if node["Index Name"] != "cash_refund_pending_alert" || node["Actual Rows"] != float64(1001) || !strings.Contains(fmt.Sprint(node["Index Cond"]), "approved_at") || node["Actual Loops"] != float64(1) {
			t.Errorf("cash-refund source is not index bounded: %v", node)
		}
	})
	if scans != 1 {
		t.Fatalf("expected one cash-refund source scan, got %d", scans)
	}

	thousand := integrationWithCashRefundHistory(t, true, 1000)
	if err := thousand.app.QueryRow(thousand.ctx, cashRefundSourceSQL, thousand.tenant, int64(600)).Scan(&exists, &at, &count, &lower, &oldest, &reference); err != nil || !exists || count != "1000" || lower || oldest == nil || reference == nil {
		t.Fatalf("cash-refund exact-1000 source exists=%v count=%s lower=%v err=%v", exists, count, lower, err)
	}
}
