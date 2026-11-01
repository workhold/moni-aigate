package operations

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/procurement"
)

func procurementAdmin(t *testing.T, f *operationFixture) string {
	t.Helper()
	id := uuid.NewString()
	f.exec(t, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture','admin')`, id, id+"@procurement.invalid")
	return id
}
func procurementAccount(t *testing.T, f *operationFixture, s *procurement.Store, admin string) procurement.Account {
	t.Helper()
	r, err := s.CreateAccount(f.ctx, admin, procurement.CreateInput{OperationID: uuid.NewString(), Label: "shared purchase", Provider: "supplier", State: "active", LimitAmount: "9007199254740993"})
	if err != nil || r.Status != "applied" {
		t.Fatalf("create: %+v %v", r, err)
	}
	var a procurement.Account
	if err = json.Unmarshal(r.After, &a); err != nil {
		t.Fatal(err)
	}
	return a
}
func procurementBind(t *testing.T, f *operationFixture, s *procurement.Store, admin, account string) procurement.Binding {
	t.Helper()
	var credential, revision string
	if err := f.app.QueryRow(f.ctx, `SELECT v.credential_id,c.management_revision::text FROM channels c JOIN channel_versions v ON v.id=c.current_version WHERE c.id=$1`, f.channel).Scan(&credential, &revision); err != nil {
		t.Fatal(err)
	}
	r, err := s.BindCredential(f.ctx, admin, procurement.BindingInput{OperationID: uuid.NewString(), ChannelID: f.channel, CredentialID: credential, AccountID: account, ExpectedChannelRevision: revision})
	if err != nil || r.Status != "applied" {
		t.Fatalf("bind %+v %v", r, err)
	}
	var b procurement.Binding
	if err = json.Unmarshal(r.After, &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProcurementManagementCASDurableReceiptsAndRoles(t *testing.T) {
	f := operationDB(t)
	s := procurement.New(f.app)
	admin := procurementAdmin(t, f)
	in := procurement.CreateInput{OperationID: uuid.NewString(), Label: "shared", Provider: "supplier", State: "active", LimitAmount: "9223372036854775807"}
	first, err := s.CreateAccount(f.ctx, admin, in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateAccount(f.ctx, admin, in)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("immutable replay: %+v %+v %v", first, replay, err)
	}
	in.Label = "changed"
	if _, err = s.CreateAccount(f.ctx, admin, in); !errors.Is(err, procurement.ErrConflict) {
		t.Fatalf("changed payload reused command: %v", err)
	}
	a, err := s.Account(f.ctx, first.TargetID)
	if err != nil || a.LimitAmount != "9223372036854775807" || a.HeldAmount != "0" || a.SpentAmount != "0" || a.Revision != "1" {
		t.Fatalf("account %+v %v", a, err)
	}
	update := procurement.ConfigurationInput{OperationID: uuid.NewString(), ExpectedRevision: "1", Label: "new label", Provider: "supplier", State: "paused", LimitAmount: "0"}
	winner, err := s.ConfigureAccount(f.ctx, admin, a.ID, update)
	if err != nil || winner.Status != "applied" {
		t.Fatalf("configure %+v %v", winner, err)
	}
	update.OperationID = uuid.NewString()
	blocked, err := s.ConfigureAccount(f.ctx, admin, a.ID, update)
	if err != nil || blocked.Status != "rejected" || blocked.Reason != "revision_conflict" {
		t.Fatalf("CAS %+v %v", blocked, err)
	}
	update.OperationID = uuid.NewString()
	if _, err = s.ConfigureAccount(f.ctx, f.operator, a.ID, update); !errors.Is(err, procurement.ErrForbidden) {
		t.Fatalf("finance configured account: %v", err)
	}
	f.exec(t, `UPDATE users SET status='suspended' WHERE id=$1`, admin)
	in.Label = "shared"
	if _, err = s.CreateAccount(f.ctx, admin, in); !errors.Is(err, procurement.ErrForbidden) {
		t.Fatalf("suspended actor replayed: %v", err)
	}
	f.exec(t, `UPDATE users SET status='active' WHERE id=$1`, admin)
	stored, err := s.Command(f.ctx, blocked.OperationID)
	if err != nil || !reflect.DeepEqual(stored, blocked) {
		t.Fatalf("GET receipt %+v %v", stored, err)
	}
	var audits int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action LIKE 'procurement.command.%'`).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("audit count %d %v", audits, err)
	}
	data, _ := json.Marshal(stored)
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "input_digest") {
		t.Fatalf("unsafe receipt %s", data)
	}
	// A mandatory audit failure must not leave the account or durable command.
	if _, err = f.owner.Exec(f.ctx, `CREATE FUNCTION fail_procurement_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture audit unavailable'; END $$; CREATE TRIGGER fail_procurement_audit BEFORE INSERT ON audit_events FOR EACH ROW WHEN (NEW.action LIKE 'procurement.command.%') EXECUTE FUNCTION fail_procurement_audit()`); err != nil {
		t.Fatal(err)
	}
	failedID := uuid.NewString()
	in.OperationID = failedID
	if _, err = s.CreateAccount(f.ctx, admin, in); err == nil {
		t.Fatal("mandatory audit failure accepted")
	}
	if _, err = s.Command(f.ctx, failedID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("failed transaction retained receipt %v", err)
	}
	var accounts int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM procurement_accounts`).Scan(&accounts); err != nil || accounts != 1 {
		t.Fatalf("failed audit left account %d %v", accounts, err)
	}
}

func TestProcurementConcurrentConfigurationHasOneCASWinner(t *testing.T) {
	f := operationDB(t)
	s := procurement.New(f.app)
	admin := procurementAdmin(t, f)
	a := procurementAccount(t, f, s, admin)
	var wg sync.WaitGroup
	receipts := make(chan procurement.Command, 2)
	errs := make(chan error, 2)
	for _, name := range []string{"operator one", "operator two"} {
		wg.Add(1)
		go func(label string) {
			defer wg.Done()
			r, e := s.ConfigureAccount(f.ctx, admin, a.ID, procurement.ConfigurationInput{OperationID: uuid.NewString(), ExpectedRevision: a.Revision, Label: label, Provider: a.Provider, State: a.State, LimitAmount: a.LimitAmount})
			receipts <- r
			errs <- e
		}(name)
	}
	wg.Wait()
	close(receipts)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	applied, rejected := 0, 0
	for r := range receipts {
		if r.Status == "applied" {
			applied++
		} else if r.Status == "rejected" && r.Reason == "revision_conflict" {
			rejected++
		}
	}
	if applied != 1 || rejected != 1 {
		t.Fatalf("CAS winners %d rejected %d", applied, rejected)
	}
}

func TestProcurementBindingIsPermanentAndVisibleAcrossHistoricalPools(t *testing.T) {
	f := operationDB(t)
	s := procurement.New(f.app)
	admin := procurementAdmin(t, f)
	a := procurementAccount(t, f, s, admin)
	c := catalog.New(f.app, f.secrets)
	before, err := c.CredentialPool(f.ctx, f.channel, "")
	if err != nil {
		t.Fatal(err)
	}
	if before.Members[0].ProcurementMode != "legacy_unmanaged" {
		t.Fatalf("legacy %+v", before)
	}
	b := procurementBind(t, f, s, admin, a.ID)
	after, err := c.CredentialPool(f.ctx, f.channel, before.ChannelVersionID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ChannelVersionID != before.ChannelVersionID || after.Members[0].ProcurementMode != "managed" || after.Members[0].ProcurementBindingID == nil || *after.Members[0].ProcurementBindingID != b.ID {
		t.Fatalf("binding projection %+v", after)
	}
	var revision string
	if err = f.app.QueryRow(f.ctx, `SELECT management_revision::text FROM channels WHERE id=$1`, f.channel).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	original, err := s.Command(f.ctx, b.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := s.BindCredential(f.ctx, admin, procurement.BindingInput{OperationID: b.OperationID, ChannelID: b.ChannelID, CredentialID: b.CredentialID, AccountID: b.AccountID, ExpectedChannelRevision: strconv.FormatInt(mustInt(t, revision)-1, 10)})
	if err != nil || !reflect.DeepEqual(original, replayed) {
		t.Fatalf("bind replay %+v %v", replayed, err)
	}
	rejected, err := s.BindCredential(f.ctx, admin, procurement.BindingInput{OperationID: uuid.NewString(), ChannelID: b.ChannelID, CredentialID: b.CredentialID, AccountID: uuid.NewString(), ExpectedChannelRevision: revision})
	if err != nil || rejected.Reason != "already_bound" {
		t.Fatalf("remap %+v %v", rejected, err)
	}
	_, err = f.app.Exec(f.ctx, `UPDATE credential_procurement_bindings SET account_id=account_id WHERE id=$1`, b.ID)
	requirePoolSQLState(t, err, "42501")
	_, err = f.owner.Exec(f.ctx, `UPDATE credential_procurement_bindings SET account_id=account_id WHERE id=$1`, b.ID)
	requirePoolSQLState(t, err, "P0001")
	var current string
	if err = f.app.QueryRow(f.ctx, `SELECT management_revision::text FROM channels WHERE id=$1`, f.channel).Scan(&current); err != nil || current != revision {
		t.Fatalf("replay changed channel revision %s %s %v", revision, current, err)
	}
}
func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		t.Fatal(e)
	}
	return n
}

func TestProcurementManualObservationNeverChangesBudgetAndPermissions(t *testing.T) {
	f := operationDB(t)
	s := procurement.New(f.app)
	admin := procurementAdmin(t, f)
	a := procurementAccount(t, f, s, admin)
	amount := "9007199254740993"
	in := procurement.ObservationInput{OperationID: uuid.NewString(), Status: "known", Amount: &amount, Currency: "CNY", Source: "manual:evidence", Semantic: "cash_balance", ObservedAt: time.Now().UTC(), EvidenceRef: "finance:statement"}
	r, err := s.AddObservation(f.ctx, admin, a.ID, in)
	if err != nil || r.Status != "applied" {
		t.Fatalf("observe %+v %v", r, err)
	}
	var observation procurement.Observation
	if err = json.Unmarshal(r.After, &observation); err != nil || observation.Amount == nil || *observation.Amount != amount {
		t.Fatalf("observation %+v %v", observation, err)
	}
	replay, err := s.AddObservation(f.ctx, admin, a.ID, in)
	if err != nil || !reflect.DeepEqual(r, replay) {
		t.Fatalf("observation replay %+v %v", replay, err)
	}
	in.OperationID = uuid.NewString()
	in.Status = "unknown"
	if _, err = s.AddObservation(f.ctx, admin, a.ID, in); !errors.Is(err, procurement.ErrInvalid) {
		t.Fatalf("unknown treated as money: %v", err)
	}
	in.Amount = nil
	if _, err = s.AddObservation(f.ctx, admin, a.ID, in); err != nil {
		t.Fatal(err)
	}
	after, err := s.Account(f.ctx, a.ID)
	if err != nil || !reflect.DeepEqual(a, after) {
		t.Fatalf("balance observation mutated budget %+v %+v %v", a, after, err)
	}
	for _, table := range []string{"credential_procurement_bindings", "procurement_budget_events", "procurement_evidence_identities", "procurement_commands", "procurement_balance_observations"} {
		for _, sql := range []string{`UPDATE ` + table + ` SET ` + map[string]string{"credential_procurement_bindings": "id=id", "procurement_budget_events": "id=id", "procurement_evidence_identities": "event_id=event_id", "procurement_commands": "operation_id=operation_id", "procurement_balance_observations": "id=id"}[table] + ` WHERE false`, `DELETE FROM ` + table + ` WHERE false`} {
			_, err = f.app.Exec(f.ctx, sql)
			requirePoolSQLState(t, err, "42501")
		}
	}
	for _, table := range []string{"procurement_accounts", "credential_procurement_bindings", "procurement_reservations", "procurement_budget_events", "procurement_evidence_identities", "procurement_commands", "procurement_cost_proposals", "procurement_balance_observations"} {
		_, err = f.projection.Exec(f.ctx, `SELECT * FROM `+table)
		requirePoolSQLState(t, err, "42501")
	}
	_, err = f.owner.Exec(f.ctx, `DELETE FROM procurement_balance_observations WHERE id=$1`, observation.ID)
	requirePoolSQLState(t, err, "P0001")
	_, err = f.app.Exec(f.ctx, `INSERT INTO procurement_commands(operation_id,actor,kind,target_id,input_digest,status,reason,before_state,after_state) SELECT $1,actor,kind,target_id,input_digest,status,reason,before_state,after_state||'{"secret":"must reject"}'::jsonb FROM procurement_commands WHERE operation_id=$2`, uuid.NewString(), r.OperationID)
	requirePoolSQLState(t, err, "23514")
}

func TestProcurementBudgetProjectionAndReservationIdentityAreDatabaseProtected(t *testing.T) {
	f := operationDB(t)
	s := procurement.New(f.app)
	admin := procurementAdmin(t, f)
	a := procurementAccount(t, f, s, admin)
	b := procurementBind(t, f, s, admin, a.ID)
	request, attempt := uuid.NewString(), uuid.NewString()
	operationMetadataSource(t, f, request, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at)
 INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,pricing_group_id,tenant_pricing_revision,retail_priced_at)
 SELECT $1,$2,$3,$4,m.id,p.id,p.input_rate,p.output_rate,100,20,m.profile_version,'fixture','fixture',instant.at+interval '1 hour','fixture',false,t.price_group_id,t.pricing_revision,instant.at
 FROM instant CROSS JOIN tenants t CROSS JOIN models m JOIN LATERAL
  (SELECT id,input_rate,output_rate FROM price_versions WHERE model_id=m.id
   AND price_group_id IS NOT DISTINCT FROM t.price_group_id AND effective_at<=instant.at
   ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1) p ON true
 WHERE t.id=$2 AND m.id=$5`, request, f.tenant, f.key, f.user, f.model)
	_, err := f.app.Exec(f.ctx, `UPDATE procurement_accounts SET held_amount=10 WHERE id=$1`, a.ID)
	requirePoolSQLState(t, err, "23514")
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model) SELECT $1,$2,$3,1,c.id,c.current_version,$4,'upstream' FROM channels c WHERE c.id=$5`, attempt, f.tenant, request, b.CredentialID, f.channel); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO procurement_reservations(attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,state) VALUES($1,$2,$3,100,20,10,10,'prepared')`, []any{attempt, a.ID, b.ID}},
		{`UPDATE procurement_accounts SET held_amount=10,revision=999 WHERE id=$1`, []any{a.ID}},
		{`INSERT INTO procurement_budget_events(event_key,account_id,attempt_id,held_delta,spent_delta,type) VALUES($1,$2,$3,10,0,'reserve')`, []any{"reserve:" + attempt, a.ID, attempt}},
	} {
		if _, err = tx.Exec(f.ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.Account(f.ctx, a.ID)
	if err != nil || after.HeldAmount != "10" || after.Revision != "1" {
		t.Fatalf("budget updates altered config revision %+v %v", after, err)
	}
	_, err = f.app.Exec(f.ctx, `UPDATE procurement_reservations SET input_bound=101 WHERE attempt_id=$1`, attempt)
	requirePoolSQLState(t, err, "P0001")
	var rev string
	if err = f.app.QueryRow(f.ctx, `UPDATE procurement_reservations SET state='sending',revision=999 WHERE attempt_id=$1 RETURNING revision::text`, attempt).Scan(&rev); err != nil || rev != "2" {
		t.Fatalf("reservation revision %s %v", rev, err)
	}
	if err = f.app.QueryRow(f.ctx, `UPDATE procurement_reservations SET revision=999 WHERE attempt_id=$1 RETURNING revision::text`, attempt).Scan(&rev); err != nil || rev != "2" {
		t.Fatalf("no-op revision %s %v", rev, err)
	}
	// The limit is a future-admission policy, so lowering below committed held is legal.
	r, err := s.ConfigureAccount(f.ctx, admin, a.ID, procurement.ConfigurationInput{OperationID: uuid.NewString(), ExpectedRevision: "1", Label: a.Label, Provider: a.Provider, State: a.State, LimitAmount: "5"})
	if err != nil || r.Status != "applied" {
		t.Fatalf("tighten budget %+v %v", r, err)
	}
	c := catalog.New(f.app, f.secrets)
	candidates, err := c.Candidates(f.ctx, f.model, 1)
	if err != nil || len(candidates) != 1 || !candidates[0].ProcurementManaged || candidates[0].ProcurementState != "active" || candidates[0].ProcurementAvailable != "-5" {
		t.Fatalf("candidate purchase snapshot %+v %v", candidates, err)
	}
	f.exec(t, `UPDATE procurement_reservations SET state='unknown',anomaly=true WHERE attempt_id=$1`, attempt)
	candidates, err = c.Candidates(f.ctx, f.model, 1)
	if err != nil || len(candidates) != 1 || candidates[0].ProcurementState != "paused" {
		t.Fatalf("unresolved anomaly bypassed active config %+v %v", candidates, err)
	}
	_, err = f.app.Exec(f.ctx, `UPDATE procurement_reservations SET state='sending',anomaly=false WHERE attempt_id=$1`, attempt)
	requirePoolSQLState(t, err, "P0001")
	_, err = f.app.Exec(f.ctx, `UPDATE procurement_reservations SET state='settled' WHERE attempt_id=$1`, attempt)
	requirePoolSQLState(t, err, "23514")
	for i := 0; i < 2; i++ {
		f.exec(t, `INSERT INTO procurement_evidence_identities(attempt_id,evidence_digest,event_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, attempt, strings.Repeat("a", 64), uuid.NewString())
	}
	var count int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM procurement_evidence_identities WHERE attempt_id=$1`, attempt).Scan(&count); err != nil || count != 1 {
		t.Fatalf("evidence dedup %d %v", count, err)
	}
	proposal := uuid.NewString()
	f.exec(t, `INSERT INTO procurement_cost_proposals(id,tenant_id,attempt_id,amount,basis,evidence_ref,reason,requester,expected_reservation_revision,expected_evidence_count) SELECT $1,$2,$3,0,'invoice','statement:one','fixture correction',$4,revision,1 FROM procurement_reservations WHERE attempt_id=$3`, proposal, f.tenant, attempt, f.operator)
	_, err = f.app.Exec(f.ctx, `UPDATE procurement_cost_proposals SET expected_evidence_count=2 WHERE id=$1`, proposal)
	requirePoolSQLState(t, err, "P0001")
}

func TestProcurementRawBindingParticipatesInChannelSynchronization(t *testing.T) {
	f := operationDB(t)
	s := procurement.New(f.app)
	admin := procurementAdmin(t, f)
	a := procurementAccount(t, f, s, admin)
	var credential, revision string
	if err := f.app.QueryRow(f.ctx, `SELECT v.credential_id,c.management_revision::text FROM channels c JOIN channel_versions v ON v.id=c.current_version WHERE c.id=$1`, f.channel).Scan(&credential, &revision); err != nil {
		t.Fatal(err)
	}
	hold, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(f.ctx)
	if _, err = hold.Exec(f.ctx, `SELECT id FROM channels WHERE id=$1 FOR SHARE`, f.channel); err != nil {
		t.Fatal(err)
	}
	connection, err := f.app.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	pid := connection.Conn().PgConn().PID()
	finished := make(chan error, 1)
	go func() {
		_, e := connection.Exec(f.ctx, `INSERT INTO credential_procurement_bindings(credential_id,channel_id,account_id,operation_id) VALUES($1,$2,$3,$4)`, credential, f.channel, a.ID, uuid.NewString())
		finished <- e
	}()
	waiting := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err = f.owner.QueryRow(f.ctx, `SELECT coalesce((SELECT wait_event_type='Lock' FROM pg_stat_activity WHERE pid=$1),false)`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("raw binding did not wait for channel synchronization barrier")
	}
	var count int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM credential_procurement_bindings WHERE credential_id=$1`, credential).Scan(&count); err != nil || count != 0 {
		t.Fatalf("binding crossed active channel lock %d %v", count, err)
	}
	if err = hold.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("raw binding did not finish after barrier release")
	}
	var after string
	if err = f.app.QueryRow(f.ctx, `SELECT management_revision::text FROM channels WHERE id=$1`, f.channel).Scan(&after); err != nil || mustInt(t, after) != mustInt(t, revision)+1 {
		t.Fatalf("raw bind revision %s -> %s %v", revision, after, err)
	}
}
