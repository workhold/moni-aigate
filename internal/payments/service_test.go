package payments

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func TestPaymentCursorStrictBinding(t *testing.T) {
	at := time.Date(2026, 11, 1, 0, 0, 0, 123456000, time.UTC)
	id := uuid.NewString()
	filter := paymentFilter("platform", "sku", "")
	cursor := encodePaymentCursor(at, id, filter)
	got, gotID, e := decodePaymentCursor(*cursor, filter)
	if e != nil || !got.Equal(at) || gotID != id {
		t.Fatal("cursor roundtrip", e)
	}
	bad := []string{*cursor + "=", strings.Repeat("x", 1025), base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"v":1,"created_at":"2026-11-01T00:00:00.123456Z","id":"` + id + `","filter":"` + filter + `"}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"created_at":null,"id":"` + id + `","filter":"` + filter + `"}`))}
	for _, raw := range bad {
		_, _, e := decodePaymentCursor(raw, filter)
		serviceCode(t, e, "invalid_cursor")
	}
	_, _, e = decodePaymentCursor(*cursor, paymentFilter("tenant:"+uuid.NewString(), "sku", ""))
	serviceCode(t, e, "invalid_cursor")
}
func TestPaymentCommandDigestBindsTargetAndNoSecrets(t *testing.T) {
	spec := paymentCommandSpec{meta: CommandMeta{OperationID: uuid.NewString(), ActorID: uuid.NewString()}, kind: "payment.sku.activate", target: uuid.NewString(), body: struct {
		Enabled bool `json:"enabled"`
	}{true}}
	a, e := paymentCommandDigest(spec)
	if e != nil {
		t.Fatal(e)
	}
	spec.target = uuid.NewString()
	b, _ := paymentCommandDigest(spec)
	if a == b {
		t.Fatal("path target omitted from digest")
	}
	raw, _ := json.Marshal(OrderView{})
	for _, bad := range []string{"canonical_body", "key_id", "provider_transaction", "parameter_digest", "signature"} {
		if strings.Contains(string(raw), bad) {
			t.Fatal("unsafe DTO field", bad)
		}
	}
}
func TestPostgresPaymentServiceCommandsAndCurrentAuthority(t *testing.T) {
	f := paymentServiceFixture(t)
	s := f.service
	in := CreateOrderInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version}
	first, e := s.CreateOrder(f.ctx, in)
	if e != nil || !first.Created || first.Order == nil || first.Order.CreationState != "queued" || first.Order.FundingState != "unconfirmed" {
		t.Fatal("initial order", e, first.Order)
	}
	replay, e := s.CreateOrder(f.ctx, in)
	if e != nil || replay.Created || replay.Order == nil || replay.Receipt.OperationID != first.Receipt.OperationID {
		t.Fatal("order replay", e)
	}
	in.SKUVersionID = uuid.NewString()
	_, e = s.CreateOrder(f.ctx, in)
	serviceCode(t, e, "commerce_operation_conflict")
	in.SKUVersionID = f.version
	if _, e = s.GetWalletOrder(f.ctx, Reader{ActorID: f.viewer, TenantID: f.tenant}, in.OperationID); e != nil {
		t.Fatal("viewer read", e)
	}
	_, e = s.GetWalletOrder(f.ctx, Reader{ActorID: f.member, TenantID: f.tenant}, in.OperationID)
	serviceCode(t, e, "forbidden")
	viewerOp := uuid.NewString()
	_, e = s.CreateOrder(f.ctx, CreateOrderInput{CommandMeta: CommandMeta{OperationID: viewerOp, ActorID: f.viewer, TenantID: f.tenant}, SKUVersionID: f.version})
	serviceCode(t, e, "forbidden")
	var count int
	if e = f.app.QueryRow(f.ctx, `SELECT count(*) FROM commerce_commands WHERE operation_id=$1`, viewerOp).Scan(&count); e != nil || count != 0 {
		t.Fatal("denied command survived", e, count)
	}
	if _, e = s.GetPlatformSKU(f.ctx, f.finance, f.sku); e != nil {
		t.Fatal("finance read", e)
	}
	_, e = s.PublishSKU(f.ctx, PublishSKUInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.finance}, SKUID: uuid.NewString(), ExpectedRevision: 0, DisplayName: "denied", Amount: 1})
	serviceCode(t, e, "forbidden")
	noop, e := s.ActivateSKU(f.ctx, ActivateSKUInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.admin}, SKUID: f.sku, ExpectedRevision: 2, VersionID: &f.version, Enabled: true})
	if e != nil || noop.Status != "accepted" {
		t.Fatal("activation no-op", e)
	}
	sku, e := s.GetPlatformSKU(f.ctx, f.admin, f.sku)
	if e != nil || sku.Revision != "2" {
		t.Fatal("no-op revision changed", e, sku.Revision)
	}
	missing := uuid.NewString()
	rejected, e := s.PublishSKU(f.ctx, PublishSKUInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.admin}, SKUID: missing, ExpectedRevision: 1, DisplayName: "bad CAS", Amount: 1})
	if e != nil || rejected.Created || rejected.Receipt.Status != "rejected" || rejected.Receipt.Reason != "payment_revision_conflict" {
		t.Fatal("CAS rejection", e, rejected)
	}
	if e = f.app.QueryRow(f.ctx, `SELECT count(*) FROM payment_skus WHERE id=$1`, missing).Scan(&count); e != nil || count != 0 {
		t.Fatal("rejected root effect", e)
	}
	serviceExec(t, f.app, f.ctx, `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.user)
	_, e = s.CreateOrder(f.ctx, CreateOrderInput{CommandMeta: CommandMeta{OperationID: in.OperationID, ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version})
	serviceCode(t, e, "forbidden")
	if e = f.app.QueryRow(f.ctx, `SELECT count(*) FROM funding_receipts`).Scan(&count); e != nil || count != 0 {
		t.Fatal("human command created money", e, count)
	}
}
func TestPostgresPaymentServiceOffRecoveryPolicyAndCancel(t *testing.T) {
	f := paymentServiceFixture(t)
	order := f.order(t)
	s := f.service
	pview, e := s.UpdatePolicy(f.ctx, UpdatePolicyInput{ActorID: f.admin, ExpectedRevision: 2, Enabled: true})
	if e != nil || pview.Revision != "2" {
		t.Fatal("policy no-op", e, pview)
	}
	_, e = s.UpdatePolicy(f.ctx, UpdatePolicyInput{ActorID: f.admin, ExpectedRevision: 1, Enabled: true})
	serviceCode(t, e, "payment_policy_conflict")
	off := *s
	offRuntime := &paymentRuntime{pool: f.app, options: f.appOptions}
	offRuntime.options.lifecycle.Mode = ModeOff
	off.runtime = offRuntime
	caps, e := off.WalletCapabilities(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant})
	if e != nil || caps.Mode != "off" || caps.Environment == nil || !caps.LocalSimulation || caps.PaymentsEnabled {
		t.Fatal("proved historical off capabilities", e, caps)
	}
	_, e = off.CreateOrder(f.ctx, CreateOrderInput{CommandMeta: CommandMeta{OperationID: order.ID, ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version})
	serviceCode(t, e, "payments_runtime_disabled")
	_, e = off.UpdatePolicy(f.ctx, UpdatePolicyInput{ActorID: f.admin, ExpectedRevision: 2, Enabled: true})
	serviceCode(t, e, "payments_runtime_disabled")
	cancelIn := CancelOrderInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.user, TenantID: f.tenant}, OrderID: order.ID}
	receipt, e := off.CancelOrder(f.ctx, cancelIn)
	if e != nil || receipt.Status != "accepted" {
		t.Fatal("historical off cancel", e)
	}
	receipt, e = off.CancelOrder(f.ctx, cancelIn)
	if e != nil || receipt.Status != "accepted" {
		t.Fatal("cancel replay", e)
	}
	safe, e := off.GetWalletOrder(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, order.ID)
	if e != nil || safe.CheckoutState != "cancel_requested" {
		t.Fatal("cancel safe view", e, safe)
	}
	second, e := off.CancelOrder(f.ctx, CancelOrderInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.user, TenantID: f.tenant}, OrderID: order.ID})
	if e != nil || second.Status != "rejected" || second.Reason != "payment_state_conflict" {
		t.Fatal("second cancel accepted", e, second)
	}
	recovery := *s
	recoveryRuntime := &paymentRuntime{pool: f.app, options: f.appOptions}
	recoveryRuntime.options.lifecycle.Recovery = true
	recovery.runtime = recoveryRuntime
	_, e = recovery.CreateOrder(f.ctx, CreateOrderInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version})
	if !errors.Is(e, ErrPaymentsRecovery) {
		t.Fatal("Recovery accepted human write", e)
	}
	if _, e = recovery.GetWalletOrder(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, order.ID); e != nil {
		t.Fatal("Recovery safe read", e)
	}
	fenced := *s
	fencedRuntime := &paymentRuntime{pool: f.app, options: f.appOptions}
	fencedRuntime.options.lifecycle.Guard = func(context.Context) error { return errors.New("private fixture failure") }
	fenced.runtime = fencedRuntime
	_, e = fenced.GetPolicy(f.ctx, f.admin)
	if !errors.Is(e, ErrPaymentsGuard) {
		t.Fatal("guard allowed DB read", e)
	}
	fencedRuntime.options.lifecycle.Guard = func(context.Context) error { return nil }
	_, e = fenced.GetPolicy(f.ctx, f.admin)
	if !errors.Is(e, ErrPaymentsGuard) {
		t.Fatal("guard revived fenced process", e)
	}
}
func serviceSignedNotification(t *testing.T, f *serviceFixture, event, order string, amount string) (NotificationInput, p.Notification) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	transaction := "txn:" + order
	n := p.Notification{SchemaVersion: 1, EventID: event, EventType: "capture.confirmed", EnvironmentID: f.env.ID, MerchantID: f.env.MerchantID, OrderID: order, ProviderTransactionID: &transaction, Amount: amount, Currency: "CNY", OccurredAt: now.Add(-time.Second).Format(paymentTimeLayout), ProviderOrderRevision: "1"}
	raw, e := p.CanonicalNotification(n)
	if e != nil {
		t.Fatal(e)
	}
	h, e := f.keyring.SignNotification(f.env, p.SignedHeaders{DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, raw)
	if e != nil {
		t.Fatal(e)
	}
	return NotificationInput{Headers: h, Body: raw}, n
}
func TestPostgresPaymentServiceDurableNotificationAndConflict(t *testing.T) {
	f := paymentServiceFixture(t)
	unknown := uuid.NewString()
	input, _ := serviceSignedNotification(t, f, "event:one", unknown, "100")
	ack, first, e := f.service.ReceiveNotification(f.ctx, input)
	if e != nil || !first || ack.Status != "received" || ack.Environment != "sandbox" || !paymentID(ack.ReceiptID) {
		t.Fatal("durable ACK", e, ack)
	}
	observations, e := f.service.ListPlatformObservations(f.ctx, f.finance, unknown, Page{})
	if e != nil || len(observations.Data) != 1 || observations.Data[0].OrderID != unknown || observations.Data[0].Kind != "capture" || observations.Data[0].Status != "pending" {
		t.Fatal("unknown logical order observation hidden", e, observations)
	}
	empty, e := f.service.ListPlatformObservations(f.ctx, f.admin, uuid.NewString(), Page{})
	if e != nil || len(empty.Data) != 0 || empty.NextCursor != nil {
		t.Fatal("empty logical order observation", e, empty)
	}
	_, e = f.service.ListPlatformObservations(f.ctx, f.user, unknown, Page{})
	serviceCode(t, e, "forbidden")
	same, first, e := f.service.ReceiveNotification(f.ctx, input)
	if e != nil || first || same != ack {
		t.Fatal("duplicate ACK changed", e, same, ack)
	}
	changed, _ := serviceSignedNotification(t, f, "event:one", unknown, "101")
	_, _, e = f.service.ReceiveNotification(f.ctx, changed)
	serviceCode(t, e, "payment_event_conflict")
	for table, want := range map[string]int{"payment_inbox": 1, "payment_event_conflicts": 1, "payment_verified_evidence": 1, "payment_evidence_processing": 1, "funding_receipts": 0} {
		var n int
		if e = f.app.QueryRow(f.ctx, `SELECT count(*) FROM `+table).Scan(&n); e != nil || n != want {
			t.Fatal("notification graph", table, n, e)
		}
	}
	input.Headers.Signature = strings.Repeat("0", 64)
	_, _, e = f.service.ReceiveNotification(f.ctx, input)
	serviceCode(t, e, "payment_signature_invalid")
	recovery := *f.service
	r := &paymentRuntime{pool: f.app, options: f.appOptions}
	r.options.lifecycle.Recovery = true
	recovery.runtime = r
	next, _ := serviceSignedNotification(t, f, "event:recovery", unknown, "100")
	if _, first, e = recovery.ReceiveNotification(f.ctx, next); e != nil || !first {
		t.Fatal("Recovery rejected durable receipt", e)
	}
	tx, e := f.app.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	evidence, e := normalizeNotificationTx(f.ctx, tx, ack.ReceiptID)
	if e != nil || !paymentID(evidence) {
		t.Fatal("stored original normalizer replay", e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
}
func TestPostgresPaymentServiceConcurrentSKUAndOrderIdempotency(t *testing.T) {
	f := paymentServiceFixture(t)
	id := uuid.NewString()
	inputs := []PublishSKUInput{{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.admin}, SKUID: id, ExpectedRevision: 0, DisplayName: "first", Amount: 10}, {CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.admin}, SKUID: id, ExpectedRevision: 0, DisplayName: "second", Amount: 20}}
	var wg sync.WaitGroup
	results := make([]PublishSKUResult, 2)
	errs := make([]error, 2)
	for i := range inputs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errs[i] = f.service.PublishSKU(f.ctx, inputs[i]) }(i)
	}
	wg.Wait()
	accepted, rejected := 0, 0
	for i, r := range results {
		if errs[i] != nil {
			t.Fatal("concurrent SKU", errs[i])
		}
		switch r.Receipt.Status {
		case "accepted":
			accepted++
		case "rejected":
			if r.Receipt.Reason != "payment_revision_conflict" {
				t.Fatal(r)
			}
			rejected++
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatal("first SKU race", results)
	}
	input := CreateOrderInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version}
	orderResults := make([]OrderResult, 8)
	orderErrs := make([]error, 8)
	for i := range orderResults {
		wg.Add(1)
		go func(i int) { defer wg.Done(); orderResults[i], orderErrs[i] = f.service.CreateOrder(f.ctx, input) }(i)
	}
	wg.Wait()
	created := 0
	for i, r := range orderResults {
		if orderErrs[i] != nil || r.Receipt.Status != "accepted" {
			t.Fatal("concurrent order", orderErrs[i], r)
		}
		if r.Created {
			created++
		}
	}
	if created != 1 {
		t.Fatal("more than one first result", created)
	}
	var count int
	if e := f.app.QueryRow(f.ctx, `SELECT count(*) FROM payment_command_outbox WHERE order_id=$1 AND kind='checkout'`, input.OperationID).Scan(&count); e != nil || count != 1 {
		t.Fatal("duplicate checkout", e, count)
	}
}
func TestPostgresPaymentServicePaginationAndIsolation(t *testing.T) {
	f := paymentServiceFixture(t)
	for i := 0; i < 52; i++ {
		f.order(t)
	}
	page, e := f.service.ListWalletOrders(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, Page{})
	if e != nil || len(page.Data) != 50 || page.NextCursor == nil {
		t.Fatal("first fixed page", e, len(page.Data))
	}
	next, e := f.service.ListWalletOrders(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, Page{Cursor: *page.NextCursor})
	if e != nil || len(next.Data) != 2 || next.NextCursor != nil {
		t.Fatal("next page", e, len(next.Data))
	}
	seen := map[string]bool{}
	for _, o := range append(page.Data, next.Data...) {
		if seen[o.ID] {
			t.Fatal("page repeated row")
		}
		seen[o.ID] = true
	}
	_, e = f.service.ListPlatformOrders(f.ctx, f.admin, Page{Cursor: *page.NextCursor})
	serviceCode(t, e, "invalid_cursor")
	other := uuid.NewString()
	serviceExec(t, f.app, f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'other')`, other)
	serviceExec(t, f.app, f.ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')`, other, f.user)
	empty, e := f.service.ListWalletOrders(f.ctx, Reader{ActorID: f.user, TenantID: other}, Page{})
	if e != nil || len(empty.Data) != 0 {
		t.Fatal("cross-tenant list leaked", e)
	}
	_, e = f.service.GetWalletOrder(f.ctx, Reader{ActorID: f.user, TenantID: other}, page.Data[0].ID)
	serviceCode(t, e, "not_found")
}
func TestPostgresPaymentNormalizerRejectsCommittedOrphan(t *testing.T) {
	f := paymentServiceFixture(t)
	input, _ := serviceSignedNotification(t, f, "event:orphan", uuid.NewString(), "100")
	ack, _, e := f.service.ReceiveNotification(f.ctx, input)
	if e != nil {
		t.Fatal(e)
	}
	// Owner-only exact corruption fixture: remove both graph rows after disabling
	// their immutable/deferred triggers. The service must fail, never repair it.
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	for _, table := range []string{"payment_evidence_processing", "payment_verified_evidence"} {
		serviceExec(t, tx, f.ctx, `ALTER TABLE `+table+` DISABLE TRIGGER ALL`)
	}
	serviceExec(t, tx, f.ctx, `DELETE FROM payment_evidence_processing WHERE evidence_id IN(SELECT id FROM payment_verified_evidence WHERE inbox_id=$1)`, ack.ReceiptID)
	serviceExec(t, tx, f.ctx, `DELETE FROM payment_verified_evidence WHERE inbox_id=$1`, ack.ReceiptID)
	for _, table := range []string{"payment_evidence_processing", "payment_verified_evidence"} {
		serviceExec(t, tx, f.ctx, `ALTER TABLE `+table+` ENABLE TRIGGER ALL`)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	_, _, e = f.service.ReceiveNotification(f.ctx, input)
	if !errors.Is(e, ErrPaymentsIntegrity) {
		t.Fatal("orphan graph repaired", e)
	}
	var count int
	if e = f.app.QueryRow(f.ctx, `SELECT count(*) FROM payment_verified_evidence`).Scan(&count); e != nil || count != 0 {
		t.Fatal("orphan was changed", e, count)
	}
}

func TestPostgresPaymentServicePostCommitRevocationKeepsOutcomeUnknown(t *testing.T) {
	f := paymentServiceFixture(t)
	var checks atomic.Int32
	runtime := &paymentRuntime{pool: f.app, options: f.appOptions}
	runtime.options.lifecycle.Guard = func(ctx context.Context) error {
		// writeAllowed, transaction entry, pre-COMMIT, then safe read entry.
		if checks.Add(1) == 4 {
			_, e := f.app.Exec(ctx, `UPDATE memberships SET status='suspended' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.user)
			return e
		}
		return nil
	}
	service := &Service{pool: f.app, runtime: runtime}
	id := uuid.NewString()
	result, e := service.CreateOrder(f.ctx, CreateOrderInput{CommandMeta: CommandMeta{OperationID: id, ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version})
	if !errors.Is(e, ErrPaymentsOutcomeUnknown) || result.Created || result.Order != nil || result.Receipt.OperationID != "" {
		t.Fatal("postcommit revocation masqueraded as safe precommit denial", e, result)
	}
	stored, e := f.service.GetPlatformOrder(f.ctx, f.admin, id)
	if e != nil || stored.ID != id {
		t.Fatal("already committed order missing", e)
	}
	var count int
	if e = f.app.QueryRow(f.ctx, `SELECT count(*) FROM commerce_operation_receipts WHERE operation_id=$1 AND status='accepted'`, id).Scan(&count); e != nil || count != 1 {
		t.Fatal("postcommit order outcome lost", e, count)
	}
}

func TestPostgresPaymentServiceRejectedDecisionRetainsParentAuthorityLocks(t *testing.T) {
	f := paymentServiceFixture(t)
	if _, e := f.service.UpdatePolicy(f.ctx, UpdatePolicyInput{ActorID: f.admin, ExpectedRevision: 2, Enabled: false}); e != nil {
		t.Fatal(e)
	}
	conn, e := f.app.Acquire(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Release()
	pid := conn.Conn().PgConn().PID()
	var checks atomic.Int32
	var updater <-chan error
	blocked := false
	runtime := &paymentRuntime{pool: f.app, options: f.appOptions}
	runtime.options.lifecycle.Guard = func(ctx context.Context) error {
		if checks.Add(1) != 3 {
			return nil
		}
		done := make(chan error, 1)
		updater = done
		go func() {
			updateCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, e := conn.Exec(updateCtx, `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.user)
			done <- e
		}()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			select {
			case e := <-done:
				done <- e
				return errors.New("authority lock was released before rejected terminal")
			default:
			}
			var waiting bool
			if e := f.owner.QueryRow(ctx, `SELECT cardinality(pg_blocking_pids($1))>0`, pid).Scan(&waiting); e != nil {
				return e
			}
			if waiting {
				blocked = true
				return nil
			}
			time.Sleep(time.Millisecond)
		}
		return errors.New("did not observe rejected parent lock")
	}
	service := &Service{pool: f.app, runtime: runtime}
	result, e := service.CreateOrder(f.ctx, CreateOrderInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version})
	if e != nil || !blocked || result.Receipt.Status != "rejected" || result.Receipt.Reason != "payments_disabled" {
		t.Fatal("rejected current authority boundary", e, blocked, result)
	}
	if updater == nil {
		t.Fatal("updater did not run")
	}
	select {
	case e := <-updater:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("authority lock leaked past commit")
	}
}
