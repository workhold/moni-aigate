package fundinglotscan

import (
	"context"
	"reflect"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// ApplicationPaymentProofRows contains ordinary descriptions of the original
// APP payment rows. It carries no database, local-file, or runtime authority.
type ApplicationPaymentProofRows struct {
	Environment *fundinglotverify.SandboxGraphEnvironmentRow
	Attestation *fundinglotverify.SandboxGraphAttestationRow
}

// ProjectApplicationPaymentProof copies the APP originals from one completed,
// qualified 84-table ReadWallet. Ordinary mode has neither original; sandbox
// mode has exactly one of each. It performs no query and uses only this call's
// context, without sharing strings, bytes, or pointers with the original Facts.
func ProjectApplicationPaymentProof(ctx context.Context, facts WalletFacts) (ApplicationPaymentProofRows, error) {
	if ctx == nil {
		return ApplicationPaymentProofRows{}, ErrInput
	}
	if ctx.Err() != nil {
		return ApplicationPaymentProofRows{}, contextFailure(ctx)
	}
	f := facts.state
	if f == nil || f.raw == nil || !epochProofReportQualified(f) {
		return ApplicationPaymentProofRows{}, ErrInput
	}
	if err := epochProofRaw(ctx, f); err != nil {
		return ApplicationPaymentProofRows{}, err
	}
	if err := legacySeedScanned(ctx, f.raw); err != nil {
		return ApplicationPaymentProofRows{}, err
	}
	if !legacySeedCounts(f.raw, f.report.Wallet.Scanned, legacySeedBindingTables(walletBindings())) {
		return ApplicationPaymentProofRows{}, ErrInput
	}
	// Failed cell qualification belongs only to this fresh projection view.
	// Neither the expired original context/Query nor its rejected map is reused.
	view := &session{ctx: ctx, tables: f.raw.tables, index: f.raw.index, rejected: map[cellRef]bool{}}
	b := sandboxBindings()
	var environments []fundinglotverify.SandboxGraphEnvironmentRow
	var attestations []fundinglotverify.SandboxGraphAttestationRow
	qualified := true
	if err := projectInto(view, b[0], &environments, &qualified); err != nil {
		return ApplicationPaymentProofRows{}, err
	}
	if err := projectInto(view, b[1], &attestations, &qualified); err != nil {
		return ApplicationPaymentProofRows{}, err
	}
	if !qualified || len(view.rejected) != 0 {
		return ApplicationPaymentProofRows{}, ErrInput
	}
	var out ApplicationPaymentProofRows
	if f.raw.mode == ModeOrdinary {
		if len(environments) != 0 || len(attestations) != 0 {
			return ApplicationPaymentProofRows{}, ErrInput
		}
	} else {
		if len(environments) != 1 || len(attestations) != 1 {
			return ApplicationPaymentProofRows{}, ErrInput
		}
		out.Environment, out.Attestation = &environments[0], &attestations[0]
		if len(out.Attestation.ProofCanonical) == 0 || !applicationPaymentProofTime(out.Environment.CreatedAt) ||
			!applicationPaymentProofTime(out.Attestation.CreatedAt) || out.Attestation.CreatedAt.IsZero() {
			return ApplicationPaymentProofRows{}, ErrInput
		}
		if err := cloneLegacySeedStrings(ctx, reflect.ValueOf(&out).Elem()); err != nil {
			return ApplicationPaymentProofRows{}, err
		}
	}
	if ctx.Err() != nil {
		return ApplicationPaymentProofRows{}, contextFailure(ctx)
	}
	return out, nil
}

func applicationPaymentProofTime(at time.Time) bool {
	return at.UTC().Year() >= 1 && at.UTC().Year() <= 9999 && at.Nanosecond()%1000 == 0
}
