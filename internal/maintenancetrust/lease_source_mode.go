package maintenancetrust

import "github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"

// SourceMode describes the original owned predecessor. A mode value carries
// no maintenance authority and cannot change the mode of a lease or Permit.
type SourceMode uint8

const (
	SourceModeOrdinary SourceMode = iota + 1
	SourceModeSandbox
)

// SourceMode is available only during a valid complete-source lease. It reads
// immutable predecessor evidence, never the current payment policy or SQL.
// Both lifecycle checks and the mode remain bound to the original Permit.
func (l *ReadLease) SourceMode() (SourceMode, error) {
	if l == nil || l.state == nil || l.state.snapshot == nil || l.state.snapshot.gate == nil {
		return 0, ErrInput
	}
	ls := l.state
	if err := ls.beginMethod(); err != nil {
		return 0, err
	}
	defer ls.endMethod()
	if ls.kind != ScanCompleteSources {
		return 0, ls.fail(ErrTransaction)
	}
	a := ls.permit.authority
	if a == nil || a.snapshot != ls.snapshot || a.predecessor == nil {
		return 0, ls.fail(ErrTransaction)
	}
	if err := a.predecessor.verifyAuthority(a); err != nil {
		return 0, ls.fail(err)
	}
	var mode SourceMode
	switch a.predecessor.mode {
	case fundinglotcatalog.Ordinary:
		mode = SourceModeOrdinary
	case fundinglotcatalog.Sandbox:
		mode = SourceModeSandbox
	default:
		return 0, ls.fail(ErrTransaction)
	}
	gs := ls.snapshot.gate
	gs.mu.Lock()
	err := ls.validLocked()
	gs.mu.Unlock()
	if err != nil {
		return 0, ls.fail(err)
	}
	return mode, nil
}
