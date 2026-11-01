package maintenancetrust

import (
	"bytes"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

type predecessorOrdinaryRows struct {
	history    [21]bool
	foreignKey bool
	policy     predecessorRow
	bypasses   [6]bool
}

// A predecessor is private evidence, never a deserializable credential. Its
// complete owned rows preserve peer-local values without changing the v1 SHA.
type predecessorEvidence struct {
	authority     *authorityStage
	nonce         *authorityNonce
	snapshot      *snapshotState
	snapshotNonce *snapshotNonce
	gateNonce     *gateNonce
	installed     *fundinglotcatalog.InstallationProof
	manifest      *manifestEvidence
	identity      *maintenanceIdentity
	purpose       snapshotMode
	mode          fundinglotcatalog.Mode
	v1SHA256      string
	canonical     []byte
	proof         predecessorV1Proof
	rows          predecessorRows
	ordinary      predecessorOrdinaryRows
	resources     predecessorResources
}

func (predecessorEvidence) String() string   { return "maintenance_predecessor_redacted" }
func (predecessorEvidence) GoString() string { return "maintenance_predecessor_redacted" }
func (predecessorEvidence) MarshalJSON() ([]byte, error) {
	return []byte(`{"predecessor":"redacted"}`), nil
}

func readPredecessorAuthority(a *authorityStage) (*predecessorEvidence, error) {
	if a == nil || a.snapshot == nil || a.snapshot.gate == nil || a.manifest == nil || a.identity == nil {
		return nil, ErrTransaction
	}
	subcall, cancel, err := a.beginPredecessor()
	if err != nil {
		return nil, err
	}
	// Stop/ack the original cancellation callback before cancelling its caller.
	defer cancel()
	defer subcall.close()
	work := subcall.ctx
	if err = a.verify(work); err != nil {
		return nil, err
	}
	if a.manifest.verifyAuthority(a) != nil {
		return nil, ErrTransaction
	}
	st := a.snapshot
	e := &predecessorEvidence{authority: a, nonce: a.nonce, snapshot: st, snapshotNonce: st.nonce, gateNonce: st.gateNonce, installed: a.installed, manifest: a.manifest, identity: a.identity, purpose: st.mode, mode: st.gate.pins.Mode}
	r := &predecessorReader{authority: a, work: work}
	switch e.mode {
	case fundinglotcatalog.Ordinary:
		if e.ordinary, err = r.readOrdinary(); err != nil {
			return nil, err
		}
	case fundinglotcatalog.Sandbox:
		tables := predecessorTables()
		for i, table := range tables {
			schema := st.gate.pins.AppSchema.Name
			switch i {
			case 2, 4, 6:
				schema = st.gate.pins.Sandbox.Mock.Name
			case 3, 5, 7:
				schema = st.gate.pins.Sandbox.Receiver.Name
			}
			if e.rows.tables[i], err = r.readTable(schema, table); err != nil {
				return nil, err
			}
		}
		if e.proof, e.canonical, e.v1SHA256, err = predecessorValidateSandbox(st.gate.pins, e.rows); err != nil {
			return nil, err
		}
	default:
		return nil, ErrTransaction
	}
	e.resources = r.resources
	if err = e.verifyAuthority(a); err != nil {
		return nil, err
	}
	if err = a.verify(work); err != nil {
		return nil, err
	}
	gs := st.gate
	gs.mu.Lock()
	valid := st.invalidErr == nil && st.ctx.Err() == nil && work.Err() == nil && a.call.ctx.Err() == nil && a.call.caller.Err() == nil && a.active && st.activeAuthority == a
	if valid {
		subcall.stopLocked()
	}
	gs.mu.Unlock()
	if !valid {
		return nil, authorityError(work, st, ErrTransaction)
	}
	return e, nil
}

// This verifies immutable issuer ownership only. Lifecycle/actual SQL binding
// belongs to authorityStage.verify, including later Permit.Verify calls.
func (e *predecessorEvidence) verifyAuthority(a *authorityStage) error {
	if e == nil || a == nil || a.snapshot == nil || a.snapshot.gate == nil || e.authority != a || e.nonce == nil || e.nonce != a.nonce || e.snapshot != a.snapshot || e.snapshotNonce == nil || e.snapshotNonce != a.snapshot.nonce || e.gateNonce == nil || e.gateNonce != a.snapshot.gateNonce || e.installed == nil || e.installed != a.installed || e.manifest == nil || e.manifest != a.manifest || e.identity == nil || e.identity != a.identity || e.purpose != a.snapshot.mode || e.mode != a.snapshot.gate.pins.Mode || e.resources.bytes < 0 || e.resources.bytes > predecessorByteBudget || e.resources.rows < 1 || e.resources.rows > predecessorRowBudget {
		return ErrTransaction
	}
	switch e.mode {
	case fundinglotcatalog.Ordinary:
		if e.v1SHA256 != "" || len(e.canonical) != 0 || !predecessorValidateOrdinary(e.ordinary) {
			return ErrIdentity
		}
	case fundinglotcatalog.Sandbox:
		p, raw, digest, err := predecessorValidateSandbox(a.snapshot.gate.pins, e.rows)
		if err != nil {
			return err
		}
		saved, err := predecessorV1CanonicalProof(e.proof)
		if err != nil || !bytes.Equal(saved, raw) || !bytes.Equal(raw, e.canonical) || digest != e.v1SHA256 || p.ProofVersion != 1 {
			return ErrIdentity
		}
	default:
		return ErrTransaction
	}
	return nil
}

func (r *predecessorReader) readOrdinary() (predecessorOrdinaryRows, error) {
	var out predecessorOrdinaryRows
	st := r.authority.snapshot
	schema := st.gate.pins.AppSchema.Name
	for i, table := range predecessorHistoryTables {
		q := "SELECT EXISTS(SELECT 1 FROM ONLY " + pgx.Identifier{schema, table}.Sanitize() + ")"
		if st.rawTx.QueryRow(r.work, q, pgx.QueryExecModeSimpleProtocol).Scan(&out.history[i]) != nil {
			return out, executionError(r.work)
		}
		if err := r.resources.addBytes(1); err != nil {
			return out, err
		}
		if out.history[i] {
			return out, ErrIdentity
		}
	}
	if st.rawTx.QueryRow(r.work, predecessorQueryConflictsSQL, pgx.QueryExecModeSimpleProtocol, schema).Scan(&out.foreignKey) != nil {
		return out, executionError(r.work)
	}
	if err := r.resources.addBytes(1); err != nil {
		return out, err
	}
	if !out.foreignKey {
		return out, ErrIdentity
	}
	policy, err := r.readTable(schema, predecessorPolicyTable)
	if err != nil {
		return out, err
	}
	out.policy = policy[0]
	if !predecessorValidatePolicy(out.policy) {
		return out, ErrIdentity
	}
	for i, item := range predecessorBypasses {
		q := "SELECT EXISTS(SELECT 1 FROM ONLY " + pgx.Identifier{schema, item.table}.Sanitize() + " WHERE " + item.predicate + ")"
		if st.rawTx.QueryRow(r.work, q, pgx.QueryExecModeSimpleProtocol).Scan(&out.bypasses[i]) != nil {
			return out, executionError(r.work)
		}
		if err = r.resources.addBytes(1); err != nil {
			return out, err
		}
		if out.bypasses[i] {
			return out, ErrIdentity
		}
	}
	if r.work.Err() != nil {
		return out, ErrTimeout
	}
	return out, nil
}
func predecessorValidatePolicy(row predecessorRow) bool {
	if !predecessorValidOwnedRow(predecessorPolicyTable, row) || !row.singleton() {
		return false
	}
	return !row["enabled"].boolean && row["revision"].int8 == 1
}
func predecessorValidateOrdinary(rows predecessorOrdinaryRows) bool {
	if !rows.foreignKey || !predecessorValidatePolicy(rows.policy) {
		return false
	}
	for _, v := range rows.history {
		if v {
			return false
		}
	}
	for _, v := range rows.bypasses {
		if v {
			return false
		}
	}
	return true
}
