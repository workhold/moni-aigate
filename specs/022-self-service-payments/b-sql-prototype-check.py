#!/usr/bin/env python3
"""Design-only PostgreSQL experiment, including isolated APP funding fixture.
No listeners, payment-provider calls or changes to an existing deployment.
Only dedicated 127.0.0.1:55439/moni_test as moni_test is accepted. Creates/deletes
exact random schemas/roles in finally; credentials stay in process memory.
"""
from pathlib import Path
import datetime as dt
import hashlib
import json
import os
import re
import secrets
import subprocess
import tempfile
from urllib.parse import urlencode
import uuid

ROOT = Path(__file__).resolve().parents[2]
HERE = Path(__file__).resolve().parent
BASE = dict(os.environ, PGHOST='127.0.0.1', PGPORT='55439', PGDATABASE='moni_test', PGUSER='moni_test', PGPASSWORD='', PGOPTIONS='')
SUFFIX = uuid.uuid4().hex
names = {k: f'pm_{k}_{SUFFIX}' for k in ['app_owner','app_runtime','app_projector','mock_owner','mock_runtime','receiver_owner','receiver_runtime']}
names.update({k: f'payment_{"sandbox" if k=="app_schema" else k[:-7]}_{SUFFIX}' for k in ['app_schema','mock_schema','receiver_schema']})
passwords = {k: secrets.token_urlsafe(32) for k in ['app_password','projector_password','mock_password','receiver_password']}
passes = []

# This helper is source only, with no embedded DSN/password. It is materialized
# privately under ignored .local so Go internal-package imports stay in-module.
APP_HELPER = r'''package main
import (
 "context"
 "encoding/json"
 "fmt"
 "os"
 "regexp"
 "time"
 "github.com/google/uuid"
 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
 "github.com/moni-ai/moni-aigate/internal/billing"
 "github.com/moni-ai/moni-aigate/internal/operations"
 "github.com/moni-ai/moni-aigate/migrations"
)
func need(stage string, err error) { if err!=nil { fmt.Fprintln(os.Stderr,"app_prototype_"+stage+"_failed");os.Exit(1) } }
func assert(stage string, ok bool) { if !ok { need(stage,fmt.Errorf("invalid")) } }
func pin(name string) string {
 v:=os.Getenv(name)
 assert("identity",regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`).MatchString(v))
 return v
}
func pool(ctx context.Context, env, role, schema, offlineOwner string) *pgxpool.Pool {
 cfg,e:=pgxpool.ParseConfig(os.Getenv(env));need("config",e)
 assert("dedicated_database",cfg.ConnConfig.Host=="127.0.0.1"&&cfg.ConnConfig.Port==55439&&cfg.ConnConfig.Database=="moni_test"&&cfg.ConnConfig.User==role)
 cfg.ConnConfig.RuntimeParams["search_path"]=pgx.Identifier{schema}.Sanitize()+",pg_catalog,pg_temp"
 cfg.ConnConfig.RuntimeParams["synchronous_commit"]="on"
 cfg.MaxConns=2
 if offlineOwner!="" { cfg.AfterConnect=func(ctx context.Context,c *pgx.Conn) error { _,e:=c.Exec(ctx,"SET ROLE "+pgx.Identifier{offlineOwner}.Sanitize());return e } }
 p,e:=pgxpool.NewWithConfig(ctx,cfg);need("pool",e);need("connect",p.Ping(ctx))
 var current,session string
 need("actual_identity",p.QueryRow(ctx,"SELECT current_user,session_user").Scan(&current,&session))
 if offlineOwner=="" { assert("actual_login",current==role&&session==role) } else { assert("offline_owner",current==offlineOwner&&session==role) }
 return p
}
func main() {
 ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
 schema,appRole,projector,owner:=pin("MONI_022_APP_SCHEMA"),pin("MONI_022_APP_ROLE"),pin("MONI_022_PROJECTOR_ROLE"),pin("MONI_022_APP_OWNER")
 if os.Getenv("MONI_022_PHASE")=="provision" {
  p:=pool(ctx,"MONI_022_ADMIN_DSN","moni_test",schema,owner);defer p.Close()
  need("apply_current_migrations",migrations.Apply(ctx,p))
  for i:=0;i<2;i++ { need("actual_provision_for",operations.ProvisionFor(ctx,p,schema,appRole,projector)) }
  need("offline_019_role_check",operations.VerifyAlertRolesFor(ctx,p,schema,appRole,projector))
  var n int;need("migration_count",p.QueryRow(ctx,"SELECT count(*) FROM schema_migrations").Scan(&n))
  need("output",json.NewEncoder(os.Stdout).Encode(map[string]any{"migrations":n,"provision_for_calls":2,"offline_019_verified":true}));return
 }
 assert("phase",os.Getenv("MONI_022_PHASE")=="fixture")
 p:=pool(ctx,"MONI_022_APP_DSN",appRole,schema,"");defer p.Close()
 project:=pool(ctx,"MONI_022_PROJECTOR_DSN",projector,schema,"");defer project.Close()
 need("runtime_migration_verify",migrations.Verify(ctx,p))
 need("actual_app_019_role",operations.VerifyAlertRole(ctx,p,operations.AlertApplicationRole))
 need("actual_projector_019_role",operations.VerifyAlertRole(ctx,project,operations.AlertProjectionRole))
 tenant,maker,checker:=uuid.NewString(),uuid.NewString(),uuid.NewString()
 _,e:=p.Exec(ctx,"INSERT INTO tenants(id,name) VALUES($1,'022 isolated APP positive fixture')",tenant);need("tenant",e)
 for _,u:=range []struct{id,role string}{{maker,"finance_operator"},{checker,"finance_approver"}} {
  _,e=p.Exec(ctx,"INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture-only',$3)",u.id,u.id+"@payment-prototype.invalid",u.role);need("financial_users",e)
 }
 s:=billing.New(p)
 in:=billing.CreditInput{TenantID:tenant,ActorID:maker,Amount:1000,BusinessRef:"bank:prototype:"+uuid.NewString(),ProofRef:"proof:prototype:verified",IdempotencyKey:uuid.NewString(),RequestHMAC:uuid.NewString()}
 order,e:=s.CreateCredit(ctx,in);need("create_credit",e)
 approve:=billing.ApprovalInput{TenantID:tenant,ActorID:checker,OrderID:order.ID}
 done,e:=s.ApproveCredit(ctx,approve);need("approve_credit",e)
 assert("dual_approval",done.State=="approved"&&done.Requester==maker&&done.Approver==checker&&maker!=checker&&done.JournalID!="")
 repeated,e:=s.CreateCredit(ctx,in);need("replay_create",e);assert("replay_source",repeated.ID==done.ID)
 again,e:=s.ApproveCredit(ctx,approve);need("replay_approve",e);assert("replay_journal",again.JournalID==done.JournalID)
 _,e=billing.New(project).Project(ctx,billing.ProjectInput{});need("actual_projector_credited",e)
 funding,e:=s.VerifyFunding(ctx,tenant);need("verify_funding",e);assert("funding_balanced",funding.Balanced)
 ledger,e:=s.VerifyLedger(ctx,billing.Query{TenantID:tenant});need("verify_ledger",e);assert("ledger_balanced",ledger.Balanced)
 var sources,journals,receipts,credited,lines,projectionReceipts int
 var available int64
 need("unique_funding",p.QueryRow(ctx,`SELECT
 (SELECT count(*) FROM credit_orders WHERE id=$1 AND state='approved' AND requester=$3 AND approver=$4),
 (SELECT count(*) FROM journals WHERE operation_id=$1 AND id=$2 AND kind='credit'),
 (SELECT count(*) FROM funding_receipts WHERE source_kind='manual_credit' AND source_id=$1 AND journal_id=$2 AND amount=1000),
 (SELECT count(*) FROM projection_outbox WHERE operation_id=$1 AND kind='credited' AND schema_version=1 AND sanitized_payload='{"amount":"1000"}'::jsonb),
 (SELECT count(*) FROM journal_lines WHERE journal_id=$2),
 (SELECT count(*) FROM projection_receipts),
 (SELECT balance FROM ledger_accounts WHERE tenant_id=$5 AND kind='AVAILABLE')`,done.ID,done.JournalID,maker,checker,tenant).Scan(&sources,&journals,&receipts,&credited,&lines,&projectionReceipts,&available))
 assert("unique_funding_counts",sources==1&&journals==1&&receipts==1&&credited==1&&lines==2&&projectionReceipts>=1&&available==1000)
 need("output",json.NewEncoder(os.Stdout).Encode(map[string]any{"actual_app_login":true,"actual_projector_login":true,"app_019_verified":true,"projector_019_verified":true,"manual_dual_approval":true,"idempotent_source_journal":true,"funding_balanced":funding.Balanced,"ledger_balanced":ledger.Balanced,"sources":sources,"journals":journals,"receipts":receipts,"credited":credited,"lines":lines,"available":available} ))
}
'''

def app_helper(phase):
    local = ROOT / '.local'
    local.mkdir(exist_ok=True)
    env = BASE.copy()
    env.update(MONI_022_PHASE=phase, MONI_022_APP_SCHEMA=names['app_schema'], MONI_022_APP_OWNER=names['app_owner'], MONI_022_APP_ROLE=names['app_runtime'], MONI_022_PROJECTOR_ROLE=names['app_projector'])
    env['MONI_022_ADMIN_DSN'] = 'postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'
    for key,role,password in [('MONI_022_APP_DSN','app_runtime','app_password'),('MONI_022_PROJECTOR_DSN','app_projector','projector_password')]:
        env[key] = 'postgres://' + names[role] + ':' + passwords[password] + '@127.0.0.1:55439/moni_test?' + urlencode({'sslmode':'disable'})
    with tempfile.TemporaryDirectory(prefix='022-sql-app-', dir=local) as directory:
        helper = Path(directory) / 'main.go'
        fd = os.open(helper, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, 'w') as source:
            source.write(APP_HELPER)
        result = subprocess.run(['go','run',str(helper)], cwd=ROOT, env=env, capture_output=True, text=True)
        if result.returncode:
            # Only helper stage codes/compiler diagnostics; no DSN in args/source.
            raise RuntimeError('private APP helper failed: ' + result.stderr.strip())
        return json.loads(result.stdout)

FUNDS_SNAPSHOT = '''SELECT encode(sha256(convert_to(jsonb_build_object(
 'journals',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journals x),'[]'::jsonb),
 'journal_lines',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journal_lines x),'[]'::jsonb),
 'receipts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_receipts x),'[]'::jsonb),
 'sources',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM credit_orders x),'[]'::jsonb),
 'accounts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM ledger_accounts x),'[]'::jsonb),
 'outbox',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.event_id) FROM projection_outbox x),'[]'::jsonb),
 'idempotency',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.tenant_id,x.operation,x.key) FROM financial_idempotency x),'[]'::jsonb),
 'audit',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM audit_events x),'[]'::jsonb))::text,'UTF8')),'hex')'''

def app_funds_snapshot():
    return run(prefix('app') + FUNDS_SNAPSHOT, 'app_runtime').splitlines()[-1]

def q(v):
    return "'" + str(v).replace("'", "''") + "'"

def uid():
    return str(uuid.uuid4())

def raw(obj):
    return json.dumps(obj, ensure_ascii=False, separators=(',', ':'))

def bs(obj):
    return 'convert_to(' + q(raw(obj)) + ",'UTF8')"

def digest(obj):
    return hashlib.sha256(raw(obj).encode()).hexdigest()

def run(sql, who='admin', fail=False):
    env = BASE.copy()
    if who != 'admin':
        env['PGUSER'] = names[who]
        env['PGPASSWORD'] = passwords[{'app_runtime':'app_password','app_projector':'projector_password','mock_runtime':'mock_password','receiver_runtime':'receiver_password'}[who]]
    result = subprocess.run(['psql','-X','-At','-v','ON_ERROR_STOP=1','-v','VERBOSITY=sqlstate'], input=sql, text=True, capture_output=True, env=env)
    if fail:
        if result.returncode == 0:
            raise AssertionError('expected SQL rejection')
        state = re.search(r'ERROR:\s+([0-9A-Z]{5})', result.stderr)
        if not state:
            raise AssertionError('expected PostgreSQL SQLSTATE rejection')
        return state.group(1)
    if result.returncode:
        state = re.search(r'ERROR:\s+([0-9A-Z]{5})', result.stderr)
        raise RuntimeError('unexpected_SQL_failure:' + (state.group(1) if state else 'connection_or_client'))
    return result.stdout.strip()

def prefix(which):
    return f'SET search_path TO {names[which+"_schema"]},pg_catalog,pg_temp;'

def sqlmock(sql, fail=False):
    return run(prefix('mock')+sql, 'mock_runtime', fail)

def sqlreceiver(sql, fail=False):
    return run(prefix('receiver')+sql, 'receiver_runtime', fail)

def check(label, value=True):
    if not value:
        raise AssertionError(label)
    passes.append(label)

variables = '\n'.join('\\set '+k+' '+q(v) for k,v in {**names,**passwords}.items())+'\n'
environment, state_dir, merchant = uid(), uid(), 'merchant:sql-prototype'

try:
    check('dedicated database identity', run("SELECT current_database()||'/'||current_user") == 'moni_test/moni_test')
    run(variables+(HERE/'b-sandbox-roles-prototype.sql').read_text())
    # Call the actual migration/provisioning code, including current 019 checks.
    provisioned = app_helper('provision')
    check('actual migrations.Apply and repeated operations.ProvisionFor', provisioned['provision_for_calls']==2 and provisioned['migrations']==len(list((ROOT/'migrations').glob('[0-9][0-9][0-9]_*.sql'))))
    check('actual offline 019 application/projector role verification', provisioned['offline_019_verified'])
    for component in ['mock','receiver']:
        run(f'SET ROLE {names[component+"_owner"]};'+prefix(component)+(HERE/f'b-{component}-schema-prototype.sql').read_text())
    check('both DDL files compile on PostgreSQL 16')
    db_oid=run("SELECT oid FROM pg_database WHERE datname=current_database()")
    oids={c:run(f"SELECT oid FROM pg_namespace WHERE nspname={q(names[c+'_schema'])}") for c in ['app','mock','receiver']}
    values=[True,environment,state_dir,db_oid,names['app_schema'],oids['app'],names['app_owner'],names['app_runtime'],names['app_projector'],names['mock_schema'],oids['mock'],names['mock_owner'],names['mock_runtime'],names['receiver_schema'],oids['receiver'],names['receiver_owner'],names['receiver_runtime'],merchant,'SQL prototype',hashlib.sha256(secrets.token_bytes(32)).hexdigest(),hashlib.sha256(secrets.token_bytes(32)).hexdigest(),hashlib.sha256(secrets.token_bytes(32)).hexdigest(),'http://127.0.0.1:19081','http://127.0.0.1:19082','http://127.0.0.1:19083/callback',1]
    columns='singleton,environment_id,state_directory_id,database_oid,app_schema_name,app_schema_oid,app_owner,app_runtime,app_projector,mock_schema_name,mock_schema_oid,mock_owner,mock_runtime,receiver_schema_name,receiver_schema_oid,receiver_owner,receiver_runtime,merchant_id,payee_label,nonce_digest,api_token_digest,control_token_digest,api_origin,control_origin,callback_url,protocol_version'
    key_digest=hashlib.sha256(bytes(range(32))).hexdigest()
    run(f'SET ROLE {names["mock_owner"]};'+prefix('mock')+f'INSERT INTO mock_environment({columns}) VALUES('+','.join(q(v) for v in values)+');'+f"INSERT INTO mock_schema_migrations(version,sha256) VALUES(1,{q(hashlib.sha256((HERE/'b-mock-schema-prototype.sql').read_bytes()).hexdigest())});INSERT INTO mock_signing_keys VALUES('qa-key',{q(key_digest)},'2020-01-01Z','2030-01-01Z',clock_timestamp());")
    run(f'SET ROLE {names["receiver_owner"]};'+prefix('receiver')+f"INSERT INTO receiver_environment(environment_id,merchant_id,database_oid,receiver_schema_name,receiver_schema_oid,receiver_owner,receiver_runtime,state_directory_id,nonce_digest,callback_url,protocol_version) VALUES({q(environment)},{q(merchant)},{db_oid},{q(names['receiver_schema'])},{oids['receiver']},{q(names['receiver_owner'])},{q(names['receiver_runtime'])},{q(state_dir)},{q(hashlib.sha256(secrets.token_bytes(32)).hexdigest())},'http://127.0.0.1:19083/callback',1);INSERT INTO receiver_schema_migrations VALUES(1,{q(hashlib.sha256((HERE/'b-receiver-schema-prototype.sql').read_bytes()).hexdigest())},clock_timestamp());INSERT INTO receiver_signing_keys VALUES('qa-key',{q(key_digest)},'2020-01-01Z','2030-01-01Z');")
    run(variables+(HERE/'b-sandbox-grants-prototype.sql').read_text())
    check('owner-only markers and signing-key digests provisioned')
    application_fixture = app_helper('fixture')
    check('actual APP and projector LOGIN pass current 019 VerifyAlertRole', all(application_fixture[k] for k in ['actual_app_login','actual_projector_login','app_019_verified','projector_019_verified']))
    check('actual APP billing dual approval creates unique source journal receipt credited', all(application_fixture[k]==1 for k in ['sources','journals','receipts','credited']) and application_fixture['lines']==2 and application_fixture['available']==1000 and application_fixture['manual_dual_approval'] and application_fixture['idempotent_source_journal'])
    check('actual projector consumes credited and APP VerifyFunding VerifyLedger pass', application_fixture['funding_balanced'] and application_fixture['ledger_balanced'])
    # This nonempty baseline precedes every cross-domain denial and own-schema experiment.
    before_funds = app_funds_snapshot()
    # Real runtime authentication: do not use SET ROLE under the superuser session.
    for role in ['app_runtime','app_projector','mock_runtime','receiver_runtime']:
        check('actual login '+role, run('SELECT current_user',role) == names[role])
        others = [names[r] for r in ['app_owner','app_runtime','app_projector','mock_owner','mock_runtime','receiver_owner','receiver_runtime'] if r!=role]
        for target in others:
            run('SET ROLE '+target,role,True)
        check('no SET ROLE escalation '+role)
    for role,foreign in [('mock_runtime',['app','receiver']),('receiver_runtime',['app','mock']),('app_runtime',['mock','receiver']),('app_projector',['mock','receiver'])]:
        for component in foreign:
            schema=names[component+'_schema']
            check('schema deny '+role+' -> '+component, run(f"SELECT NOT has_schema_privilege(current_user,{q(schema)},'USAGE') AND NOT has_schema_privilege(current_user,{q(schema)},'CREATE')",role)=='t')
            check('all relation/column/function privileges deny '+role+' -> '+component, run(f"SELECT NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN unnest(ARRAY['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']) a WHERE n.nspname={q(schema)} AND c.relkind IN ('r','p','v','m','f') AND has_table_privilege(current_user,c.oid,a)) AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace CROSS JOIN unnest(ARRAY['SELECT','INSERT','UPDATE','REFERENCES']) a WHERE n.nspname={q(schema)} AND c.relkind IN ('r','p','v','m','f') AND has_any_column_privilege(current_user,c.oid,a)) AND NOT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname={q(schema)} AND has_function_privilege(current_user,p.oid,'EXECUTE'))",role)=='t')
            run(f'SELECT * FROM {schema}.'+('tenants' if component=='app' else component+'_environment'),role,True)
            if component=='app':
                for denied in [f'INSERT INTO {schema}.credit_orders DEFAULT VALUES',f'UPDATE {schema}.ledger_accounts SET balance=balance',f'DELETE FROM {schema}.funding_receipts']:
                    check('actual application funding mutation denied '+role+' '+denied.split()[0],run(denied,role,True)=='42501')
    check('no SECURITY DEFINER',run("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname IN ("+','.join(q(names[c+'_schema']) for c in ['app','mock','receiver'])+") AND p.prosecdef")=='0')
    check('function search paths pinned to owned schema/catalog/temp',run("SELECT count(*)=0 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname IN ("+','.join(q(names[c+'_schema']) for c in ['mock','receiver'])+") AND (p.proconfig IS NULL OR NOT (('search_path='||n.nspname||', pg_catalog, pg_temp')=ANY(p.proconfig)))")=='t')
    check('temporary marker cannot shadow pinned helper',sqlmock("CREATE TEMP TABLE mock_environment(singleton boolean,environment_id text);INSERT INTO pg_temp.mock_environment VALUES(true,'forged');SELECT mock_reply_valid('{}'::jsonb,false)").splitlines()[-1]=='f')
    now_text=run("SELECT to_char(clock_timestamp() AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"')")
    now=dt.datetime.fromisoformat(now_text.replace('Z','+00:00'))
    def stamp(t): return t.strftime('%Y-%m-%dT%H:%M:%S.%fZ')
    def checkout(command=None,order=None,created=None):
        c=command or uid();o=order or uid();created=created or now-dt.timedelta(seconds=10)
        p=dict(schema_version=1,environment_id=environment,merchant_id=merchant,command_id=c,merchant_order_id=f'mps1.{environment}.{o}',amount='123456',currency='CNY',created_at=stamp(created),expires_at=stamp(created+dt.timedelta(seconds=900)))
        return p
    def result(status,checkout_ref=None,failure=None,capture=None,closed=None): return dict(status=status,checkout_ref=checkout_ref,failure_code=failure,capture=capture,closed_at=closed)
    def reply(p,res,rev='1',kind='checkout',state='completed'):
        return dict(schema_version=1,environment_id=environment,merchant_id=merchant,merchant_order_id=p['merchant_order_id'],command_id=p['command_id'],command_kind=kind,parameter_digest=digest(p),command_state=state,provider_order_revision=rev,result=res)
    def command_sql(p,rep,kind='checkout'):
        return f"INSERT INTO mock_commands VALUES({q(p['command_id'])},{q(p['merchant_order_id'])},{q(kind)},{q(digest(p))},{bs(p)},{bs(rep)},{q(digest(rep))},clock_timestamp());"
    def completed_sql(p,rep):
        return f"INSERT INTO mock_command_processing(command_id,state,completed_at) VALUES({q(p['command_id'])},'completed',transaction_timestamp());INSERT INTO mock_command_results VALUES({q(p['command_id'])},{bs(rep)},{q(digest(rep))},{q(rep['provider_order_revision'])},transaction_timestamp());"
    def order_sql(p,ref,state='ready',closed=None):
        return f"INSERT INTO mock_orders(merchant_order_id,order_id,environment_id,merchant_id,amount,created_at,expires_at,state,checkout_ref,closed_at) VALUES({q(p['merchant_order_id'])},{q(p['merchant_order_id'][-36:])},{q(environment)},{q(merchant)},{q(p['amount'])},{q(p['created_at'])},{q(p['expires_at'])},{q(state)},{q(ref)},{q(closed) if closed else 'NULL'});"
    def transition_sql(p,kind,command=None,control=None,when=None,rev=1):
        return f"INSERT INTO mock_order_transitions(merchant_order_id,revision,kind,command_id,control_operation_id,occurred_at,fact_digest) VALUES({q(p['merchant_order_id'])},{rev},{q(kind)},{q(command) if command else 'NULL'},{q(control) if control else 'NULL'},{q(when or p['created_at'])},mock_transition_digest({q(p['merchant_order_id'])},{rev},{q(kind)},{q(command) if command else 'NULL'},{q(control) if control else 'NULL'},{q(when or p['created_at'])}));"
    p=checkout();ref='checkout:'+uid();rep=reply(p,result('ready',checkout_ref=ref))
    create=command_sql(p,rep)+order_sql(p,ref)+completed_sql(p,rep)+transition_sql(p,'ready',command=p['command_id'])
    sqlmock('BEGIN;'+create+'COMMIT;');check('checkout ready has complete immutable command/order/transition/result')
    sqlmock(command_sql(p,rep),True);check('duplicate command cannot create second row')
    orphan=checkout();orphanrep=reply(orphan,result('ready',checkout_ref='checkout:'+uid()))
    sqlmock('BEGIN;'+command_sql(orphan,orphanrep)+'COMMIT;',True);check('deferred incomplete command rollback')
    bad=checkout();bad['amount']=None;badrep=reply(bad,result('ready',checkout_ref='checkout:'+uid()))
    sqlmock('BEGIN;'+command_sql(bad,badrep)+'COMMIT;',True);check('NULL typed command rejected')
    sqlmock("UPDATE mock_commands SET initial_reply=initial_reply",True)
    sqlmock("UPDATE mock_environment SET merchant_id=merchant_id",True)
    sqlmock("INSERT INTO mock_signing_keys VALUES('forged',repeat('b',64),'2020-01-01Z','2030-01-01Z',clock_timestamp())",True)
    sqlmock("DELETE FROM mock_command_results",True)
    sqlmock("CREATE TABLE forbidden(id integer)",True);check('runtime immutable history/marker/key/DDL forbidden')
    def query_sql(p,rep,state=None):
        query=uid();pr=dict(schema_version=1,environment_id=environment,merchant_id=merchant,query_id=query,command_id=p['command_id'],merchant_order_id=p['merchant_order_id'],command_kind=rep['command_kind'],parameter_digest=digest(p));snap=dict(rep);snap['query_id']=query
        if state: snap.update(command_state=state,provider_order_revision=None,result=None)
        sql=f"INSERT INTO mock_queries(query_id,command_id,merchant_order_id,command_kind,expected_parameter_digest,request_digest,canonical_parameters,snapshot_body,snapshot_digest) VALUES({q(query)},{q(p['command_id'])},{q(p['merchant_order_id'])},{q(rep['command_kind'])},{q(digest(p))},{q(digest(pr))},{bs(pr)},{bs(snap)},{q(digest(snap))});"
        return query,snap,sql
    query,qs,query_insert=query_sql(p,rep);sqlmock(query_insert);check('ready query snapshot stored')
    unseen=checkout();ur=reply(unseen,None,None,state='not_seen');uq,us,ui=query_sql(unseen,ur,'not_seen');sqlmock(ui);check('unknown original command supports not_seen without FK')
    upref='checkout:'+uid();uprep=reply(unseen,result('ready',checkout_ref=upref));sqlmock('BEGIN;'+command_sql(unseen,uprep)+order_sql(unseen,upref)+completed_sql(unseen,uprep)+transition_sql(unseen,'ready',command=unseen['command_id'])+'COMMIT;');check('late command does not invalidate old not_seen snapshot',sqlmock(f"SELECT snapshot_digest FROM mock_queries WHERE query_id={q(uq)}").splitlines()[-1]==digest(us))
    def control(op,action,scenario,target,target_field,resource):
        pr=dict(operation_id=op,action=action,scenario=scenario,merchant_order_id=None,command_id=None,query_id=None,event_id=None);pr[target_field]=target
        re=dict(operation_id=op,status='accepted',reason='accepted',resource_id=resource)
        return f"INSERT INTO mock_controls(operation_id,action,scenario,target_reference,parameter_digest,canonical_parameters,result_body) VALUES({q(op)},{q(action)},{q(scenario)},{q(target)},{q(digest(pr))},{bs(pr)},{bs(re)});"
    target,first_arm,second_arm,new_arm=uid(),uid(),uid(),uid()
    def arm_plan(operation,boundary):
        return control(operation,'arm_fault',boundary,target,'command_id',operation)+f"INSERT INTO mock_fault_plans(control_operation_id,boundary,target_kind,target_id) VALUES({q(operation)},{q(boundary)},'command',{q(target)});"
    sqlmock('BEGIN;'+arm_plan(first_arm,'before_command_commit_disconnect_once')+'COMMIT;')
    check('active target unique rejects second arm with different boundary',sqlmock('BEGIN;'+arm_plan(second_arm,'after_command_commit_drop_reply_once')+'COMMIT;',True)=='23505')
    check('second arm rollback preserves only original plan and no orphan control',sqlmock(f"SELECT (SELECT count(*) FROM mock_fault_plans WHERE target_kind='command' AND target_id={q(target)} AND remaining=1)=1 AND NOT EXISTS(SELECT 1 FROM mock_controls WHERE operation_id={q(second_arm)})").splitlines()[-1]=='t')
    sqlmock(f"UPDATE mock_fault_plans SET remaining=0,consumed_at=clock_timestamp() WHERE control_operation_id={q(first_arm)};")
    sqlmock('BEGIN;'+arm_plan(new_arm,'after_command_commit_drop_reply_once')+'COMMIT;')
    check('consumed target admits new plan while retaining consumed history',sqlmock(f"SELECT count(*)=2 AND count(*) FILTER(WHERE remaining=1)=1 AND count(*) FILTER(WHERE remaining=0)=1 FROM mock_fault_plans WHERE target_kind='command' AND target_id={q(target)}").splitlines()[-1]=='t')
    capid,op,eventpk=uid(),uid(),uid();eventid='event:'+uid();txn='transaction:'+uid()
    event=dict(schema_version=1,event_id=eventid,event_type='capture.confirmed',environment_id=environment,merchant_id=merchant,order_id=p['merchant_order_id'][-36:],provider_transaction_id=txn,amount=p['amount'],currency='CNY',occurred_at=stamp(now),provider_order_revision='2')
    capsql=control(op,'capture_order','normal',p['merchant_order_id'],'merchant_order_id',capid)
    capsql+=f"INSERT INTO mock_captures(id,merchant_order_id,provider_transaction_id,amount,currency,occurred_at,provider_order_revision,control_operation_id) VALUES({q(capid)},{q(p['merchant_order_id'])},{q(txn)},{q(p['amount'])},'CNY',{q(stamp(now))},2,{q(op)});"
    capsql+=f"UPDATE mock_orders SET state='captured' WHERE merchant_order_id={q(p['merchant_order_id'])};"+transition_sql(p,'captured',control=op,when=stamp(now),rev=2)
    capsql+=f"INSERT INTO mock_notification_events(id,event_id,merchant_order_id,event_type,canonical_body,business_digest,control_operation_id,canonical_capture_id) VALUES({q(eventpk)},{q(eventid)},{q(p['merchant_order_id'])},'capture.confirmed',{bs(event)},{q(digest(event))},{q(op)},{q(capid)});INSERT INTO mock_notification_delivery(event_id) VALUES({q(eventpk)});"
    sqlmock('BEGIN;'+capsql+'COMMIT;');check('capture/control/transition/event/delivery atomic complete')
    check('old ready query body frozen after capture',sqlmock(f"SELECT snapshot_digest FROM mock_queries WHERE query_id={q(query)}").splitlines()[-1]==digest(qs))
    capture=dict(provider_transaction_id=txn,amount=p['amount'],currency='CNY',occurred_at=stamp(now),provider_order_revision='2');caprep=reply(p,result('captured',capture=capture),'2')
    nq,ns,ni=query_sql(p,caprep);sqlmock(ni);check('new query observes canonical capture')
    sqlmock(f"UPDATE mock_orders SET state='closed',closed_at=clock_timestamp() WHERE merchant_order_id={q(p['merchant_order_id'])}",True);check('captured order cannot reopen or close')
    claim=f"UPDATE mock_notification_delivery SET state='leased',generation=generation+1,attempt_count=attempt_count+1 WHERE event_id={q(eventpk)} AND state IN ('pending','retry_wait');"
    attempt=f"INSERT INTO mock_delivery_attempts(event_id,generation,attempt_no,delivery_id,delivery_time,signing_key_id,body_digest,claimed_at,lease_until) SELECT d.event_id,d.generation,d.attempt_count,{q(uid())},floor(extract(epoch FROM d.lease_until-interval '60 seconds'))::bigint,'qa-key',n.business_digest,d.lease_until-interval '60 seconds',d.lease_until FROM mock_notification_delivery d JOIN mock_notification_events n ON n.id=d.event_id WHERE d.event_id={q(eventpk)};"
    sqlmock('BEGIN;'+claim+attempt+'COMMIT;');check('callback durable claim/generation/attempt before IO')
    sqlmock(f"UPDATE mock_notification_delivery SET attempt_count=0,generation=0 WHERE event_id={q(eventpk)}",True);check('callback budget cannot reset')
    check('stale ACK CAS affects zero rows',sqlmock(f"WITH x AS (UPDATE mock_notification_delivery SET state='delivered' WHERE event_id={q(eventpk)} AND state='leased' AND generation=0 RETURNING 1) SELECT count(*) FROM x").splitlines()[-1]=='0')
    sqlmock(f"BEGIN;INSERT INTO mock_delivery_observations(event_id,generation,outcome,error_code) VALUES({q(eventpk)},1,'failed','timeout');UPDATE mock_notification_delivery SET state='retry_wait',last_error_code='timeout' WHERE event_id={q(eventpk)} AND generation=1 AND state='leased';COMMIT;");check('callback failure records bounded retry without replacing event')
    import time
    time.sleep(1.05) # Real first backoff boundary; no database clock mutation.
    sqlmock('BEGIN;'+claim+re.sub(r"(SELECT d.event_id,d.generation,d.attempt_count,)'[^']+'",lambda m:m[1]+q(uid()),attempt)+'COMMIT;')
    sqlmock(f"UPDATE mock_notification_delivery SET state='delivered' WHERE event_id={q(eventpk)} AND state='leased' AND generation=2",True)
    check('delivered without immutable ACK evidence rejected')
    ack=dict(receipt_id=uid(),event_id=eventid,status='received',environment='sandbox',received_at=stamp(now))
    ack_insert=f"INSERT INTO mock_delivery_observations(event_id,generation,outcome,http_status,receipt_data) VALUES({q(eventpk)},2,'ack',202,{bs(ack)});"
    sqlmock(ack_insert.replace(",202,",",204,"),True);check('other 2xx cannot become callback ACK')
    sqlmock('BEGIN;'+ack_insert.replace(",202,",",NULL,")+f"UPDATE mock_notification_delivery SET state='delivered' WHERE event_id={q(eventpk)} AND state='leased' AND generation=2;COMMIT;",True)
    check('NULL HTTP status cannot become callback ACK',sqlmock(f"SELECT NOT EXISTS(SELECT 1 FROM mock_delivery_observations WHERE event_id={q(eventpk)} AND generation=2) AND EXISTS(SELECT 1 FROM mock_notification_delivery WHERE event_id={q(eventpk)} AND state='leased' AND generation=2)").splitlines()[-1]=='t')
    wrong=dict(ack);wrong['event_id']='wrong:event'
    sqlmock(ack_insert.replace(bs(ack),bs(wrong)),True);check('mismatched receipt event cannot ACK')
    sqlmock('BEGIN;'+ack_insert+f"UPDATE mock_notification_delivery SET state='delivered' WHERE event_id={q(eventpk)} AND state='leased' AND generation=2;COMMIT;")
    check('matching 202 receipt durably explains delivered')
    sqlmock("UPDATE mock_delivery_observations SET outcome=outcome",True);check('callback observations immutable')
    sqlmock(f"UPDATE mock_notification_delivery SET state='pending',attempt_count=0,generation=0 WHERE event_id={q(eventpk)}",True);check('delivered callback cannot reset its budget')
    # Offline boundary checkpoints, NOT twenty real scheduler claims or crash recovery.
    # Only the fresh fixture owner installs history; every tested transition below
    # uses the actual runtime login with the ordinary guards fully enabled.
    def last_claim_checkpoint(expired=False):
        cp=checkout(created=now-dt.timedelta(hours=3));ref='checkout:'+uid();rep=reply(cp,result('ready',checkout_ref=ref))
        cid,operation,eid=uid(),uid(),uid();external='event:'+uid();transaction='transaction:'+uid();captured=now-dt.timedelta(hours=3)+dt.timedelta(seconds=1)
        body=dict(schema_version=1,event_id=external,event_type='capture.confirmed',environment_id=environment,merchant_id=merchant,order_id=cp['merchant_order_id'][-36:],provider_transaction_id=transaction,amount=cp['amount'],currency='CNY',occurred_at=stamp(captured),provider_order_revision='2')
        fixture=command_sql(cp,rep)+order_sql(cp,ref)+completed_sql(cp,rep)+transition_sql(cp,'ready',command=cp['command_id'])
        run(f'SET ROLE {names["mock_owner"]};'+prefix('mock')+'BEGIN;'+fixture+'COMMIT;')
        fixture=control(operation,'capture_order','normal',cp['merchant_order_id'],'merchant_order_id',cid)
        fixture+=f"INSERT INTO mock_captures(id,merchant_order_id,provider_transaction_id,amount,currency,occurred_at,provider_order_revision,control_operation_id,created_at) VALUES({q(cid)},{q(cp['merchant_order_id'])},{q(transaction)},{q(cp['amount'])},'CNY',{q(stamp(captured))},2,{q(operation)},{q(stamp(captured))});"
        fixture+=f"UPDATE mock_orders SET state='captured' WHERE merchant_order_id={q(cp['merchant_order_id'])};"+transition_sql(cp,'captured',control=operation,when=stamp(captured),rev=2)
        fixture+=f"INSERT INTO mock_notification_events(id,event_id,merchant_order_id,event_type,canonical_body,business_digest,control_operation_id,canonical_capture_id,created_at) VALUES({q(eid)},{q(external)},{q(cp['merchant_order_id'])},'capture.confirmed',{bs(body)},{q(digest(body))},{q(operation)},{q(cid)},{q(stamp(captured))});INSERT INTO mock_notification_delivery(event_id,created_at) VALUES({q(eid)},{q(stamp(captured))});"
        run(f'SET ROLE {names["mock_owner"]};'+prefix('mock')+'BEGIN;'+fixture+'COMMIT;')
        lease="clock_timestamp()-interval '1 microsecond'" if expired else "clock_timestamp()+interval '60 seconds'"
        checkpoint="ALTER TABLE mock_notification_delivery DISABLE TRIGGER mock_delivery_guard;ALTER TABLE mock_delivery_attempts DISABLE TRIGGER mock_input;"
        checkpoint+=f"UPDATE mock_notification_delivery SET state='leased',generation=20,attempt_count=20,lease_until={lease} WHERE event_id={q(eid)};"
        checkpoint+=f"INSERT INTO mock_delivery_attempts(event_id,generation,attempt_no,delivery_id,delivery_time,signing_key_id,body_digest,claimed_at,lease_until) SELECT d.event_id,g,g,gen_random_uuid()::text,floor(extract(epoch FROM d.lease_until-interval '60 seconds'-(20-g)*interval '400 seconds'))::bigint,'qa-key',n.business_digest,d.lease_until-interval '60 seconds'-(20-g)*interval '400 seconds',d.lease_until-(20-g)*interval '400 seconds' FROM mock_notification_delivery d JOIN mock_notification_events n ON n.id=d.event_id CROSS JOIN generate_series(1,20) g WHERE d.event_id={q(eid)};"
        checkpoint+="SET CONSTRAINTS ALL IMMEDIATE;ALTER TABLE mock_notification_delivery ENABLE TRIGGER mock_delivery_guard;ALTER TABLE mock_delivery_attempts ENABLE TRIGGER mock_input;"
        run(f'SET ROLE {names["mock_owner"]};'+prefix('mock')+'BEGIN;'+checkpoint+'COMMIT;')
        return eid,external
    last,last_external=last_claim_checkpoint()
    review=f"UPDATE mock_notification_delivery SET state='review_required',last_error_code='attempts_exhausted' WHERE event_id={q(last)} AND state='leased' AND generation=20;"
    sqlmock(review,True);check('checkpoint live twentieth claim without failure cannot enter review')
    sqlmock(f"INSERT INTO mock_delivery_observations(event_id,generation,outcome,error_code) VALUES({q(last)},19,'failed','timeout');")
    sqlmock(review,True);check('checkpoint old generation failure cannot exhaust live twentieth claim')
    sqlmock('BEGIN;'+f"INSERT INTO mock_delivery_observations(event_id,generation,outcome,error_code) VALUES({q(last)},20,'failed','timeout');"+review+'COMMIT;')
    check('checkpoint twentieth failure atomically records evidence and review',sqlmock(f"SELECT state='review_required' AND generation=20 AND attempt_count=20 AND lease_until IS NULL AND last_error_code='attempts_exhausted' AND (SELECT count(*) FROM mock_delivery_attempts WHERE event_id={q(last)})=20 FROM mock_notification_delivery WHERE event_id={q(last)}").splitlines()[-1]=='t')
    sqlmock(f"UPDATE mock_notification_delivery SET state='leased',generation=21,attempt_count=21 WHERE event_id={q(last)}",True);check('checkpoint exhausted review forbids twenty-first claim')
    expired,_=last_claim_checkpoint(True)
    sqlmock(f"UPDATE mock_notification_delivery SET state='review_required',last_error_code='attempts_exhausted' WHERE event_id={q(expired)} AND state='leased' AND generation=20;")
    check('checkpoint expired twentieth lease without observation enters review',sqlmock(f"SELECT state='review_required' AND lease_until IS NULL AND NOT EXISTS(SELECT 1 FROM mock_delivery_observations WHERE event_id={q(expired)}) FROM mock_notification_delivery WHERE event_id={q(expired)}").splitlines()[-1]=='t')
    acknowledged,ack_external=last_claim_checkpoint()
    last_receipt=dict(receipt_id=uid(),event_id=ack_external,status='received',environment='sandbox',received_at=stamp(now))
    sqlmock(f"INSERT INTO mock_delivery_observations(event_id,generation,outcome,http_status,receipt_data) VALUES({q(acknowledged)},20,'ack',200,{bs(last_receipt)});")
    sqlmock(f"UPDATE mock_notification_delivery SET state='review_required',last_error_code='attempts_exhausted' WHERE event_id={q(acknowledged)} AND state='leased' AND generation=20;",True);check('checkpoint ACK evidence cannot explain live twentieth failure')
    sqlmock(f"UPDATE mock_notification_delivery SET state='delivered' WHERE event_id={q(acknowledged)} AND state='leased' AND generation=20;")
    check('checkpoint live twentieth claim can still ACK with 200',sqlmock(f"SELECT state='delivered' AND attempt_count=20 FROM mock_notification_delivery WHERE event_id={q(acknowledged)}").splitlines()[-1]=='t')
    # cancel-first tombstone and late checkout rejection.
    tomborder=uid();cancel=dict(schema_version=1,environment_id=environment,merchant_id=merchant,command_id=uid(),merchant_order_id=f'mps1.{environment}.{tomborder}')
    cr=reply(cancel,result('closed',closed=stamp(now)),'1','cancel')
    sqlmock('BEGIN;'+command_sql(cancel,cr,'cancel')+f"INSERT INTO mock_orders(merchant_order_id,order_id,environment_id,merchant_id,state,closed_at) VALUES({q(cancel['merchant_order_id'])},{q(tomborder)},{q(environment)},{q(merchant)},'closed',{q(stamp(now))});"+completed_sql(cancel,cr)+transition_sql(cancel,'closed',command=cancel['command_id'],when=stamp(now))+'COMMIT;');check('cancel-first immutable tombstone with complete cancel receipt')
    late=checkout(order=tomborder);lr=reply(late,result('rejected',failure='order_closed'))
    sqlmock('BEGIN;'+command_sql(late,lr)+completed_sql(late,lr)+'COMMIT;');check('late checkout rejected without filling or reopening tombstone')
    # persisted hold -> explicit control release, no replacement of original response.
    held=checkout();hr=reply(held,None,None,state='in_progress');fault=uid()
    sqlmock('BEGIN;'+control(fault,'arm_fault','hold_checkout_before_effect',held['command_id'],'command_id',fault)+f"INSERT INTO mock_fault_plans(control_operation_id,boundary,target_kind,target_id) VALUES({q(fault)},'hold_checkout_before_effect','command',{q(held['command_id'])});COMMIT;")
    sqlmock('BEGIN;'+f"UPDATE mock_fault_plans SET remaining=0,consumed_at=clock_timestamp() WHERE control_operation_id={q(fault)};"+command_sql(held,hr)+f"INSERT INTO mock_command_processing(command_id,state,held_by_control_id) VALUES({q(held['command_id'])},'held',{q(fault)});COMMIT;");check('hold persists original intent/receipt without order effect')
    release=uid();heldref='checkout:'+uid();heldfinal=reply(held,result('ready',checkout_ref=heldref))
    sqlmock('BEGIN;'+control(release,'release_command','normal',held['command_id'],'command_id',held['command_id'])+order_sql(held,heldref)+f"UPDATE mock_command_processing SET state='completed',completed_at=transaction_timestamp(),released_by_control_id={q(release)} WHERE command_id={q(held['command_id'])};INSERT INTO mock_command_results VALUES({q(held['command_id'])},{bs(heldfinal)},{q(digest(heldfinal))},1,transaction_timestamp());"+transition_sql(held,'ready',command=held['command_id'])+'COMMIT;')
    check('release adds final result and preserves original in_progress bytes',sqlmock(f"SELECT initial_reply_digest FROM mock_commands WHERE command_id={q(held['command_id'])}").splitlines()[-1]==digest(hr))
    # Durable receiver owns a separate receipt, cannot touch application/mock data.
    inbox,receipt,delivery=uid(),uid(),uid()
    receive=f"INSERT INTO receiver_inbox(id,environment_id,merchant_id,event_id,business_digest,canonical_body,first_delivery_id,delivery_time,signing_key_id) VALUES({q(inbox)},{q(environment)},{q(merchant)},{q(eventid)},{q(digest(event))},{bs(event)},{q(delivery)},floor(extract(epoch FROM clock_timestamp()))::bigint,'qa-key');"
    receipt_insert=f"WITH data AS (SELECT i.id,i.verified_at,convert_to(json_build_object('receipt_id',{q(receipt)},'event_id',i.event_id,'status','received','environment','sandbox','received_at',to_char(i.verified_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"'))::text,'UTF8') body FROM receiver_inbox i WHERE id={q(inbox)}) INSERT INTO receiver_receipts(inbox_id,receipt_id,received_at,receipt_data,receipt_digest) SELECT id,{q(receipt)},verified_at,body,receiver_hash(body) FROM data;"
    sqlreceiver('BEGIN;'+receive+receipt_insert+'COMMIT;');check('receiver durable inbox+immutable receipt commit together')
    sqlreceiver('BEGIN;'+receive+'COMMIT;',True);check('receiver same event cannot create second receipt')
    sqlreceiver("UPDATE receiver_receipts SET receipt_data=receipt_data",True);check('receiver receipts cannot mutate')
    another=dict(event);another['event_id']='event:'+uid();ab=uid()
    sqlreceiver('BEGIN;'+f"INSERT INTO receiver_inbox(id,environment_id,merchant_id,event_id,business_digest,canonical_body,first_delivery_id,delivery_time,signing_key_id) VALUES({q(ab)},{q(environment)},{q(merchant)},{q(another['event_id'])},{q(digest(another))},{bs(another)},{q(uid())},floor(extract(epoch FROM clock_timestamp()))::bigint,'qa-key');COMMIT;",True);check('receiver inbox without receipt rejected at commit')
    sqlreceiver(f"INSERT INTO receiver_conflicts(inbox_id,observed_digest,delivery_id,delivery_time,signing_key_id) VALUES({q(inbox)},{q('c'*64)},{q(uid())},floor(extract(epoch FROM clock_timestamp()))::bigint,'qa-key');");check('receiver stores conflict as immutable nonfinancial evidence')
    after_funds=app_funds_snapshot()
    check('nonempty APP source journal receipt credited accounts outbox full hash unchanged',before_funds==after_funds)
    print(json.dumps(dict(status='pass',checks=len(passes),tests=passes,scope='DDL/roles/trigger and isolated APP billing fixture only; no HTTP/mock runtime/signature verification/process kill',checkpoint_scope='Twentieth-claim threshold cases use offline owner-installed complete claim checkpoints, then actual restricted runtime transitions. Not twenty real scheduler claims/backoffs/crashes.',application_provision=provisioned,application_fixture=application_fixture,application_financial_hash=after_funds,application_financial_baseline='Nonempty legal manual dual approval by actual APP LOGIN, before all cross-schema denials; full sorted sources/journals/lines/receipts/accounts/outbox/idempotency/audit compared by actual APP LOGIN.'),ensure_ascii=False,indent=2))
finally:
    # Always only exact generated resources. No prefix scan, no unrelated DB settings.
    run('\n'.join('DROP SCHEMA IF EXISTS '+names[k]+' CASCADE;' for k in ['receiver_schema','mock_schema','app_schema']))
    run('\n'.join('DROP ROLE IF EXISTS '+names[k]+';' for k in ['receiver_runtime','receiver_owner','mock_runtime','mock_owner','app_projector','app_runtime','app_owner']))
