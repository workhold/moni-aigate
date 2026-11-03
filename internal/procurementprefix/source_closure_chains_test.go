package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func sourceTestRows(t *testing.T, rows Rows) *sourceClosureState {
	t.Helper()
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	s := &sourceClosureState{w: w, bad: make(map[string]keySet), tables: make(map[string][]sourceRawRow)}
	for _, v := range []reflect.Value{reflect.ValueOf(rows.OriginalRows), reflect.ValueOf(rows.PrefixRows)} {
		if err := s.physicalRows(v); err != nil {
			t.Fatal(err)
		}
	}
	return s
}
func sourceRequestChainFixture() PrefixRows {
	return PrefixRows{RequestOrigins: []RequestOriginRow{{RequestID: "request", TenantID: "tenant", CreatedXID: "10"}}, RequestHeads: []RequestHeadRow{{RequestID: "request", Ordinal: "2", LastDecisionID: sourcePtr("d2"), UpdatedXID: "12"}}, RequestDecisions: []RequestDecisionRow{
		{ID: "d1", RequestID: "request", TenantID: "tenant", Ordinal: "1", CreatedXID: "11", AfterLatestID: sourcePtr("f1"), BeforeStatus: "accepted", AfterStatus: "failed"},
		{ID: "d2", RequestID: "request", TenantID: "tenant", Ordinal: "2", PreviousDecisionID: sourcePtr("d1"), CreatedXID: "12", BeforeLatestID: sourcePtr("f1"), AfterLatestID: sourcePtr("f2"), BeforeStatus: "sending", AfterStatus: "succeeded"},
	}}
}
func TestSourceRequestChainEdges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PrefixRows)
		bad    bool
	}{
		{"ordered", func(*PrefixRows) {}, false},
		{"input order independent", func(r *PrefixRows) {
			r.RequestDecisions[0], r.RequestDecisions[1] = r.RequestDecisions[1], r.RequestDecisions[0]
		}, false},
		{"external request status allowed", func(r *PrefixRows) { r.RequestDecisions[1].BeforeStatus = "changed_by_customer_finish" }, false},
		{"missing predecessor", func(r *PrefixRows) { r.RequestDecisions[1].PreviousDecisionID = nil }, true},
		{"wrong predecessor", func(r *PrefixRows) { r.RequestDecisions[1].PreviousDecisionID = sourcePtr("unrelated") }, true},
		{"first predecessor forbidden", func(r *PrefixRows) { r.RequestDecisions[0].PreviousDecisionID = sourcePtr("old") }, true},
		{"ordinal gap", func(r *PrefixRows) { r.RequestDecisions[1].Ordinal = "3" }, true},
		{"duplicate ordinal", func(r *PrefixRows) { r.RequestDecisions[1].Ordinal = "1" }, true},
		{"wrong latest before", func(r *PrefixRows) { r.RequestDecisions[1].BeforeLatestID = sourcePtr("f0") }, true},
		{"head tail", func(r *PrefixRows) { r.RequestHeads[0].LastDecisionID = sourcePtr("d1") }, true},
		{"head xid", func(r *PrefixRows) { r.RequestHeads[0].UpdatedXID = "11" }, true},
		{"missing origin", func(r *PrefixRows) { r.RequestOrigins = nil }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := sourceRequestChainFixture()
			tc.mutate(&rows)
			s := sourceNewTestState(t, rows)
			chains, err := s.sourceChain("pp_request_decisions", "request_chain", "request_id", "ordinal", "previous_decision_id")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.requestChain(rows, chains); err != nil {
				t.Fatal(err)
			}
			if (len(s.bad["request_chain"]) > 0) != tc.bad {
				t.Fatalf("bad=%v keys=%v", tc.bad, s.bad["request_chain"])
			}
		})
	}
}

func sourceAttemptChainFixture() PrefixRows {
	return PrefixRows{AttemptOrigins: []AttemptOriginRow{{AttemptID: "attempt", ManagementMode: "unmanaged", CreatedXID: "1"}}, AttemptHeads: []AttemptHeadRow{{AttemptID: "attempt", Ordinal: "1", LastNodeID: sourcePtr("node"), UpdatedXID: "2"}}, OperationRoots: []OperationRootRow{{ID: "root", ScopeKind: "request", AttemptID: sourcePtr("attempt"), CreatedXID: "2"}}, RootSteps: []RootStepRow{{RootID: "root", StepNo: "1", Kind: "incoming", SourceID: "incoming", CreatedXID: "2"}}, AttemptNodes: []AttemptNodeRow{{ID: "node", AttemptID: "attempt", Ordinal: "1", RootID: "root", StepNo: "1", StepKind: "incoming", SourceID: "incoming", CreatedXID: "2"}}}
}
func TestSourceAttemptNodeEdges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PrefixRows)
		bad    bool
	}{
		{"exact step", func(*PrefixRows) {}, false},
		{"step missing node", func(r *PrefixRows) { r.AttemptNodes = nil }, true},
		{"node wrong source", func(r *PrefixRows) { r.AttemptNodes[0].SourceID = "other" }, true},
		{"node wrong xid", func(r *PrefixRows) { r.AttemptNodes[0].CreatedXID = "3" }, true},
		{"node wrong root", func(r *PrefixRows) { r.AttemptNodes[0].RootID = "other" }, true},
		{"first previous", func(r *PrefixRows) { r.AttemptNodes[0].PreviousNodeID = sourcePtr("other") }, true},
		{"ordinal gap", func(r *PrefixRows) { r.AttemptNodes[0].Ordinal = "2" }, true},
		{"tail mismatch", func(r *PrefixRows) { r.AttemptHeads[0].LastNodeID = sourcePtr("other") }, true},
		{"head0 xid mismatch", func(r *PrefixRows) {
			r.AttemptNodes = nil
			r.RootSteps = nil
			r.AttemptHeads[0].Ordinal = "0"
			r.AttemptHeads[0].LastNodeID = nil
			r.AttemptHeads[0].UpdatedXID = "2"
		}, true},
		{"node on birth", func(r *PrefixRows) {
			r.AttemptNodes[0].StepKind = "attempt_birth"
			r.RootSteps[0].Kind = "attempt_birth"
		}, true},
		{"node without attempt root", func(r *PrefixRows) { r.OperationRoots[0].AttemptID = nil }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := sourceAttemptChainFixture()
			tc.mutate(&rows)
			s := sourceNewTestState(t, rows)
			chains, err := s.sourceChain("pp_attempt_nodes", "attempt_chain", "attempt_id", "ordinal", "previous_node_id")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.attemptChain(rows, chains, map[string][]sourceRawRow{}); err != nil {
				t.Fatal(err)
			}
			if (len(s.bad["attempt_chain"]) > 0) != tc.bad {
				t.Fatalf("bad=%v keys=%v", tc.bad, s.bad["attempt_chain"])
			}
		})
	}
}
func sourceAccountChainFixture() PrefixRows {
	p := sourcePtr
	first := AccountJournalRow{ID: "j1", AccountID: "account", Ordinal: "1", RootID: "root", StepNo: "1", CreatedXID: "1", Kind: "create", SourceKind: "command", SourceID: "op", CommandSourceID: p("command"), AfterLabel: "label", AfterProvider: "provider", AfterState: "active", AfterLimitAmount: "100", AfterConfigurationRevision: "1", AfterHeldAmount: "0", AfterSpentAmount: "0", AfterBlockingCount: "0"}
	second := AccountJournalRow{ID: "j2", AccountID: "account", Ordinal: "2", PreviousID: p("j1"), CreatedXID: "2", Kind: "configure", BeforeLabel: p("label"), BeforeProvider: p("provider"), BeforeState: p("active"), BeforeLimitAmount: p("100"), BeforeConfigurationRevision: p("1"), BeforeHeldAmount: p("0"), BeforeSpentAmount: p("0"), BeforeBlockingCount: p("0"), AfterLabel: "next", AfterProvider: "provider", AfterState: "active", AfterLimitAmount: "100", AfterConfigurationRevision: "2", AfterHeldAmount: "0", AfterSpentAmount: "0", AfterBlockingCount: "0"}
	return PrefixRows{AccountOrigins: []AccountOriginRow{{AccountID: "account", CreateOperationID: "op", CommandSourceID: "command", RootID: "root", StepNo: "1", CreatedXID: "1", JournalID: "j1"}}, AccountJournal: []AccountJournalRow{first, second}, AccountHeads: []AccountHeadRow{{AccountID: "account", Ordinal: "2", JournalID: "j2", UpdatedXID: "2", Label: "next", Provider: "provider", State: "active", LimitAmount: "100", ConfigurationRevision: "2", HeldAmount: "0", SpentAmount: "0", BlockingCount: "0"}}}
}
func TestSourceAccountChainEdges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*PrefixRows)
		bad    bool
	}{
		{"full continuity", func(*PrefixRows) {}, false},
		{"wrong predecessor", func(r *PrefixRows) { r.AccountJournal[1].PreviousID = sourcePtr("old") }, true},
		{"first before not NULL", func(r *PrefixRows) { r.AccountJournal[0].BeforeHeldAmount = sourcePtr("0") }, true},
		{"config discontinuity", func(r *PrefixRows) { r.AccountJournal[1].BeforeLabel = sourcePtr("invented") }, true},
		{"bucket discontinuity", func(r *PrefixRows) { r.AccountJournal[1].BeforeHeldAmount = sourcePtr("5") }, true},
		{"origin journal", func(r *PrefixRows) { r.AccountOrigins[0].JournalID = "other" }, true},
		{"head projection", func(r *PrefixRows) { r.AccountHeads[0].BlockingCount = "1" }, true},
		{"head xid", func(r *PrefixRows) { r.AccountHeads[0].UpdatedXID = "1" }, true},
		{"duplicate create", func(r *PrefixRows) { r.AccountJournal[1].Kind = "create" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := sourceAccountChainFixture()
			tc.mutate(&rows)
			s := sourceNewTestState(t, rows)
			chains, err := s.sourceChain("pp_account_journal", "account_chain", "account_id", "ordinal", "previous_id")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.accountChain(rows, chains); err != nil {
				t.Fatal(err)
			}
			if (len(s.bad["account_chain"]) > 0) != tc.bad {
				t.Fatalf("bad=%v keys=%v", tc.bad, s.bad["account_chain"])
			}
		})
	}
}
func TestSourceEvidenceMemberChainEdges(t *testing.T) {
	fixture := func() PrefixRows {
		return PrefixRows{AttemptOrigins: []AttemptOriginRow{{AttemptID: "a", ManagementMode: "managed", CreatedXID: "1"}}, AttemptHeads: []AttemptHeadRow{{AttemptID: "a", Ordinal: "0", UpdatedXID: "1", MemberCount: sourcePtr("2"), MemberTailID: sourcePtr("m2")}}, EvidenceMembers: []EvidenceMemberRow{{ID: "m1", AttemptID: "a", MemberOrdinal: "1"}, {ID: "m2", AttemptID: "a", MemberOrdinal: "2", PreviousMemberID: sourcePtr("m1")}}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*PrefixRows)
		bad    bool
	}{
		{"two members", func(*PrefixRows) {}, false},
		{"wrong previous", func(r *PrefixRows) { r.EvidenceMembers[1].PreviousMemberID = sourcePtr("other") }, true},
		{"gap", func(r *PrefixRows) { r.EvidenceMembers[1].MemberOrdinal = "3" }, true},
		{"head count", func(r *PrefixRows) { r.AttemptHeads[0].MemberCount = sourcePtr("1") }, true},
		{"head tail", func(r *PrefixRows) { r.AttemptHeads[0].MemberTailID = sourcePtr("m1") }, true},
		{"unmanaged members", func(r *PrefixRows) { r.AttemptOrigins[0].ManagementMode = "unmanaged" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := fixture()
			tc.mutate(&rows)
			s := sourceNewTestState(t, rows)
			members, err := s.sourceChain("pp_evidence_members", "evidence_chain", "attempt_id", "member_ordinal", "previous_member_id")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.attemptChain(rows, map[string][]sourceRawRow{}, members); err != nil {
				t.Fatal(err)
			}
			if (len(s.bad["evidence_chain"]) > 0) != tc.bad {
				t.Fatalf("bad=%v keys=%v", tc.bad, s.bad["evidence_chain"])
			}
		})
	}
}

func TestSourceErrorsReturnNoPartialResult(t *testing.T) {
	if result, err := checkSourceClosure(nil, Rows{}); !errors.Is(err, ErrInput) || !reflect.DeepEqual(result, sourceClosureResult{}) {
		t.Fatal("nil work must return zero result")
	}
	ctx, cancel := context.WithCancel(context.Background())
	w, _, err := newReplayWork(ctx, Rows{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if result, err := checkSourceClosure(w, Rows{}); !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(result, sourceClosureResult{}) {
		t.Fatal("cancel must return zero result")
	}
	expired, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if result, err := checkSourceClosure(&replayWork{b: budget{ctx: expired}}, Rows{}); !errors.Is(err, ErrTimeout) || !reflect.DeepEqual(result, sourceClosureResult{}) {
		t.Fatal("timeout must return zero result")
	}
	tooMany := Rows{OriginalRows: OriginalRows{Users: make([]UserRow, 100001)}}
	if result, err := checkSourceClosure(&replayWork{b: budget{ctx: context.Background()}}, tooMany); !errors.Is(err, ErrResource) || !reflect.DeepEqual(result, sourceClosureResult{}) {
		t.Fatal("resource limit must return zero result")
	}
	empty, _, err := newReplayWork(context.Background(), Rows{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := checkSourceClosure(empty, Rows{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete || len(result.Unimplemented) == 0 || len(result.Qualified) != 0 {
		t.Fatal("partial implementation must never qualify objects")
	}
}
