package operations

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func operationsChannelInput(t *testing.T, f *operationFixture, action string) catalog.ChannelOperationInput {
	t.Helper()
	target := catalog.ChannelOperationTarget{ChannelID: f.channel}
	if err := f.app.QueryRow(f.ctx, `SELECT current_version,management_revision::text FROM channels WHERE id=$1`, f.channel).Scan(&target.ExpectedVersion, &target.ExpectedRevision); err != nil {
		t.Fatal(err)
	}
	return catalog.ChannelOperationInput{Action: action, Targets: []catalog.ChannelOperationTarget{target}}
}

func TestProvisionedChannelOperationReceiptIsReadableAppendOnly(t *testing.T) {
	f := operationDB(t)
	c := catalog.New(f.app, f.secrets)
	in := operationsChannelInput(t, f, "disable")
	id := uuid.NewString()
	receipt, err := c.ApplyChannelOperation(f.ctx, f.operator, id, in)
	if err != nil || receipt.Status != "applied" || receipt.ChangedCount != 1 {
		t.Fatalf("app cannot atomically apply and append receipt: %+v %v", receipt, err)
	}
	read, err := c.ChannelOperation(f.ctx, id)
	if err != nil || !reflect.DeepEqual(receipt, read) {
		t.Fatalf("app cannot read complete immutable receipt: %+v %v", read, err)
	}
	for _, sql := range []string{
		`UPDATE channel_batch_operations SET changed_count=0 WHERE operation_id=$1`,
		`DELETE FROM channel_batch_operations WHERE operation_id=$1`,
	} {
		_, err = f.app.Exec(f.ctx, sql, id)
		requirePoolSQLState(t, err, "42501")
		_, err = f.owner.Exec(f.ctx, sql, id)
		requirePoolSQLState(t, err, "P0001")
	}
	for _, sql := range []string{
		`SELECT operation_id FROM channel_batch_operations`,
		`INSERT INTO channel_batch_operations SELECT * FROM channel_batch_operations WHERE false`,
		`UPDATE channel_batch_operations SET actor=actor`,
		`DELETE FROM channel_batch_operations`,
	} {
		_, err = f.projection.Exec(f.ctx, sql)
		requirePoolSQLState(t, err, "42501")
	}
	enable := operationsChannelInput(t, f, "enable")
	if result, applyErr := c.ApplyChannelOperation(f.ctx, f.operator, uuid.NewString(), enable); applyErr != nil || result.Status != "applied" {
		t.Fatalf("later channel change failed: %+v %v", result, applyErr)
	}
	beforeReplay := operationsChannelInput(t, f, "enable")
	replayed, err := c.ApplyChannelOperation(f.ctx, f.operator, id, in)
	if err != nil || !reflect.DeepEqual(receipt, replayed) {
		t.Fatalf("replay reconstructed historical receipt from current state: %+v %v", replayed, err)
	}
	if afterReplay := operationsChannelInput(t, f, "enable"); !reflect.DeepEqual(beforeReplay, afterReplay) {
		t.Fatal("historical replay mutated the current channel")
	}
	var count int
	var diff string
	if err = f.app.QueryRow(f.ctx, `SELECT count(*),min(sanitized_diff::text) FROM audit_events WHERE target=$1 AND action IN ('channel.batch.apply','channel.batch.reject')`, id).Scan(&count, &diff); err != nil || count != 1 || diff != "{}" {
		t.Fatalf("operation did not retain exactly one bounded audit: %d %q %v", count, diff, err)
	}
	_, err = f.app.Exec(f.ctx, `INSERT INTO audit_events(actor,action,target) VALUES($1,'channel.batch.reject',$2)`, f.operator, id)
	requirePoolSQLState(t, err, "23505")
	body, err := json.Marshal(replayed)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"provider-old-secret", "https://example.com", "secret_ciphertext", "base_url", "input_digest"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("receipt disclosed forbidden field %q", forbidden)
		}
	}
}

func TestProvisionedChannelManagementRevisionCannotBeForged(t *testing.T) {
	f := operationDB(t)
	in := operationsChannelInput(t, f, "disable")
	revision, err := strconv.ParseInt(in.Targets[0].ExpectedRevision, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE channels SET management_revision=9223372036854775807 WHERE id=$1 RETURNING management_revision`,
		`UPDATE channels SET enabled=enabled WHERE id=$1 RETURNING management_revision`,
		`UPDATE channels SET name=name || '-renamed' WHERE id=$1 RETURNING management_revision`,
	} {
		var actual int64
		if err = f.app.QueryRow(f.ctx, sql, f.channel).Scan(&actual); err != nil || actual != revision+1 {
			t.Fatalf("ordinary app UPDATE bypassed revision: before=%d after=%d err=%v", revision, actual, err)
		}
		revision = actual
	}
	var actual int64
	if err = f.owner.QueryRow(f.ctx, `UPDATE channels SET management_revision=0 WHERE id=$1 RETURNING management_revision`, f.channel).Scan(&actual); err != nil || actual != revision+1 {
		t.Fatalf("owner ordinary UPDATE reset revision: before=%d after=%d err=%v", revision, actual, err)
	}
	// A caller-supplied INSERT revision must also be replaced, while completing
	// the channel, version and frozen membership in the same transaction.
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	channel, credential, version := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err = tx.QueryRow(f.ctx, `INSERT INTO channels(id,name,current_version,management_revision) VALUES($1,'forged-revision',$2,999) RETURNING management_revision`, channel, version).Scan(&actual); err != nil || actual != 1 {
		t.Fatalf("INSERT retained forged revision: %d %v", actual, err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'fixture-ciphertext')`, credential, channel); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://example.com',$3)`, version, channel, credential); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1)`, version, channel, credential); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionedChannelOperationDatabaseRejectsUnsafeReceipts(t *testing.T) {
	f := operationDB(t)
	c := catalog.New(f.app, f.secrets)
	receipt, err := c.ApplyChannelOperation(f.ctx, f.operator, uuid.NewString(), operationsChannelInput(t, f, "disable"))
	if err != nil {
		t.Fatal(err)
	}
	var digest string
	if err = f.app.QueryRow(f.ctx, `SELECT input_digest FROM channel_batch_operations WHERE operation_id=$1`, receipt.OperationID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	type row struct {
		id, actor, action, digest, status string
		targets, changes                  int
		items                             any
	}
	fresh := func() row {
		body, marshalErr := json.Marshal(receipt.Items)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		var items any
		if decodeErr := json.Unmarshal(body, &items); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return row{uuid.NewString(), f.operator, receipt.Action, digest, receipt.Status, 1, 1, items}
	}
	item := func(r *row) map[string]any { return r.items.([]any)[0].(map[string]any) }
	state := func(r *row, key string) map[string]any { return item(r)[key].(map[string]any) }
	insert := func(r row) error {
		body, marshalErr := json.Marshal(r.items)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		_, insertErr := f.app.Exec(f.ctx, `INSERT INTO channel_batch_operations(operation_id,actor,action,input_digest,status,target_count,changed_count,items) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, r.id, r.actor, r.action, r.digest, r.status, r.targets, r.changes, body)
		return insertErr
	}
	for _, tc := range []struct {
		name   string
		mutate func(*row)
	}{
		{"operation-not-uuid", func(r *row) { r.id = "invalid" }},
		{"invalid-action", func(r *row) { r.action = "delete" }},
		{"invalid-status", func(r *row) { r.status = "pending" }},
		{"invalid-digest", func(r *row) { r.digest = strings.Repeat("G", 64) }},
		{"empty-targets", func(r *row) { r.targets = 0 }},
		{"too-many-targets", func(r *row) { r.targets = 101 }},
		{"negative-changes", func(r *row) { r.changes = -1 }},
		{"excess-changes", func(r *row) { r.changes = 2 }},
		{"wrong-change-count", func(r *row) { r.changes = 0 }},
		{"items-object", func(r *row) { r.items = map[string]any{} }},
		{"items-empty", func(r *row) { r.items = []any{} }},
		{"items-count-mismatch", func(r *row) { r.targets = 2 }},
		{"item-unknown-secret", func(r *row) { item(r)["secret"] = "must-never-persist" }},
		{"before-unknown-url", func(r *row) { state(r, "before")["base_url"] = "https://private.invalid" }},
		{"after-unknown-error", func(r *row) { state(r, "after")["error"] = "must-never-persist" }},
		{"missing-before", func(r *row) { delete(item(r), "before") }},
		{"missing-after", func(r *row) { delete(item(r), "after") }},
		{"before-null", func(r *row) { item(r)["before"] = nil }},
		{"after-null", func(r *row) { item(r)["after"] = nil }},
		{"channel-not-uuid", func(r *row) { item(r)["channel_id"] = "arbitrary" }},
		{"version-not-uuid", func(r *row) { item(r)["expected_version"] = "arbitrary" }},
		{"revision-number", func(r *row) { item(r)["expected_revision"] = 1 }},
		{"revision-leading-zero", func(r *row) { item(r)["expected_revision"] = "01" }},
		{"revision-overflow", func(r *row) { item(r)["expected_revision"] = "9223372036854775808" }},
		{"before-enabled-string", func(r *row) { state(r, "before")["enabled"] = "true" }},
		{"after-wrong-revision", func(r *row) { state(r, "after")["management_revision"] = state(r, "before")["management_revision"] }},
		{"after-wrong-version", func(r *row) { state(r, "after")["current_version"] = uuid.NewString() }},
		{"after-wrong-quarantine", func(r *row) { state(r, "after")["quarantined"] = true }},
		{"after-wrong-enabled", func(r *row) { state(r, "after")["enabled"] = true }},
		{"live-over-pool-bound", func(r *row) { item(r)["live_credentials"] = 33 }},
		{"metric-negative", func(r *row) { item(r)["published_bindings"] = -1 }},
		{"metric-fraction", func(r *row) { item(r)["published_bindings"] = 1.5 }},
		{"metric-overflow", func(r *row) { item(r)["published_bindings"] = int64(2147483648) }},
		{"older-more-than-published", func(r *row) { item(r)["older_version_bindings"] = 2 }},
		{"unbounded-reason", func(r *row) { item(r)["reason"] = strings.Repeat("x", 262144) }},
		{"nonempty-success-reason", func(r *row) { item(r)["reason"] = "provider-error" }},
		{"preflight-status-in-receipt", func(r *row) { item(r)["status"] = "ready" }},
		{"rejected-with-changed-item", func(r *row) { r.status = "rejected" }},
		{"rejected-without-blocked", func(r *row) {
			r.status, r.changes = "rejected", 0
			item(r)["status"] = "not_applied"
			delete(item(r), "after")
		}},
		{"blocked-with-after", func(r *row) {
			r.status, r.changes = "rejected", 0
			item(r)["status"], item(r)["reason"] = "blocked", "stale_revision"
			item(r)["expected_revision"] = "9223372036854775807"
		}},
		{"blocked-with-unsafe-reason", func(r *row) {
			r.status, r.changes = "rejected", 0
			item(r)["status"], item(r)["reason"] = "blocked", "raw-provider-error"
			delete(item(r), "after")
		}},
		{"blocked-with-untrue-reason", func(r *row) {
			r.status, r.changes = "rejected", 0
			item(r)["status"], item(r)["reason"] = "blocked", "stale_revision"
			delete(item(r), "after")
		}},
		{"duplicate-items", func(r *row) { r.targets, r.changes = 2, 2; r.items = append(r.items.([]any), item(r)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fresh()
			tc.mutate(&r)
			requirePoolSQLState(t, insert(r), "23514")
		})
	}
	r := fresh()
	r.actor = uuid.NewString()
	requirePoolSQLState(t, insert(r), "23503")
	r = fresh()
	r.id = receipt.OperationID
	requirePoolSQLState(t, insert(r), "23505")
	// The exact upper bound is usable; database checks also reject 101 items
	// independently of application-level validation.
	var bounded []any
	for i := 0; i < 101; i++ {
		r = fresh()
		entry := item(&r)
		entry["channel_id"] = fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		bounded = append(bounded, entry)
	}
	sort.Slice(bounded, func(i, j int) bool {
		return bounded[i].(map[string]any)["channel_id"].(string) < bounded[j].(map[string]any)["channel_id"].(string)
	})
	r = fresh()
	r.items, r.targets, r.changes = bounded[:100], 100, 100
	if err = insert(r); err != nil {
		t.Fatalf("valid 100-item receipt rejected: %v", err)
	}
	r.id, r.items, r.targets, r.changes = uuid.NewString(), bounded, 101, 101
	requirePoolSQLState(t, insert(r), "23514")
}

func TestProvisionedChannelOperationAuditFailureRollsBack(t *testing.T) {
	f := operationDB(t)
	c := catalog.New(f.app, f.secrets)
	in := operationsChannelInput(t, f, "disable")
	id := uuid.NewString()
	_, err := f.owner.Exec(f.ctx, `CREATE FUNCTION fail_channel_batch_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action IN ('channel.batch.apply','channel.batch.reject') THEN RAISE EXCEPTION 'fixture channel batch audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_channel_batch_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_channel_batch_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ApplyChannelOperation(f.ctx, f.operator, id, in); err == nil {
		t.Fatal("audit failure returned an apparent successful apply")
	}
	if after := operationsChannelInput(t, f, "disable"); !reflect.DeepEqual(in, after) {
		t.Fatal("audit failure committed the channel revision")
	}
	var enabled bool
	var receipts, audits int
	if err = f.app.QueryRow(f.ctx, `SELECT enabled,(SELECT count(*) FROM channel_batch_operations WHERE operation_id=$2),(SELECT count(*) FROM audit_events WHERE target=$2) FROM channels WHERE id=$1`, f.channel, id).Scan(&enabled, &receipts, &audits); err != nil || !enabled || receipts != 0 || audits != 0 {
		t.Fatalf("audit failure left partial business/history facts: enabled=%t receipts=%d audits=%d err=%v", enabled, receipts, audits, err)
	}
	if _, err = f.owner.Exec(f.ctx, `DROP TRIGGER fail_channel_batch_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	receipt, err := c.ApplyChannelOperation(f.ctx, f.operator, id, in)
	if err != nil || receipt.Status != "applied" || receipt.ChangedCount != 1 {
		t.Fatalf("rolled-back operation ID could not be retried: %+v %v", receipt, err)
	}
}
