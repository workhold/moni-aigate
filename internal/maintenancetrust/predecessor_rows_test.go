package maintenancetrust

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

func predecessorRowsFixture(t *testing.T) (fundinglotcatalog.Pins, predecessorRows) {
	t.Helper()
	raw, p := predecessorGolden(t)
	role := func(r predecessorV1RolePin) fundinglotcatalog.RolePin {
		return fundinglotcatalog.RolePin{Name: r.Name, OID: r.OID}
	}
	schema := func(s predecessorV1SchemaPin) fundinglotcatalog.SchemaPin {
		return fundinglotcatalog.SchemaPin{Name: s.Name, OID: s.OID, NamespaceOwner: role(s.Owner), ObjectOwner: role(s.Owner)}
	}
	worker := role(p.Worker)
	pins := fundinglotcatalog.Pins{Mode: fundinglotcatalog.Sandbox, Database: fundinglotcatalog.DatabasePin{Host: p.Database.Host, Port: p.Database.Port, Name: p.Database.Name, OID: p.Database.OID}, AppSchema: schema(p.App), App: role(p.App.Runtime), Worker: &worker, Projector: role(p.Projector), Sandbox: &fundinglotcatalog.SandboxPins{Mock: schema(p.Mock), MockRuntime: role(p.Mock.Runtime), Receiver: schema(p.Receiver), ReceiverRuntime: role(p.Receiver.Runtime)}}
	now := time.Date(2025, 1, 2, 3, 4, 5, 123456000, time.UTC)
	var rows predecessorRows
	for i, table := range predecessorTables() {
		count := 1
		if table.keys {
			count = len(p.Keys)
		}
		for j := 0; j < count; j++ {
			row := make(predecessorRow, len(table.columns))
			for _, c := range table.columns {
				v := predecessorScalar{kind: c.kind}
				switch c.kind {
				case predecessorText:
					v.text = strings.Repeat("a", c.min)
				case predecessorBool:
					v.boolean = true
				case predecessorTimestamp:
					v.instant = now
				case predecessorInt4:
					v.int4 = 1
				}
				row[c.name] = v
			}
			rows.tables[i] = append(rows.tables[i], row)
		}
	}
	text := func(i int, name, value string) {
		v := rows.tables[i][0][name]
		v.text = value
		rows.tables[i][0][name] = v
	}
	oid := func(i int, name string, value uint32) {
		v := rows.tables[i][0][name]
		v.oid = value
		rows.tables[i][0][name] = v
	}
	text(0, "id", p.EnvironmentID)
	text(0, "merchant_id", p.MerchantID)
	text(0, "payee_label", p.PayeeLabel)
	oid(0, "database_oid", p.Database.OID)
	oid(0, "schema_oid", p.App.OID)
	text(0, "nonce_digest", p.AppNonceDigest)
	text(0, "app_role", p.App.Runtime.Name)
	text(0, "worker_role", p.Worker.Name)
	text(0, "projector_role", p.Projector.Name)
	text(1, "environment_id", p.EnvironmentID)
	oid(1, "database_oid", p.Database.OID)
	oid(1, "schema_oid", p.App.OID)
	oid(1, "schema_owner_oid", p.App.Owner.OID)
	oid(1, "app_role_oid", p.App.Runtime.OID)
	oid(1, "worker_role_oid", p.Worker.OID)
	oid(1, "projector_role_oid", p.Projector.OID)
	text(1, "app_nonce_digest", p.AppNonceDigest)
	text(1, "worker_nonce_digest", p.WorkerNonceDigest)
	text(1, "proof_sha256", predecessorV1SHA(raw))
	rows.tables[1][0]["proof_canonical"] = predecessorScalar{kind: predecessorBytes, data: bytes.Clone(raw)}
	for _, i := range []int{2, 3} {
		text(i, "environment_id", p.EnvironmentID)
		text(i, "merchant_id", p.MerchantID)
		oid(i, "database_oid", p.Database.OID)
		text(i, "callback_url", p.CallbackURL)
		text(i, "state_directory_id", fmt.Sprintf("23456789-1234-4234-8234-123456789ab%d", i))
		text(i, "nonce_digest", strings.Repeat(fmt.Sprint(i+5), 64))
	}
	text(2, "payee_label", p.PayeeLabel)
	text(2, "app_projector", p.Projector.Name)
	text(2, "api_token_digest", p.APITokenDigest)
	text(2, "control_token_digest", strings.Repeat("9", 64))
	text(2, "api_origin", p.APIOrigin)
	text(2, "control_origin", p.ControlOrigin)
	for _, s := range []struct {
		prefix string
		pin    predecessorV1SchemaPin
	}{{"app", p.App}, {"mock", p.Mock}, {"receiver", p.Receiver}} {
		text(2, s.prefix+"_schema_name", s.pin.Name)
		oid(2, s.prefix+"_schema_oid", s.pin.OID)
		text(2, s.prefix+"_owner", s.pin.Owner.Name)
		text(2, s.prefix+"_runtime", s.pin.Runtime.Name)
	}
	text(3, "receiver_schema_name", p.Receiver.Name)
	oid(3, "receiver_schema_oid", p.Receiver.OID)
	text(3, "receiver_owner", p.Receiver.Owner.Name)
	text(3, "receiver_runtime", p.Receiver.Runtime.Name)
	text(4, "sha256", "3942a0af2577bdad4e841f2b1fa3f0502a3a0db898d6f050fbaf2da45e7e0127")
	text(5, "sha256", "3c1a4956b55ef058ad6a927e678bf026017a4f3d23bc60d19cb02640e98f433e")
	for _, i := range []int{6, 7} {
		for j, key := range p.Keys {
			row := rows.tables[i][j]
			row["key_id"] = predecessorScalar{kind: predecessorText, text: key.ID}
			row["key_digest"] = predecessorScalar{kind: predecessorText, text: key.Digest}
			before, _ := predecessorV1Instant(key.NotBefore)
			after, _ := predecessorV1Instant(key.NotAfter)
			row["not_before"] = predecessorScalar{kind: predecessorTimestamp, instant: before}
			row["not_after"] = predecessorScalar{kind: predecessorTimestamp, instant: after}
		}
	}
	return pins, rows
}
func TestPredecessorCompleteSandboxRows(t *testing.T) {
	pins, rows := predecessorRowsFixture(t)
	_, raw, digest, err := predecessorValidateSandbox(pins, rows)
	if err != nil || digest != predecessorV1SHA(raw) {
		t.Fatal("complete historical predecessor rejected", err)
	}
	if rows.tables[2][0]["state_directory_id"].text == rows.tables[3][0]["state_directory_id"].text {
		t.Fatal("independent state directory vector missing")
	}
	// Keys include an already expired historical interval. No active key is inferred.
	for _, i := range []int{2, 3} {
		v := rows.tables[i][0]["nonce_digest"]
		v.text = strings.Repeat("a", 64)
		rows.tables[i][0]["nonce_digest"] = v
	}
	if _, _, _, err = predecessorValidateSandbox(pins, rows); err != nil {
		t.Fatal("valid peer-local values incorrectly bound to v1")
	}
}
func TestPredecessorEveryPersistentColumnRequired(t *testing.T) {
	for i, table := range predecessorTables() {
		for _, c := range table.columns {
			t.Run(table.name+"/"+c.name, func(t *testing.T) {
				pins, rows := predecessorRowsFixture(t)
				rows.tables[i][0][c.name] = predecessorScalar{kind: c.kind}
				if _, raw, sha, err := predecessorValidateSandbox(pins, rows); err == nil || raw != nil || sha != "" {
					t.Fatal("invalid complete persistent column accepted")
				}
				pins, rows = predecessorRowsFixture(t)
				delete(rows.tables[i][0], c.name)
				if _, _, _, err := predecessorValidateSandbox(pins, rows); err == nil {
					t.Fatal("unread persistent column accepted")
				}
			})
		}
	}
}
func TestPredecessorSharedValueAndCounts(t *testing.T) {
	for i, table := range predecessorTables() {
		for _, c := range table.columns {
			if c.kind != predecessorText || c.name == "state_directory_id" || c.name == "control_token_digest" || (c.name == "nonce_digest" && (i == 2 || i == 3)) {
				continue
			}
			t.Run(table.name+"/"+c.name, func(t *testing.T) {
				pins, rows := predecessorRowsFixture(t)
				v := rows.tables[i][0][c.name]
				replacement := "b"
				if v.text[0] == 'b' {
					replacement = "c"
				}
				v.text = replacement + v.text[1:]
				rows.tables[i][0][c.name] = v
				if _, _, _, err := predecessorValidateSandbox(pins, rows); err == nil {
					t.Fatal("different shared field accepted")
				}
			})
		}
	}
	for i, table := range predecessorTables() {
		t.Run(table.name+"/missing", func(t *testing.T) {
			pins, rows := predecessorRowsFixture(t)
			rows.tables[i] = nil
			if _, _, _, err := predecessorValidateSandbox(pins, rows); err == nil {
				t.Fatal("missing row accepted")
			}
		})
		t.Run(table.name+"/extra", func(t *testing.T) {
			pins, rows := predecessorRowsFixture(t)
			rows.tables[i] = append(rows.tables[i], rows.tables[i][0])
			if _, _, _, err := predecessorValidateSandbox(pins, rows); err == nil {
				t.Fatal("extra row accepted")
			}
		})
	}
	pins, rows := predecessorRowsFixture(t)
	v := rows.tables[2][0]["control_token_digest"]
	v.text = rows.tables[2][0]["api_token_digest"].text
	rows.tables[2][0]["control_token_digest"] = v
	if _, _, _, err := predecessorValidateSandbox(pins, rows); err == nil {
		t.Fatal("API/control collision accepted")
	}
	pins, rows = predecessorRowsFixture(t)
	v = rows.tables[6][0]["not_before"]
	v.instant = v.instant.Add(time.Microsecond)
	rows.tables[6][0]["not_before"] = v
	if _, _, _, err := predecessorValidateSandbox(pins, rows); err == nil {
		t.Fatal("microsecond key drift accepted")
	}
	pins, rows = predecessorRowsFixture(t)
	pins.Sandbox.MockRuntime.OID++
	if _, _, _, err := predecessorValidateSandbox(pins, rows); err == nil {
		t.Fatal("different actual role OID accepted")
	}
}
func TestPredecessorFiniteTimesAndResources(t *testing.T) {
	good := time.Date(2020, 1, 1, 8, 0, 0, 123456000, time.FixedZone("eight", 8*3600))
	v, err := predecessorFinite(pgtype.Timestamptz{Time: good, Valid: true})
	if err != nil || v.Location() != time.UTC || !v.Equal(good) {
		t.Fatal("finite typed time not normalized")
	}
	for _, bad := range []pgtype.Timestamptz{{}, {Valid: true, InfinityModifier: pgtype.Infinity}, {Valid: true, InfinityModifier: pgtype.NegativeInfinity}, {Valid: true, Time: time.Time{}}, {Valid: true, Time: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}, {Valid: true, Time: time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)}, {Valid: true, Time: good.Add(time.Nanosecond)}} {
		if _, err := predecessorFinite(bad); err == nil {
			t.Fatal("nonfinite/out-of-bound timestamp accepted")
		}
	}
	budget := predecessorResources{}
	if budget.addBytes(predecessorByteBudget) != nil || budget.addBytes(1) != ErrExecution {
		t.Fatal("byte budget not exact")
	}
	for i := 0; i < 262; i++ {
		if budget.addRow() != nil {
			t.Fatal("legal maximum row rejected")
		}
	}
	if budget.addRow() != ErrExecution {
		t.Fatal("row budget exceeded")
	}
}

func TestPredecessorPeerDigestUniquenessIndependentOfV1(t *testing.T) {
	pins, rows := predecessorRowsFixture(t)
	var p predecessorV1Proof
	if predecessorV1StrictDecode(rows.tables[1][0]["proof_canonical"].data, &p) != nil {
		t.Fatal("fixture proof malformed")
	}
	p.Keys[1].Digest = p.Keys[0].Digest
	if predecessorV1ValidateProof(p) != nil {
		t.Fatal("pure historical v1 format was tightened")
	}
	raw, err := predecessorV1CanonicalProof(p)
	if err != nil {
		t.Fatal(err)
	}
	rows.tables[1][0]["proof_canonical"] = predecessorScalar{kind: predecessorBytes, data: raw}
	rows.tables[1][0]["proof_sha256"] = predecessorScalar{kind: predecessorText, text: predecessorV1SHA(raw)}
	for _, i := range []int{6, 7} {
		v := rows.tables[i][1]["key_digest"]
		v.text = p.Keys[1].Digest
		rows.tables[i][1]["key_digest"] = v
	}
	if _, _, _, err = predecessorValidateSandbox(pins, rows); err == nil {
		t.Fatal("peer UNIQUE digest relation was not enforced")
	}
}
func TestPredecessorOrdinaryCompletePredicates(t *testing.T) {
	policy := predecessorRow{"singleton": {kind: predecessorBool, boolean: true}, "enabled": {kind: predecessorBool}, "revision": {kind: predecessorInt8, int8: 1}}
	base := predecessorOrdinaryRows{foreignKey: true, policy: policy}
	if !predecessorValidateOrdinary(base) {
		t.Fatal("ordinary-off rejected")
	}
	for i, name := range predecessorHistoryTables {
		t.Run(name, func(t *testing.T) {
			v := base
			v.history[i] = true
			if predecessorValidateOrdinary(v) {
				t.Fatal("history not excluded")
			}
		})
	}
	for i, item := range predecessorBypasses {
		t.Run(item.table, func(t *testing.T) {
			v := base
			v.bypasses[i] = true
			if predecessorValidateOrdinary(v) {
				t.Fatal("bypass not excluded")
			}
		})
	}
	v2 := base
	v2.foreignKey = false
	if predecessorValidateOrdinary(v2) {
		t.Fatal("missing mandatory FK proof accepted")
	}
	for _, field := range []string{"singleton", "enabled", "revision"} {
		v := base
		v.policy = predecessorRow{}
		for k, s := range policy {
			v.policy[k] = s
		}
		s := v.policy[field]
		if field == "revision" {
			s.int8 = 2
		} else {
			s.boolean = !s.boolean
		}
		v.policy[field] = s
		if predecessorValidateOrdinary(v) {
			t.Fatal("noninitial policy accepted")
		}
	}
}
