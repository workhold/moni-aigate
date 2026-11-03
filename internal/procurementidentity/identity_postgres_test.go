package procurementidentity_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	mathrand "math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/procurementidentity"
)

func identityMust(t *testing.T, operation string, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(operation + " failed")
	}
}

// This oracle uses the original persisted Go structs, not a second declaration
// of the SQL's field order or a new business writer as expected output.
func identityOracle(t *testing.T, in billing.FinalizationInput) ([]byte, []byte, []byte) {
	t.Helper()
	in.ObservedAt = time.Time{}
	final, err := json.Marshal(in)
	identityMust(t, "marshal original finalization ABI", err)
	usage, err := json.Marshal(in.Usage)
	identityMust(t, "marshal original usage ABI", err)
	evidence, err := json.Marshal(struct {
		Usage  billing.Usage
		Reason string
	}{in.Usage, in.TransportEndReason})
	identityMust(t, "marshal original evidence ABI", err)
	return final, evidence, usage
}
func identityArguments(in billing.FinalizationInput) []any {
	return []any{in.ID, in.TenantID, in.RequestID, in.AttemptID, in.Source, in.SourceEventID, in.Usage.Status, in.Usage.InputTokens, in.Usage.OutputTokens, in.Usage.TotalTokens, in.Usage.CachedTokens, in.Usage.ReasoningTokens, in.ProtocolOutcome, in.TransportEndReason, in.DeliveryStage, in.FinishReason, in.ContentWritten}
}
func identityPlaceholders(n int) string {
	args := make([]string, n)
	for i := range args {
		args[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(args, ",")
}
func TestPostgresOriginalProcurementIdentityABI(t *testing.T) {
	dsn := os.Getenv("PROCUREMENT_IDENTITY_DATABASE_URL")
	if dsn == "" {
		t.Skip("PROCUREMENT_IDENTITY_DATABASE_URL required for dedicated identity PostgreSQL acceptance")
	}
	config, err := pgx.ParseConfig(dsn)
	identityMust(t, "parse dedicated database configuration", err)
	if config.Host != "127.0.0.1" || config.Port != 55439 || config.Database != "moni_test" || config.User != "moni_test" || len(config.Fallbacks) != 0 {
		t.Fatal("requires isolated 127.0.0.1:55439/moni_test and moni_test LOGIN without fallbacks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner, err := pgx.ConnectConfig(ctx, config)
	identityMust(t, "connect owner fixture", err)
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		identityMust(t, "close fixture owner", owner.Close(c))
	})
	var version int
	identityMust(t, "read server version", owner.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version))
	if version < 160000 || version >= 170000 {
		t.Fatal("requires PostgreSQL16")
	}
	var entropy [16]byte
	_, err = rand.Read(entropy[:])
	identityMust(t, "create isolated identity", err)
	suffix := hex.EncodeToString(entropy[:])
	schema, role := "pp_identity_"+suffix, "pp_identity_app_"+suffix
	q := func(s string) string { return pgx.Identifier{s}.Sanitize() }
	var secret [24]byte
	_, err = rand.Read(secret[:])
	identityMust(t, "create isolated password", err)
	password := hex.EncodeToString(secret[:])
	roleCreated, schemaCreated := false, false
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if schemaCreated {
			_, e := owner.Exec(c, "DROP SCHEMA "+q(schema)+" CASCADE")
			identityMust(t, "drop isolated schema", e)
		}
		if roleCreated {
			_, e := owner.Exec(c, "DROP ROLE "+q(role))
			identityMust(t, "drop isolated role", e)
		}
		var schemas, roles int
		identityMust(t, "confirm fixture cleanup", owner.QueryRow(c, "SELECT (SELECT count(*) FROM pg_namespace WHERE nspname=$1),(SELECT count(*) FROM pg_roles WHERE rolname=$2)", schema, role).Scan(&schemas, &roles))
		if schemas != 0 || roles != 0 {
			t.Error("isolated fixture objects survived cleanup")
		}
	})
	_, err = owner.Exec(ctx, "CREATE ROLE "+q(role)+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+password+"'")
	identityMust(t, "create restricted APP", err)
	roleCreated = true
	_, err = owner.Exec(ctx, "CREATE SCHEMA "+q(schema))
	identityMust(t, "create isolated schema", err)
	schemaCreated = true
	_, err = owner.Exec(ctx, "REVOKE ALL ON SCHEMA "+q(schema)+" FROM PUBLIC")
	identityMust(t, "revoke public schema", err)
	_, err = owner.Exec(ctx, "GRANT USAGE ON SCHEMA "+q(schema)+" TO "+q(role))
	identityMust(t, "grant restricted schema usage", err)
	template := procurementidentity.SQLTemplate()
	_, err = owner.Exec(ctx, strings.ReplaceAll(template, "{{MONI_SCHEMA}}", q(schema)))
	identityMust(t, "install fixed identity-only template", err)
	var functions, forbidden int
	identityMust(t, "inspect exact pure functions", owner.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE p.prosecdef OR NOT p.proisstrict OR p.provolatile<>'i' OR p.proparallel<>'s' OR p.proconfig IS NOT NULL OR EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE')) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1`, schema).Scan(&functions, &forbidden))
	if functions != 6 || forbidden != 0 {
		t.Fatal("identity material has extra functions or broadened authority")
	}
	var tables int
	identityMust(t, "inspect absent business data", owner.QueryRow(ctx, "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1", schema).Scan(&tables))
	if tables != 0 {
		t.Fatal("identity template created relations")
	}
	callerConfig := config.Copy()
	callerConfig.User = role
	callerConfig.Password = password
	caller, err := pgx.ConnectConfig(ctx, callerConfig)
	identityMust(t, "connect actual restricted APP LOGIN", err)
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		identityMust(t, "close actual APP", caller.Close(c))
	})
	var login string
	identityMust(t, "confirm actual login", caller.QueryRow(ctx, "SELECT session_user").Scan(&login))
	if login != role {
		t.Fatal("fixture used role impersonation")
	}
	_, err = caller.Exec(ctx, "SELECT "+q(schema)+".pp_json_string_v1('before-grant')")
	var denied *pgconn.PgError
	if !errors.As(err, &denied) || denied.Code != "42501" {
		t.Fatal("ungranted APP executed identity function")
	}
	// No business objects are installed. These exact grants are test-only and do
	// not grant the material package an installer or runtime role privileges.
	signatures := []string{"pp_json_string_v1(text)", "pp_usage_json_v1(text,bigint,bigint,bigint,bigint,bigint)", "pp_finalization_identity_v1(text,text,text,text,text,text,text,bigint,bigint,bigint,bigint,bigint,text,text,text,text,boolean)", "pp_evidence_identity_v1(text,bigint,bigint,bigint,bigint,bigint,text)", "pp_finalization_digest_v1(text,text,text,text,text,text,text,bigint,bigint,bigint,bigint,bigint,text,text,text,text,boolean)", "pp_evidence_digest_v1(text,bigint,bigint,bigint,bigint,bigint,text)"}
	for _, signature := range signatures {
		_, err = owner.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+q(schema)+"."+signature+" TO "+q(role))
		identityMust(t, "grant exact test function", err)
	}
	finalArgs := identityPlaceholders(17)
	evidenceArgs := "$7,$8,$9,$10,$11,$12,$14"
	usageArgs := "$7,$8,$9,$10,$11,$12"
	query := "SELECT " + q(schema) + ".pp_finalization_identity_v1(" + finalArgs + ")," + q(schema) + ".pp_evidence_identity_v1(" + evidenceArgs + ")," + q(schema) + ".pp_usage_json_v1(" + usageArgs + ")," + q(schema) + ".pp_finalization_digest_v1(" + finalArgs + ")," + q(schema) + ".pp_evidence_digest_v1(" + evidenceArgs + ")"
	seed := billing.FinalizationInput{ID: "id-A", TenantID: "tenant-B", RequestID: "request-C", AttemptID: "attempt-D", Source: "source-E", SourceEventID: "event-F", Usage: billing.Usage{Status: "usage-G", InputTokens: 101, OutputTokens: 202, TotalTokens: 303, CachedTokens: 404, ReasoningTokens: 505}, ProtocolOutcome: "outcome-H", TransportEndReason: "reason-I", DeliveryStage: "stage-J", FinishReason: "finish-K", ObservedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.FixedZone("other", 3600))}
	verify := func(t *testing.T, in billing.FinalizationInput) {
		t.Helper()
		wantFinal, wantEvidence, wantUsage := identityOracle(t, in)
		var final, evidence []byte
		var usage, finalHash, evidenceHash string
		identityMust(t, "call actual SQL identity", caller.QueryRow(ctx, query, identityArguments(in)...).Scan(&final, &evidence, &usage, &finalHash, &evidenceHash))
		fh, eh := sha256.Sum256(wantFinal), sha256.Sum256(wantEvidence)
		if !bytes.Equal(final, wantFinal) || !bytes.Equal(evidence, wantEvidence) || usage != string(wantUsage) || finalHash != hex.EncodeToString(fh[:]) || evidenceHash != hex.EncodeToString(eh[:]) {
			t.Fatal("original Go and SQL identity bytes or hashes differ")
		}
	}
	t.Run("exact_original_struct_and_raw_delivery", func(t *testing.T) {
		verify(t, seed)
		for _, stage := range []string{"none", "committed", "content", "unknown"} {
			for _, content := range []bool{false, true} {
				in := seed
				in.DeliveryStage = stage
				in.ContentWritten = content
				in.AttemptID = ""
				verify(t, in)
			}
		}
	})
	t.Run("escaping_and_unicode", func(t *testing.T) {
		values := []string{"", "ASCII", "中文模型", "🙂𝄞", "e\u0301", "é", "\uFFFD", "\u0085\u00a0\ufeff", "<>&", "\u2028\u2029", `\u2028\u003c\\\"/`, "\"\\/\x7f"}
		for r := rune(1); r < 32; r++ {
			values = append(values, string(r))
		}
		for _, value := range values {
			var quoted string
			identityMust(t, "encode SQL string", caller.QueryRow(ctx, "SELECT "+q(schema)+".pp_json_string_v1($1)", value).Scan(&quoted))
			want, e := json.Marshal(value)
			identityMust(t, "marshal independent string", e)
			if quoted != string(want) {
				t.Fatal("Go string escaping differs")
			}
			in := seed
			in.ID = value
			in.Source = value
			in.SourceEventID = value
			in.Usage.Status = value
			in.TransportEndReason = value
			in.FinishReason = value
			verify(t, in)
		}
	})
	t.Run("full_int64_each_position", func(t *testing.T) {
		for position := 0; position < 5; position++ {
			for _, v := range []int64{math.MinInt64, -1, 0, 1, math.MaxInt64} {
				in := seed
				fields := []*int64{&in.Usage.InputTokens, &in.Usage.OutputTokens, &in.Usage.TotalTokens, &in.Usage.CachedTokens, &in.Usage.ReasoningTokens}
				*fields[position] = v
				verify(t, in)
			}
		}
	})
	t.Run("fixed_seed_unicode_and_integer_differential", func(t *testing.T) {
		r := mathrand.New(mathrand.NewPCG(0x65766964656e6365, 0x7072656669780001))
		alphabet := []rune("AZ09中文é🙂\"\\<>&\b\f\n\r\t\u2028\u2029\u0301\x01\x1b\x7f")
		next := func() string {
			rr := make([]rune, r.IntN(65))
			for i := range rr {
				rr[i] = alphabet[r.IntN(len(alphabet))]
			}
			return string(rr)
		}
		for i := 0; i < 768; i++ {
			in := seed
			in.ID = next()
			in.TenantID = next()
			in.RequestID = next()
			in.AttemptID = next()
			in.Source = next()
			in.SourceEventID = next()
			in.Usage = billing.Usage{Status: next(), InputTokens: int64(r.Uint64()), OutputTokens: int64(r.Uint64()), TotalTokens: int64(r.Uint64()), CachedTokens: int64(r.Uint64()), ReasoningTokens: int64(r.Uint64())}
			in.ProtocolOutcome = next()
			in.TransportEndReason = next()
			in.DeliveryStage = next()
			in.FinishReason = next()
			in.ContentWritten = r.IntN(2) == 1
			verify(t, in)
		}
	})
	t.Run("strict_null_every_argument", func(t *testing.T) {
		for _, signature := range signatures {
			name := strings.Split(signature, "(")[0]
			var args []any
			switch name {
			case "pp_json_string_v1":
				args = []any{"x"}
			case "pp_usage_json_v1":
				args = []any{"x", int64(1), int64(2), int64(3), int64(4), int64(5)}
			case "pp_finalization_identity_v1", "pp_finalization_digest_v1":
				args = identityArguments(seed)
			default:
				args = []any{"x", int64(1), int64(2), int64(3), int64(4), int64(5), "reason"}
			}
			for i := range args {
				copyArgs := append([]any(nil), args...)
				copyArgs[i] = nil
				var isNull bool
				identityMust(t, "check strict NULL", caller.QueryRow(ctx, "SELECT "+q(schema)+"."+name+"("+identityPlaceholders(len(args))+") IS NULL", copyArgs...).Scan(&isNull))
				if !isNull {
					t.Fatal("NULL input was treated as a valid identity")
				}
			}
		}
	})
	t.Run("session_settings_and_invalid_text_domain", func(t *testing.T) {
		for _, mode := range []string{"on", "off"} {
			_, e := caller.Exec(ctx, "SET standard_conforming_strings="+mode)
			identityMust(t, "set string mode", e)
			_, e = caller.Exec(ctx, "SET search_path=pg_catalog,pg_temp")
			identityMust(t, "set unrelated search path", e)
			verify(t, seed)
			escaped := seed
			escaped.Source = "<>&\u2028\u2029\b\f\n\r\t\\\""
			escaped.TransportEndReason = `\u003c\\u2028`
			verify(t, escaped)
		}
		for _, value := range []string{"a\x00b", string([]byte{0xff})} {
			_, e := caller.Exec(ctx, "SELECT "+q(schema)+".pp_json_string_v1($1)", value)
			var domain *pgconn.PgError
			if !errors.As(e, &domain) || domain.Code != "22021" {
				t.Fatal("unrepresentable input did not receive PostgreSQL text-domain rejection")
			}
		}
	})
}
