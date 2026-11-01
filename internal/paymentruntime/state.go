package paymentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func attestState(ctx context.Context, p *pgxpool.Pool, c Config) error {
	tx, e := p.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return ErrState
	}
	defer tx.Rollback(ctx)
	m := c.manifest
	prefix := string(m.Component)
	marker := pgx.Identifier{prefix + "_environment"}.Sanitize()
	var count int
	var body []byte
	if tx.QueryRow(ctx, "SELECT count(*) FROM "+marker).Scan(&count) != nil || count != 1 {
		return ErrState
	}
	if tx.QueryRow(ctx, "SELECT to_jsonb(e) FROM "+marker+" e").Scan(&body) != nil {
		return ErrState
	}
	var actual map[string]json.RawMessage
	if json.Unmarshal(body, &actual) != nil {
		return ErrState
	}
	// PostgreSQL's OID type is serialized as a JSON string, unlike an integer.
	oidText := func(oid uint32) string { return strconv.FormatUint(uint64(oid), 10) }
	expected := map[string]any{"singleton": true, "environment_id": m.EnvironmentID, "merchant_id": m.MerchantID, "database_oid": oidText(m.Database.OID), "state_directory_id": m.StateDirectoryID, "nonce_digest": c.nonceDigest, "callback_url": m.CallbackURL, "protocol_version": 1}
	if m.Component == Mock {
		expected["payee_label"] = m.PayeeLabel
		expected["api_token_digest"] = c.apiDigest
		expected["control_token_digest"] = c.controlDigest
		expected["api_origin"] = c.origins[API]
		expected["control_origin"] = c.origins[Control]
		expected["app_projector"] = m.AppProjector.Name
		for _, x := range []struct {
			prefix string
			pin    schemaPin
		}{{"app", m.App}, {"mock", m.Mock}, {"receiver", m.Receiver}} {
			expected[x.prefix+"_schema_name"] = x.pin.Name
			expected[x.prefix+"_schema_oid"] = oidText(x.pin.OID)
			expected[x.prefix+"_owner"] = x.pin.Owner.Name
			expected[x.prefix+"_runtime"] = x.pin.Runtime.Name
		}
	} else {
		expected["receiver_schema_name"] = m.Receiver.Name
		expected["receiver_schema_oid"] = oidText(m.Receiver.OID)
		expected["receiver_owner"] = m.Receiver.Owner.Name
		expected["receiver_runtime"] = m.Receiver.Runtime.Name
	}
	for k, v := range expected {
		want, _ := json.Marshal(v)
		if !bytes.Equal(actual[k], want) {
			return ErrState
		}
	}
	var version int
	var digest string
	migration := pgx.Identifier{prefix + "_schema_migrations"}.Sanitize()
	if tx.QueryRow(ctx, "SELECT count(*) FROM "+migration).Scan(&count) != nil || count != 1 {
		return ErrState
	}
	if tx.QueryRow(ctx, "SELECT version,sha256 FROM "+migration).Scan(&version, &digest) != nil || version != m.OwnSchemaSQL.SchemaVersion || digest != m.OwnSchemaSQL.SHA256 {
		return ErrState
	}
	rows, e := tx.Query(ctx, "SELECT key_id,key_digest,not_before,not_after FROM "+pgx.Identifier{prefix + "_signing_keys"}.Sanitize()+" ORDER BY key_id")
	if e != nil {
		return ErrState
	}
	keys := map[string]keyMaterial{}
	for _, key := range m.Keys {
		keys[key.ID] = key
	}
	valid := true
	count = 0
	for rows.Next() {
		var id, digest string
		var before, after time.Time
		if rows.Scan(&id, &digest, &before, &after) != nil {
			valid = false
			break
		}
		key, ok := keys[id]
		window := c.windows[id]
		if !ok || key.Digest != digest || !before.Equal(window.before) || !after.Equal(window.after) {
			valid = false
			break
		}
		count++
	}
	rows.Close()
	if rows.Err() != nil || !valid || count != len(keys) {
		return ErrState
	}
	var now time.Time
	if tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now) != nil {
		return ErrState
	}
	active := c.windows[m.ActiveKeyID]
	second := time.Unix(now.Unix(), 0).UTC()
	if second.Before(active.before) || second.After(active.after) {
		return ErrState
	}
	if tx.Commit(ctx) != nil {
		return ErrState
	}
	return nil
}
