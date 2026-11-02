package fundinglotscan

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func epochProjectionFacts(t *testing.T) (WalletFacts, *readerQuery) {
	t.Helper()
	q := wrNew(t)
	wrEpoch(q)
	f := wrRead(t, q)
	if f.Summary().Status != "consistent" || !f.Summary().CoverageComplete {
		t.Fatal("original empty-wallet graph is not qualified")
	}
	return f, q
}

func epochProjectionCell(f WalletFacts, table, column string) *cell {
	s := f.state.raw
	i := s.index[table]
	return &s.tables[i].rows[0][s.tables[i].spec.index[column]]
}

func epochProjectionZero(t *testing.T, f WalletFacts, want error) {
	t.Helper()
	out, err := ProjectEpochProof(context.Background(), f)
	if err != want || !reflect.DeepEqual(out, EpochProofRows{}) {
		t.Fatalf("want zero/%v, got %+v/%v", want, out, err)
	}
}

func TestProjectEpochProofOriginalAndOwned(t *testing.T) {
	f, q := epochProjectionFacts(t)
	before, calls := f.Summary(), len(q.calls)
	out, err := ProjectEpochProof(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	at := fundinglotverify.WalletLotsTime{Valid: true, Value: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC).In(time.Local)}
	proof := []byte("independent wallet reader fixture")
	want := EpochProofRows{
		Epoch:       fundinglotverify.WalletLotsEpochRow{Singleton: true, ID: wrID(1), SchemaVersion: "1", DatabaseOID: "101", SchemaOID: "102", BasisDigest: strings.Repeat("a", 64), ManifestDigest: strings.Repeat("b", 64), DescriptorDigest: strings.Repeat("c", 64), RuntimeProofDigest: wrSHA(proof), TenantCount: "0", ReceiptCount: "0", SettlementCount: "0", ApprovedRefundCount: "0", OpeningCount: "0", LegacySourceCount: "0", AvailableTotal: "0", HeldTotal: "0", Actor: "offline-maintenance", CreatedAt: at},
		Attestation: fundinglotverify.WalletLotsAttestationRow{Singleton: true, EpochID: wrID(1), ProofVersion: "2", Mode: "ordinary", DatabaseOID: "101", SchemaOID: "102", SchemaOwnerOID: "103", AppRoleOID: "104", ProjectorRoleOID: "105", ProofCanonical: proof, ProofSHA256: wrSHA(proof), ManifestDigest: strings.Repeat("b", 64), DescriptorDigest: strings.Repeat("c", 64), CreatedAt: at},
	}
	if !reflect.DeepEqual(out, want) || len(q.calls) != calls || f.Summary() != before || len(f.state.raw.rejected) != 0 {
		t.Fatalf("projection changed originals or lost fields: %+v", out)
	}
	if unsafe.StringData(out.Epoch.ID) == unsafe.StringData(epochProjectionCell(f, "funding_lot_epoch", "id").text) {
		t.Fatal("output string storage aliases facts")
	}
	out.Attestation.ProofCanonical[0] ^= 1
	again, err := ProjectEpochProof(context.Background(), f)
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatal("output byte mutation changed original facts", err)
	}
	// The old read context and Query are not a hidden dependency of this pure
	// projection. They may have ended immediately after the complete Read.
	old, cancel := context.WithCancel(context.Background())
	cancel()
	f.state.raw.ctx, f.state.raw.query = old, nil
	if _, err = ProjectEpochProof(context.Background(), f); err != nil {
		t.Fatalf("projector retained expired read context/query: %v", err)
	}
}

func TestProjectEpochProofNullableAndNativeCopyCodec(t *testing.T) {
	// White-box vectors isolate the projection codec, not source legitimacy.
	// Public callers cannot mutate private facts or turn this DTO into authority.
	f, _ := epochProjectionFacts(t)
	worker, predecessor := "205", strings.Repeat("d", 64)
	*epochProjectionCell(f, "funding_lot_runtime_attestations", "worker_role_oid") = readerText(worker)
	*epochProjectionCell(f, "funding_lot_runtime_attestations", "predecessor_payment_digest") = readerText(predecessor)
	// Keep logical transport accounting equal after the nullable values change.
	delta := len(worker) + len(predecessor)
	f.state.raw.bytes += delta
	f.state.report.SourceBytes += uint64(delta)
	for _, year := range []int{-4712, 1, 12345, 294276} {
		at := time.Date(year, 2, 3, 4, 5, 6, 123456000, time.UTC)
		for _, table := range []string{"funding_lot_epoch", "funding_lot_runtime_attestations"} {
			*epochProjectionCell(f, table, "created_at") = cell{kind: timeKind, valid: true, instant: pgtype.Timestamptz{Time: at, Valid: true}}
		}
		out, err := ProjectEpochProof(context.Background(), f)
		if err != nil || out.Attestation.WorkerRoleOID == nil || *out.Attestation.WorkerRoleOID != worker || out.Attestation.PredecessorPaymentDigest == nil || *out.Attestation.PredecessorPaymentDigest != predecessor || !out.Epoch.CreatedAt.Value.Equal(at) || !out.Attestation.CreatedAt.Value.Equal(at) {
			t.Fatalf("nullable/native codec narrowed original values: %+v %v", out, err)
		}
		*out.Attestation.WorkerRoleOID, *out.Attestation.PredecessorPaymentDigest = "changed", "changed"
		if epochProjectionCell(f, "funding_lot_runtime_attestations", "worker_role_oid").text != worker || epochProjectionCell(f, "funding_lot_runtime_attestations", "predecessor_payment_digest").text != predecessor {
			t.Fatal("nullable output aliases raw fact storage")
		}
	}
}

func TestProjectEpochProofQualificationsAndCompletion(t *testing.T) {
	for name, damage := range map[string]func(WalletFacts){
		"wrong scope":             func(f WalletFacts) { f.state.report.Scope = "complete_business_source_graphs_v1" },
		"wrong mode":              func(f WalletFacts) { f.state.report.Mode = ModeSandbox },
		"mismatch":                func(f WalletFacts) { f.state.report.Status = "mismatch" },
		"missing EOF":             func(f WalletFacts) { f.state.report.PhysicalEOFComplete = false },
		"old 68 count":            func(f WalletFacts) { f.state.report.ScannedTables = 68 },
		"missing coverage":        func(f WalletFacts) { f.state.report.CoverageComplete = false },
		"algorithm not executed":  func(f WalletFacts) { f.state.report.Algorithms[7] = "input_mismatch" },
		"source algorithm stale":  func(f WalletFacts) { f.state.raw.report.Algorithms[0] = "input_mismatch" },
		"source report mismatch":  func(f WalletFacts) { f.state.raw.report.Status = "mismatch" },
		"rejected field":          func(f WalletFacts) { f.state.raw.rejected[cellRef{"tenants", 0, 0}] = true },
		"raw table unfinished":    func(f WalletFacts) { f.state.raw.tables[0].eof = false },
		"wrong wallet scan count": func(f WalletFacts) { f.state.report.Wallet.Scanned.Epochs++ },
		"wallet mismatch":         func(f WalletFacts) { f.state.report.Wallet.Mismatches.EpochRows++ },
		"wallet unknown":          func(f WalletFacts) { f.state.report.Wallet.Unproven.ReplayPrefixUnproven++ },
		"ledger mismatch":         func(f WalletFacts) { f.state.report.Ledger.Mismatches.UnbalancedJournals++ },
		"missing singleton":       func(f WalletFacts) { f.state.raw.tables[f.state.raw.index["funding_lot_epoch"]].rows = nil },
		"null required field":     func(f WalletFacts) { epochProjectionCell(f, "funding_lot_epoch", "id").valid = false },
		"null proof": func(f WalletFacts) {
			epochProjectionCell(f, "funding_lot_runtime_attestations", "proof_canonical").valid = false
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := epochProjectionFacts(t)
			damage(f)
			out, err := ProjectEpochProof(context.Background(), f)
			if err == nil || !reflect.DeepEqual(out, EpochProofRows{}) {
				t.Fatalf("unqualified facts returned partial/success DTO: %+v %v", out, err)
			}
		})
	}
	epochProjectionZero(t, WalletFacts{}, ErrInput)
	for _, at := range []pgtype.Timestamptz{{}, {Valid: true, InfinityModifier: pgtype.Infinity}, {Valid: true, Time: time.Date(2026, 1, 1, 0, 0, 0, 1, time.UTC)}} {
		f, _ := epochProjectionFacts(t)
		*epochProjectionCell(f, "funding_lot_epoch", "created_at") = cell{kind: timeKind, valid: at.Valid, instant: at}
		epochProjectionZero(t, f, ErrInput)
	}
}

func TestProjectEpochProofRawBudgetAndStructure(t *testing.T) {
	for name, test := range map[string]struct {
		mutate func(WalletFacts)
		want   error
	}{
		"proof byte exception remains bounded": {func(f WalletFacts) {
			epochProjectionCell(f, "funding_lot_runtime_attestations", "proof_canonical").data = make([]byte, 262145)
		}, ErrResource},
		"numeric remains ordinary field": {func(f WalletFacts) {
			epochProjectionCell(f, "funding_lot_epoch", "available_total").text = strings.Repeat("1", fieldLimit+1)
		}, ErrResource},
		"invalid utf8":       {func(f WalletFacts) { epochProjectionCell(f, "funding_lot_epoch", "id").text = string([]byte{0xff}) }, ErrInput},
		"nul text":           {func(f WalletFacts) { epochProjectionCell(f, "funding_lot_epoch", "id").text = "\x00" }, ErrInput},
		"wrong cell kind":    {func(f WalletFacts) { epochProjectionCell(f, "funding_lot_epoch", "id").kind = bytesKind }, ErrExecution},
		"wrong index":        {func(f WalletFacts) { f.state.raw.index["funding_lot_epoch"] = 0 }, ErrExecution},
		"wrong report bytes": {func(f WalletFacts) { f.state.report.SourceBytes++ }, ErrExecution},
		"wrong raw bytes":    {func(f WalletFacts) { f.state.raw.bytes++ }, ErrExecution},
		"duplicate singleton": {func(f WalletFacts) {
			table := &f.state.raw.tables[f.state.raw.index["funding_lot_epoch"]]
			table.rows = append(table.rows, table.rows[0])
		}, ErrExecution},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := epochProjectionFacts(t)
			test.mutate(f)
			epochProjectionZero(t, f, test.want)
		})
	}
	for _, count := range []int{rowLimit + 1, 257} {
		f, _ := epochProjectionFacts(t)
		table := &f.state.raw.tables[f.state.raw.index["users"]]
		for n := 0; n < count; n++ {
			id := fmt.Sprintf("%06d", n)
			if count == 257 {
				id += strings.Repeat("a", fieldLimit-len(id))
			}
			table.rows = append(table.rows, rawRow{readerText(id)})
		}
		epochProjectionZero(t, f, ErrResource)
	}
}

func TestProjectEpochProofFreshViewAndContext(t *testing.T) {
	f, _ := epochProjectionFacts(t)
	// A failed required-field projection must not mutate the original rejected
	// map, even when all earlier completion metadata was intact.
	c := epochProjectionCell(f, "funding_lot_epoch", "actor")
	old := *c
	c.valid = false
	_, _ = ProjectEpochProof(context.Background(), f)
	if len(f.state.raw.rejected) != 0 {
		t.Fatal("projection changed the original rejected map")
	}
	*c = old
	if out, err := ProjectEpochProof(nil, f); err != ErrInput || !reflect.DeepEqual(out, EpochProofRows{}) {
		t.Fatal("nil context was not atomic input failure")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := ProjectEpochProof(cancelled, f); err != ErrCanceled || !reflect.DeepEqual(out, EpochProofRows{}) {
		t.Fatal("cancellation was not atomic")
	}
	timed, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if out, err := ProjectEpochProof(timed, f); err != ErrTimeout || !reflect.DeepEqual(out, EpochProofRows{}) {
		t.Fatal("expired caller was not timeout")
	}
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for n := 0; n < 4; n++ {
				out, err := ProjectEpochProof(context.Background(), f)
				if err != nil || out.Epoch.ID != wrID(1) {
					t.Errorf("concurrent read failed: %v", err)
				}
			}
		}()
	}
	workers.Wait()
}

func TestProjectEpochProofCancellationAlwaysZero(t *testing.T) {
	f, _ := epochProjectionFacts(t)
	probe := &seedCountingContext{Context: context.Background()}
	if _, err := ProjectEpochProof(probe, f); err != nil {
		t.Fatal(err)
	}
	// Cover cancellation during raw requalification, projection and final owned
	// copying as well as entry. No interrupted stage may publish partial rows.
	for _, failure := range []struct{ cause, want error }{{context.Canceled, ErrCanceled}, {context.DeadlineExceeded, ErrTimeout}} {
		for _, at := range []int{1, 5, 45, probe.calls / 2, probe.calls - 3, probe.calls} {
			ctx := &seedCountingContext{Context: context.Background(), failAt: at, failure: failure.cause}
			out, err := ProjectEpochProof(ctx, f)
			if err != failure.want || !reflect.DeepEqual(out, EpochProofRows{}) {
				t.Fatalf("interrupted projection at %d published rows: %+v %v", at, out, err)
			}
			if len(f.state.raw.rejected) != 0 {
				t.Fatal("interrupted projection changed original facts")
			}
		}
	}
}
