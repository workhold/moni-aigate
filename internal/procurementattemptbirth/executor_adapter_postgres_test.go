//go:build historical

package procurementattemptbirth_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/procurement"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
)

func abAdapterCommandOracle(t *testing.T, f *fixture, got procurement.Command) {
	t.Helper()
	want, err := procurement.New(f.app).Command(f.ctx, got.OperationID)
	must(t, "read original immutable public command", err)
	if got.OperationID != want.OperationID || got.Actor != want.Actor || got.Kind != want.Kind || got.TargetID != want.TargetID || got.Status != want.Status || got.Reason != want.Reason || !got.CreatedAt.Equal(want.CreatedAt) || !abExecutorJSONEqual(t, got.Before, want.Before) || !abExecutorJSONEqual(t, got.After, want.After) {
		t.Fatal("adapter changed original public command DTO")
	}
}

func TestPostgresExecutorAdapterAccountPublicDTOAndReplay(t *testing.T) {
	f := newFixture(t)
	s, err := procurement.NewProspective(f.app, procurement.ProspectiveOptions{Schema: f.b.Schema})
	must(t, "construct explicit account adapter", err)
	in := procurement.CreateInput{OperationID: uuid.NewString(), Label: "adapter", Provider: "fixture", State: "active", LimitAmount: "300"}
	created, err := s.CreateAccount(f.ctx, f.admin, in)
	must(t, "actual APP account adapter create", err)
	abAdapterCommandOracle(t, f, created)
	for _, tc := range []struct{ name, target, revision, limit, status, reason string }{
		{"changed", created.TargetID, "1", "400", "applied", ""},
		{"noop", created.TargetID, "2", "400", "applied", ""},
		{"revision_conflict", created.TargetID, "1", "500", "rejected", "revision_conflict"},
		{"not_found", uuid.NewString(), "1", "500", "rejected", "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := procurement.ConfigurationInput{OperationID: uuid.NewString(), ExpectedRevision: tc.revision, Label: "adapter", Provider: "fixture", State: "active", LimitAmount: tc.limit}
			got, e := s.ConfigureAccount(f.ctx, f.admin, tc.target, config)
			must(t, "actual APP configure adapter", e)
			if got.Status != tc.status || got.Reason != tc.reason {
				t.Fatal("public rejection or noop changed")
			}
			abAdapterCommandOracle(t, f, got)
			before := f.snapshot(t)
			replay, e := s.ConfigureAccount(f.ctx, f.admin, tc.target, config)
			must(t, "configure adapter replay", e)
			if !reflect.DeepEqual(replay, got) || f.snapshot(t) != before {
				t.Fatal("adapter replay changed DTO or 34-table state")
			}
		})
	}
	before := f.snapshot(t)
	replay, err := s.CreateAccount(f.ctx, f.admin, in)
	must(t, "old create adapter replay after configuration", err)
	if !reflect.DeepEqual(replay, created) || f.snapshot(t) != before {
		t.Fatal("create adapter substituted mutable current account")
	}
}

func TestPostgresExecutorAdapterAttemptPublicDTOBudgetAndConflict(t *testing.T) {
	secondCredential := uuid.NewString()
	secondVersion := uuid.NewString()
	f := newFixture(t, func(f *fixture) {
		tx, err := f.owner.Begin(f.ctx)
		must(t, "begin original alternate version publication", err)
		defer tx.Rollback(f.ctx)
		_, err = tx.Exec(f.ctx, "INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unused-fixture')", secondCredential, f.channel)
		must(t, "prepare genuine second credential before candidate installation", err)
		_, err = tx.Exec(f.ctx, "INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)", secondVersion, f.channel, secondCredential)
		must(t, "create original alternate immutable version", err)
		_, err = tx.Exec(f.ctx, "INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'adapter-second',1,10)", secondVersion, f.channel, secondCredential)
		must(t, "publish credential pool with its original version transaction", err)
		must(t, "commit original alternate version publication", tx.Commit(f.ctx))
	})
	account := f.create(t, 300)
	f.bind(t, account.TargetID)
	s, err := billing.NewProspective(f.app, billing.ProspectiveOptions{Schema: f.b.Schema})
	must(t, "construct explicit billing adapter", err)
	request := f.reserve(t)
	q := abExecutorAttemptInput(f, request.RequestID)
	in := billing.FrozenAttemptInput{TenantID: q.TenantID, RequestID: q.RequestID, Sequence: int(q.Sequence), ChannelID: q.ChannelID, ChannelVersionID: q.ChannelVersionID, CredentialID: q.CredentialID, UpstreamModel: q.UpstreamModel, CostQuote: q.CostQuote}
	got, err := s.RecordAttempt(f.ctx, in)
	must(t, "actual APP billing adapter committed attempt", err)
	var root, source string
	must(t, "independently locate committed private birth", f.app.QueryRow(f.ctx, "SELECT pp_origin_root_id,id FROM upstream_attempts WHERE id=$1", got.ID).Scan(&root, &source))
	abExecutorAttemptOracle(t, f, procurementexecution.AttemptReceipt{Outcome: "applied", RootID: &root, SourceID: &source, AttemptID: got.ID, RequestID: got.RequestID, Sequence: int32(got.Sequence), DeliveryState: got.DeliveryState, CostStatus: got.CostStatus}, q, true)
	before := f.snapshot(t)
	replay, err := s.RecordAttempt(f.ctx, in)
	must(t, "billing adapter replay", err)
	if replay != got || f.snapshot(t) != before {
		t.Fatal("attempt adapter replay changed public DTO or state")
	}
	assertRejection := func(input billing.FrozenAttemptInput, code, state string) {
		t.Helper()
		before := f.snapshot(t)
		zero, e := s.RecordAttempt(f.ctx, input)
		var public *billing.Error
		var pg *pgconn.PgError
		if zero != (billing.Attempt{}) || !errors.As(e, &public) || public.Code != code || !errors.As(e, &pg) || pg.Code != state || f.snapshot(t) != before {
			t.Fatalf("adapter error lost zero DTO/classification/cause/rollback: %v", e)
		}
	}
	conflict := in
	conflict.CredentialID = secondCredential
	conflict.ChannelVersionID = secondVersion
	assertRejection(conflict, "idempotency_conflict", "PP116")
	second := in
	second.RequestID = f.reserve(t).RequestID
	assertRejection(second, "procurement_budget_exceeded", "PP115")
}
