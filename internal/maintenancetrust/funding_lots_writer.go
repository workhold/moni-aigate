package maintenancetrust

import (
	"bytes"
	"context"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotinit"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// These declarations are the entire initialization write allowlist. Columns,
// casts and RETURNING expressions never come from a public argument or Plan.
// t=text, i=int8, n=exact integer numeric, o=oid, v=int4, b=bool,
// y=bytea, z=finite native timestamptz. Uppercase permits SQL NULL.
type fundingLotsWriteSpec struct {
	table, columns, kinds string
	stamp                 string // x: top xid + native time; z: native time only
}

var fundingLotsWriteSpecs = [...]fundingLotsWriteSpec{
	{"funding_lot_legacy_receipts", "receipt_id epoch_id tenant_id source_kind source_id amount journal_id", "tttttit", ""},
	{"funding_lot_legacy_sandbox_receipts", "receipt_id epoch_id tenant_id capture_id amount journal_id", "ttttit", ""},
	{"funding_lot_legacy_settlements", "settlement_id epoch_id tenant_id reservation_id request_id charged_amount refunded_amount_at_cutover journal_id", "tttttiit", ""},
	{"funding_lot_legacy_approved_refunds", "refund_order_id epoch_id tenant_id settlement_id amount journal_id", "ttttit", ""},
	{"funding_lot_legacy_openings", "id epoch_id tenant_id kind account_id reservation_id amount observed_at", "ttttTTiz", ""},
	{"funding_wallet_legacy_sources", "epoch_id tenant_id kind source_id journal_id journal_operation_id amount request_id reservation_id settlement_id", "ttttttiTTT", ""},
	{"funding_wallet_operations", "id tenant_id epoch_id seq previous_seq previous_id kind source_id journal_id journal_operation_id event_count available_delta held_delta consumed_delta issued_delta", "tttiITttTTinnnn", "x"},
	{"funding_wallet_heads", "tenant_id epoch_id tail_seq tail_operation_id", "ttit", ""},
	{"funding_lots", "id tenant_id origin_kind funding_receipt_id legacy_opening_id legacy_refund_order_id issued_amount currency refund_class fifo_at", "tttTTTittz", "z"},
	{"funding_lot_events", "id tenant_id lot_id wallet_operation_id wallet_seq kind operation_id journal_id previous_event_id before_revision after_revision before_available before_held before_consumed after_available after_held after_consumed available_delta inference_held_delta consumed_delta issued_delta", "ttttitTTTiiiiiiiinnnn", "x"},
	{"funding_lot_balances", "lot_id tenant_id available_amount inference_held_amount consumed_amount revision last_event_id", "ttiiiit", ""},
	{"funding_reservation_lots", "id tenant_id reservation_id lot_id allocation_index amount", "ttttii", "x"},
	{"funding_lot_runtime_attestations", "singleton epoch_id proof_version mode database_oid schema_oid schema_owner_oid app_role_oid worker_role_oid projector_role_oid predecessor_payment_digest proof_canonical proof_sha256 manifest_digest descriptor_digest created_at", "btvtooooOoTytttz", ""},
	{"funding_lot_epoch", "singleton id schema_version database_oid schema_oid basis_digest manifest_digest descriptor_digest runtime_proof_digest tenant_count receipt_count settlement_count approved_refund_count opening_count legacy_source_count available_total held_total actor created_at", "btvoottttiiiiiinntz", ""},
}

func fundingLotsWriteCast(kind byte) string {
	switch kind {
	case 't', 'T':
		return "text"
	case 'i', 'I':
		return "int8"
	case 'n':
		return "numeric"
	case 'o', 'O':
		return "oid"
	case 'v':
		return "int4"
	case 'b':
		return "bool"
	case 'y':
		return "bytea"
	case 'z':
		return "timestamptz"
	}
	return ""
}

func fundingLotsWriteSQL(schema string, spec fundingLotsWriteSpec) (string, error) {
	columns := strings.Fields(spec.columns)
	if len(columns) == 0 || len(columns) != len(spec.kinds) || spec.stamp != "" && spec.stamp != "x" && spec.stamp != "z" {
		return "", ErrExecution
	}
	names, values, returning := make([]string, len(columns)), make([]string, len(columns)), make([]string, len(columns))
	for i, column := range columns {
		cast := fundingLotsWriteCast(spec.kinds[i])
		if cast == "" {
			return "", ErrExecution
		}
		name := pgx.Identifier{column}.Sanitize()
		names[i], values[i] = name, "$"+strconv.Itoa(i+1)+"::pg_catalog."+cast
		returning[i] = name
		switch spec.kinds[i] {
		case 'i', 'I', 'n', 'o', 'O', 'v':
			returning[i] += "::pg_catalog.text"
		case 'z':
			returning[i] = "pg_catalog.timestamptz_send(" + name + ")"
		}
	}
	if spec.stamp == "x" {
		returning = append(returning, "created_xid::pg_catalog.text")
	}
	if spec.stamp != "" {
		returning = append(returning, "pg_catalog.timestamptz_send(created_at)")
	}
	return "INSERT INTO " + pgx.Identifier{schema, spec.table}.Sanitize() + "(" + strings.Join(names, ",") + ") VALUES(" + strings.Join(values, ",") + ") RETURNING " + strings.Join(returning, ","), nil
}

func fundingLotsWriterContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInput
	}
	if ctx.Err() != nil {
		return ErrTimeout
	}
	return nil
}

// Return only builtin pgx arguments and the exact expected text/native echo.
// A mutable descriptive Plan cannot supply a codec, scanner or driver option.
func fundingLotsWriteValue(kind byte, value any) (any, any, error) {
	if value == nil {
		if kind == 'T' || kind == 'I' || kind == 'O' {
			return nil, nil, nil
		}
		return nil, nil, ErrExecution
	}
	switch kind {
	case 't', 'T', 'i', 'I', 'n':
		s, ok := value.(string)
		if !ok || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return nil, nil, ErrExecution
		}
		if len(s) > 65536 {
			return nil, nil, ErrResource
		}
		if kind == 't' || kind == 'T' {
			return s, s, nil
		}
		if kind == 'n' {
			v, valid := new(big.Int).SetString(s, 10)
			if !valid || v.String() != s {
				return nil, nil, ErrExecution
			}
			return s, s, nil
		}
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil || strconv.FormatInt(v, 10) != s {
			return nil, nil, ErrExecution
		}
		return v, s, nil
	case 'o', 'O':
		v, ok := value.(uint32)
		if !ok || v == 0 {
			return nil, nil, ErrExecution
		}
		return v, strconv.FormatUint(uint64(v), 10), nil
	case 'v':
		v, ok := value.(int32)
		if !ok || v < 1 {
			return nil, nil, ErrExecution
		}
		return v, strconv.FormatInt(int64(v), 10), nil
	case 'b':
		v, ok := value.(bool)
		if !ok || !v {
			return nil, nil, ErrExecution
		}
		return v, v, nil
	case 'y':
		v, ok := value.([]byte)
		if !ok || len(v) == 0 {
			return nil, nil, ErrExecution
		}
		if len(v) > 262144 {
			return nil, nil, ErrResource
		}
		return bytes.Clone(v), bytes.Clone(v), nil
	case 'z':
		v, ok := value.(time.Time)
		if !ok || v.UTC().Year() < -4712 || v.UTC().Year() > 294276 || v.Nanosecond()%1000 != 0 {
			return nil, nil, ErrExecution
		}
		return v, fundinglotverify.WalletLotsTime{Value: v, Valid: true}, nil
	}
	return nil, nil, ErrExecution
}

func fundingLotsNullable(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func (p *fundingLotsPhase) requireFundingLotsWriting(ctx context.Context) error {
	if p == nil || p.snapshot == nil || p.snapshot.gate == nil || ctx == nil {
		return ErrInput
	}
	gs := p.snapshot.gate
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := p.validLocked(ctx); err != nil {
		return err
	}
	if p.stage != fundingLotsWriting || p.purpose != snapshotInit || p.snapshot.mode != snapshotInit || p.pending != nil || p.operation {
		return ErrTransaction
	}
	return nil
}

func (p *fundingLotsPhase) insertFundingLots(ctx context.Context, index int, values ...any) error {
	if err := p.requireFundingLotsWriting(ctx); err != nil {
		return err
	}
	if index < 0 || index >= len(fundingLotsWriteSpecs) {
		return ErrExecution
	}
	spec := fundingLotsWriteSpecs[index]
	if len(values) != len(spec.kinds) {
		return ErrExecution
	}
	sql, err := fundingLotsWriteSQL(p.snapshot.gate.pins.AppSchema.Name, spec)
	if err != nil {
		return err
	}
	arguments, expected := make([]any, len(values)), make([]any, len(values))
	destinations := make([]any, len(values))
	for i, value := range values {
		arguments[i], expected[i], err = fundingLotsWriteValue(spec.kinds[i], value)
		if err != nil {
			return err
		}
		switch spec.kinds[i] {
		case 'b':
			destinations[i] = new(bool)
		case 'y', 'z':
			destinations[i] = new([]byte)
		default:
			destinations[i] = new(pgtype.Text)
		}
	}
	var xid pgtype.Text
	var stamp []byte
	if spec.stamp == "x" {
		destinations = append(destinations, &xid)
	}
	if spec.stamp != "" {
		destinations = append(destinations, &stamp)
	}
	rows, err := p.query(ctx, sql, arguments...)
	if err != nil {
		return err
	}
	defer rows.Close()
	next, err := rows.Next()
	if err != nil {
		return err
	}
	if !next {
		return authorityLeafFailure(ctx, ErrExecution)
	}
	if err = rows.Scan(destinations...); err != nil {
		return err
	}
	for i, want := range expected {
		switch got := destinations[i].(type) {
		case *pgtype.Text:
			if want == nil && got.Valid || want != nil && (!got.Valid || got.String != want.(string)) {
				return ErrExecution
			}
		case *bool:
			if *got != want.(bool) {
				return ErrExecution
			}
		case *[]byte:
			if spec.kinds[i] == 'y' {
				if !bytes.Equal(*got, want.([]byte)) {
					return ErrExecution
				}
			} else {
				instant, e := fundingLotsProofTime(*got)
				if e != nil || !fundingLotsProofTimeEqual(instant, want.(fundinglotverify.WalletLotsTime)) {
					return ErrExecution
				}
			}
		}
	}
	if spec.stamp == "x" {
		v, e := strconv.ParseUint(xid.String, 10, 64)
		if !xid.Valid || e != nil || v == 0 || strconv.FormatUint(v, 10) != xid.String || xid.String != p.snapshot.xid {
			return ErrExecution
		}
	}
	if spec.stamp != "" {
		if _, err = fundingLotsProofTime(stamp); err != nil {
			return ErrExecution
		}
	}
	if next, err = rows.Next(); err != nil {
		return err
	}
	if next {
		return authorityLeafFailure(ctx, ErrExecution)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return p.verify(ctx)
}

func (p *fundingLotsPhase) writeFundingLots(ctx context.Context) error {
	if err := p.requireFundingLotsWriting(ctx); err != nil {
		return err
	}
	if p.canonical == nil || len(p.draft.SettlementLots) != 0 || len(p.draft.RefundLots) != 0 || !p.epochAt.Valid || p.epochAt.Infinity != 0 {
		return ErrExecution
	}
	binding, err := p.canonical.Bindings()
	if err != nil || binding.EpochID != p.epochID || p.draft.Metadata.EpochID != p.epochID || !fundingLotsProofTimeEqual(p.draft.Metadata.EpochAt, p.epochAt) {
		return ErrExecution
	}
	for _, r := range p.draft.LegacyReceipts {
		if err := p.insertFundingLots(ctx, 0, r.ReceiptID, r.EpochID, r.TenantID, r.SourceKind, r.SourceID, r.Amount, r.JournalID); err != nil {
			return err
		}
	}
	for _, r := range p.draft.LegacySandboxReceipts {
		if err := p.insertFundingLots(ctx, 1, r.ReceiptID, r.EpochID, r.TenantID, r.CaptureID, r.Amount, r.JournalID); err != nil {
			return err
		}
	}
	for _, r := range p.draft.LegacySettlements {
		if err := p.insertFundingLots(ctx, 2, r.SettlementID, r.EpochID, r.TenantID, r.ReservationID, r.RequestID, r.ChargedAmount, r.RefundedAmountAtCutover, r.JournalID); err != nil {
			return err
		}
	}
	for _, r := range p.draft.LegacyApprovedRefunds {
		if err := p.insertFundingLots(ctx, 3, r.RefundOrderID, r.EpochID, r.TenantID, r.SettlementID, r.Amount, r.JournalID); err != nil {
			return err
		}
	}
	for _, r := range p.draft.LegacyOpenings {
		if err := p.insertFundingLots(ctx, 4, r.ID, r.EpochID, r.TenantID, r.Kind, fundingLotsNullable(r.AccountID), fundingLotsNullable(r.ReservationID), r.Amount, r.ObservedAt.Value); err != nil {
			return err
		}
	}
	for _, r := range p.draft.LegacySources {
		if err := p.insertFundingLots(ctx, 5, r.EpochID, r.TenantID, r.Kind, r.SourceID, r.JournalID, r.JournalOperationID, r.Amount, fundingLotsNullable(r.RequestID), fundingLotsNullable(r.ReservationID), fundingLotsNullable(r.SettlementID)); err != nil {
			return err
		}
	}
	for _, r := range p.draft.Operations {
		if err := p.insertFundingLots(ctx, 6, r.ID, r.TenantID, r.EpochID, r.Seq, fundingLotsNullable(r.PreviousSeq), fundingLotsNullable(r.PreviousID), r.Kind, r.SourceID, fundingLotsNullable(r.JournalID), fundingLotsNullable(r.JournalOperationID), r.EventCount, r.AvailableDelta, r.HeldDelta, r.ConsumedDelta, r.IssuedDelta); err != nil {
			return err
		}
	}
	for _, r := range p.draft.Heads {
		if err := p.insertFundingLots(ctx, 7, r.TenantID, r.EpochID, r.TailSeq, r.TailOperationID); err != nil {
			return err
		}
	}
	for _, r := range p.draft.Lots {
		if err := p.insertFundingLots(ctx, 8, r.ID, r.TenantID, r.OriginKind, fundingLotsNullable(r.FundingReceiptID), fundingLotsNullable(r.LegacyOpeningID), fundingLotsNullable(r.LegacyRefundOrderID), r.IssuedAmount, r.Currency, r.RefundClass, r.FIFOAt.Value); err != nil {
			return err
		}
	}
	for _, r := range p.draft.Events {
		if err := p.insertFundingLots(ctx, 9, r.ID, r.TenantID, r.LotID, r.WalletOperationID, r.WalletSeq, r.Kind, fundingLotsNullable(r.OperationID), fundingLotsNullable(r.JournalID), fundingLotsNullable(r.PreviousEventID), r.BeforeRevision, r.AfterRevision, r.BeforeAvailable, r.BeforeHeld, r.BeforeConsumed, r.AfterAvailable, r.AfterHeld, r.AfterConsumed, r.AvailableDelta, r.HeldDelta, r.ConsumedDelta, r.IssuedDelta); err != nil {
			return err
		}
	}
	for _, r := range p.draft.Balances {
		if err := p.insertFundingLots(ctx, 10, r.LotID, r.TenantID, r.AvailableAmount, r.InferenceHeldAmount, r.ConsumedAmount, r.Revision, r.LastEventID); err != nil {
			return err
		}
	}
	for _, r := range p.draft.ReservationLots {
		if err := p.insertFundingLots(ctx, 11, r.ID, r.TenantID, r.ReservationID, r.LotID, r.AllocationIndex, r.Amount); err != nil {
			return err
		}
	}
	var worker any
	if binding.Sandbox != nil {
		worker = binding.Sandbox.WorkerRole.OID
	}
	m := p.draft.Metadata
	if err = p.insertFundingLots(ctx, 12, true, p.epochID, int32(2), binding.Mode, binding.Database.OID, binding.AppSchema.OID, binding.AppSchema.ObjectOwner.OID, binding.AppRole.OID, worker, binding.ProjectorRole.OID, fundingLotsNullable(binding.PredecessorPaymentDigest), p.canonical.Bytes(), p.canonical.SHA256(), binding.ManifestDigest, binding.BasisDescriptorDigest, p.epochAt.Value); err != nil {
		return err
	}
	if err = p.insertFundingLots(ctx, 13, true, p.epochID, int32(1), binding.Database.OID, binding.AppSchema.OID, binding.BasisDigest, binding.ManifestDigest, binding.BasisDescriptorDigest, p.canonical.SHA256(), strconv.FormatUint(m.TenantCount, 10), strconv.FormatUint(m.ReceiptCount, 10), strconv.FormatUint(m.SettlementCount, 10), strconv.FormatUint(m.ApprovedRefundCount, 10), strconv.FormatUint(m.OpeningCount, 10), strconv.FormatUint(m.LegacySourceCount, 10), m.AvailableTotal, m.HeldTotal, "offline-maintenance", p.epochAt.Value); err != nil {
		return err
	}
	if err = p.verify(ctx); err != nil {
		return err
	}
	if _, err = p.snapshot.rawTx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE", pgx.QueryExecModeSimpleProtocol); err != nil {
		return authorityLeafFailure(ctx, ErrExecution)
	}
	return p.verify(ctx)
}

// Only the generated C0 projection is measured here. The phase adds its own
// actual complete-68 source bytes and fixed 1MiB metadata reservation. This
// helper is descriptive and cannot authorize a writer or accept a report.
func fundingLotsGeneratedBytes(ctx context.Context, plan *fundinglotinit.Plan) (uint64, error) {
	if err := fundingLotsWriterContext(ctx); err != nil {
		return 0, err
	}
	if plan == nil || len(plan.SettlementLots) != 0 || len(plan.RefundLots) != 0 {
		return 0, ErrInput
	}
	groups := []struct {
		rows  any
		stamp uint64
	}{
		{plan.LegacyReceipts, 0}, {plan.LegacySandboxReceipts, 0}, {plan.LegacySettlements, 0},
		{plan.LegacyApprovedRefunds, 0}, {plan.LegacyOpenings, 0}, {plan.LegacySources, 0},
		{plan.Operations, 52}, {plan.Heads, 0}, {plan.Lots, 32}, {plan.Events, 52},
		{plan.Balances, 0}, {plan.ReservationLots, 52},
	}
	var generatedRows uint64
	for _, group := range groups {
		n := uint64(reflect.ValueOf(group.rows).Len())
		if n > 100000-generatedRows {
			return 0, ErrResource
		}
		generatedRows += n
	}
	var total uint64
	for _, group := range groups {
		rows := reflect.ValueOf(group.rows)
		for i := 0; i < rows.Len(); i++ {
			if err := fundingLotsGeneratedValue(ctx, rows.Index(i), &total); err != nil {
				return 0, err
			}
			if err := fundingLotsGeneratedAdd(&total, group.stamp); err != nil {
				return 0, err
			}
			if err := fundingLotsWriterContext(ctx); err != nil {
				return 0, err
			}
		}
	}
	if err := fundingLotsWriterContext(ctx); err != nil {
		return 0, err
	}
	return total, nil
}

var fundingLotsTimeType = reflect.TypeOf(fundinglotverify.WalletLotsTime{})

func fundingLotsGeneratedValue(ctx context.Context, value reflect.Value, total *uint64) error {
	if err := fundingLotsWriterContext(ctx); err != nil {
		return err
	}
	if value.Type() == fundingLotsTimeType {
		return fundingLotsGeneratedAdd(total, 32)
	}
	switch value.Kind() {
	case reflect.String:
		if value.Len() > 65536 {
			return ErrResource
		}
		return fundingLotsGeneratedAdd(total, uint64(value.Len()))
	case reflect.Pointer:
		if !value.IsNil() {
			return fundingLotsGeneratedValue(ctx, value.Elem(), total)
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if err := fundingLotsGeneratedValue(ctx, value.Field(i), total); err != nil {
				return err
			}
		}
	default:
		return ErrExecution
	}
	return nil
}

func fundingLotsGeneratedAdd(total *uint64, n uint64) error {
	const limit = uint64(16 << 20)
	if n > limit || *total > limit-n {
		return ErrResource
	}
	*total += n
	return nil
}
