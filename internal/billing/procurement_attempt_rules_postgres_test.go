package billing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	rand "math/rand/v2"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/procurementattemptrules"
	"github.com/moni-ai/moni-aigate/internal/procurementidentity"
	"github.com/moni-ai/moni-aigate/internal/routing"
	"github.com/moni-ai/moni-aigate/migrations"
)

type attemptRuleFixture struct {
	ctx        context.Context
	owner, app *pgxpool.Pool
	schema     string
	channel    string
	cutoff     time.Time
}

func attemptRuleMust(t *testing.T, step string, err error) {
	t.Helper()
	if err != nil {
		var p *pgconn.PgError
		if errors.As(err, &p) {
			t.Fatalf("%s: SQLSTATE=%s message=%s", step, p.Code, p.Message)
		}
		t.Fatalf("%s failed", step)
	}
}

func newAttemptRuleFixture(t *testing.T) *attemptRuleFixture {
	t.Helper()
	dsn := os.Getenv("PROCUREMENT_ATTEMPT_RULES_DATABASE_URL")
	if dsn == "" {
		t.Skip("PROCUREMENT_ATTEMPT_RULES_DATABASE_URL required for isolated PostgreSQL original-rule differential")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	attemptRuleMust(t, "parse dedicated database", err)
	c := cfg.ConnConfig
	if c.Host != "127.0.0.1" || c.Port != 55439 || c.Database != "moni_test" || c.User != "moni_test" || len(c.Fallbacks) != 0 {
		t.Fatal("requires dedicated PG16 moni_test LOGIN/database at 127.0.0.1:55439 without fallbacks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	base, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	attemptRuleMust(t, "connect fixture owner", err)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schema, role := "pp_at_"+suffix, "pp_at_app_"+suffix
	q := func(s string) string { return pgx.Identifier{s}.Sanitize() }
	f := &attemptRuleFixture{ctx: ctx, schema: schema, channel: uuid.NewString()}
	t.Cleanup(func() {
		if f.app != nil {
			f.app.Close()
		}
		if f.owner != nil {
			f.owner.Close()
		}
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		defer base.Close()
		defer cancel()
		_, e := base.Exec(cleanup, "DROP SCHEMA IF EXISTS "+q(schema)+" CASCADE")
		attemptRuleMust(t, "drop exact fixture schema", e)
		_, e = base.Exec(cleanup, "DROP ROLE IF EXISTS "+q(role))
		attemptRuleMust(t, "drop exact fixture role", e)
		var remains bool
		attemptRuleMust(t, "verify cleanup", base.QueryRow(cleanup, `SELECT EXISTS(SELECT FROM pg_catalog.pg_namespace WHERE nspname=$1) OR EXISTS(SELECT FROM pg_catalog.pg_roles WHERE rolname=$2)`, schema, role).Scan(&remains))
		if remains {
			t.Error("rule fixture survived cleanup")
		}
	})
	var version int
	attemptRuleMust(t, "read PostgreSQL version", base.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version))
	if version < 160000 || version >= 170000 || strconv.IntSize != 64 {
		t.Fatal("requires PostgreSQL16 and 64-bit Go int")
	}
	_, err = base.Exec(ctx, "CREATE ROLE "+q(role)+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS")
	attemptRuleMust(t, "create actual restricted LOGIN", err)
	_, err = base.Exec(ctx, "CREATE SCHEMA "+q(schema))
	attemptRuleMust(t, "create isolated schema", err)
	_, err = base.Exec(ctx, "REVOKE ALL ON SCHEMA "+q(schema)+" FROM PUBLIC")
	attemptRuleMust(t, "revoke public schema access", err)
	_, err = base.Exec(ctx, "GRANT USAGE ON SCHEMA "+q(schema)+" TO "+q(role))
	attemptRuleMust(t, "grant exact namespace", err)
	owned := cfg.Copy()
	owned.MaxConns = 2
	owned.ConnConfig.RuntimeParams["search_path"] = schema
	owned.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	owned.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	f.owner, err = pgxpool.NewWithConfig(ctx, owned)
	attemptRuleMust(t, "connect schema owner", err)
	attemptRuleMust(t, "install unchanged original migrations through12", migrations.ApplyThrough(ctx, f.owner, "012_key_configuration.sql"))
	tx, err := f.owner.Begin(ctx)
	attemptRuleMust(t, "begin real quote parent fixture", err)
	defer tx.Rollback(ctx)
	credential, channelVersion := uuid.NewString(), uuid.NewString()
	_, err = tx.Exec(ctx, "INSERT INTO channels(id,name,current_version) VALUES($1,'rule-fixture',$2)", f.channel, channelVersion)
	attemptRuleMust(t, "seed real channel", err)
	_, err = tx.Exec(ctx, "INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'fixture-disabled')", credential, f.channel)
	attemptRuleMust(t, "seed real credential", err)
	_, err = tx.Exec(ctx, "INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://example.invalid',$3)", channelVersion, f.channel, credential)
	attemptRuleMust(t, "seed real version", err)
	_, err = tx.Exec(ctx, "INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1)", channelVersion, f.channel, credential)
	attemptRuleMust(t, "seed required frozen credential membership", err)
	attemptRuleMust(t, "legacy quote fixture cutoff", tx.QueryRow(ctx, "SELECT clock_timestamp()-interval '1 day'").Scan(&f.cutoff))
	// Equal-effective rows are legal legacy data before migration013. Install
	// them through that real historical boundary; never disable its new guard.
	for _, row := range []struct {
		id, basis     string
		input, output *int64
		created       time.Time
	}{{"a", "contract", attemptRuleRate(1), attemptRuleRate(2), f.cutoff}, {"z", "invoice", attemptRuleRate(0), nil, f.cutoff.Add(time.Second)}, {"zz", "unknown", nil, nil, f.cutoff.Add(time.Second)}} {
		_, err = tx.Exec(ctx, "INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at,created_at) VALUES($1,$2,'ties',$3,$4,$5,$6,$7)", row.id, f.channel, row.input, row.output, row.basis, f.cutoff, row.created)
		attemptRuleMust(t, "seed legal pre13 quote tie", err)
	}
	attemptRuleMust(t, "commit quote parents and legal legacy ties", tx.Commit(ctx))
	attemptRuleMust(t, "complete unchanged original22 migrations", migrations.ApplyThrough(ctx, f.owner, "022_payment_runtime_attestation.sql"))
	identity := procurementidentity.SQLTemplate()
	sum := sha256.Sum256([]byte(identity))
	if hex.EncodeToString(sum[:]) != procurementidentity.MaterialSHA256 {
		t.Fatal("identity dependency digest mismatch")
	}
	_, err = f.owner.Exec(ctx, strings.ReplaceAll(identity, "{{MONI_SCHEMA}}", q(schema)))
	attemptRuleMust(t, "install unchanged identity dependency", err)
	material := procurementattemptrules.SQLTemplate()
	sum = sha256.Sum256([]byte(material))
	if hex.EncodeToString(sum[:]) != procurementattemptrules.MaterialSHA256 {
		t.Fatal("attempt rule digest mismatch")
	}
	_, err = f.owner.Exec(ctx, strings.ReplaceAll(material, "{{MONI_SCHEMA}}", q(schema)))
	attemptRuleMust(t, "install rule-only candidate", err)
	caller := owned.Copy()
	caller.ConnConfig.User = role
	caller.MaxConns = 2
	f.app, err = pgxpool.NewWithConfig(ctx, caller)
	attemptRuleMust(t, "connect actual restricted APP", err)
	var session, current string
	attemptRuleMust(t, "confirm actual LOGIN", f.app.QueryRow(ctx, "SELECT session_user,current_user").Scan(&session, &current))
	if session != role || current != role {
		t.Fatal("rule fixture impersonated APP")
	}
	_, err = f.app.Exec(ctx, "SELECT pp_at_uuid_parse_v1('denied')")
	var denied *pgconn.PgError
	if !errors.As(err, &denied) || denied.Code != "42501" {
		t.Fatal("APP executed rule before exact grant")
	}
	var count, forbidden int
	attemptRuleMust(t, "inspect exact rule inventory", f.owner.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE p.prosecdef OR p.proisstrict OR p.proconfig IS NOT NULL OR p.provolatile<>CASE WHEN p.proname='pp_at_current_quote_v1' THEN 'v'::"char" ELSE 'i'::"char" END OR p.proparallel<>CASE WHEN p.proname='pp_at_current_quote_v1' THEN 'u'::"char" ELSE 's'::"char" END OR EXISTS(SELECT FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE')) FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND left(p.proname,6)='pp_at_'`, schema).Scan(&count, &forbidden))
	if count != 5 || forbidden != 0 {
		t.Fatal("rule attributes, inventory, or PUBLIC EXECUTE differ")
	}
	for _, signature := range []string{
		"pp_json_string_v1(text)", "pp_at_uuid_parse_v1(text)", "pp_at_route_json_v1(jsonb,text,text)",
		"pp_at_expected_shape_v1(boolean,bytea,bigint,bigint,bytea)",
		"pp_at_quote_equal_v1(boolean,bytea,bigint,bigint,bytea,text,bigint,bigint,text)",
		"pp_at_current_quote_v1(text,text)",
	} {
		_, err = f.owner.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+q(schema)+"."+signature+" TO "+q(role))
		attemptRuleMust(t, "grant exact test function", err)
	}
	_, err = f.owner.Exec(ctx, "GRANT SELECT(id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at,created_at) ON cost_versions TO "+q(role))
	attemptRuleMust(t, "grant exact read-only quote columns", err)
	_, err = f.app.Exec(ctx, "INSERT INTO cost_versions(channel_id,upstream_model) VALUES('denied','denied')")
	if !errors.As(err, &denied) || denied.Code != "42501" {
		t.Fatal("pure rule APP obtained business DML")
	}
	return f
}

func attemptRuleRate(v int64) *int64 { return &v }

func TestPostgresProcurementAttemptOriginalRules(t *testing.T) {
	f := newAttemptRuleFixture(t)
	baseID, versionID := "01234567-89ab-cdef-0123-456789abcdef", "fedcba98-7654-3210-fedc-ba9876543210"
	t.Run("uuid_original_parse_acceptance", func(t *testing.T) {
		values := []string{"", baseID, strings.ReplaceAll(baseID, "-", ""), strings.ToUpper(baseID), "urn:uuid:" + baseID, "URN:UUID:" + strings.ToUpper(baseID), "{" + baseID + "}", "x" + baseID + "y", "\n" + baseID + "\t", "<" + baseID + ">", "00000000-0000-0000-0000-000000000000", "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF", "é" + baseID, baseID + "é", " " + baseID, baseID + " ", "urn:guid:" + baseID, strings.Replace(baseID, "-", "+", 1), "g" + baseID[1:], "🙂", "\u2028" + baseID + "\u2029"}
		for i := 1; i < 128; i++ {
			values = append(values, string(rune(i))+baseID+string(rune(128-i)))
		}
		for i, value := range values {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				var got bool
				attemptRuleMust(t, "SQL UUID acceptance", f.app.QueryRow(f.ctx, "SELECT pp_at_uuid_parse_v1($1)", value).Scan(&got))
				if got != validID(value) {
					t.Fatal("SQL UUID accepts different set from original billing.validID")
				}
			})
		}
		var got bool
		attemptRuleMust(t, "NULL UUID", f.app.QueryRow(f.ctx, "SELECT pp_at_uuid_parse_v1(NULL)").Scan(&got))
		if got {
			t.Fatal("NULL UUID accepted")
		}
	})
	seed := AttemptInput{ChannelID: baseID, ChannelVersionID: versionID, RouteDecisions: []routing.Decision{{ChannelID: baseID, VersionID: versionID, Priority: -7, Weight: 9, Reason: "selected"}}}
	verifyRoute := func(t *testing.T, in AttemptInput) {
		t.Helper()
		want, wantErr := validatedRouteDecisions(in)
		raw, err := json.Marshal(in.RouteDecisions)
		attemptRuleMust(t, "marshal original typed routing.Decision", err)
		if len(in.RouteDecisions) == 0 {
			raw = []byte("[]")
		}
		var got []byte
		err = f.app.QueryRow(f.ctx, "SELECT pp_at_route_json_v1($1,$2,$3)", raw, in.ChannelID, in.ChannelVersionID).Scan(&got)
		if wantErr == nil {
			attemptRuleMust(t, "SQL route encoding", err)
			if !bytes.Equal(got, want) {
				t.Fatal("route bytes differ from original validator and Go JSON encoder")
			}
			return
		}
		var business *Error
		var sqlErr *pgconn.PgError
		if !errors.As(wantErr, &business) || !errors.As(err, &sqlErr) || sqlErr.Code != "PP101" || sqlErr.Message != business.Message {
			t.Fatalf("route SQL rejection differs from original business branch: want=%v got=%v", wantErr, err)
		}
	}
	t.Run("typed_routes_original_bytes_and_errors", func(t *testing.T) {
		verifyRoute(t, seed)
		verifyRoute(t, AttemptInput{})
		for _, priority := range []int{math.MinInt64, -1, 0, 1, math.MaxInt64} {
			for _, weight := range []uint64{0, 1, math.MaxInt64, 1 << 63, math.MaxUint64} {
				in := seed
				in.RouteDecisions = []routing.Decision{seed.RouteDecisions[0]}
				in.RouteDecisions[0].Priority, in.RouteDecisions[0].Weight = priority, weight
				verifyRoute(t, in)
			}
		}
		for _, reason := range []string{"weighted_not_selected", "lower_priority", "disabled", "already_tried", "capacity_full", "quarantined", "circuit_open", "half_open_busy", "channel_quarantined", "credential_revoked", "binding_isolated", "region_forbidden", "cost_unknown", "cost_limit_exceeded", "higher_cost", "procurement_paused", "procurement_budget_exceeded", "procurement_bound_invalid", "procurement_budget_unavailable", "not_a_reason"} {
			in := seed
			in.RouteDecisions = append([]routing.Decision{seed.RouteDecisions[0]}, routing.Decision{ChannelID: strings.ToUpper(baseID), VersionID: versionID, Weight: 1, Reason: reason})
			verifyRoute(t, in)
		}
		for _, wrappers := range [][2]string{{"<", ">"}, {"&", "\""}, {"\\", "/"}, {"\n", "\t"}, {"\x01", "\x1f"}, {"\u2028", "\u2029"}} {
			in := seed
			in.ChannelID = wrappers[0] + baseID + wrappers[1]
			in.ChannelVersionID = wrappers[0] + versionID + wrappers[1]
			in.RouteDecisions = []routing.Decision{{ChannelID: in.ChannelID, VersionID: in.ChannelVersionID, Weight: math.MaxUint64, Reason: "selected"}}
			verifyRoute(t, in)
		}
		for _, kind := range []string{"duplicate", "two_selected", "no_selected", "channel_mismatch", "version_mismatch", "invalid_uuid"} {
			in := seed
			in.RouteDecisions = []routing.Decision{seed.RouteDecisions[0]}
			switch kind {
			case "duplicate":
				in.RouteDecisions = append(in.RouteDecisions, routing.Decision{ChannelID: baseID, VersionID: versionID, Weight: 1, Reason: "disabled"})
			case "two_selected":
				in.RouteDecisions = append(in.RouteDecisions, routing.Decision{ChannelID: strings.ToUpper(baseID), VersionID: versionID, Weight: 1, Reason: "selected"})
			case "no_selected":
				in.RouteDecisions[0].Reason = "disabled"
			case "channel_mismatch":
				in.ChannelID = strings.ToUpper(baseID)
			case "version_mismatch":
				in.ChannelVersionID = strings.ToUpper(versionID)
			case "invalid_uuid":
				in.RouteDecisions[0].ChannelID = "invalid"
			}
			verifyRoute(t, in)
		}
	})
	t.Run("count_bound_and_fixed_seed_differential", func(t *testing.T) {
		in := seed
		in.RouteDecisions = nil
		for i := 0; i < 1000; i++ {
			in.RouteDecisions = append(in.RouteDecisions, routing.Decision{ChannelID: fmt.Sprintf("%08x-89ab-cdef-0123-456789abcdef", i), VersionID: versionID, Priority: math.MinInt64, Weight: math.MaxUint64, Reason: "procurement_budget_unavailable"})
		}
		in.ChannelID = in.RouteDecisions[0].ChannelID
		in.RouteDecisions[0].Reason = "selected"
		verifyRoute(t, in)
		in.RouteDecisions = append(in.RouteDecisions, seed.RouteDecisions[0])
		verifyRoute(t, in)
		r := rand.New(rand.NewPCG(0x617474656d707431, 0x72756c6573000001))
		for i := 0; i < 128; i++ {
			in := seed
			in.RouteDecisions = []routing.Decision{{ChannelID: baseID, VersionID: versionID, Priority: int(r.Uint64()), Weight: r.Uint64(), Reason: "selected"}}
			if i%3 == 0 {
				in.RouteDecisions[0].VersionID = "x" + versionID + "y"
				in.ChannelVersionID = in.RouteDecisions[0].VersionID
			}
			verifyRoute(t, in)
		}
	})
	t.Run("direct_sql_structure_and_numeric_protocol", func(t *testing.T) {
		good, err := json.Marshal(seed.RouteDecisions)
		attemptRuleMust(t, "marshal protocol seed", err)
		object := strings.TrimSuffix(strings.TrimPrefix(string(good), "["), "]")
		invalid := []string{"null", "{}", "1", `[null]`, `[[]]`, `[{}]`, "[" + strings.TrimSuffix(object, "}") + `,"extra":0}]`, strings.Replace(string(good), `"weight":9`, `"weight":18446744073709551616`, 1), strings.Replace(string(good), `"weight":9`, `"weight":-1`, 1), strings.Replace(string(good), `"weight":9`, `"weight":1.0`, 1), strings.Replace(string(good), `"weight":9`, `"weight":"1"`, 1), strings.Replace(string(good), `"priority":-7`, `"priority":9223372036854775808`, 1), strings.Replace(string(good), `"priority":-7`, `"priority":null`, 1), strings.Replace(string(good), `"reason":"selected"`, `"reason":false`, 1)}
		for i, raw := range invalid {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				var got []byte
				err := f.app.QueryRow(f.ctx, "SELECT pp_at_route_json_v1($1,$2,$3)", raw, baseID, versionID).Scan(&got)
				var p *pgconn.PgError
				if !errors.As(err, &p) || p.Code != "PP101" || p.Message != "invalid route decision" {
					t.Fatalf("invalid typed protocol was not PP101: %v", err)
				}
			})
		}
		var got []byte
		attemptRuleMust(t, "empty ignores NULL selected target", f.app.QueryRow(f.ctx, "SELECT pp_at_route_json_v1('[]',NULL,NULL)").Scan(&got))
		if string(got) != "[]" {
			t.Fatal("empty decisions changed")
		}
		for _, tc := range []struct {
			args    []any
			message string
		}{{[]any{nil, baseID, versionID}, "invalid route decision"}, {[]any{string(good), nil, versionID}, "selected route mismatch"}, {[]any{string(good), baseID, nil}, "selected route mismatch"}} {
			err := f.app.QueryRow(f.ctx, "SELECT pp_at_route_json_v1($1,$2,$3)", tc.args...).Scan(&got)
			var p *pgconn.PgError
			if !errors.As(err, &p) || p.Code != "PP101" || p.Message != tc.message {
				t.Fatalf("route explicit NULL branch differs: %v", err)
			}
		}
		// JSONB normalizes this exponent to an integer before the typed leaf.
		raw := strings.Replace(string(good), `"weight":9`, `"weight":9e0`, 1)
		attemptRuleMust(t, "JSONB normalized integer", f.app.QueryRow(f.ctx, "SELECT pp_at_route_json_v1($1,$2,$3)", raw, baseID, versionID).Scan(&got))
		if !bytes.Equal(got, good) {
			t.Fatal("JSONB integer normalization changed output bytes")
		}
	})
	t.Run("expected_bytes_shape_and_original_equal", func(t *testing.T) {
		rate := func(v int64) *int64 { return &v }
		quotes := []routing.CostQuote{{}, {Basis: "unknown"}, {VersionID: baseID, Basis: "contract", InputRate: rate(0), OutputRate: rate(0)}, {VersionID: baseID, Basis: "invoice", InputRate: rate(math.MaxInt64), OutputRate: nil}, {VersionID: baseID, Basis: "unknown", InputRate: rate(-1), OutputRate: rate(math.MinInt64)}, {VersionID: "\x00\xff", Basis: "\xfe\x00", InputRate: rate(-1)}, {VersionID: "", Basis: ""}, {VersionID: strings.Repeat("z", 300000), Basis: "ignored"}}
		for i, expected := range quotes {
			for j, actual := range quotes[:5] {
				t.Run(fmt.Sprintf("%d_%d", i, j), func(t *testing.T) {
					// Non-nil empty BYTEA preserves an existing empty Go string.
					args := []any{true, []byte(expected.VersionID), expected.InputRate, expected.OutputRate, []byte(expected.Basis), actual.VersionID, actual.InputRate, actual.OutputRate, actual.Basis}
					var shape, equal bool
					attemptRuleMust(t, "original Equal differential", f.app.QueryRow(f.ctx, "SELECT pp_at_expected_shape_v1($1,$2,$3,$4,$5),pp_at_quote_equal_v1($1,$2,$3,$4,$5,$6,$7,$8,$9)", args...).Scan(&shape, &equal))
					if !shape || equal != expected.Equal(actual) {
						t.Fatal("expected BYTEA union or original routing.CostQuote.Equal differs")
					}
				})
			}
		}
		cases := []struct {
			args []any
			want bool
		}{{[]any{false, nil, nil, nil, nil}, true}, {[]any{true, []byte{}, nil, nil, []byte{}}, true}, {[]any{nil, nil, nil, nil, nil}, false}, {[]any{false, []byte{}, nil, nil, nil}, false}, {[]any{false, nil, int64(0), nil, nil}, false}, {[]any{true, nil, nil, nil, []byte{}}, false}, {[]any{true, []byte{}, nil, nil, nil}, false}}
		for i, tc := range cases {
			t.Run("shape_"+strconv.Itoa(i), func(t *testing.T) {
				var got bool
				attemptRuleMust(t, "expected shape", f.app.QueryRow(f.ctx, "SELECT pp_at_expected_shape_v1($1,$2,$3,$4,$5)", tc.args...).Scan(&got))
				if got != tc.want {
					t.Fatal("expected shape NULL union differs")
				}
			})
		}
		var equal bool
		attemptRuleMust(t, "absent actual version is empty quoteOf string", f.app.QueryRow(f.ctx, "SELECT pp_at_quote_equal_v1(true,''::bytea,NULL,NULL,convert_to('unknown','UTF8'),NULL,NULL,NULL,'unknown')").Scan(&equal))
		if !equal {
			t.Fatal("noquote NULL version did not map to quoteOf empty string")
		}
		for i, args := range [][]any{
			{false, nil, nil, nil, nil, "", nil, nil, "unknown"},
			{nil, nil, nil, nil, nil, "", nil, nil, "unknown"},
			{true, nil, nil, nil, []byte("unknown"), "", nil, nil, "unknown"},
			{true, []byte{}, nil, nil, nil, "", nil, nil, "unknown"},
			{true, []byte{}, nil, nil, []byte{}, "", nil, nil, nil},
		} {
			t.Run("equal_false_"+strconv.Itoa(i), func(t *testing.T) {
				attemptRuleMust(t, "explicit nonNULL false Equal", f.app.QueryRow(f.ctx, "SELECT pp_at_quote_equal_v1($1,$2,$3,$4,$5,$6,$7,$8,$9)", args...).Scan(&equal))
				if equal {
					t.Fatal("absent/invalid expected or NULL actual basis matched")
				}
			})
		}
	})
}

func TestPostgresProcurementAttemptQuoteSampling(t *testing.T) {
	f := newAttemptRuleFixture(t)
	read := func(t *testing.T, model string, wantID *string, wantIn, wantOut *int64, wantBasis string) {
		t.Helper()
		var before, at, after time.Time
		attemptRuleMust(t, "sample before", f.app.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&before))
		rows, err := f.app.Query(f.ctx, "SELECT * FROM pp_at_current_quote_v1($1,$2)", f.channel, model)
		attemptRuleMust(t, "select real quote and instant", err)
		defer rows.Close()
		var id *string
		var input, output *int64
		var basis string
		if !rows.Next() {
			t.Fatal("quote function returned no instant row")
		}
		attemptRuleMust(t, "scan quote row", rows.Scan(&at, &id, &input, &output, &basis))
		if rows.Next() {
			t.Fatal("quote function returned extra rows")
		}
		attemptRuleMust(t, "finish quote rows", rows.Err())
		attemptRuleMust(t, "sample after", f.app.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&after))
		sameString := id == nil && wantID == nil || id != nil && wantID != nil && *id == *wantID
		if !sameString || !routing.EqualCostQuote(routing.CostQuote{VersionID: "same", InputRate: input, OutputRate: output, Basis: basis}, routing.CostQuote{VersionID: "same", InputRate: wantIn, OutputRate: wantOut, Basis: wantBasis}) || at.Before(before) || at.After(after) {
			t.Fatal("quote choice, nullable dimensions, or real sampled instant differs")
		}
		var original attemptCost
		err = f.app.QueryRow(f.ctx, currentAttemptCostSQL, f.channel, model).Scan(&original.ID, &original.InputRate, &original.OutputRate, &original.Basis)
		if wantID == nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal("original query unexpectedly found quote")
			}
		} else {
			attemptRuleMust(t, "original current quote query", err)
			if original.ID == nil || *original.ID != *wantID || !quoteOf(original).Equal(routing.CostQuote{VersionID: *wantID, InputRate: wantIn, OutputRate: wantOut, Basis: wantBasis}) {
				t.Fatal("new choice differs from original query")
			}
		}
	}
	t.Run("no_quote_still_returns_real_instant", func(t *testing.T) { read(t, "absent", nil, nil, nil, "unknown") })
	t.Run("null_selectors_return_one_noquote_row", func(t *testing.T) {
		var count int
		var valid bool
		attemptRuleMust(t, "NULL quote selectors", f.app.QueryRow(f.ctx, `SELECT count(*)::integer,bool_and(quote_selected_at IS NOT NULL AND cost_version_id IS NULL AND input_rate IS NULL AND output_rate IS NULL AND basis='unknown') FROM pp_at_current_quote_v1(NULL,NULL)`).Scan(&count, &valid))
		if count != 1 || !valid {
			t.Fatal("NULL selectors lost the noquote instant or NULL dimensions")
		}
	})
	cutoff := f.cutoff
	insert := func(id, model, basis string, input, output *int64, effective, created time.Time) {
		t.Helper()
		_, err := f.owner.Exec(f.ctx, "INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)", id, f.channel, model, input, output, basis, effective, created)
		attemptRuleMust(t, "insert real immutable quote", err)
	}
	rate := func(v int64) *int64 { return &v }
	insert("future", "ties", "contract", rate(100), rate(100), cutoff.Add(48*time.Hour), cutoff)
	t.Run("equal_effective_created_and_id_order", func(t *testing.T) { id := "zz"; read(t, "ties", &id, nil, nil, "unknown") })
	insert("new_effective", "ties", "contract", rate(0), rate(0), cutoff.Add(time.Second), cutoff.Add(-time.Hour))
	t.Run("effective_precedes_created_and_zero_is_known", func(t *testing.T) { id := "new_effective"; read(t, "ties", &id, rate(0), rate(0), "contract") })
	insert("partial", "partial", "invoice", rate(9), nil, cutoff, cutoff)
	t.Run("partial_quote_preserves_null", func(t *testing.T) { id := "partial"; read(t, "partial", &id, rate(9), nil, "invoice") })
}
