package fundinglotbasis_test

import (
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
)

func readIndependentPrefixBasis(t *testing.T, f *independentBasisPG, tx pgx.Tx) (fundinglotbasis.Report, *independentBasisQuery) {
	t.Helper()
	q := f.query(tx)
	facts, err := fundinglotbasis.ReadPrefix(f.ctx, q, f.schema)
	if err != nil {
		if q.lastError != nil {
			independentBasisPGMust(t, "execute actual prefix basis SQL", q.lastError)
		}
		independentBasisPGMust(t, "read actual prefix basis", err)
	}
	r := facts.Summary()
	if r.Scope != "fixed_112_table_prefix_basis_v1" || !r.PhysicalEOFComplete || r.ScannedTables != 112 || r.ScannedColumns != 1004 || r.DescriptorSHA256 != fundinglotbasis.PeriodicPrefixDescriptorSHA256 || len(q.pages) != 112 || len(q.counts) != 112 {
		t.Fatal("actual prefix basis did not completely enumerate its fixed descriptor")
	}
	for _, rows := range q.rows {
		if !rows.eof || !rows.checked || !rows.closed {
			t.Fatal("actual prefix basis omitted EOF, Err, or Close")
		}
	}
	return r, q
}

func rejectIndependentPrefixBasis(t *testing.T, f *independentBasisPG, tx pgx.Tx) {
	t.Helper()
	q := f.query(tx)
	facts, err := fundinglotbasis.ReadPrefix(f.ctx, q, f.schema)
	if err != fundinglotbasis.ErrMetadata || !reflect.DeepEqual(facts.Summary(), fundinglotbasis.Report{}) || q.business != 0 {
		t.Fatal("prefix metadata rejection returned partial facts or read business records")
	}
}

func TestIndependentPostgresPrefixBasis(t *testing.T) {
	f := newIndependentBasisPG(t)
	var originalRows uint64
	t.Run("legacy_layout_rejected_and_original_reader_preserved", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadOnly)
		rejectIndependentPrefixBasis(t, f, tx)
		r, _ := f.compareReference(t, tx)
		originalRows = r.SourceRows
	})
	// Install the SHA-authenticated candidate only inside this isolated owner
	// fixture. This intentionally does not exercise the B-P2 test installer or
	// claim production installation, roles, catalog proof, or financial validity.
	sql, err := periodicbudgetprefixschema.SQL()
	independentBasisPGMust(t, "load frozen prefix candidate fixture material", err)
	_, err = f.owner.Exec(f.ctx, sql)
	independentBasisPGMust(t, "install isolated physical prefix fixture", err)
	t.Run("full_native_metadata_and_empty_table_eof", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadOnly)
		r, q := readIndependentPrefixBasis(t, f, tx)
		if originalRows < 22 || r.SourceRows != originalRows {
			t.Fatal("prefix basis lost original history or migration seed rows")
		}
		for name, n := range q.pages {
			if n != 1 || q.counts[name] != 1 {
				t.Fatal("physical empty-table EOF or count omitted")
			}
		}
		f.reject(t, tx, fundinglotbasis.ErrMetadata)
	})
	t.Run("native_generated_columns_and_129_row_binary_cursor", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadWrite)
		f.exec(t, tx, "SET LOCAL session_replication_role='replica'")
		// Physical orphan records deliberately test record_send and pagination.
		// Their financial graph is not asserted to be valid by a basis fingerprint.
		f.exec(t, tx, `INSERT INTO periodic_budget_observation_origins(control_id,tracking_started_at)
 VALUES('11111111-1111-4111-8111-111111111111','2026-11-03 01:02:03.123456+00')`)
		f.exec(t, tx, `INSERT INTO periodic_budget_observation_heads(control_id)
 VALUES('11111111-1111-4111-8111-111111111111')`)
		f.exec(t, tx, `INSERT INTO periodic_budget_observation_journal
 (id,control_id,ordinal,kind,source_id,event_seq,configuration_revision,configuration_operation_id,observed_at)
 SELECT md5('basis-observation:'||n::text)::uuid::text,'11111111-1111-4111-8111-111111111111',n,
 'command',md5('basis-source:'||n::text)::uuid::text,0,1,'22222222-2222-4222-8222-222222222222',
 '2026-11-03 01:02:03.123456+00'::timestamptz FROM generate_series(1,129) AS n`)
		before, q := readIndependentPrefixBasis(t, f, tx)
		if before.SourceRows != originalRows+131 || q.pages["periodic_budget_observation_journal"] != 2 {
			t.Fatal("actual new source records or binary page transition missing")
		}
		var generated bool
		independentBasisPGMust(t, "inspect actual generated fixture values", tx.QueryRow(f.ctx, `SELECT bool_and(command_source_id=source_id AND window_source_id IS NULL) FROM periodic_budget_observation_journal`).Scan(&generated))
		if !generated {
			t.Fatal("fixture did not produce native generated discriminator columns")
		}
		f.exec(t, tx, `UPDATE periodic_budget_observation_origins SET tracking_started_at=tracking_started_at+interval '1 microsecond'`)
		after, _ := readIndependentPrefixBasis(t, f, tx)
		if after.BasisSHA256 == before.BasisSHA256 || after.SourceRows != before.SourceRows || after.SourceBytes != before.SourceBytes {
			t.Fatal("one native field change did not change the actual prefix basis")
		}
	})
	for _, tc := range []struct{ name, sql string }{
		{"generated_nullability", `ALTER TABLE periodic_budget_observation_journal ALTER COLUMN command_source_id SET NOT NULL`},
		{"binding_type", `ALTER TABLE periodic_budget_operations ALTER COLUMN observation_id TYPE varchar`},
		{"extra_source_column", `ALTER TABLE periodic_budget_observation_heads ADD COLUMN unexpected text`},
		{"new_source_row_security", `ALTER TABLE periodic_budget_observation_origins ENABLE ROW LEVEL SECURITY`},
		{"new_source_primary_key_order", `ALTER TABLE periodic_budget_observation_journal DROP CONSTRAINT periodic_budget_observation_journal_pkey CASCADE; ALTER TABLE periodic_budget_observation_journal ADD PRIMARY KEY(control_id,id)`},
	} {
		t.Run("metadata_drift/"+tc.name, func(t *testing.T) {
			tx := f.begin(t, pgx.ReadWrite)
			f.exec(t, tx, tc.sql)
			rejectIndependentPrefixBasis(t, f, tx)
		})
	}
}
