package fundinglotscan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

// These are untrusted owner-RR reader tests. They never issue a Permit, strict
// runtime proof, maintenance initialization capability, or production writer.
type walletPGFixture struct {
	*completeSourcePGFixture
	tenant, epoch, opening, lot, event, genesis string
}
type walletPGQuery struct {
	ctx    context.Context
	tx     pgx.Tx
	schema string
	calls  map[string]int
}

func (q *walletPGQuery) Query(sql string, args ...any) (Rows, error) {
	for _, group := range [][]binding{fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings(), walletBindings()} {
		for _, b := range group {
			if strings.Contains(sql, " FROM ONLY "+pgx.Identifier{q.schema, b.table}.Sanitize()+" AS s ") {
				q.calls[b.table]++
				rows, err := q.tx.Query(q.ctx, sql, append([]any{pgx.QueryExecModeSimpleProtocol}, args...)...)
				if err != nil {
					return nil, err
				}
				return completeSourcePGRows{rows: rows}, nil
			}
		}
	}
	return nil, ErrExecution
}
func (f *walletPGFixture) query(tx pgx.Tx) *walletPGQuery {
	return &walletPGQuery{ctx: f.ctx, tx: tx, schema: f.schema, calls: map[string]int{}}
}
func (f *walletPGFixture) read(t *testing.T, tx pgx.Tx) WalletFacts {
	t.Helper()
	q := f.query(tx)
	facts, err := ReadWallet(f.ctx, q, f.schema, ModeOrdinary)
	completeSourcePGMust(t, "read original source-wallet union", err)
	if !facts.Summary().PhysicalEOFComplete || facts.Summary().ScannedTables != 84 || len(q.calls) != 84 {
		t.Fatal("owner RR omitted a physical source/C0 table")
	}
	for _, n := range q.calls {
		if n != 1 {
			t.Fatal("small original shared table read more than once")
		}
	}
	return facts
}
func newWalletPGFixture(t *testing.T) *walletPGFixture {
	t.Helper()
	f := &walletPGFixture{completeSourcePGFixture: newCompleteSourcePGFixture(t), tenant: uuid.NewString(), epoch: uuid.NewString(), opening: uuid.NewString(), lot: uuid.NewString(), event: uuid.NewString(), genesis: uuid.NewString()}
	maker, checker := uuid.NewString(), uuid.NewString()
	_, err := f.owner.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'Wallet reader fixture')`, f.tenant)
	completeSourcePGMust(t, "seed original tenant", err)
	_, err = f.owner.Exec(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$1||'@example.invalid','fixture-only','finance_operator'),($2,$2||'@example.invalid','fixture-only','finance_approver')`, maker, checker)
	completeSourcePGMust(t, "seed distinct finance actors", err)
	store := billing.New(f.owner)
	credit, err := store.CreateCredit(f.ctx, billing.CreditInput{TenantID: f.tenant, ActorID: maker, Amount: 40, BusinessRef: "wallet-reader:" + uuid.NewString(), ProofRef: "fixture:verified-bank-transfer", IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString()})
	completeSourcePGMust(t, "create original manual credit through API", err)
	approved, err := store.ApproveCredit(f.ctx, billing.ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: checker})
	completeSourcePGMust(t, "approve original manual credit through API", err)
	if approved.State != "approved" || approved.JournalID == "" {
		t.Fatal("original API did not commit funded history")
	}
	ddl, err := os.ReadFile(filepath.Join("..", "..", "specs", "022-self-service-payments", "c0-production-schema.sql"))
	h := sha256.Sum256(ddl)
	if err != nil || hex.EncodeToString(h[:]) != "e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047" {
		t.Fatal("raw-wallet fixture candidate DDL changed without review")
	}
	_, err = f.owner.Exec(f.ctx, string(ddl))
	completeSourcePGMust(t, "install unchanged candidate C0 DDL in isolated schema", err)
	tx := f.begin(t, pgx.ReadWrite)
	var at time.Time
	completeSourcePGMust(t, "sample exact opening time", tx.QueryRow(f.ctx, `SELECT date_trunc('microseconds',clock_timestamp())`).Scan(&at))
	// The source API committed 40. All expected principal and buckets below
	// are handwritten; no writer planner or SQL business checker supplies them.
	f.exec(t, tx, `INSERT INTO funding_lot_legacy_receipts(receipt_id,epoch_id,tenant_id,source_kind,source_id,amount,journal_id)
 SELECT id,$1,tenant_id,'manual_credit',$2,40,journal_id FROM funding_receipts WHERE source_id=$2`, f.epoch, credit.ID)
	f.exec(t, tx, `INSERT INTO funding_wallet_legacy_sources(epoch_id,tenant_id,kind,source_id,journal_id,journal_operation_id,amount)
 SELECT $1,tenant_id,'manual_credit',id,journal_id,$2,40 FROM funding_receipts WHERE source_id=$2`, f.epoch, credit.ID)
	f.exec(t, tx, `INSERT INTO funding_lot_legacy_openings(id,epoch_id,tenant_id,kind,account_id,amount,observed_at)
 SELECT $1,$2,$3,'available',id,40,$4 FROM ledger_accounts WHERE tenant_id=$3 AND kind='AVAILABLE'`, f.opening, f.epoch, f.tenant, at)
	f.exec(t, tx, `INSERT INTO funding_wallet_operations(id,tenant_id,epoch_id,seq,kind,source_id,event_count,available_delta,held_delta,consumed_delta,issued_delta)
 VALUES($1,$2,$3,0,'epoch_opening',$2,1,40,0,0,40)`, f.genesis, f.tenant, f.epoch)
	f.exec(t, tx, `INSERT INTO funding_wallet_heads(tenant_id,epoch_id,tail_seq,tail_operation_id) VALUES($1,$2,0,$3)`, f.tenant, f.epoch, f.genesis)
	f.exec(t, tx, `INSERT INTO funding_lots(id,tenant_id,origin_kind,legacy_opening_id,issued_amount,currency,refund_class,fifo_at)
 VALUES($1,$2,'legacy_available',$3,40,'CNY','nonrefundable',$4)`, f.lot, f.tenant, f.opening, at)
	f.exec(t, tx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,wallet_operation_id,wallet_seq,kind,before_revision,after_revision,before_available,before_held,before_consumed,after_available,after_held,after_consumed,available_delta,inference_held_delta,consumed_delta,issued_delta)
 VALUES($1,$2,$3,$4,0,'legacy_opening',0,1,0,0,0,40,0,0,40,0,0,40)`, f.event, f.tenant, f.lot, f.genesis)
	f.exec(t, tx, `INSERT INTO funding_lot_balances(lot_id,tenant_id,available_amount,inference_held_amount,consumed_amount,revision,last_event_id) VALUES($1,$2,40,0,0,1,$3)`, f.lot, f.tenant, f.event)
	proof := []byte(`{"scope":"reader_fixture_only_not_runtime_proof"}`)
	f.exec(t, tx, `INSERT INTO funding_lot_runtime_attestations(singleton,epoch_id,proof_version,mode,database_oid,schema_oid,schema_owner_oid,app_role_oid,projector_role_oid,proof_canonical,proof_sha256,manifest_digest,descriptor_digest,created_at)
 SELECT true,$1,2,'ordinary',(SELECT oid FROM pg_database WHERE datname=current_database()),current_schema()::regnamespace::oid,
 (SELECT oid FROM pg_roles WHERE rolname=current_user),(SELECT oid FROM pg_roles WHERE rolname='pg_read_all_data'),(SELECT oid FROM pg_roles WHERE rolname='pg_write_all_data'),$2,encode(sha256($2::bytea),'hex'),repeat('b',64),repeat('c',64),$3`, f.epoch, proof, at)
	f.exec(t, tx, `INSERT INTO funding_lot_epoch(singleton,id,schema_version,database_oid,schema_oid,basis_digest,manifest_digest,descriptor_digest,runtime_proof_digest,tenant_count,receipt_count,settlement_count,approved_refund_count,opening_count,legacy_source_count,available_total,held_total,actor,created_at)
 SELECT true,$1,1,(SELECT oid FROM pg_database WHERE datname=current_database()),current_schema()::regnamespace::oid,repeat('a',64),repeat('b',64),repeat('c',64),encode(sha256($2::bytea),'hex'),1,1,0,0,1,1,40,0,'offline-maintenance',$3`, f.epoch, proof, at)
	// Enforce original deferred triggers/FKs before committing. This is a real
	// DDL-valid lot history, while the marker remains explicitly non-authority.
	f.exec(t, tx, "SET CONSTRAINTS ALL IMMEDIATE")
	completeSourcePGMust(t, "commit nonempty original C0 fixture", tx.Commit(f.ctx))
	return f
}

func TestPostgresSourceWalletReaderNonemptyC0(t *testing.T) {
	f := newWalletPGFixture(t)
	t.Run("same_owner_rr_eight_algorithms_and_old68_unchanged", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadOnly)
		facts := f.read(t, tx)
		r := facts.Summary()
		if r.Status != "consistent" || !r.CoverageComplete || r.InputMismatchFields != 0 || !r.Wallet.RowsConsistent || r.Wallet.Scanned.Lots != 1 || r.Wallet.Scanned.Events != 1 || r.Wallet.Scanned.LegacySources != 1 {
			t.Fatalf("real positive source+lot graph rejected: %+v", r)
		}
		for i, state := range r.Algorithms {
			want := "executed"
			if i == 6 {
				want = "not_applicable"
			}
			if state != want {
				t.Fatalf("algorithm%d=%s", i, state)
			}
		}
		old, _ := f.completeSourcePGFixture.read(t, tx, ModeOrdinary)
		if old.Summary().Status != "consistent" || old.Summary().ScannedTables != 68 || old.Summary().Ledger != r.Ledger || old.Summary().Funding != r.Funding {
			t.Fatal("84 read changed old68 supplied-source semantics")
		}
	})
	t.Run("false_conserving_cache_is_data_mismatch", func(t *testing.T) {
		tx := f.corruptionTx(t)
		f.exec(t, tx, `UPDATE funding_lot_balances SET available_amount=39,consumed_amount=1 WHERE lot_id=$1`, f.lot)
		r := f.read(t, tx).Summary()
		if r.Status != "mismatch" || r.Algorithms[7] != "executed" || r.Wallet.Mismatches.BalanceRows == 0 || r.Wallet.Unproven.ReplayPrefixUnproven != 0 {
			t.Fatalf("real false cache was not independently compared: %+v", r)
		}
	})
	t.Run("missing_event_is_not_a_scan_or_authority_error", func(t *testing.T) {
		tx := f.corruptionTx(t)
		f.exec(t, tx, `DELETE FROM funding_lot_events WHERE id=$1`, f.event)
		r := f.read(t, tx).Summary()
		if r.Status != "mismatch" || r.Wallet.Mismatches.MissingFacts == 0 || r.Algorithms[7] != "executed" {
			t.Fatalf("real missing event hidden: %+v", r)
		}
	})
	t.Run("original_rr_snapshot_excludes_later_generic_audit", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadOnly)
		before := f.read(t, tx).Summary()
		_, err := f.owner.Exec(f.ctx, `INSERT INTO audit_events(id,actor,action,target) VALUES($1,'reader-fixture','qa.wallet.reader','fixture')`, uuid.NewString())
		completeSourcePGMust(t, "commit independent generic audit", err)
		after := f.read(t, tx).Summary()
		if before != after {
			t.Fatal("source or wallet read escaped original RR snapshot")
		}
		fresh := f.begin(t, pgx.ReadOnly)
		now := f.read(t, fresh).Summary()
		if now.SourceRows != before.SourceRows+1 || !now.Wallet.RowsConsistent {
			t.Fatal("fresh RR did not observe independent commit")
		}
	})
	for _, size := range []int{65537, 262144, 262145} {
		t.Run("c0_proof_bytes_"+strconv.Itoa(size), func(t *testing.T) {
			tx := f.corruptionTx(t)
			if size > 262144 {
				f.relaxChecks(t, tx, "funding_lot_runtime_attestations")
			}
			proof := bytes.Repeat([]byte{0xff}, size)
			f.exec(t, tx, `UPDATE funding_lot_runtime_attestations SET proof_canonical=$1,proof_sha256=encode(sha256($1::bytea),'hex')`, proof)
			f.exec(t, tx, `UPDATE funding_lot_epoch SET runtime_proof_digest=encode(sha256($1::bytea),'hex')`, proof)
			q := f.query(tx)
			facts, err := ReadWallet(f.ctx, q, f.schema, ModeOrdinary)
			if size > 262144 {
				wrZero(t, facts, err, ErrResource)
				return
			}
			completeSourcePGMust(t, "read bounded original C0 bytea", err)
			if !facts.Summary().Wallet.RowsConsistent || facts.Summary().ScannedTables != 84 {
				t.Fatal("legal C0 proof bytes truncated or rejected")
			}
			got := completeSourcePGCell(t, facts.state.raw, "funding_lot_runtime_attestations", "proof_canonical", 0)
			if !got.valid || !bytes.Equal(got.data, proof) {
				t.Fatal("PG binary bytea was reencoded or aliased")
			}
		})
	}
	t.Run("native_large_year_and_xid_numeric_preserved", func(t *testing.T) {
		tx := f.corruptionTx(t)
		for _, pair := range [][2]string{{"funding_lot_epoch", "created_at"}, {"funding_lot_runtime_attestations", "created_at"}, {"funding_lot_legacy_openings", "observed_at"}, {"funding_lots", "fifo_at"}} {
			f.exec(t, tx, "UPDATE "+pgx.Identifier{pair[0]}.Sanitize()+" SET "+pgx.Identifier{pair[1]}.Sanitize()+"='10000-01-01 00:00:00+00'::timestamptz")
		}
		facts := f.read(t, tx)
		if !facts.Summary().Wallet.RowsConsistent || facts.Summary().InputMismatchFields != 0 {
			t.Fatal("finite five-digit native year was narrowed")
		}
		f.exec(t, tx, `UPDATE funding_wallet_operations SET created_xid='9223372036854775808'::xid8,available_delta=1234567890123456789012345678901234567890::numeric`)
		facts = f.read(t, tx)
		if facts.Summary().Wallet.RowsConsistent || facts.Summary().Algorithms[7] != "executed" {
			t.Fatal("bad raw declaration hid core or became execution error")
		}
		for _, pair := range [][2]string{{"created_xid", "9223372036854775808"}, {"available_delta", "1234567890123456789012345678901234567890"}} {
			if completeSourcePGCell(t, facts.state.raw, "funding_wallet_operations", pair[0], 0).text != pair[1] {
				t.Fatal("native xid/NUMERIC lost decimal bytes")
			}
		}
	})
	t.Run("late_c0_permission_error_returns_zero", func(t *testing.T) {
		role := "wr_read_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err := f.admin.Exec(f.ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN")
		completeSourcePGMust(t, "create exact isolated read-denial role", err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := f.admin.Exec(ctx, "REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" FROM "+pgx.Identifier{role}.Sanitize()+"; REVOKE ALL PRIVILEGES ON SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" FROM "+pgx.Identifier{role}.Sanitize()+"; DROP ROLE "+pgx.Identifier{role}.Sanitize())
			if err != nil {
				t.Error("exact isolated read-denial role cleanup failed")
			}
		})
		_, err = f.admin.Exec(f.ctx, "GRANT USAGE ON SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()+"; GRANT SELECT ON ALL TABLES IN SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" TO "+pgx.Identifier{role}.Sanitize()+"; REVOKE SELECT ON "+pgx.Identifier{f.schema, "funding_consumption_refund_lots"}.Sanitize()+" FROM "+pgx.Identifier{role}.Sanitize())
		completeSourcePGMust(t, "grant fixed union except last C0 relation", err)
		tx := f.begin(t, pgx.ReadOnly)
		f.exec(t, tx, "SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize())
		q := f.query(tx)
		facts, err := ReadWallet(f.ctx, q, f.schema, ModeOrdinary)
		wrZero(t, facts, err, ErrExecution)
		if len(q.calls) != 84 || q.calls["funding_consumption_refund_lots"] != 1 {
			t.Fatal("denial did not exercise the late original C0 relation")
		}
	})
}
