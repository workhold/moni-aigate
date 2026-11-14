package billing

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func creditInputForTest(tenant, actor, business string, amount Money, bonus Money, basis string, fiat FiatMoney) CreditInput {
	return CreditInput{TenantID: tenant, ActorID: actor, BusinessRef: business, ProofRef: "proof:034", Amount: amount, BonusAmount: bonus, BasisKind: basis, FiatCurrency: "CNY", FiatAmount: fiat, TransferRef: "transfer:034", FxNote: "manual settlement", IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString()}
}

func TestPostgresCreditPaidBonusGrantUnknownAndReplay(t *testing.T) {
	f := fundingFixture(t)
	f.tenant = accountBatchTenant(t, f)
	f.key = uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, f.tenant, f.user); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, f.tenant, f.model); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'034','034-key',$4,$5,1000000000000)`, f.key, f.tenant, f.user, f.key, []string{f.model}); err != nil {
		t.Fatal(err)
	}
	in := creditInputForTest(f.tenant, f.operator, "paid-bonus", 100, 20, "paid", 90)
	created, err := f.store.CreateCredit(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: created.ID, ActorID: f.approver})
	if err != nil || approved.State != "approved" || approved.JournalID == "" {
		t.Fatalf("paid+bonus approval failed: %+v %v", approved, err)
	}
	var paid, bonus FundingReceipt
	rows, err := f.pool.Query(f.ctx, `SELECT id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at,basis_kind,coalesce(fiat_currency,''),fiat_amount,parent_ref FROM funding_receipts WHERE parent_ref=$1 ORDER BY source_kind`, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var r FundingReceipt
		if err = rows.Scan(&r.ID, &r.TenantID, &r.SourceKind, &r.SourceID, &r.Amount, &r.Currency, &r.JournalID, &r.CreatedAt, &r.BasisKind, &r.FiatCurrency, &r.FiatAmount, &r.ParentRef); err != nil {
			t.Fatal(err)
		}
		if r.Currency != "COIN" || r.ParentRef != created.ID {
			t.Fatalf("wrong receipt identity: %+v", r)
		}
		switch r.SourceKind {
		case FundingManualCredit:
			paid = r
		case FundingManualBonus:
			bonus = r
		default:
			t.Fatalf("unexpected source kind: %s", r.SourceKind)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if paid.ID == "" || bonus.ID == "" || paid.Amount != 100 || bonus.Amount != 20 || paid.BasisKind != "paid" || bonus.BasisKind != "grant" || paid.FiatAmount == nil || *paid.FiatAmount != 90 || bonus.FiatAmount != nil || !bonus.CreatedAt.After(paid.CreatedAt) || paid.JournalID == bonus.JournalID {
		t.Fatalf("paid/bonus contract mismatch: paid=%+v bonus=%+v", paid, bonus)
	}
	if paid.JournalID != approved.JournalID {
		t.Fatal("paid journal is not the primary journal")
	}
	var journalKinds int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM journals WHERE id IN ($1,$2) AND kind IN ('credit','credit_bonus')`, paid.JournalID, bonus.JournalID).Scan(&journalKinds); err != nil || journalKinds != 2 {
		t.Fatalf("expected two journals, got %d %v", journalKinds, err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: created.ID, ActorID: f.approver}); err != nil {
		t.Fatal("idempotent approval failed", err)
	}
	var receipts, journals, outbox int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM funding_receipts WHERE parent_ref=$1),(SELECT count(*) FROM journals WHERE id IN ($2,$3)),(SELECT count(*) FROM projection_outbox o JOIN journals j ON j.operation_id=o.operation_id WHERE j.id IN ($2,$3))`, created.ID, paid.JournalID, bonus.JournalID).Scan(&receipts, &journals, &outbox); err != nil || receipts != 2 || journals != 2 || outbox != 2 {
		t.Fatalf("approval replay duplicated facts: receipts=%d journals=%d outbox=%d err=%v", receipts, journals, outbox, err)
	}
	check, err := f.store.VerifyFunding(f.ctx, f.tenant)
	if err != nil || !check.Balanced {
		var invalid, kind string
		_ = f.pool.QueryRow(f.ctx, `SELECT id,kind FROM journals WHERE NOT funding_journal_valid(id) LIMIT 1`).Scan(&invalid, &kind)
		t.Fatalf("paired funding is unbalanced: %+v %v invalid=%s kind=%s", check, err, invalid, kind)
	}
	wallet, err := f.store.CheckWalletSources(f.ctx, f.tenant)
	if err != nil || !wallet.Balanced {
		var event, kind string
		_ = f.pool.QueryRow(f.ctx, `SELECT id,kind FROM funding_lot_events WHERE NOT wallet_event_valid(id) LIMIT 1`).Scan(&event, &kind)
		t.Fatalf("paired wallet sources are unbalanced: %+v %v event=%s kind=%s", wallet, err, event, kind)
	}

	meterquote.PublishDefaultPrice(t, f.ctx, f.pool, meterquote.DefaultPrice{ModelID: f.model, Actor: f.admin, InputRate: 500000, OutputRate: 100000, EffectiveAt: time.Now().UTC().Add(-time.Hour)})
	first := f.input()
	first.InputUpperBound, first.OutputLimit = 100, 10
	reservation, err := f.store.Reserve(f.ctx, first)
	if err != nil || reservation.Amount != 51 {
		t.Fatalf("first paid reservation failed: %+v %v", reservation, err)
	}
	second := f.input()
	second.RequestID, second.IdempotencyKey, second.RequestHMAC = uuid.NewString(), uuid.NewString(), uuid.NewString()
	second.InputUpperBound, second.OutputLimit = 100, 10
	reservation2, err := f.store.Reserve(f.ctx, second)
	if err != nil || reservation2.Amount != 51 {
		t.Fatalf("second paid/bonus reservation failed: %+v %v", reservation2, err)
	}
	var paidReserved, bonusReserved int64
	var firstPaid, firstBonus int64
	_ = f.pool.QueryRow(f.ctx, `SELECT coalesce(sum(a.amount) FILTER(WHERE l.source_kind='manual_credit'),0),coalesce(sum(a.amount) FILTER(WHERE l.source_kind='manual_bonus'),0) FROM funding_reservation_lots a JOIN funding_lots l ON l.id=a.lot_id WHERE a.reservation_id=$1`, reservation.ID).Scan(&firstPaid, &firstBonus)
	if err = f.pool.QueryRow(f.ctx, `SELECT coalesce(sum(a.amount) FILTER(WHERE l.source_kind='manual_credit'),0),coalesce(sum(a.amount) FILTER(WHERE l.source_kind='manual_bonus'),0) FROM funding_reservation_lots a JOIN funding_lots l ON l.id=a.lot_id WHERE a.reservation_id=$1`, reservation2.ID).Scan(&paidReserved, &bonusReserved); err != nil || paidReserved != 49 || bonusReserved != 2 {
		var available, held, consumed int64
		_ = f.pool.QueryRow(f.ctx, `SELECT coalesce(sum(available_amount),0),coalesce(sum(held_amount),0),coalesce(sum(consumed_amount),0) FROM funding_lot_balances WHERE tenant_id=$1`, f.tenant).Scan(&available, &held, &consumed)
		t.Fatalf("paid source was not consumed before bonus: first=%d/%d second=%d/%d balances=%d/%d/%d err=%v", firstPaid, firstBonus, paidReserved, bonusReserved, available, held, consumed, err)
	}
	f.wallet(t, 18, 102)
}

func TestPostgresCreditGrantUnknownSelfServiceAndSingleOperator(t *testing.T) {
	f := fundingFixture(t)
	grantTenant := accountBatchTenant(t, f)
	grantIn := creditInputForTest(grantTenant, f.operator, "pure-grant", 0, 50, "grant", 0)
	grantIn.FiatCurrency = ""
	grant, err := f.store.CreateCredit(f.ctx, grantIn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: grantTenant, OrderID: grant.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	var grantKind, grantJournal string
	var grantAmount int64
	if err = f.pool.QueryRow(f.ctx, `SELECT source_kind,journal_id,amount FROM funding_receipts WHERE parent_ref=$1`, grant.ID).Scan(&grantKind, &grantJournal, &grantAmount); err != nil || grantKind != "manual_bonus" || grantAmount != 50 {
		t.Fatalf("pure grant source mismatch: %s %d %v", grantKind, grantAmount, err)
	}
	var paidCount int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_receipts WHERE parent_ref=$1 AND source_kind='manual_credit'`, grant.ID).Scan(&paidCount); err != nil || paidCount != 0 {
		t.Fatal("pure grant created a paid source")
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT kind FROM journals WHERE id=$1`, grantJournal).Scan(&grantKind); err != nil || grantKind != "credit_bonus" {
		t.Fatalf("pure grant journal kind mismatch: %s %v", grantKind, err)
	}

	unknownTenant := accountBatchTenant(t, f)
	unknownIn := creditInputForTest(unknownTenant, f.operator, "unknown-basis", 70, 0, "unknown", 0)
	unknownIn.FiatCurrency = ""
	unknown, err := f.store.CreateCredit(f.ctx, unknownIn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: unknownTenant, OrderID: unknown.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	var unknownKind, unknownClearing string
	if err = f.pool.QueryRow(f.ctx, `SELECT f.source_kind,a.kind FROM funding_receipts f JOIN journals j ON j.id=f.journal_id JOIN journal_lines l ON l.journal_id=j.id JOIN ledger_accounts a ON a.id=l.account_id WHERE f.parent_ref=$1 AND a.tenant_id IS NULL`, unknown.ID).Scan(&unknownKind, &unknownClearing); err != nil || unknownKind != "manual_unknown" || unknownClearing != "OPERATING_GRANT_CLEARING" {
		t.Fatalf("unknown source mismatch: %s %s %v", unknownKind, unknownClearing, err)
	}

	request, err := f.store.CreateWalletRequest(f.ctx, WalletRequestInput{TenantID: f.tenant, ActorID: f.user, Kind: "topup", Note: "bank transfer self-service", IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString()})
	if err != nil || request.State != "open" {
		t.Fatalf("self-service request was not open: %+v %v", request, err)
	}
	var effects int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM credit_orders WHERE business_ref='self-service')+(SELECT count(*) FROM journals WHERE operation_id=$1)`, request.ID).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("self-service created wallet effects: %d %v", effects, err)
	}
	maker := creditInputForTest(f.tenant, f.operator, "self-service", 25, 0, "paid", 25)
	maker.RequestID = request.ID
	pending, err := f.store.CreateCredit(f.ctx, maker)
	if err != nil || pending.State != "pending" || pending.RequestedBy != f.user || pending.RequestID != request.ID {
		t.Fatalf("maker link failed: %+v %v", pending, err)
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT state FROM wallet_requests WHERE id=$1`, request.ID).Scan(&request.State); err != nil || request.State != "linked" {
		t.Fatalf("request was not linked without wallet effects: %+v %v", request, err)
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM funding_receipts WHERE parent_ref=$1)+(SELECT count(*) FROM journals WHERE operation_id=$1)`, pending.ID).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("linked request created wallet effects before approval: %d %v", effects, err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: pending.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT state FROM wallet_requests WHERE id=$1 AND linked_order_id=$2`, request.ID, pending.ID).Scan(&request.State); err != nil || request.State != "closed" {
		t.Fatalf("approval did not close the linked request: %+v %v", request, err)
	}

	adminOrder, err := f.store.CreateCredit(f.ctx, creditInputForTest(f.tenant, f.admin, "admin-single", 10, 0, "paid", 10))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: adminOrder.ID, ActorID: f.admin}); err != nil {
		t.Fatal("admin single-operator approval failed", err)
	}
	var exception bool
	if err = f.pool.QueryRow(f.ctx, `SELECT coalesce(sanitized_diff->>'single_operator_exception','false')::boolean FROM audit_events WHERE action='credit.approved' AND target=$1`, adminOrder.ID).Scan(&exception); err != nil || !exception {
		t.Fatal("single-operator exception was not audited")
	}
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO wallet_operator_settings(id,version,single_operator,actor,reason) VALUES($1,2,false,$2,'disable for test')`, uuid.NewString(), f.admin); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CreateCredit(f.ctx, creditInputForTest(f.tenant, f.admin, "admin-disabled", 11, 0, "paid", 11)); err == nil {
		t.Fatal("admin operation was allowed after disabling the switch")
	}
	check, err := f.store.VerifyFunding(f.ctx, f.tenant)
	if err != nil || !check.Balanced {
		t.Fatalf("disabling the switch invalidated an approved historical order: %+v %v", check, err)
	}
}

func TestPostgresCreditDirectSQLMissingBonusRollsBack(t *testing.T) {
	f := fundingFixture(t)
	order, err := f.store.CreateCredit(f.ctx, creditInputForTest(f.tenant, f.operator, "missing-bonus", 40, 15, "paid", 40))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	paid, err := writeCreditSourceTx(f.ctx, tx, CreditOrder{ID: order.ID, TenantID: order.TenantID, Amount: 40, BonusAmount: 15, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: orderFiat(40)}, FundingScope{TenantID: f.tenant, ActorID: f.approver}, FundingManualCredit, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE credit_orders SET state='approved',approver=$2,journal_id=$3,single_operator=wallet_single_operator() WHERE id=$1`, order.ID, f.approver, paid.JournalID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err == nil {
		t.Fatal("approved credit with a missing bonus committed")
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_receipts WHERE parent_ref=$1`, order.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("missing-bonus transaction left committed facts: %d %v", count, err)
	}
}

func orderFiat(v int64) *FiatMoney { return (*FiatMoney)(&v) }

func TestPostgresCreditFixedClockConcurrentTextAndMediaFIFO(t *testing.T) {
	f := fundingFixture(t)
	f.tenant = accountBatchTenant(t, f)
	f.key = uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, f.tenant, f.user); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, f.tenant, f.model); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'034-fixed','034-fixed-key',$4,$5,1000000000000)`, f.key, f.tenant, f.user, f.key, []string{f.model}); err != nil {
		t.Fatal(err)
	}
	order, err := f.store.CreateCredit(f.ctx, creditInputForTest(f.tenant, f.operator, "fixed-clock", 100, 20, "paid", 100))
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	paid, err := writeCreditSourceTx(f.ctx, tx, order, FundingScope{TenantID: f.tenant, ActorID: f.approver}, FundingManualCredit, fixed)
	if err != nil {
		t.Fatal(err)
	}
	bonus, err := writeCreditSourceTx(f.ctx, tx, order, FundingScope{TenantID: f.tenant, ActorID: f.approver}, FundingManualBonus, fixed)
	if err != nil {
		t.Fatal(err)
	}
	if !paid.CreatedAt.Equal(fixed) || !bonus.CreatedAt.Equal(fixed.Add(time.Microsecond)) {
		t.Fatalf("fixed clock timestamps: paid=%s bonus=%s want=%s/%s", paid.CreatedAt, bonus.CreatedAt, fixed, fixed.Add(time.Microsecond))
	}
	if _, err = tx.Exec(f.ctx, `UPDATE credit_orders SET state='approved',approver=$2,journal_id=$3,single_operator=wallet_single_operator() WHERE id=$1`, order.ID, f.approver, paid.JournalID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}

	meterquote.PublishDefaultPrice(t, f.ctx, f.pool, meterquote.DefaultPrice{ModelID: f.model, Actor: f.admin, InputRate: 500000, OutputRate: 100000, EffectiveAt: time.Now().UTC().Add(-time.Hour)})
	type reserveResult struct {
		reservation Reservation
		err         error
	}
	start := make(chan struct{})
	results := make(chan reserveResult, 2)
	for i := 0; i < 2; i++ {
		input := f.input()
		input.InputUpperBound, input.OutputLimit = 100, 10
		go func() {
			<-start
			reservation, err := f.store.Reserve(f.ctx, input)
			results <- reserveResult{reservation, err}
		}()
	}
	close(start)
	reservations := make([]Reservation, 0, 2)
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		reservations = append(reservations, result.reservation)
	}
	rows, err := f.pool.Query(f.ctx, `SELECT a.reservation_id,coalesce(sum(a.amount) FILTER(WHERE l.source_kind='manual_credit'),0),coalesce(sum(a.amount) FILTER(WHERE l.source_kind='manual_bonus'),0) FROM funding_reservation_lots a JOIN funding_lots l ON l.id=a.lot_id WHERE a.reservation_id=ANY($1::text[]) GROUP BY a.reservation_id ORDER BY 2 DESC`, []string{reservations[0].ID, reservations[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type allocation struct {
		reservation string
		paid, bonus int64
	}
	allocations := []allocation{}
	for rows.Next() {
		var a allocation
		if err = rows.Scan(&a.reservation, &a.paid, &a.bonus); err != nil {
			t.Fatal(err)
		}
		allocations = append(allocations, a)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 2 || allocations[0].paid != 51 || allocations[0].bonus != 0 || allocations[1].paid != 49 || allocations[1].bonus != 2 {
		t.Fatalf("concurrent paid-before-bonus allocations: %+v", allocations)
	}
	for _, reservation := range reservations {
		if reservation.ID == allocations[1].reservation {
			if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: reservation.RequestID, Reason: "not_sent"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Releasing only the second request leaves the first 51-unit paid hold in
	// place. A 60-unit media reservation must cross from 49 paid into bonus.
	digest := sha256.Sum256([]byte("fixed-clock-media"))
	media, err := f.store.ReserveMedia(f.ctx, MediaReserveInput{TenantID: f.tenant, UserID: f.user, KeyID: f.key, RequestID: uuid.NewString(), AttemptID: uuid.NewString(), Operation: "tts", ModelKey: "mock-tts", ProfileVersion: "22222222-2222-4222-8222-222222222222", IdempotencyKey: uuid.NewString(), RequestDigest: digest[:], QuantityBound: 60, Deadline: time.Now().UTC().Add(time.Minute)})
	if err != nil || media.Amount != 60 {
		t.Fatalf("media paired-source reservation: %+v %v", media, err)
	}
	var mediaPaid, mediaBonus int64
	if err = f.pool.QueryRow(f.ctx, `SELECT coalesce(sum(a.amount) FILTER(WHERE l.source_kind='manual_credit'),0),coalesce(sum(a.amount) FILTER(WHERE l.source_kind='manual_bonus'),0) FROM media_reservation_lots a JOIN funding_lots l ON l.id=a.lot_id WHERE a.reservation_id=$1`, media.ID).Scan(&mediaPaid, &mediaBonus); err != nil || mediaPaid != 49 || mediaBonus != 11 {
		t.Fatalf("media paid-before-bonus allocation=%d/%d %v", mediaPaid, mediaBonus, err)
	}
	f.wallet(t, 9, 111)
	f.check(t)
}
