package maintenancetrust

import (
	"bytes"
	"context"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxschema"
)

const predecessorByteBudget = 1 << 20
const predecessorRowBudget = 262

// Each retained scalar has the type declared by the fixed projection. These
// private owned rows include columns absent from the historical v1 canonical.
type predecessorScalar struct {
	kind    predecessorKind
	text    string
	data    []byte
	boolean bool
	oid     uint32
	int4    int32
	int8    int64
	instant time.Time
}
type predecessorRow map[string]predecessorScalar
type predecessorRows struct{ tables [8][]predecessorRow }
type predecessorResources struct{ bytes, rows int }

func (r *predecessorResources) addBytes(n int) error {
	if n < 0 || n > predecessorByteBudget-r.bytes {
		return ErrExecution
	}
	r.bytes += n
	return nil
}
func (r *predecessorResources) addRow() error {
	if r.rows >= predecessorRowBudget {
		return ErrExecution
	}
	r.rows++
	return nil
}

func predecessorFinite(t pgtype.Timestamptz) (time.Time, error) {
	if !t.Valid || t.InfinityModifier != pgtype.Finite {
		return time.Time{}, ErrIdentity
	}
	v := t.Time.UTC()
	if v.IsZero() || v.Year() < 1 || v.Year() > 9999 || v.Nanosecond()%1000 != 0 {
		return time.Time{}, ErrIdentity
	}
	return v, nil
}

// Reads only the already bounded projection. No production caller can supply
// a tx: the reader is created only by readPredecessorAuthority.
func (r *predecessorReader) readTable(schema string, table predecessorTable) ([]predecessorRow, error) {
	rows, err := r.authority.snapshot.rawTx.Query(r.work, predecessorProjectionSQL(schema, table), pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		return nil, executionError(r.work)
	}
	defer rows.Close()
	limit := 1
	if table.keys {
		limit = 128
	}
	out := make([]predecessorRow, 0, limit)
	for rows.Next() {
		row, err := predecessorScanRow(rows, table, &r.resources)
		if err != nil {
			return nil, err
		}
		if len(out) >= limit {
			return nil, ErrExecution
		}
		if err = r.resources.addRow(); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if rows.Err() != nil {
		return nil, executionError(r.work)
	}
	if r.work.Err() != nil {
		return nil, ErrTimeout
	}
	if len(out) == 0 {
		return nil, ErrIdentity
	}
	return out, nil
}

func predecessorScanRow(rows pgx.Rows, table predecessorTable, budget *predecessorResources) (predecessorRow, error) {
	qualified := pgtype.Bool{}
	destinations := []any{&qualified}
	for _, c := range table.columns {
		switch c.kind {
		case predecessorText:
			destinations = append(destinations, &pgtype.Text{})
		case predecessorBytes:
			destinations = append(destinations, new([]byte))
		case predecessorBool:
			destinations = append(destinations, &pgtype.Bool{})
		case predecessorOID:
			destinations = append(destinations, &pgtype.Uint32{})
		case predecessorInt4:
			destinations = append(destinations, &pgtype.Int4{})
		case predecessorInt8:
			destinations = append(destinations, &pgtype.Int8{})
		case predecessorTimestamp:
			destinations = append(destinations, &pgtype.Timestamptz{})
		default:
			return nil, ErrExecution
		}
	}
	if rows.Scan(destinations...) != nil {
		return nil, ErrExecution
	}
	if err := budget.addBytes(1); err != nil {
		return nil, err
	}
	if !qualified.Valid || !qualified.Bool {
		return nil, ErrIdentity
	}
	row := make(predecessorRow, len(table.columns))
	for i, c := range table.columns {
		value := predecessorScalar{kind: c.kind}
		n := 0
		switch v := destinations[i+1].(type) {
		case *pgtype.Text:
			if !v.Valid || !utf8.ValidString(v.String) || len(v.String) < c.min || len(v.String) > c.max {
				return nil, ErrIdentity
			}
			value.text = v.String
			n = len(v.String)
		case *[]byte:
			if *v == nil || len(*v) < c.min || len(*v) > c.max {
				return nil, ErrIdentity
			}
			value.data = bytes.Clone(*v)
			n = len(*v)
		case *pgtype.Bool:
			if !v.Valid {
				return nil, ErrIdentity
			}
			value.boolean = v.Bool
			n = 1
		case *pgtype.Uint32:
			if !v.Valid {
				return nil, ErrIdentity
			}
			value.oid = v.Uint32
			n = 4
		case *pgtype.Int4:
			if !v.Valid {
				return nil, ErrIdentity
			}
			value.int4 = v.Int32
			n = 4
		case *pgtype.Int8:
			if !v.Valid {
				return nil, ErrIdentity
			}
			value.int8 = v.Int64
			n = 8
		case *pgtype.Timestamptz:
			instant, err := predecessorFinite(*v)
			if err != nil {
				return nil, err
			}
			value.instant = instant
			n = 8
		default:
			return nil, ErrExecution
		}
		if err := budget.addBytes(n); err != nil {
			return nil, err
		}
		row[c.name] = value
	}
	return row, nil
}

func (r predecessorRow) matchesText(name, want string) bool {
	v, ok := r[name]
	return ok && v.kind == predecessorText && v.text == want
}
func (r predecessorRow) matchesOID(name string, want uint32) bool {
	v, ok := r[name]
	return ok && v.kind == predecessorOID && v.oid == want
}
func (r predecessorRow) matchesInt(name string, want int32) bool {
	v, ok := r[name]
	return ok && v.kind == predecessorInt4 && v.int4 == want
}
func (r predecessorRow) singleton() bool {
	v, ok := r["singleton"]
	return ok && v.kind == predecessorBool && v.boolean
}

func predecessorProofMatchesPins(p predecessorV1Proof, pins fundinglotcatalog.Pins) bool {
	if pins.Mode != fundinglotcatalog.Sandbox || pins.Sandbox == nil || pins.Worker == nil {
		return false
	}
	role := func(r predecessorV1RolePin, p fundinglotcatalog.RolePin, login bool) bool {
		return r.Name == p.Name && r.OID == p.OID && r.Login == login
	}
	schema := func(s predecessorV1SchemaPin, p fundinglotcatalog.SchemaPin, r fundinglotcatalog.RolePin) bool {
		return s.Name == p.Name && s.OID == p.OID && p.NamespaceOwner == p.ObjectOwner && role(s.Owner, p.ObjectOwner, false) && role(s.Runtime, r, true)
	}
	return p.Database.Host == pins.Database.Host && p.Database.Port == pins.Database.Port && p.Database.Name == pins.Database.Name && p.Database.OID == pins.Database.OID &&
		schema(p.App, pins.AppSchema, pins.App) && schema(p.Mock, pins.Sandbox.Mock, pins.Sandbox.MockRuntime) && schema(p.Receiver, pins.Sandbox.Receiver, pins.Sandbox.ReceiverRuntime) &&
		role(p.Worker, *pins.Worker, true) && role(p.Projector, pins.Projector, true)
}

func predecessorValidateSandbox(pins fundinglotcatalog.Pins, rows predecessorRows) (predecessorV1Proof, []byte, string, error) {
	var zero predecessorV1Proof
	for i, table := range predecessorTables() {
		n := len(rows.tables[i])
		if n == 0 || (!table.keys && n != 1) || (table.keys && n > 128) {
			return zero, nil, "", ErrIdentity
		}
		for _, row := range rows.tables[i] {
			if !predecessorValidOwnedRow(table, row) {
				return zero, nil, "", ErrIdentity
			}
		}
	}
	app, att, mock, receiver := rows.tables[0][0], rows.tables[1][0], rows.tables[2][0], rows.tables[3][0]
	raw := att["proof_canonical"].data
	digest := att["proof_sha256"].text
	var p predecessorV1Proof
	if predecessorV1StrictDecode(raw, &p) != nil || predecessorV1ValidateProof(p) != nil || !predecessorProofMatchesPins(p, pins) || predecessorV1SHA(raw) != digest {
		return zero, nil, "", ErrIdentity
	}
	canonical, err := predecessorV1CanonicalProof(p)
	if err != nil || !bytes.Equal(raw, canonical) {
		return zero, nil, "", ErrIdentity
	}
	if !app.singleton() || !att.singleton() || !mock.singleton() || !receiver.singleton() {
		return zero, nil, "", ErrIdentity
	}
	checks := []bool{
		app.matchesText("id", p.EnvironmentID), app.matchesText("merchant_id", p.MerchantID), app.matchesText("payee_label", p.PayeeLabel), app.matchesOID("database_oid", p.Database.OID), app.matchesOID("schema_oid", p.App.OID),
		app.matchesText("nonce_digest", p.AppNonceDigest), app.matchesText("app_role", p.App.Runtime.Name), app.matchesText("worker_role", p.Worker.Name), app.matchesText("projector_role", p.Projector.Name), app.matchesInt("protocol_version", 1),
		att.matchesText("environment_id", p.EnvironmentID), att.matchesOID("database_oid", p.Database.OID), att.matchesOID("schema_oid", p.App.OID), att.matchesOID("schema_owner_oid", p.App.Owner.OID),
		att.matchesOID("app_role_oid", p.App.Runtime.OID), att.matchesOID("worker_role_oid", p.Worker.OID), att.matchesOID("projector_role_oid", p.Projector.OID), att.matchesText("app_nonce_digest", p.AppNonceDigest), att.matchesText("worker_nonce_digest", p.WorkerNonceDigest), att.matchesInt("proof_version", 1),
		mock.matchesText("environment_id", p.EnvironmentID), mock.matchesText("merchant_id", p.MerchantID), mock.matchesText("payee_label", p.PayeeLabel), mock.matchesOID("database_oid", p.Database.OID),
		mock.matchesText("app_projector", p.Projector.Name), mock.matchesText("api_token_digest", p.APITokenDigest), mock.matchesText("api_origin", p.APIOrigin), mock.matchesText("control_origin", p.ControlOrigin), mock.matchesText("callback_url", p.CallbackURL), mock.matchesInt("protocol_version", 1),
		receiver.matchesText("environment_id", p.EnvironmentID), receiver.matchesText("merchant_id", p.MerchantID), receiver.matchesOID("database_oid", p.Database.OID), receiver.matchesText("callback_url", p.CallbackURL), receiver.matchesInt("protocol_version", 1),
	}
	for _, s := range []struct {
		prefix string
		pin    predecessorV1SchemaPin
	}{{"app", p.App}, {"mock", p.Mock}, {"receiver", p.Receiver}} {
		checks = append(checks, mock.matchesText(s.prefix+"_schema_name", s.pin.Name), mock.matchesOID(s.prefix+"_schema_oid", s.pin.OID), mock.matchesText(s.prefix+"_owner", s.pin.Owner.Name), mock.matchesText(s.prefix+"_runtime", s.pin.Runtime.Name))
	}
	checks = append(checks, receiver.matchesText("receiver_schema_name", p.Receiver.Name), receiver.matchesOID("receiver_schema_oid", p.Receiver.OID), receiver.matchesText("receiver_owner", p.Receiver.Owner.Name), receiver.matchesText("receiver_runtime", p.Receiver.Runtime.Name))
	for _, v := range checks {
		if !v {
			return zero, nil, "", ErrIdentity
		}
	}
	if !predecessorV1UUID(mock["state_directory_id"].text) || !predecessorV1UUID(receiver["state_directory_id"].text) || !predecessorV1HashText.MatchString(mock["nonce_digest"].text) || !predecessorV1HashText.MatchString(receiver["nonce_digest"].text) || !predecessorV1HashText.MatchString(mock["control_token_digest"].text) || mock["control_token_digest"].text == mock["api_token_digest"].text {
		return zero, nil, "", ErrIdentity
	}
	for i, c := range []paymentsandboxschema.Component{paymentsandboxschema.Mock, paymentsandboxschema.Receiver} {
		version, e := paymentsandboxschema.Version(c)
		if e != nil {
			return zero, nil, "", ErrSchema
		}
		sha, e := paymentsandboxschema.Digest(c)
		if e != nil {
			return zero, nil, "", ErrSchema
		}
		expected := []string{"3942a0af2577bdad4e841f2b1fa3f0502a3a0db898d6f050fbaf2da45e7e0127", "3c1a4956b55ef058ad6a927e678bf026017a4f3d23bc60d19cb02640e98f433e"}[i]
		if version != 1 || sha != expected {
			return zero, nil, "", ErrSchema
		}
		migration := rows.tables[4+i][0]
		if !migration.matchesInt("version", int32(version)) || !migration.matchesText("sha256", sha) {
			return zero, nil, "", ErrIdentity
		}
		keys := rows.tables[6+i]
		if len(keys) != len(p.Keys) {
			return zero, nil, "", ErrIdentity
		}
		digests := map[string]bool{}
		for j, key := range keys {
			want := p.Keys[j]
			d := key["key_digest"].text
			if !key.matchesText("key_id", want.ID) || !key.matchesText("key_digest", want.Digest) || digests[d] || key["not_before"].instant.Format("2006-01-02T15:04:05.000000Z") != want.NotBefore || key["not_after"].instant.Format("2006-01-02T15:04:05.000000Z") != want.NotAfter {
				return zero, nil, "", ErrIdentity
			}
			digests[d] = true
		}
	}
	return p, bytes.Clone(raw), digest, nil
}

func predecessorValidOwnedRow(table predecessorTable, row predecessorRow) bool {
	if len(row) != len(table.columns) {
		return false
	}
	for _, c := range table.columns {
		v, ok := row[c.name]
		if !ok || v.kind != c.kind {
			return false
		}
		switch c.kind {
		case predecessorText:
			if !utf8.ValidString(v.text) || len(v.text) < c.min || len(v.text) > c.max {
				return false
			}
		case predecessorBytes:
			if len(v.data) < c.min || len(v.data) > c.max {
				return false
			}
		case predecessorTimestamp:
			if _, e := predecessorFinite(pgtype.Timestamptz{Time: v.instant, Valid: true}); e != nil {
				return false
			}
		}
	}
	return true
}

// context is intentionally used only by the authority-owned reader, not an
// alternative constructor accepting arbitrary transaction/proof material.
type predecessorReader struct {
	authority *authorityStage
	work      context.Context
	resources predecessorResources
}
