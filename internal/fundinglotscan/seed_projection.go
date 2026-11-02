package fundinglotscan

import (
	"context"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// LegacySeedRows contains ordinary supplied data, never maintenance authority.
type LegacySeedRows = fundinglotverify.LegacySeedRows

// ProjectLegacySeed copies the nine legacy business projections from a
// completed original 68-table Read. It performs no query and authenticates no
// database, snapshot, initialization plan, or right to write funds.
func ProjectLegacySeed(ctx context.Context, facts Facts) (LegacySeedRows, error) {
	if ctx == nil {
		return LegacySeedRows{}, ErrInput
	}
	if ctx.Err() != nil {
		return LegacySeedRows{}, contextFailure(ctx)
	}
	s := facts.state
	if s == nil || !legacySeedReportQualified(s) {
		return LegacySeedRows{}, ErrInput
	}
	if err := legacySeedRaw(ctx, s); err != nil {
		return LegacySeedRows{}, err
	}
	if err := legacySeedScanned(ctx, s); err != nil {
		return LegacySeedRows{}, err
	}
	// projectInto may record qualification failures. Its fresh view has no
	// Query, old context, or shared rejected map, and cannot mutate the facts.
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

func legacySeedBindings() []binding {
	return []binding{
		bind[fundinglotverify.LegacySeedTenantRow]("tenants", "id", "id"),
		bind[fundinglotverify.LedgerAccountRow]("ledger_accounts", "id", "id tenant_id kind balance"),
		bind[fundinglotverify.WalletLotsJournalRow]("journals", "id", "id tenant_id operation_id kind currency created_txid reference actor created_at"),
		bind[fundinglotverify.LedgerLineRow]("journal_lines", "id", "id journal_id account_id signed_delta"),
		bind[fundinglotverify.WalletLotsReceiptRow]("funding_receipts", "id", "id tenant_id source_kind source_id amount currency journal_id created_at"),
		bind[fundinglotverify.WalletLotsRequestRow]("request_records", "id", "id tenant_id owner_id billing_status"),
		bind[fundinglotverify.WalletLotsReservationRow]("reservations", "id", "id tenant_id request_id amount status"),
		bind[fundinglotverify.WalletLotsSettlementRow]("settlements", "id", "id tenant_id request_id charged_amount refunded_amount status reason journal_id"),
		bind[fundinglotverify.WalletLotsRefundRow]("refund_orders", "id", "id tenant_id settlement_id amount state requester approver journal_id"),
	}
}

func legacySeedReportQualified(s *session) bool {
	r := s.report
	if r.Scope != "complete_business_source_graphs_v1" || r.Mode != s.mode || s.mode != ModeOrdinary && s.mode != ModeSandbox ||
		r.Status != "consistent" || !r.PhysicalEOFComplete || r.ScannedTables != tableCount || !r.CoverageComplete ||
		r.InputMismatchFields != 0 || r.JournalMetadataMismatches != 0 || r.OrdinaryBoundaryMismatches != 0 || len(s.rejected) != 0 {
		return false
	}
	for i := 0; i < 6; i++ {
		if r.Algorithms[i] != "executed" {
			return false
		}
	}
	if r.Ledger.Scope != "supplied_ledger_rows" || !r.Ledger.RowsConsistent || r.Ledger.Mismatches != (fundinglotverify.LedgerRowMismatches{}) ||
		r.Links.Scope != "manual_voucher_receipt_links" || !r.Links.ScopedLinksConsistent || r.Links.UnknownReceiptKinds != 0 || r.Links.Mismatches != (fundinglotverify.FundingSourceLinkMismatches{}) ||
		r.Funding.Scope != "manual_voucher_funding_graph_v1" || !r.Funding.ScopedGraphConsistent || r.Funding.Mismatches != (fundinglotverify.FundingSourceGraphMismatches{}) ||
		r.Customer.Scope != "customer_key_financial_graph_v1" || !r.Customer.Balanced || r.Customer.Mismatches != (fundinglotverify.CustomerKeyGraphMismatches{}) ||
		r.Procurement.Scope != "procurement_domain_evidence_graph_v1" || r.Procurement.Status != "balanced" || !r.Procurement.Balanced || !r.Procurement.CoverageComplete ||
		r.Procurement.Structural != (fundinglotverify.ProcurementEvidenceGraphStructural{}) || r.Procurement.Mismatches != (fundinglotverify.ProcurementEvidenceGraphMismatches{}) || r.Procurement.Unproven != (fundinglotverify.ProcurementEvidenceGraphUnproven{}) ||
		r.Periodic.Scope != "periodic_budget_graph_v1" || r.Periodic.Status != "balanced" || !r.Periodic.Balanced || !r.Periodic.CoverageComplete ||
		r.Periodic.Mismatches != (fundinglotverify.PeriodicBudgetGraphMismatches{}) || r.Periodic.Unproven != (fundinglotverify.PeriodicBudgetGraphGaps{}) {
		return false
	}
	if s.mode == ModeOrdinary {
		return r.Algorithms[6] == "not_applicable" && r.Sandbox == (fundinglotverify.SandboxSourceGraphCheck{}) && r.Links.UncoveredSandboxReceipts == 0 && r.Funding.UncoveredSandboxReceipts == 0
	}
	return r.Algorithms[6] == "executed" && r.Sandbox.Scope == "sandbox_stored_verified_source_graph_v1" && r.Sandbox.Status == "consistent" && r.Sandbox.ScopedGraphConsistent &&
		r.Sandbox.Mismatches == (fundinglotverify.SandboxSourceGraphMismatches{}) && r.Sandbox.Unproven == (fundinglotverify.SandboxSourceGraphUnproven{}) &&
		r.Links.UncoveredSandboxReceipts >= 0 && uint64(r.Links.UncoveredSandboxReceipts) == r.Funding.UncoveredSandboxReceipts
}

// Recheck the fixed registry and its logical transport budgets before allocating
// output slices. These are data budgets, not bounds on process RSS.
func legacySeedRaw(ctx context.Context, s *session) error {
	want, index, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings())
	if err != nil {
		return ErrExecution
	}
	if len(s.tables) != tableCount {
		return ErrInput
	}
	if !reflect.DeepEqual(s.index, index) {
		return ErrExecution
	}
	budget := &session{}
	for i, table := range s.tables {
		if ctx.Err() != nil {
			return contextFailure(ctx)
		}
		if !table.eof {
			return ErrInput
		}
		if !reflect.DeepEqual(table.spec, want[i].spec) {
			return ErrExecution
		}
		var previous []any
		for _, row := range table.rows {
			if ctx.Err() != nil {
				return contextFailure(ctx)
			}
			if len(row) != len(table.spec.columns) {
				return ErrExecution
			}
			for ci, c := range row {
				if c.kind != table.spec.columns[ci].kind || c.kind == timeKind && c.valid != c.instant.Valid {
					return ErrExecution
				}
			}
			if err := budget.addTableRow(table.spec, row); err != nil {
				return err
			}
			for _, c := range row {
				if c.kind == textKind && c.valid && (!utf8.ValidString(c.text) || strings.ContainsRune(c.text, 0)) {
					return ErrInput
				}
			}
			cursor, err := rowCursor(table.spec, row)
			if err != nil || previous != nil && !cursorAfter(cursor, previous, table.spec.keys) {
				return ErrExecution
			}
			previous = cursor
		}
	}
	if s.rows != budget.rows || s.bytes != budget.bytes || s.report.SourceRows != uint64(budget.rows) || s.report.SourceBytes != uint64(budget.bytes) {
		return ErrExecution
	}
	return nil
}

func legacySeedCounts(s *session, counts any, tables []string) bool {
	v := reflect.ValueOf(counts)
	if v.Kind() != reflect.Struct || v.NumField() != len(tables) {
		return false
	}
	for i, name := range tables {
		field := v.Field(i)
		var n uint64
		switch field.Kind() {
		case reflect.Int64:
			if field.Int() < 0 {
				return false
			}
			n = uint64(field.Int())
		case reflect.Uint64:
			n = field.Uint()
		default:
			return false
		}
		if n != uint64(len(s.tables[s.index[name]].rows)) {
			return false
		}
	}
	return true
}

func legacySeedBindingTables(b []binding) []string {
	tables := make([]string, len(b))
	for i := range b {
		tables[i] = b[i].table
	}
	return tables
}

func legacySeedScanned(ctx context.Context, s *session) error {
	r := s.report
	if !legacySeedCounts(s, r.Ledger.Scanned, []string{"ledger_accounts", "journals", "journal_lines"}) ||
		!legacySeedCounts(s, r.Links.Scanned, []string{"funding_receipts", "credit_orders", "voucher_redemptions"}) ||
		!legacySeedCounts(s, r.Funding.Scanned, []string{"users", "tenants", "ledger_accounts", "journals", "journal_lines", "audit_events", "projection_outbox", "funding_receipts", "credit_orders", "voucher_redemptions", "voucher_batches", "vouchers", "voucher_revocations", "voucher_revocation_decisions", "commerce_commands", "commerce_operation_receipts"}) ||
		!legacySeedCounts(s, r.Customer.Scanned, []string{"tenants", "users", "api_keys", "request_records", "reservations", "settlements", "refund_orders", "ledger_accounts", "journals", "journal_lines"}) ||
		!legacySeedCounts(s, r.Procurement.Scanned, legacySeedBindingTables(procurementBindings())) ||
		!legacySeedCounts(s, r.Periodic.Scanned, legacySeedBindingTables(periodicBindings())) {
		return ErrInput
	}
	if s.mode == ModeSandbox {
		if !legacySeedCounts(s, r.Sandbox.Scanned, legacySeedBindingTables(sandboxBindings())) {
			return ErrInput
		}
		var sandbox uint64
		t := s.tables[s.index["funding_receipts"]]
		for _, row := range t.rows {
			if ctx.Err() != nil {
				return contextFailure(ctx)
			}
			if row[t.spec.index["source_kind"]].text == "sandbox_capture" {
				sandbox++
			}
		}
		if r.Funding.UncoveredSandboxReceipts != sandbox {
			return ErrInput
		}
	}
	return nil
}

// projectInto already owns every slice and nullable pointer. Clone strings as
// well, while treating native time envelopes as scalar leaves.
func cloneLegacySeedStrings(ctx context.Context, v reflect.Value) error {
	if v.Type() == budgetTimeType || v.Type() == goTimeType {
		return nil
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if err := cloneLegacySeedStrings(ctx, v.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if ctx.Err() != nil {
				return contextFailure(ctx)
			}
			if err := cloneLegacySeedStrings(ctx, v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Pointer:
		if !v.IsNil() {
			return cloneLegacySeedStrings(ctx, v.Elem())
		}
	case reflect.String:
		v.SetString(strings.Clone(v.String()))
	}
	return nil
}
