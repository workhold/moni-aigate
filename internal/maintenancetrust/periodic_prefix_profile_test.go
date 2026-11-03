package maintenancetrust

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

func prefixLockTargets(query, schema string) ([]string, error) {
	rest, ok := strings.CutPrefix(query, "LOCK TABLE ")
	if !ok {
		return nil, fmt.Errorf("missing LOCK")
	}
	rest, ok = strings.CutSuffix(rest, " IN SHARE ROW EXCLUSIVE MODE")
	if !ok {
		return nil, fmt.Errorf("mode or extra command")
	}
	var tables []string
	for {
		rest, ok = strings.CutPrefix(rest, "ONLY ")
		if !ok {
			return nil, fmt.Errorf("missing per-target ONLY")
		}
		gotSchema, tail, err := initQuotedIdentifier(rest)
		if err != nil || gotSchema != schema {
			return nil, fmt.Errorf("schema not exactly qualified")
		}
		rest, ok = strings.CutPrefix(tail, ".")
		if !ok {
			return nil, fmt.Errorf("unqualified target")
		}
		table, tail, err := initQuotedIdentifier(rest)
		if err != nil {
			return nil, err
		}
		tables = append(tables, table)
		if tail == "" {
			return tables, nil
		}
		rest, ok = strings.CutPrefix(tail, ", ")
		if !ok {
			return nil, fmt.Errorf("target separator")
		}
	}
}

func TestPeriodicPrefixInitFixed128TargetsPreservesLegacy125(t *testing.T) {
	want := append(slices.Clone(initLockTables[:]), "periodic_budget_observation_heads", "periodic_budget_observation_journal", "periodic_budget_observation_origins")
	slices.Sort(want)
	if len(want) != 128 || !strictInitTableOrder(want) || checkFrozenInitTables(initLockTables[:]) != nil {
		t.Fatal("reviewed old125+new3 inventory changed")
	}
	for _, schema := range []string{"target", "public", `tenant, "quoted".测试; SELECT 1 --`} {
		got, err := prefixLockTargets(periodicPrefixInitLockSQL(schema), schema)
		if err != nil || !slices.Equal(got, want) || len(got) != 128 || !strictInitTableOrder(got) {
			t.Fatal("prefix lock is not the one C-ordered128 complete command", err)
		}
		if checkInitLockSQL(initLockSQL(schema), schema) != nil {
			t.Fatal("legacy lock grammar or frozen125 inventory expanded")
		}
	}
	query := periodicPrefixInitLockSQL("target")
	for name, changed := range map[string]string{
		"missing_ONLY":    strings.Replace(query, "ONLY ", "", 1),
		"unqualified":     strings.Replace(query, `"target".`, "", 1),
		"mode":            strings.Replace(query, "SHARE ROW EXCLUSIVE", "SHARE", 1),
		"extra_statement": query + "; SELECT 1",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := prefixLockTargets(changed, "target"); err == nil {
				t.Fatal("changed single-lock grammar accepted")
			}
		})
	}
}

func TestPeriodicPrefixPublicBeginsConsumeOneGateAndRespectBusy(t *testing.T) {
	for _, init := range []bool{false, true} {
		for _, scenario := range []string{"nil_context", "canceled", "absent_actual_connection"} {
			t.Run(fmt.Sprint(init)+"/"+scenario, func(t *testing.T) {
				g := &Gate{state: &gateState{nonce: &gateNonce{}}}
				ctx := context.Background()
				want := ErrTransaction
				if scenario == "nil_context" {
					ctx, want = nil, ErrInput
				} else if scenario == "canceled" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
					want = ErrTimeout
				}
				var snapshot *Snapshot
				var err error
				if init {
					snapshot, err = g.BeginPeriodicPrefixInit(ctx)
				} else {
					snapshot, err = g.BeginPeriodicPrefixPlan(ctx)
				}
				if snapshot != nil || err != want || !g.state.beginAttempted || !g.state.closed || g.state.busy || g.state.snapshot != nil {
					t.Fatal("failed V3 Begin did not close its sole original gate", err)
				}
				if next, err := g.BeginPlan(context.Background()); next != nil || err != ErrTransaction {
					t.Fatal("failed V3 Begin retried through legacy entry")
				}
			})
		}
	}
	for _, g := range []*Gate{nil, {}, {state: &gateState{busy: true}}} {
		for _, begin := range []func(context.Context) (*Snapshot, error){g.BeginPeriodicPrefixPlan, g.BeginPeriodicPrefixInit} {
			if snapshot, err := begin(context.Background()); snapshot != nil || err == nil {
				t.Fatal("invalid or busy gate issued snapshot")
			}
		}
		if g != nil && g.state != nil && g.state.beginAttempted {
			t.Fatal("busy rejection consumed another owner's Begin")
		}
	}
}

func TestPeriodicPrefixInstallationSharesOriginalOneAttempt(t *testing.T) {
	for _, selected := range []fundinglotcatalog.Profile{fundinglotcatalog.LegacyV1, fundinglotcatalog.PeriodicPrefixV3} {
		for _, firstPrefix := range []bool{false, true} {
			t.Run(fmt.Sprint(selected)+"/"+fmt.Sprint(firstPrefix), func(t *testing.T) {
				s, _, peer := pureSnapshotLifecycle(t)
				s.state.profile = selected
				var err error
				if firstPrefix {
					_, err = s.VerifyPeriodicPrefixInstallation(context.Background())
				} else {
					_, err = s.VerifyInstallation(context.Background())
				}
				if err != ErrSchema || !s.state.catalogUsed || s.state.installation != nil || s.Outcome() != OutcomeAbandoned {
					t.Fatal("absence of owned transaction produced installation evidence", err)
				}
				requireSnapshotSocketClosed(t, peer)
				copySnapshot := *s
				if p, err := copySnapshot.VerifyPeriodicPrefixInstallation(context.Background()); p != nil || err == nil {
					t.Fatal("V3 retry after first installation attempt")
				}
				if p, err := copySnapshot.VerifyInstallation(context.Background()); p != nil || err == nil {
					t.Fatal("legacy retry after first installation attempt")
				}
			})
		}
	}
	for _, snapshot := range []*Snapshot{nil, {}} {
		if p, err := snapshot.VerifyPeriodicPrefixInstallation(context.Background()); p != nil || err != ErrInput {
			t.Fatal("zero snapshot issued V3 installation")
		}
	}
}

func TestPeriodicPrefixSourceProfileCannotBeCallerSelected(t *testing.T) {
	for _, profile := range []fundinglotcatalog.Profile{0, fundinglotcatalog.LegacyV1, fundinglotcatalog.PeriodicPrefixV3, 255} {
		for _, installation := range []*fundinglotcatalog.InstallationProof{nil, {}} {
			st := &snapshotState{profile: profile, installation: installation}
			if st.sourceProfile() != 0 {
				t.Fatal("enum or empty installation manufactured owned source profile")
			}
		}
	}
	if (*snapshotState)(nil).sourceProfile() != 0 {
		t.Fatal("nil snapshot has source profile")
	}
	for _, lease := range []*ReadLease{nil, {}} {
		if profile, err := lease.SourceProfile(); profile != 0 || err != ErrInput {
			t.Fatal("zero lease has source profile")
		}
	}
	for _, kind := range []ScanKind{ScanCompleteSources, ScanSourceWallet, ScanLedger} {
		l, _, driver := pureCompleteSourceLease(t, fundinglotcatalog.Ordinary)
		l.state.kind = kind
		l.state.snapshot.profile = fundinglotcatalog.PeriodicPrefixV3
		if profile, err := l.SourceProfile(); profile != 0 || err != ErrTransaction || !l.state.revoked {
			t.Fatal("private enum and descriptive authority manufactured source profile")
		}
		if driver.nexts.Load() != 0 || driver.scans.Load() != 0 || driver.closes.Load() != 0 {
			t.Fatal("source profile rejection performed a query")
		}
	}
	if fundingLotsOnce != uint8(128) || ScanSourceWallet != 7 || validScanKind(8) {
		t.Fatal("V3 changed fixed uint8 once or public scan namespace")
	}
}

func TestPeriodicPrefixScanAndPhaseFailuresKeepOriginalOnceBits(t *testing.T) {
	for _, profile := range []fundinglotcatalog.Profile{fundinglotcatalog.LegacyV1, fundinglotcatalog.PeriodicPrefixV3} {
		for _, kind := range []ScanKind{ScanCompleteSources, ScanSourceWallet} {
			t.Run(fmt.Sprint(profile)+"/"+fmt.Sprint(kind), func(t *testing.T) {
				s, permit := pureFundingLotsPermit(t)
				s.state.profile = profile
				if lease, err := permit.BeginScan(nil, kind); lease != nil || err != ErrInput {
					t.Fatal("invalid scan attempt produced lease")
				}
				if permit.state.scanAttempted != uint8(1)<<uint(kind-1) || !permit.state.revoked || s.state.activeLease != nil || s.state.gate.busy {
					t.Fatal("profile altered or recovered the original kind6/7 once bit")
				}
				s.state.profile = fundinglotcatalog.PeriodicPrefixV3
				if lease, err := permit.BeginScan(context.Background(), kind); lease != nil || err != ErrTransaction {
					t.Fatal("switching profile retried a consumed scan")
				}
			})
		}
		t.Run(fmt.Sprint(profile)+"/phase", func(t *testing.T) {
			s, permit := pureFundingLotsPermit(t)
			s.state.profile = profile
			if phase, err := permit.beginFundingLots(nil, snapshotPlan, fundingLotsUnitEpoch); phase != nil || err != ErrInput {
				t.Fatal("invalid main phase produced authority")
			}
			if permit.state.scanAttempted != uint8(128) || s.state.fundingLots == nil || !s.state.fundingLots.released {
				t.Fatal("main phase no longer shares the eighth uint8 bit")
			}
			s.state.profile = fundinglotcatalog.PeriodicPrefixV3
			if phase, err := permit.beginFundingLots(context.Background(), snapshotPlan, fundingLotsUnitEpoch); phase != nil || err != ErrTransaction {
				t.Fatal("profile switch repeated the original main phase")
			}
		})
	}
}

func TestPeriodicPrefixBasisProfileBoundaries(t *testing.T) {
	legacy := fundinglotbasis.Report{Scope: "fixed_109_table_basis_v1", PhysicalEOFComplete: true, ScannedTables: 109, ScannedColumns: 976,
		SourceRows: 100000, SourceBytes: 64 << 20, BasisSHA256: strings.Repeat("a", 64), ManifestSHA256: authorityManifestSHA,
		DescriptorSHA256: fundingLotsBasisDescriptor, SendMetadataSHA256: fundingLotsBasisSend}
	prefix := legacy
	prefix.Scope, prefix.ScannedTables, prefix.ScannedColumns, prefix.DescriptorSHA256 = "fixed_112_table_prefix_basis_v1", 112, 1004, fundinglotbasis.PeriodicPrefixDescriptorSHA256
	for _, original := range []struct {
		name    string
		profile fundinglotcatalog.Profile
		report  fundinglotbasis.Report
	}{{"legacy", fundinglotcatalog.LegacyV1, legacy}, {"prefix", fundinglotcatalog.PeriodicPrefixV3, prefix}} {
		t.Run(original.name, func(t *testing.T) {
			if !fundingLotsProofBasisProfileValid(original.report, original.profile) {
				t.Fatal("exact inclusive basis limits rejected")
			}
			for _, profile := range []fundinglotcatalog.Profile{0, fundinglotcatalog.LegacyV1, fundinglotcatalog.WalletReadV2, fundinglotcatalog.PeriodicPrefixV3, 255} {
				if profile != original.profile && fundingLotsProofBasisProfileValid(original.report, profile) {
					t.Fatal("basis crossed independently selected profile")
				}
			}
			for field, edit := range map[string]func(*fundinglotbasis.Report){
				"scope":      func(r *fundinglotbasis.Report) { r.Scope += "x" },
				"EOF":        func(r *fundinglotbasis.Report) { r.PhysicalEOFComplete = false },
				"tables":     func(r *fundinglotbasis.Report) { r.ScannedTables-- },
				"columns":    func(r *fundinglotbasis.Report) { r.ScannedColumns-- },
				"rows":       func(r *fundinglotbasis.Report) { r.SourceRows++ },
				"bytes":      func(r *fundinglotbasis.Report) { r.SourceBytes++ },
				"basis":      func(r *fundinglotbasis.Report) { r.BasisSHA256 = strings.Repeat("A", 64) },
				"manifest":   func(r *fundinglotbasis.Report) { r.ManifestSHA256 = strings.Repeat("b", 64) },
				"descriptor": func(r *fundinglotbasis.Report) { r.DescriptorSHA256 = strings.Repeat("c", 64) },
				"send":       func(r *fundinglotbasis.Report) { r.SendMetadataSHA256 = strings.Repeat("d", 64) },
			} {
				r := original.report
				edit(&r)
				if fundingLotsProofBasisProfileValid(r, original.profile) {
					t.Fatal("altered independently bounded basis accepted", field)
				}
			}
		})
	}
}

type prefixBasisQueryCounter struct{ calls int }

func (q *prefixBasisQueryCounter) Query(string, ...any) (fundinglotbasis.Rows, error) {
	q.calls++
	return nil, fundinglotbasis.ErrExecution
}

func TestPeriodicPrefixOwnedBasisReaderRejectsUnknownBeforeQuery(t *testing.T) {
	for _, profile := range []fundinglotcatalog.Profile{0, fundinglotcatalog.WalletReadV2, 255} {
		q := &prefixBasisQueryCounter{}
		facts, err := readOwnedBasis(context.Background(), q, "target", profile)
		if facts.Summary() != (fundinglotbasis.Report{}) || err != ErrSchema || q.calls != 0 {
			t.Fatal("unknown profile selected or queried a basis")
		}
	}
	for _, profile := range []fundinglotcatalog.Profile{fundinglotcatalog.LegacyV1, fundinglotcatalog.PeriodicPrefixV3} {
		if facts, err := readOwnedBasis(context.Background(), nil, "target", profile); facts.Summary() != (fundinglotbasis.Report{}) || err != fundinglotbasis.ErrInput {
			t.Fatal("known profile bypassed basis input closure")
		}
	}
}

func TestPeriodicPrefixObservationInputConsumesOriginalGate(t *testing.T) {
	for _, scenario := range []string{"nil_context", "canceled", "invalid_expected", "absent_actual_connection"} {
		t.Run(scenario, func(t *testing.T) {
			g := &Gate{state: &gateState{nonce: &gateNonce{}}}
			ctx, expected, want := context.Background(), fundingLotsObservationExpected(), ErrTransaction
			if scenario == "nil_context" {
				ctx, want = nil, ErrInput
			} else if scenario == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = ErrTimeout
			} else if scenario == "invalid_expected" {
				expected.RuntimeProofDigest, want = strings.Repeat("A", 64), ErrInput
			}
			result, err := g.ObservePeriodicPrefixFundingLots(ctx, expected)
			if result != (FundingLotsObservation{}) || err != want || !g.state.beginAttempted || !g.state.closed || g.state.busy || g.state.snapshot != nil {
				t.Fatal("invalid V3 observation retained gate or produced evidence", err)
			}
			if result, err = g.ObserveFundingLots(context.Background(), fundingLotsObservationExpected()); result != (FundingLotsObservation{}) || err != ErrTransaction {
				t.Fatal("failed V3 observation repeated through legacy entry")
			}
		})
	}
}
