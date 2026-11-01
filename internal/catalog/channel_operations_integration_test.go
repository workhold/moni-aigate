package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

type channelOperationFixture struct {
	*credentialPoolFixture
	actor string
	batch *catalog.Store
}

func channelOperationDB(t *testing.T) *channelOperationFixture {
	t.Helper()
	f := credentialPoolDB(t, true)
	actor := uuid.NewString()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture','admin')`, actor, actor+"@channel-operation.invalid"); err != nil {
		t.Fatal(err)
	}
	// Batch operations need no cryptographic key or supplier-secret access.
	return &channelOperationFixture{credentialPoolFixture: f, actor: actor, batch: catalog.New(f.app, nil)}
}

func (f *channelOperationFixture) target(t *testing.T, channel string) catalog.ChannelOperationTarget {
	t.Helper()
	v := catalog.ChannelOperationTarget{ChannelID: channel}
	if err := f.owner.QueryRow(f.ctx, `SELECT current_version,management_revision::text FROM channels WHERE id=$1`, channel).Scan(&v.ExpectedVersion, &v.ExpectedRevision); err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *channelOperationFixture) newChannel(t *testing.T) string {
	t.Helper()
	id, err := f.store.SaveChannel(f.ctx, f.actor, "", catalog.ChannelInput{Name: "batch-channel", BaseURL: "https://example.net/v1", Secret: "batch-channel-secret"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *channelOperationFixture) apply(t *testing.T, id string, in catalog.ChannelOperationInput) catalog.ChannelOperationReceipt {
	t.Helper()
	receipt, err := f.batch.ApplyChannelOperation(f.ctx, f.actor, id, in)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func equalOperationReceipts(a, b catalog.ChannelOperationReceipt) bool {
	one, _ := json.Marshal(a)
	two, _ := json.Marshal(b)
	return bytes.Equal(one, two)
}

type channelOperationFacts struct {
	Credentials, Versions, Members, Audits, Receipts, Policies int64
}

func (f *channelOperationFixture) facts(t *testing.T) channelOperationFacts {
	t.Helper()
	var v channelOperationFacts
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM channel_credentials),(SELECT count(*) FROM channel_versions),(SELECT count(*) FROM channel_version_credentials),(SELECT count(*) FROM audit_events),(SELECT count(*) FROM channel_batch_operations),(SELECT count(*) FROM route_policy_versions)`).Scan(&v.Credentials, &v.Versions, &v.Members, &v.Audits, &v.Receipts, &v.Policies); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestChannelOperationPreviewIsReadOnlyAndCountsFrozenBindings(t *testing.T) {
	f := channelOperationDB(t)
	f.publish(t, 0)
	if _, err := f.store.SaveChannel(f.ctx, f.actor, f.channel, catalog.ChannelInput{Name: "unpublished-metadata-version"}); err != nil {
		t.Fatal(err)
	}
	known := f.target(t, f.channel)
	unknown := catalog.ChannelOperationTarget{ChannelID: uuid.NewString(), ExpectedVersion: uuid.NewString(), ExpectedRevision: "1"}
	before := f.facts(t)
	for i := 0; i < 2; i++ {
		preview, err := f.batch.PreviewChannelOperation(f.ctx, catalog.ChannelOperationInput{Action: "disable", Targets: []catalog.ChannelOperationTarget{known, unknown}})
		if err != nil || preview.CanApply || preview.ChangedCount != 0 || len(preview.Items) != 2 {
			t.Fatal("mixed preflight did not block the batch", preview, err)
		}
		for _, item := range preview.Items {
			if item.ChannelID == known.ChannelID {
				if item.Status != "ready" || item.Before == nil || item.Before.CurrentVersion != known.ExpectedVersion || item.LiveCredentials != 1 || item.PublishedBindings != 1 || item.OlderVersionBindings != 1 || item.After != nil || item.Reason != "" {
					t.Fatal("preflight lost frozen binding counts or current state", item)
				}
			} else if item.Status != "blocked" || item.Reason != "not_found" || item.Before != nil || item.LiveCredentials != 0 || item.PublishedBindings != 0 || item.OlderVersionBindings != 0 {
				t.Fatal("missing channel was given a fabricated state", item)
			}
		}
	}
	if f.facts(t) != before || f.target(t, f.channel) != known {
		t.Fatal("preflight wrote a fact or channel revision")
	}
}

func TestChannelOperationApplyReplayReorderConflictAndNoop(t *testing.T) {
	f := channelOperationDB(t)
	other := f.newChannel(t)
	one, two := f.target(t, f.channel), f.target(t, other)
	in := catalog.ChannelOperationInput{Action: "disable", Targets: []catalog.ChannelOperationTarget{two, one}}
	id := uuid.NewString()
	before := f.facts(t)
	receipt := f.apply(t, id, in)
	if receipt.OperationID != id || receipt.Actor != f.actor || receipt.Status != "applied" || receipt.TargetCount != 2 || receipt.ChangedCount != 2 || receipt.CreatedAt.IsZero() {
		t.Fatal("apply did not return its complete committed receipt", receipt)
	}
	for i, item := range receipt.Items {
		if item.Status != "changed" || item.Before == nil || item.After == nil || !item.Before.Enabled || item.After.Enabled || item.Reason != "" || item.After.CurrentVersion != item.Before.CurrentVersion || item.After.Quarantined != item.Before.Quarantined || item.After.ManagementRevision != "2" || i > 0 && receipt.Items[i-1].ChannelID >= item.ChannelID {
			t.Fatal("applied item did not preserve its immutable before/after facts", item)
		}
	}
	after := f.facts(t)
	if after.Credentials != before.Credentials || after.Versions != before.Versions || after.Members != before.Members || after.Policies != before.Policies || after.Audits != before.Audits+1 || after.Receipts != before.Receipts+1 {
		t.Fatal("batch changed non-status configuration or missed its unique audit")
	}
	queried, err := f.batch.ChannelOperation(f.ctx, id)
	if err != nil || !equalOperationReceipts(receipt, queried) {
		t.Fatal("receipt query changed committed facts", err)
	}
	in.Targets = []catalog.ChannelOperationTarget{one, two}
	replay := f.apply(t, id, in)
	if !equalOperationReceipts(receipt, replay) || f.facts(t) != after {
		t.Fatal("reordered replay executed the stale command again")
	}
	for _, conflict := range []struct {
		actor string
		in    catalog.ChannelOperationInput
	}{{uuid.NewString(), in}, {f.actor, catalog.ChannelOperationInput{Action: "enable", Targets: in.Targets}}} {
		if _, err = f.batch.ApplyChannelOperation(f.ctx, conflict.actor, id, conflict.in); !errors.Is(err, catalog.ErrConflict) || f.facts(t) != after {
			t.Fatal("operation ID accepted another actor or digest", err)
		}
	}
	currentOne, currentTwo := f.target(t, f.channel), f.target(t, other)
	noop := f.apply(t, uuid.NewString(), catalog.ChannelOperationInput{Action: "disable", Targets: []catalog.ChannelOperationTarget{currentOne, currentTwo}})
	if noop.Status != "applied" || noop.ChangedCount != 0 || f.target(t, f.channel) != currentOne || f.target(t, other) != currentTwo {
		t.Fatal("no-op manufactured a channel management revision", noop)
	}
	for _, item := range noop.Items {
		if item.Status != "unchanged" || !reflect.DeepEqual(item.Before, item.After) {
			t.Fatal("no-op receipt did not retain its state", item)
		}
	}
}

func TestChannelOperationMixedRejectionAndHistoricalReplay(t *testing.T) {
	f := channelOperationDB(t)
	other := f.newChannel(t)
	ready := f.target(t, f.channel)
	stale := f.target(t, other)
	stale.ExpectedRevision = "99"
	missing := catalog.ChannelOperationTarget{ChannelID: uuid.NewString(), ExpectedVersion: uuid.NewString(), ExpectedRevision: "1"}
	in := catalog.ChannelOperationInput{Action: "disable", Targets: []catalog.ChannelOperationTarget{ready, stale, missing}}
	id := uuid.NewString()
	before := f.facts(t)
	receipt := f.apply(t, id, in)
	if receipt.Status != "rejected" || receipt.ChangedCount != 0 || f.target(t, f.channel) != ready || f.target(t, other).ExpectedRevision != "1" {
		t.Fatal("mixed rejection partially changed channels", receipt)
	}
	for _, item := range receipt.Items {
		if item.After != nil {
			t.Fatal("rejected receipt claims an after state")
		}
		switch item.ChannelID {
		case ready.ChannelID:
			if item.Status != "not_applied" || item.Reason != "" {
				t.Fatal(item)
			}
		case stale.ChannelID:
			if item.Status != "blocked" || item.Reason != "stale_revision" {
				t.Fatal(item)
			}
		default:
			if item.Status != "blocked" || item.Reason != "not_found" || item.Before != nil {
				t.Fatal(item)
			}
		}
	}
	after := f.facts(t)
	if after.Audits != before.Audits+1 || after.Receipts != before.Receipts+1 || after.Versions != before.Versions {
		t.Fatal("rejection did not atomically preserve a single audit/receipt")
	}
	disabled := false
	if _, err := f.store.SaveChannel(f.ctx, f.actor, f.channel, catalog.ChannelInput{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	changedFacts := f.facts(t)
	if replay := f.apply(t, id, in); !equalOperationReceipts(receipt, replay) || f.facts(t) != changedFacts {
		t.Fatal("rejected replay re-evaluated later channel state")
	}
	queried, err := f.batch.ChannelOperation(f.ctx, id)
	if err != nil || !equalOperationReceipts(receipt, queried) {
		t.Fatal("historical receipt was overwritten by current state", err)
	}
}

func TestChannelOperationEnableGuardsAndManagementABA(t *testing.T) {
	t.Run("quarantine_before_unchanged", func(t *testing.T) {
		f := channelOperationDB(t)
		if _, err := f.owner.Exec(f.ctx, `UPDATE channels SET enabled=true,quarantined=true WHERE id=$1`, f.channel); err != nil {
			t.Fatal(err)
		}
		target := f.target(t, f.channel)
		receipt := f.apply(t, uuid.NewString(), catalog.ChannelOperationInput{Action: "enable", Targets: []catalog.ChannelOperationTarget{target}})
		if receipt.Status != "rejected" || receipt.Items[0].Reason != "channel_quarantined" || f.target(t, f.channel) != target {
			t.Fatal("already-enabled quarantined channel was treated as valid", receipt)
		}
		disabled := f.apply(t, uuid.NewString(), catalog.ChannelOperationInput{Action: "disable", Targets: []catalog.ChannelOperationTarget{target}})
		if disabled.Status != "applied" || !disabled.Items[0].After.Quarantined || disabled.Items[0].After.Enabled {
			t.Fatal("disable cleared quarantine or failed to close its channel", disabled)
		}
	})
	t.Run("revoked_before_unchanged", func(t *testing.T) {
		f := channelOperationDB(t)
		pool := f.pool(t, "")
		if err := f.store.RevokeCredential(f.ctx, f.actor, f.channel, pool.Members[0].CredentialID); err != nil {
			t.Fatal(err)
		}
		target := f.target(t, f.channel)
		receipt := f.apply(t, uuid.NewString(), catalog.ChannelOperationInput{Action: "enable", Targets: []catalog.ChannelOperationTarget{target}})
		if receipt.Status != "rejected" || receipt.Items[0].Reason != "no_live_credential" || f.target(t, f.channel) != target {
			t.Fatal("already-enabled all-revoked channel was treated as valid", receipt)
		}
		disabled := f.apply(t, uuid.NewString(), catalog.ChannelOperationInput{Action: "disable", Targets: []catalog.ChannelOperationTarget{target}})
		if disabled.Status != "applied" || disabled.Items[0].After.Enabled || disabled.Items[0].LiveCredentials != 0 {
			t.Fatal("disable required a live supplier credential", disabled)
		}
	})
	t.Run("same_version_boolean_ABA", func(t *testing.T) {
		f := channelOperationDB(t)
		target := f.target(t, f.channel)
		for _, enabled := range []bool{false, true} {
			if _, err := f.owner.Exec(f.ctx, `UPDATE channels SET enabled=$2,management_revision=1 WHERE id=$1`, f.channel, enabled); err != nil {
				t.Fatal(err)
			}
		}
		if current := f.target(t, f.channel); current.ExpectedVersion != target.ExpectedVersion || current.ExpectedRevision != "3" {
			t.Fatal("revision did not detect a same-version boolean ABA", current)
		}
		receipt := f.apply(t, uuid.NewString(), catalog.ChannelOperationInput{Action: "enable", Targets: []catalog.ChannelOperationTarget{target}})
		if receipt.Status != "rejected" || receipt.Items[0].Reason != "stale_revision" {
			t.Fatal("stale no-op bypassed the management revision", receipt)
		}
		if _, err := f.store.SaveChannel(f.ctx, f.actor, f.channel, catalog.ChannelInput{Name: "metadata-new-version"}); err != nil {
			t.Fatal(err)
		}
		receipt = f.apply(t, uuid.NewString(), catalog.ChannelOperationInput{Action: "disable", Targets: []catalog.ChannelOperationTarget{target}})
		if receipt.Items[0].Reason != "stale_version" {
			t.Fatal("stale version precedence was lost", receipt)
		}
	})
}

func TestChannelOperationConcurrentReplayAndGlobalChannelLockOrder(t *testing.T) {
	for _, sameID := range []bool{true, false} {
		t.Run(fmt.Sprintf("same_id_%v", sameID), func(t *testing.T) {
			f := channelOperationDB(t)
			other := f.newChannel(t)
			one, two := f.target(t, f.channel), f.target(t, other)
			ids := []string{uuid.NewString(), uuid.NewString()}
			if sameID {
				ids[1] = ids[0]
			}
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			type outcome struct {
				receipt catalog.ChannelOperationReceipt
				err     error
			}
			results := make(chan outcome, 2)
			start := make(chan struct{})
			var workers sync.WaitGroup
			for i := 0; i < 2; i++ {
				workers.Add(1)
				go func(i int) {
					defer workers.Done()
					<-start
					targets := []catalog.ChannelOperationTarget{one, two}
					if i == 1 {
						targets[0], targets[1] = targets[1], targets[0]
					}
					r, err := f.batch.ApplyChannelOperation(ctx, f.actor, ids[i], catalog.ChannelOperationInput{Action: "disable", Targets: targets})
					results <- outcome{r, err}
				}(i)
			}
			close(start)
			workers.Wait()
			first, second := <-results, <-results
			if first.err != nil || second.err != nil {
				t.Fatal("concurrent command failed or deadlocked", first.err, second.err)
			}
			if sameID {
				if !equalOperationReceipts(first.receipt, second.receipt) || first.receipt.Status != "applied" || f.facts(t).Receipts != 1 {
					t.Fatal("same-ID waiter did not see the committed original receipt")
				}
			} else if first.receipt.Status == second.receipt.Status || f.facts(t).Receipts != 2 {
				t.Fatal("different IDs both applied stale snapshots or lost a rejection")
			}
			if f.target(t, f.channel).ExpectedRevision != "2" || f.target(t, other).ExpectedRevision != "2" {
				t.Fatal("concurrent replay incremented a channel more than once")
			}
		})
	}
}

func (f *channelOperationFixture) singleConnectionBatch(t *testing.T) (*catalog.Store, string) {
	t.Helper()
	cfg := f.app.Config()
	cfg.MaxConns = 1
	name := "channel_op_" + uuid.NewString()
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	p, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return catalog.New(p, nil), name
}

func (f *channelOperationFixture) waitForLock(t *testing.T, application string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, application).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("batch did not reach the expected PostgreSQL lock barrier")
}

func TestChannelOperationLockWaitReadsFreshChannelAndCredentialState(t *testing.T) {
	for _, credentialBarrier := range []bool{false, true} {
		t.Run(fmt.Sprintf("credential_barrier_%v", credentialBarrier), func(t *testing.T) {
			f := channelOperationDB(t)
			if credentialBarrier {
				disabled := false
				if _, err := f.store.SaveChannel(f.ctx, f.actor, f.channel, catalog.ChannelInput{Enabled: &disabled}); err != nil {
					t.Fatal(err)
				}
			}
			target := f.target(t, f.channel)
			pool := f.pool(t, "")
			blocker, err := f.owner.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(f.ctx)
			if credentialBarrier {
				_, err = blocker.Exec(f.ctx, `SELECT id FROM channel_credentials WHERE id=$1 FOR UPDATE`, pool.Members[0].CredentialID)
			} else {
				_, err = blocker.Exec(f.ctx, `SELECT id FROM channels WHERE id=$1 FOR UPDATE`, f.channel)
			}
			if err != nil {
				t.Fatal(err)
			}
			batch, application := f.singleConnectionBatch(t)
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			type outcome struct {
				receipt catalog.ChannelOperationReceipt
				err     error
			}
			result := make(chan outcome, 1)
			go func() {
				r, err := batch.ApplyChannelOperation(ctx, f.actor, uuid.NewString(), catalog.ChannelOperationInput{Action: "enable", Targets: []catalog.ChannelOperationTarget{target}})
				result <- outcome{r, err}
			}()
			f.waitForLock(t, application)
			if credentialBarrier {
				_, err = blocker.Exec(f.ctx, `UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE id=$1`, pool.Members[0].CredentialID)
			} else {
				_, err = blocker.Exec(f.ctx, `UPDATE channels SET enabled=false WHERE id=$1`, f.channel)
				if err == nil {
					_, err = blocker.Exec(f.ctx, `UPDATE channels SET enabled=true WHERE id=$1`, f.channel)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = blocker.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			got := <-result
			reason := "stale_revision"
			if credentialBarrier {
				reason = "no_live_credential"
			}
			if got.err != nil || got.receipt.Status != "rejected" || got.receipt.Items[0].Reason != reason {
				t.Fatal("apply used a snapshot from before its lock wait", got.receipt, got.err)
			}
			if _, err = batch.ChannelOperation(f.ctx, got.receipt.OperationID); err != nil {
				t.Fatal("single-connection operation leaked an open result or transaction", err)
			}
		})
	}
}

func TestChannelOperationAuditAndActorFailureRollbackOnSingleConnection(t *testing.T) {
	f := channelOperationDB(t)
	f.batch, _ = f.singleConnectionBatch(t)
	target := f.target(t, f.channel)
	in := catalog.ChannelOperationInput{Action: "disable", Targets: []catalog.ChannelOperationTarget{target}}
	id := uuid.NewString()
	before := f.facts(t)
	if _, err := f.owner.Exec(f.ctx, "REVOKE INSERT ON audit_events FROM "+pgx.Identifier{f.appRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.batch.ApplyChannelOperation(f.ctx, f.actor, id, in); err == nil || f.target(t, f.channel) != target || f.facts(t) != before {
		t.Fatal("audit rejection left a channel change or partial receipt", err)
	}
	if _, err := f.batch.ChannelOperation(f.ctx, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("failed transaction exposed a committed receipt", err)
	}
	if _, err := f.owner.Exec(f.ctx, "GRANT INSERT ON audit_events TO "+pgx.Identifier{f.appRole}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.batch.ApplyChannelOperation(f.ctx, uuid.NewString(), id, in); err == nil || f.target(t, f.channel) != target || f.facts(t) != before {
		t.Fatal("actor foreign-key rejection did not roll back channel state", err)
	}
	if receipt := f.apply(t, id, in); receipt.Status != "applied" || receipt.ChangedCount != 1 {
		t.Fatal("rolled-back operation ID could not be retried explicitly", receipt)
	}
}

func TestChannelOperationInputValidationAndHundredTargetBoundary(t *testing.T) {
	f := channelOperationDB(t)
	target := f.target(t, f.channel)
	cases := map[string]catalog.ChannelOperationInput{
		"empty":        {Action: "disable"},
		"action":       {Action: "rotate", Targets: []catalog.ChannelOperationTarget{target}},
		"duplicate":    {Action: "disable", Targets: []catalog.ChannelOperationTarget{target, target}},
		"noncanonical": {Action: "disable", Targets: []catalog.ChannelOperationTarget{{ChannelID: "urn:uuid:" + target.ChannelID, ExpectedVersion: target.ExpectedVersion, ExpectedRevision: "1"}}},
	}
	for _, revision := range []string{"", "0", "01", "+1", "-1", "1e1", " 1", "9223372036854775808"} {
		invalid := target
		invalid.ExpectedRevision = revision
		cases["revision_"+revision] = catalog.ChannelOperationInput{Action: "disable", Targets: []catalog.ChannelOperationTarget{invalid}}
	}
	invalidVersion := target
	invalidVersion.ExpectedVersion = strings.ToUpper(target.ExpectedVersion)
	cases["uppercase_version"] = catalog.ChannelOperationInput{Action: "disable", Targets: []catalog.ChannelOperationTarget{invalidVersion}}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			before := f.facts(t)
			if _, err := f.batch.PreviewChannelOperation(f.ctx, in); !errors.Is(err, catalog.ErrInvalid) {
				t.Fatal("preflight accepted malformed input", err)
			}
			if _, err := f.batch.ApplyChannelOperation(f.ctx, f.actor, uuid.NewString(), in); !errors.Is(err, catalog.ErrInvalid) || f.facts(t) != before || f.target(t, f.channel) != target {
				t.Fatal("malformed apply wrote a receipt or channel", err)
			}
		})
	}
	if _, err := f.batch.ApplyChannelOperation(f.ctx, f.actor, "invalid-id", catalog.ChannelOperationInput{Action: "disable", Targets: []catalog.ChannelOperationTarget{target}}); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatal("apply accepted a noncanonical operation ID", err)
	}
	if _, err := f.batch.ChannelOperation(f.ctx, "invalid-id"); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatal("receipt query accepted a malformed operation ID", err)
	}
	targets := []catalog.ChannelOperationTarget{target}
	for len(targets) < 100 {
		targets = append(targets, f.target(t, f.newChannel(t)))
	}
	preview, err := f.batch.PreviewChannelOperation(f.ctx, catalog.ChannelOperationInput{Action: "disable", Targets: targets})
	if err != nil || !preview.CanApply || preview.ChangedCount != 100 || len(preview.Items) != 100 {
		t.Fatal("100-target preflight rejected valid bounds", err)
	}
	receipt := f.apply(t, uuid.NewString(), catalog.ChannelOperationInput{Action: "disable", Targets: targets})
	if receipt.Status != "applied" || receipt.TargetCount != 100 || receipt.ChangedCount != 100 || len(receipt.Items) != 100 {
		t.Fatal("100-target apply did not atomically change the complete set")
	}
	for _, item := range receipt.Items {
		if item.Status != "changed" || item.After.ManagementRevision != strconv.Itoa(2) {
			t.Fatal("large batch did not record each actual revision")
		}
	}
	targets = append(targets, catalog.ChannelOperationTarget{ChannelID: uuid.NewString(), ExpectedVersion: uuid.NewString(), ExpectedRevision: "1"})
	before := f.facts(t)
	if _, err = f.batch.ApplyChannelOperation(f.ctx, f.actor, uuid.NewString(), catalog.ChannelOperationInput{Action: "disable", Targets: targets}); !errors.Is(err, catalog.ErrInvalid) || f.facts(t) != before {
		t.Fatal("101-target apply bypassed the maximum bound", err)
	}
}
