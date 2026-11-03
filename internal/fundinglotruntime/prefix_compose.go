package fundinglotruntime

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/migrations"
)

// VerifyOrdinaryPeriodicPrefixApplicationTx authenticates all 87 source/wallet
// tables and the fixed V3 catalog in the original actual APP RR RO transaction.
func VerifyOrdinaryPeriodicPrefixApplicationTx(ctx context.Context, tx pgx.Tx, pins fundinglotcatalog.Pins,
	expected *fundinglotproof.CanonicalV2) (*RuntimeProof, error) {
	return verifyApplicationProfileTx(ctx, tx, pins, expected, nil, fundinglotcatalog.Ordinary, fundinglotcatalog.PeriodicPrefixV3)
}

// VerifySandboxPeriodicPrefixApplicationTx additionally binds original local
// APP material and its predecessor. Neither entry ends or renews the caller tx.
func VerifySandboxPeriodicPrefixApplicationTx(ctx context.Context, tx pgx.Tx, pins fundinglotcatalog.Pins,
	expected *fundinglotproof.CanonicalV2, local *ApplicationState) (*RuntimeProof, error) {
	return verifyApplicationProfileTx(ctx, tx, pins, expected, local, fundinglotcatalog.Sandbox, fundinglotcatalog.PeriodicPrefixV3)
}

type applicationGraph struct {
	legacy  fundinglotscan.WalletReport
	prefix  fundinglotscan.PrefixWalletReport
	epoch   fundinglotscan.EpochProofRows
	payment fundinglotscan.ApplicationPaymentProofRows
}

func readApplicationGraph(w *verificationWindow, q *walletQuery, schema string, mode fundinglotscan.Mode, profile fundinglotcatalog.Profile) (applicationGraph, error) {
	var out applicationGraph
	if profile == fundinglotcatalog.PeriodicPrefixV3 {
		facts, err := fundinglotscan.ReadWalletPrefix(w.ctx, q, schema, mode)
		if err != nil || w.check() != nil {
			return applicationGraph{}, ErrRuntime
		}
		out.prefix = facts.Summary()
		if !runtimePrefixReportComplete(out.prefix, mode) {
			return applicationGraph{}, ErrRuntime
		}
		out.epoch, err = fundinglotscan.ProjectEpochProofPrefix(w.ctx, facts)
		if err != nil || w.check() != nil {
			return applicationGraph{}, ErrRuntime
		}
		out.payment, err = fundinglotscan.ProjectApplicationPaymentProofPrefix(w.ctx, facts)
		if err != nil || w.check() != nil {
			return applicationGraph{}, ErrRuntime
		}
		return out, nil
	}
	if profile != fundinglotcatalog.WalletReadV2 {
		return applicationGraph{}, ErrRuntime
	}
	facts, err := fundinglotscan.ReadWallet(w.ctx, q, schema, mode)
	if err != nil || w.check() != nil {
		return applicationGraph{}, ErrRuntime
	}
	out.legacy = facts.Summary()
	if !runtimeReportComplete(out.legacy, mode) {
		return applicationGraph{}, ErrRuntime
	}
	out.epoch, err = fundinglotscan.ProjectEpochProof(w.ctx, facts)
	if err != nil || w.check() != nil {
		return applicationGraph{}, ErrRuntime
	}
	out.payment, err = fundinglotscan.ProjectApplicationPaymentProof(w.ctx, facts)
	if err != nil || w.check() != nil {
		return applicationGraph{}, ErrRuntime
	}
	return out, nil
}

func verifyProfileRuntimeHistory(w *verificationWindow, tx pgx.Tx, schema string, profile fundinglotcatalog.Profile) error {
	if profile == fundinglotcatalog.PeriodicPrefixV3 {
		if w.check() != nil || migrations.VerifyOriginal22SnapshotTx(w.ctx, tx, schema) != nil || w.check() != nil {
			return ErrRuntime
		}
		return nil
	}
	if profile != fundinglotcatalog.WalletReadV2 {
		return ErrRuntime
	}
	return verifyRuntimeHistory(w, tx, schema)
}

func runtimePrefixReportComplete(r fundinglotscan.PrefixWalletReport, mode fundinglotscan.Mode) bool {
	if mode != fundinglotscan.ModeOrdinary && mode != fundinglotscan.ModeSandbox ||
		r.Scope != "complete_source_wallet_prefix_graphs_v1" || r.Mode != mode || r.Status != "consistent" ||
		!r.PhysicalEOFComplete || !r.CoverageComplete || r.ScannedTables != 87 || r.ScannedColumns != 804 || r.SourceRows > 100000 || r.SourceBytes > 16<<20 ||
		r.InputMismatchFields != 0 || r.JournalMetadataMismatches != 0 || r.OrdinaryBoundaryMismatches != 0 || r.ObservationSourceMismatches != 0 {
		return false
	}
	for i, state := range r.Algorithms {
		want := "executed"
		if i == 6 && mode == fundinglotscan.ModeOrdinary {
			want = "not_applicable"
		}
		if state != want {
			return false
		}
	}
	return true
}
