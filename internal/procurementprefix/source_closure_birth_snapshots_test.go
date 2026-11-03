package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sourceSnapshotFixture() Rows {
	p := sourcePtr
	stamp := Time{Valid: true, Time: time.Date(2026, 11, 3, 1, 2, 3, 0, time.UTC)}
	root := "11111111-1111-4111-8111-111111111111"
	origin := RequestOriginRow{RequestID: "request", RootID: root, StepNo: "1", StepKind: "request_birth", CreatedXID: "1", TenantID: "tenant", KeyID: "key", OwnerID: "owner", ModelID: "model", PriceVersionID: "price", InputRate: "1", OutputRate: "2", InputUpperBound: "100", OutputLimit: "20", ProfileVersion: "profile", PolicyVersion: "policy", RequestHMAC: "hmac", RouteRevision: "0", RequestDeadline: stamp, InstanceID: "instance", Streaming: true, TenantPricingRevision: "1", RetailPricedAt: stamp, RequestCreatedAt: stamp, PBCreatedXID: "1", PBAdmittedAt: stamp, InitialStatus: "accepted", InitialBillingStatus: "reserved"}
	original := RequestRecordRow{ID: "request", TenantID: "tenant", KeyID: "key", OwnerID: "owner", ModelID: "model", PriceVersionID: "price", InputRate: "1", OutputRate: "2", InputUpperBound: "100", OutputLimit: "20", ProfileVersion: "profile", PolicyVersion: "policy", RequestHMAC: "hmac", RouteRevision: "0", RequestDeadline: stamp, InstanceID: "instance", Streaming: true, TenantPricingRevision: p("1"), RetailPricedAt: stamp, CreatedAt: stamp, PBCreatedXID: p("1"), PBAdmittedAt: stamp, Status: "succeeded", BillingStatus: "released", PPOriginRootID: p(root), PPOriginRootStep: p("1"), PPOriginXID: p("1")}
	attemptOrigin := AttemptOriginRow{AttemptID: "attempt", RootID: root, StepNo: "1", StepKind: "attempt_birth", CreatedXID: "1", TenantID: "tenant", RequestID: "request", Sequence: "1", ChannelID: sourceRouteChannel, ChannelVersionID: sourceRouteVersion, CredentialID: "credential", UpstreamModel: "model", RouteDecisions: sourceRouteCanonical, CostBasis: "unknown", QuoteSelectedAt: stamp, AttemptCreatedAt: stamp, PBCreatedXID: "1", InitialDeliveryState: "prepared", InitialCostStatus: "unknown", ManagementMode: "unmanaged"}
	attempt := UpstreamAttemptRow{ID: "attempt", TenantID: "tenant", RequestID: "request", Sequence: "1", ChannelID: sourceRouteChannel, ChannelVersionID: sourceRouteVersion, CredentialID: "credential", UpstreamModel: "model", RouteDecisions: sourceRouteCanonical, CostBasis: "unknown", CreatedAt: stamp, PBCreatedXID: p("1"), PPQuoteSelectedAt: stamp, PPOriginRootID: p(root), PPOriginRootStep: p("1"), PPOriginXID: p("1"), DeliveryState: "completed", CostStatus: "contract", CostAmount: p("7")}
	return Rows{OriginalRows: OriginalRows{RequestRecords: []RequestRecordRow{original}, UpstreamAttempts: []UpstreamAttemptRow{attempt}}, PrefixRows: PrefixRows{RequestOrigins: []RequestOriginRow{origin}, AttemptOrigins: []AttemptOriginRow{attemptOrigin}}}
}
func TestSourceBirthSnapshotsIndependentColumns(t *testing.T) {
	// This literal inventory comes from the request/attempt birth contracts, not
	// the production comparison registry. Every frozen original column is mutated.
	inventories := []struct {
		table   string
		columns []string
	}{
		{"request_records", []string{"id", "tenant_id", "key_id", "owner_id", "model_id", "price_version_id", "input_rate", "output_rate", "input_upper_bound", "output_limit", "profile_version", "policy_version", "request_hmac", "route_revision", "request_deadline", "instance_id", "streaming", "client_ip", "pricing_group_id", "tenant_pricing_revision", "retail_priced_at", "created_at", "pb_created_xid", "pb_tenant_control", "pb_key_control", "pb_admitted_at"}},
		{"upstream_attempts", []string{"id", "tenant_id", "request_id", "sequence", "channel_id", "channel_version_id", "credential_id", "upstream_model", "cost_version_id", "cost_input_rate", "cost_output_rate", "cost_basis", "pp_quote_selected_at", "created_at", "pb_created_xid", "route_decisions"}},
	}
	baseline := sourceTestRows(t, sourceSnapshotFixture())
	if err := baseline.birthSnapshots(); err != nil {
		t.Fatal(err)
	}
	if len(baseline.bad["pointer"]) != 0 {
		t.Fatal("independent frozen snapshots rejected; current status/cost/delivery are intentionally different")
	}
	for _, inventory := range inventories {
		for _, column := range inventory.columns {
			t.Run(inventory.table+"/"+column, func(t *testing.T) {
				rows := sourceSnapshotFixture()
				var row reflect.Value
				if inventory.table == "request_records" {
					row = reflect.ValueOf(&rows.OriginalRows.RequestRecords[0]).Elem()
				} else {
					row = reflect.ValueOf(&rows.OriginalRows.UpstreamAttempts[0]).Elem()
				}
				field := sourceField(row, column)
				switch {
				case field.Type() == timeType:
					value := field.Interface().(Time)
					value.Time = value.Time.Add(time.Microsecond)
					field.Set(reflect.ValueOf(value))
				case field.Kind() == reflect.Pointer:
					field.Set(reflect.ValueOf(sourcePtr("")))
				case field.Kind() == reflect.Bool:
					field.SetBool(!field.Bool())
				default:
					field.SetString("changed")
				}
				s := sourceTestRows(t, rows)
				if err := s.birthSnapshots(); err != nil {
					t.Fatal(err)
				}
				if len(s.bad["pointer"]) == 0 {
					t.Fatal("changed frozen original column accepted")
				}
			})
		}
	}
	rows := sourceSnapshotFixture()
	rows.OriginalRows.UpstreamAttempts[0].RouteDecisions = strings.ReplaceAll(sourceRouteCanonical, ",", ", ")
	s := sourceTestRows(t, rows)
	if err := s.birthSnapshots(); err != nil {
		t.Fatal(err)
	}
	if len(s.bad["pointer"]) != 0 {
		t.Fatal("PG JSONB whitespace was compared as raw original bytes")
	}
	rows = sourceSnapshotFixture()
	rows.OriginalRows.RequestRecords = nil
	s = sourceTestRows(t, rows)
	if err := s.birthSnapshots(); err != nil {
		t.Fatal(err)
	}
	if len(s.bad["pointer"]) == 0 {
		t.Fatal("source without original accepted")
	}
	rows = sourceSnapshotFixture()
	rows.PrefixRows.RequestOrigins = nil
	s = sourceTestRows(t, rows)
	if err := s.birthSnapshots(); err != nil {
		t.Fatal(err)
	}
	if len(s.bad["pointer"]) == 0 {
		t.Fatal("tracked original without source accepted")
	}
}
func TestSourceBirthSnapshotEntryAndPhysicalRowDedup(t *testing.T) {
	run := func(rows Rows) sourceClosureResult {
		w, _, err := newReplayWork(context.Background(), rows)
		if err != nil {
			t.Fatal(err)
		}
		got, err := checkSourceClosure(w, rows)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	rows := sourceSnapshotFixture()
	// This entry fixture has no attempt nodes. The narrow birthSnapshot test
	// above deliberately uses later values; the complete entry now also checks
	// that current values follow the actual mutation/delivery chain.
	rows.OriginalRows.UpstreamAttempts[0].DeliveryState = "prepared"
	rows.OriginalRows.UpstreamAttempts[0].CostStatus = "unknown"
	rows.OriginalRows.UpstreamAttempts[0].CostAmount = nil
	if got := run(rows); got.Stats.OriginalPointerRows != 0 {
		t.Fatalf("complete source/original pointer fixture rejected: %+v", got.Stats)
	}
	rows.OriginalRows.RequestRecords[0].InputRate = "9"
	rows.OriginalRows.RequestRecords[0].OutputRate = "8"
	if got := run(rows); got.Stats.OriginalPointerRows != 2 {
		t.Fatalf("two bad fields count each of two physical rows once: %+v", got.Stats)
	}
	rows = sourceSnapshotFixture()
	rows.OriginalRows.UpstreamAttempts[0].PPLastCostMutationID = sourcePtr("legacy-fake")
	rows.OriginalRows.UpstreamAttempts[0].PPOriginRootID = nil
	rows.OriginalRows.UpstreamAttempts[0].PPOriginRootStep = nil
	rows.OriginalRows.UpstreamAttempts[0].PPOriginXID = nil
	rows.OriginalRows.UpstreamAttempts[0].PPQuoteSelectedAt = Time{}
	if got := run(rows); got.Stats.InvalidScalarRows != 1 {
		t.Fatalf("original NULL union phase missing from entry: %+v", got.Stats)
	}
}

func TestSourceRemainingLeavesCancellation(t *testing.T) {
	rows := Rows{OriginalRows: OriginalRows{RequestRecords: make([]RequestRecordRow, 1000)}}
	s := sourceTestRows(t, rows)
	s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 6}
	s.w.b.steps = 0
	if err := s.originalUnions(); !errors.Is(err, ErrCanceled) {
		t.Fatalf("original group loop did not cancel: %v", err)
	}
	rows = sourceSnapshotFixture()
	for i := 0; i < 100; i++ {
		rows.PrefixRows.RequestOrigins = append(rows.PrefixRows.RequestOrigins, rows.PrefixRows.RequestOrigins[0])
		rows.OriginalRows.RequestRecords = append(rows.OriginalRows.RequestRecords, rows.OriginalRows.RequestRecords[0])
	}
	s = sourceTestRows(t, rows)
	s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 2}
	s.w.b.steps = 255
	if err := s.birthSnapshots(); !errors.Is(err, ErrCanceled) {
		t.Fatalf("snapshot loop did not cancel: %v", err)
	}
}
