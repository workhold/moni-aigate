package procurementprefix

import (
	"context"
	"strconv"
	"strings"
)

// A replayWork belongs to one complete input and carries its original absolute
// deadline. Domains never issue public proof or restart this budget.
type replayWork struct{ b budget }

func newReplayWork(ctx context.Context, rows Rows) (*replayWork, Scan, error) {
	scanned, err := budgetRows(ctx, rows)
	if err != nil {
		return nil, Scan{}, err
	}
	w := &replayWork{b: budget{ctx: ctx, rows: scanned.TotalRows, bytes: scanned.LogicalBytes}}
	w.b.deadline, _ = ctx.Deadline()
	if err := w.b.check(); err != nil {
		return nil, Scan{}, err
	}
	return w, scanned, nil
}

func (w *replayWork) tick() error {
	if w == nil || w.b.ctx == nil {
		return ErrInput
	}
	return w.b.tick()
}

type keySet map[string]struct{}

// Byte lengths make arbitrary TEXT components unambiguous, including empty
// strings, colons, NUL in invalid raw input, and UTF8 multi-byte characters.
func tupleKey(parts ...string) string {
	var out strings.Builder
	for _, part := range parts {
		out.WriteString(strconv.Itoa(len(part)))
		out.WriteByte(':')
		out.WriteString(part)
	}
	return out.String()
}

func (s keySet) add(parts ...string) { s[tupleKey(parts...)] = struct{}{} }

type propositionKeys struct {
	OriginalDeliveryPredecessor keySet
	FinalizationOrder           keySet
	ConflictIncoming            keySet
	ConflictReason              keySet
	IncomingBinding             keySet
	ProcurementEventOrder       keySet
	CostRecordedEligibility     keySet
	CurrentCostOrder            keySet
	ProposalBarrier             keySet
	AccountPauseOrdering        keySet
	AccountAdmissionOrder       keySet
}

func newPropositionKeys() propositionKeys {
	return propositionKeys{
		OriginalDeliveryPredecessor: make(keySet), FinalizationOrder: make(keySet),
		ConflictIncoming: make(keySet), ConflictReason: make(keySet), IncomingBinding: make(keySet),
		ProcurementEventOrder: make(keySet), CostRecordedEligibility: make(keySet), CurrentCostOrder: make(keySet),
		ProposalBarrier: make(keySet), AccountPauseOrdering: make(keySet), AccountAdmissionOrder: make(keySet),
	}
}

// Stable bottom-up merge sorting charges every comparison and copy to the
// caller's original work. Cancellation returns without exposing partial output.
func replaySort[T any](w *replayWork, rows []T, less func(T, T) bool) error {
	if w == nil || w.b.ctx == nil {
		return ErrInput
	}
	if err := w.b.check(); err != nil {
		return err
	}
	if len(rows) < 2 {
		return nil
	}
	other := make([]T, len(rows))
	source, target := rows, other
	for width := 1; width < len(rows); width *= 2 {
		for start := 0; start < len(rows); start += 2 * width {
			middle, end := min(start+width, len(rows)), min(start+2*width, len(rows))
			a, b := start, middle
			for i := start; i < end; i++ {
				if err := w.tick(); err != nil {
					return err
				}
				if a < middle && (b >= end || !less(source[b], source[a])) {
					target[i] = source[a]
					a++
				} else {
					target[i] = source[b]
					b++
				}
			}
		}
		source, target = target, source
	}
	for i := range rows {
		if err := w.tick(); err != nil {
			return err
		}
		rows[i] = source[i]
	}
	return w.b.check()
}
