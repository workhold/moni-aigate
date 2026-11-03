package fundinglotscan

import (
	"context"
	"reflect"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// ProjectLegacySeedPrefix returns deep-copied ordinary DTOs from one completed,
// qualified prefix union. It performs no query and grants no authority.
func ProjectLegacySeedPrefix(ctx context.Context, facts PrefixFacts) (LegacySeedRows, error) {
	s, err := qualifyPrefixSource(ctx, facts)
	if err != nil {
		return LegacySeedRows{}, err
	}
	view := &session{ctx: ctx, tables: s.tables, index: s.index, rejected: map[cellRef]bool{}}
	var out LegacySeedRows
	qualified := true
	b := legacySeedBindings()
	for _, step := range []func() error{
		func() error { return projectInto(view, b[0], &out.Tenants, &qualified) },
		func() error { return projectInto(view, b[1], &out.Accounts, &qualified) },
		func() error { return projectInto(view, b[2], &out.Journals, &qualified) },
		func() error { return projectInto(view, b[3], &out.Lines, &qualified) },
		func() error { return projectInto(view, b[4], &out.Receipts, &qualified) },
		func() error { return projectInto(view, b[5], &out.Requests, &qualified) },
		func() error { return projectInto(view, b[6], &out.Reservations, &qualified) },
		func() error { return projectInto(view, b[7], &out.Settlements, &qualified) },
		func() error { return projectInto(view, b[8], &out.Refunds, &qualified) },
	} {
		if err := step(); err != nil {
			return LegacySeedRows{}, err
		}
	}
	if !qualified || len(view.rejected) != 0 {
		return LegacySeedRows{}, ErrInput
	}
	if err := cloneLegacySeedStrings(ctx, reflect.ValueOf(&out).Elem()); err != nil {
		return LegacySeedRows{}, err
	}
	if ctx.Err() != nil {
		return LegacySeedRows{}, contextFailure(ctx)
	}
	return out, nil
}

// ProjectEpochProofPrefix returns deep-copied ordinary DTOs from one completed,
// qualified prefix union. It performs no query and grants no authority.
func ProjectEpochProofPrefix(ctx context.Context, facts PrefixWalletFacts) (EpochProofRows, error) {
	s, err := qualifyPrefixWallet(ctx, facts)
	if err != nil {
		return EpochProofRows{}, err
	}
	view := &session{ctx: ctx, tables: s.tables, index: s.index, rejected: map[cellRef]bool{}}
	b := walletBindings()
	var epochs []fundinglotverify.WalletLotsEpochRow
	var attestations []fundinglotverify.WalletLotsAttestationRow
	qualified := true
	if err := projectInto(view, b[9], &epochs, &qualified); err != nil {
		return EpochProofRows{}, err
	}
	if err := projectInto(view, b[10], &attestations, &qualified); err != nil {
		return EpochProofRows{}, err
	}
	if !qualified || len(view.rejected) != 0 || len(epochs) != 1 || len(attestations) != 1 {
		return EpochProofRows{}, ErrInput
	}
	out := EpochProofRows{Epoch: epochs[0], Attestation: attestations[0]}
	if len(out.Attestation.ProofCanonical) == 0 || !epochProofTime(out.Epoch.CreatedAt) || !epochProofTime(out.Attestation.CreatedAt) {
		return EpochProofRows{}, ErrInput
	}
	if err := cloneLegacySeedStrings(ctx, reflect.ValueOf(&out).Elem()); err != nil {
		return EpochProofRows{}, err
	}
	if ctx.Err() != nil {
		return EpochProofRows{}, contextFailure(ctx)
	}
	return out, nil
}

// ProjectApplicationPaymentProofPrefix returns deep-copied ordinary DTOs from one completed,
// qualified prefix union. It performs no query and grants no authority.
func ProjectApplicationPaymentProofPrefix(ctx context.Context, facts PrefixWalletFacts) (ApplicationPaymentProofRows, error) {
	s, err := qualifyPrefixWallet(ctx, facts)
	if err != nil {
		return ApplicationPaymentProofRows{}, err
	}
	view := &session{ctx: ctx, tables: s.tables, index: s.index, rejected: map[cellRef]bool{}}
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
	if s.mode == ModeOrdinary {
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
