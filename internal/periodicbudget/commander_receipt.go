package periodicbudget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

type commanderSafeInput struct {
	ExpectedRevision string `json:"expected_revision"`
	PeriodKind       string `json:"period_kind"`
	Enabled          bool   `json:"enabled"`
	LimitAmount      string `json:"limit_amount"`
}

func commanderInput(in PutPeriodicBudget) commanderSafeInput {
	return commanderSafeInput{in.ExpectedRevision, in.PeriodKind, in.Enabled, in.LimitAmount}
}
func commanderDecodeInput(raw []byte, operation string) (commanderSafeInput, error) {
	var in commanderSafeInput
	fields, err := readObject(raw, "expected_revision", "period_kind", "enabled", "limit_amount")
	if err != nil {
		return in, err
	}
	for _, raw := range fields {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return in, ErrInvariant
		}
	}
	if json.Unmarshal(raw, &in) != nil || (PutPeriodicBudget{operation, in.ExpectedRevision, in.PeriodKind, in.Enabled, in.LimitAmount}).Validate() != nil {
		return in, ErrInvariant
	}
	return in, nil
}

const commanderOperationColumns = `operation_id,kind,scope,actor,target,input_fingerprint,resource_id,revision::text,pricing_time(created_at),result_snapshot,safe_input`

func commanderScanReceipt(ctx context.Context, tx pgx.Tx, row pgx.Row) (CommandReceipt, commanderSafeInput, error) {
	var out CommandReceipt
	var resultRaw, inputRaw []byte
	err := row.Scan(&out.Operation.OperationID, &out.Operation.Kind, &out.Operation.Scope, &out.Operation.Actor, &out.Operation.Target, &out.Operation.InputFingerprint, &out.Operation.ResourceID, &out.Operation.Revision, &out.Operation.CreatedAt, &resultRaw, &inputRaw)
	if err != nil {
		return out, commanderSafeInput{}, err
	}
	in, err := commanderDecodeInput(inputRaw, out.Operation.OperationID)
	if err != nil {
		return CommandReceipt{}, in, err
	}
	out.Result, err = readDecodeView(resultRaw)
	if err != nil || out.Validate() != nil {
		return CommandReceipt{}, in, ErrInvariant
	}
	fp, err := Fingerprint(out.Operation.Kind, out.Operation.Actor, out.Operation.Scope, out.Operation.Target, PutPeriodicBudget{out.Operation.OperationID, in.ExpectedRevision, in.PeriodKind, in.Enabled, in.LimitAmount})
	if err != nil || fp != out.Operation.InputFingerprint {
		return CommandReceipt{}, in, ErrInvariant
	}
	var scope, id string
	var tenant *string
	err = tx.QueryRow(ctx, `SELECT scope,coalesce(account_id,key_id,tenant_id),tenant_id FROM periodic_budget_controls WHERE id=$1`, out.Operation.ResourceID).Scan(&scope, &id, &tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommandReceipt{}, in, ErrInvariant
	}
	if err != nil {
		return CommandReceipt{}, in, err
	}
	if scope != out.Result.Scope || id != out.Result.ScopeID || scope == "key" && (tenant == nil || *tenant != out.Operation.Scope) || scope != "key" && out.Operation.Scope != "platform" {
		return CommandReceipt{}, in, ErrInvariant
	}
	return out, in, nil
}
func commanderOriginal(ctx context.Context, tx pgx.Tx, actor string, target commanderTarget, in PutPeriodicBudget) (CommandReceipt, bool, error) {
	row := tx.QueryRow(ctx, `SELECT `+commanderOperationColumns+` FROM periodic_budget_operations WHERE operation_id=$1`, in.OperationID)
	out, original, err := commanderScanReceipt(ctx, tx, row)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommandReceipt{}, false, nil
	}
	if err != nil {
		return CommandReceipt{}, false, err
	}
	fp, err := Fingerprint(target.kind(), actor, target.identity(), target.path(), in)
	if err != nil {
		return CommandReceipt{}, false, err
	}
	if out.Operation.OperationID != in.OperationID || out.Operation.Actor != actor || out.Operation.Kind != target.kind() || out.Operation.Scope != target.identity() || out.Operation.Target != target.path() || out.Operation.InputFingerprint != fp || original != commanderInput(in) {
		return CommandReceipt{}, false, ErrOperationConflict
	}
	return out, true, nil
}
func commanderInsertReceipt(ctx context.Context, tx pgx.Tx, actor, control string, target commanderTarget, in PutPeriodicBudget) (CommandReceipt, error) {
	raw, err := json.Marshal(commanderInput(in))
	if err != nil {
		return CommandReceipt{}, ErrInvariant
	}
	out, stored, err := commanderScanReceipt(ctx, tx, tx.QueryRow(ctx, `INSERT INTO periodic_budget_operations(operation_id,kind,actor,scope,target,resource_id,safe_input)
 VALUES($1,$2,$3,$4,$5,$6,$7::jsonb) RETURNING `+commanderOperationColumns, in.OperationID, target.kind(), actor, target.identity(), target.path(), control, raw))
	if err != nil {
		return CommandReceipt{}, err
	}
	fp, err := Fingerprint(target.kind(), actor, target.identity(), target.path(), in)
	if err != nil {
		return CommandReceipt{}, err
	}
	if out.Operation.OperationID != in.OperationID || out.Operation.Kind != target.kind() || out.Operation.Actor != actor || out.Operation.Scope != target.identity() || out.Operation.Target != target.path() || out.Operation.InputFingerprint != fp || out.Operation.ResourceID != control || stored != commanderInput(in) {
		return CommandReceipt{}, ErrInvariant
	}
	return out, nil
}
