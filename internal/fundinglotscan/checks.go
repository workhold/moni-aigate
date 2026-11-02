package fundinglotscan

import (
	"errors"
	"strconv"
	"strings"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func (s *session) run(index int, qualified bool, fn func() error) error {
	if s.ctx.Err() != nil {
		return contextFailure(s.ctx)
	}
	if !qualified {
		s.report.Algorithms[index] = "input_mismatch"
		return nil
	}
	if err := fn(); err != nil {
		if s.ctx.Err() != nil {
			return contextFailure(s.ctx)
		}
		for _, resource := range []error{
			fundinglotverify.ErrLedgerRowsResource, fundinglotverify.ErrFundingSourceRowsResource,
			fundinglotverify.ErrFundingSourceGraphRowsResource, fundinglotverify.ErrCustomerKeyGraphRowsResource,
			fundinglotverify.ErrProcurementEvidenceGraphRowsResource, fundinglotverify.ErrPeriodicBudgetGraphRowsResource,
			fundinglotverify.ErrSandboxSourceGraphRowsResource,
		} {
			if errors.Is(err, resource) {
				return ErrResource
			}
		}
		return ErrExecution
	}
	s.report.Algorithms[index] = "executed"
	return nil
}

func (s *session) check() error {
	ledger, qualified, err := projectLedger(s)
	if err != nil {
		return err
	}
	if err = s.run(0, qualified, func() error {
		v, e := fundinglotverify.CheckLedgerRows(s.ctx, ledger)
		if e != nil {
			return e
		}
		if v.Scope != "supplied_ledger_rows" {
			return ErrExecution
		}
		s.report.Ledger = v
		return nil
	}); err != nil {
		return err
	}
	links, qualified, err := projectLinks(s)
	if err != nil {
		return err
	}
	if err = s.run(1, qualified, func() error {
		v, e := fundinglotverify.CheckFundingSourceLinks(s.ctx, links)
		if e != nil {
			return e
		}
		if v.Scope != "manual_voucher_receipt_links" {
			return ErrExecution
		}
		s.report.Links = v
		return nil
	}); err != nil {
		return err
	}
	funding, qualified, err := projectFunding(s)
	if err != nil {
		return err
	}
	if err = s.run(2, qualified, func() error {
		v, e := fundinglotverify.CheckFundingSourceGraph(s.ctx, funding)
		if e != nil {
			return e
		}
		if v.Scope != "manual_voucher_funding_graph_v1" {
			return ErrExecution
		}
		s.report.Funding = v
		return nil
	}); err != nil {
		return err
	}
	customer, qualified, err := projectCustomer(s)
	if err != nil {
		return err
	}
	if err = s.run(3, qualified, func() error {
		v, e := fundinglotverify.CheckCustomerKeyGraph(s.ctx, customer)
		if e != nil {
			return e
		}
		if v.Scope != "customer_key_financial_graph_v1" {
			return ErrExecution
		}
		s.report.Customer = v
		return nil
	}); err != nil {
		return err
	}
	procurement, qualified, err := projectProcurement(s)
	if err != nil {
		return err
	}
	if err = s.run(4, qualified, func() error {
		v, e := fundinglotverify.CheckProcurementEvidenceGraph(s.ctx, procurement)
		if e != nil {
			return e
		}
		if v.Scope != "procurement_domain_evidence_graph_v1" {
			return ErrExecution
		}
		s.report.Procurement = v
		return nil
	}); err != nil {
		return err
	}
	periodic, qualified, err := projectPeriodic(s)
	if err != nil {
		return err
	}
	if err = s.run(5, qualified, func() error {
		v, e := fundinglotverify.CheckPeriodicBudgetGraph(s.ctx, periodic)
		if e != nil {
			return e
		}
		if v.Scope != "periodic_budget_graph_v1" {
			return ErrExecution
		}
		s.report.Periodic = v
		return nil
	}); err != nil {
		return err
	}
	if s.mode == ModeOrdinary {
		if err = s.ordinary(); err != nil {
			return err
		}
		if s.report.OrdinaryBoundaryMismatches == 0 {
			s.report.Algorithms[6] = "not_applicable"
		} else {
			s.report.Algorithms[6] = "input_mismatch"
		}
	} else {
		sandbox, ok, e := projectSandbox(s)
		if e != nil {
			return e
		}
		if err = s.run(6, ok, func() error {
			v, e := fundinglotverify.CheckSandboxSourceGraph(s.ctx, sandbox)
			if e != nil {
				return e
			}
			if v.Scope != "sandbox_stored_verified_source_graph_v1" {
				return ErrExecution
			}
			s.report.Sandbox = v
			return nil
		}); err != nil {
			return err
		}
	}
	if err = s.journalMetadata(); err != nil {
		return err
	}
	complete := len(s.rejected) == 0
	mismatch := !complete || s.report.JournalMetadataMismatches != 0 || s.report.OrdinaryBoundaryMismatches != 0
	for i, state := range s.report.Algorithms {
		if state != "executed" && !(i == 6 && state == "not_applicable" && s.mode == ModeOrdinary) {
			complete = false
			mismatch = true
		}
	}
	if s.report.Algorithms[0] == "executed" && !s.report.Ledger.RowsConsistent {
		mismatch = true
	}
	if s.report.Algorithms[1] == "executed" && (!s.report.Links.ScopedLinksConsistent || s.report.Links.UnknownReceiptKinds != 0) {
		mismatch = true
	}
	if s.report.Algorithms[2] == "executed" && !s.report.Funding.ScopedGraphConsistent {
		mismatch = true
	}
	if s.report.Algorithms[3] == "executed" && !s.report.Customer.Balanced {
		mismatch = true
	}
	if s.report.Algorithms[4] == "executed" {
		if s.report.Procurement.Status == "mismatch" {
			mismatch = true
		}
		complete = complete && s.report.Procurement.CoverageComplete
	}
	if s.report.Algorithms[5] == "executed" {
		if s.report.Periodic.Status == "mismatch" {
			mismatch = true
		}
		complete = complete && s.report.Periodic.CoverageComplete
	}
	if s.report.Algorithms[6] == "executed" {
		if s.report.Sandbox.Status == "mismatch" {
			mismatch = true
		}
		complete = complete && s.report.Sandbox.Unproven == (fundinglotverify.SandboxSourceGraphUnproven{})
	} else if s.report.Links.UncoveredSandboxReceipts != 0 || s.report.Funding.UncoveredSandboxReceipts != 0 {
		mismatch = true
		complete = false
	}
	s.report.CoverageComplete = complete
	s.report.Status = "consistent"
	if mismatch {
		s.report.Status = "mismatch"
	} else if !complete {
		s.report.Status = "indeterminate"
	}
	return nil
}

func (s *session) journalMetadata() error {
	t := &s.tables[s.index["journals"]]
	xi, ti := t.spec.index["created_txid"], t.spec.index["created_at"]
	for i, row := range t.rows {
		if i&255 == 0 && s.ctx.Err() != nil {
			return contextFailure(s.ctx)
		}
		x, stamp := row[xi], row[ti]
		n, e := strconv.ParseInt(x.text, 10, 64)
		v := stamp.instant
		if !x.valid || e != nil || n <= 0 || strconv.FormatInt(n, 10) != x.text || !v.Valid || v.InfinityModifier != 0 || v.Time.IsZero() || v.Time.UTC().Year() < 1 || v.Time.UTC().Year() > 9999 || v.Time.Nanosecond()%1000 != 0 {
			s.report.JournalMetadataMismatches++
		}
	}
	return nil
}

func (s *session) ordinary() error {
	for _, b := range sandboxBindings()[:23] {
		t := &s.tables[s.index[b.table]]
		if b.table == "payment_policy" {
			if len(t.rows) != 1 {
				s.report.OrdinaryBoundaryMismatches++
				continue
			}
			r := t.rows[0]
			single, enabled, revision := r[t.spec.index["singleton"]], r[t.spec.index["enabled"]], r[t.spec.index["revision"]]
			if !single.valid || !single.boolean || !enabled.valid || enabled.boolean || !revision.valid || revision.text != "1" {
				s.report.OrdinaryBoundaryMismatches++
			}
		} else {
			s.report.OrdinaryBoundaryMismatches += uint64(len(t.rows))
		}
	}
	for _, item := range []struct {
		table, column, value string
		prefix               bool
	}{
		{"commerce_commands", "kind", "payment.", true}, {"commerce_operation_receipts", "kind", "payment.", true},
		{"audit_events", "action", "payment.", true}, {"funding_receipts", "source_kind", "sandbox_capture", false},
		{"journals", "kind", "sandbox_capture", false}, {"ledger_accounts", "kind", "SANDBOX_FUNDING_CLEARING", false},
	} {
		t := &s.tables[s.index[item.table]]
		ci := t.spec.index[item.column]
		for i, row := range t.rows {
			if i&255 == 0 && s.ctx.Err() != nil {
				return contextFailure(s.ctx)
			}
			c := row[ci]
			if c.valid && (item.prefix && strings.HasPrefix(c.text, item.value) || !item.prefix && c.text == item.value) {
				s.report.OrdinaryBoundaryMismatches++
			}
		}
	}
	return nil
}
