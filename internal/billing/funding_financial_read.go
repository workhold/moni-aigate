package billing

import (
	"context"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// FundingFinancialFacts owns the one fixed financial graph. Local summaries
// carry no maintenance, full-source, cutover, or financial-write authority.
type FundingFinancialFacts struct{ snapshot *fundingFinancialSnapshot }

type fundingFinancialSnapshot struct {
	ledger        fundingLedgerSnapshot
	sources       fundinglotverify.FundingSourceGraphRows
	customer      fundinglotverify.CustomerKeyGraphRows
	links         fundinglotverify.FundingSourceLinksCheck
	sourceCheck   fundinglotverify.FundingSourceGraphCheck
	customerCheck fundinglotverify.CustomerKeyGraphCheck
}

func (FundingFinancialFacts) String() string   { return "funding_financial_facts_redacted" }
func (FundingFinancialFacts) GoString() string { return "funding_financial_facts_redacted" }
func (FundingFinancialFacts) MarshalJSON() ([]byte, error) {
	return []byte(`{"funding_financial_facts":"redacted"}`), nil
}
func (f FundingFinancialFacts) LedgerSummary() fundinglotverify.LedgerRowsCheck {
	if f.snapshot == nil {
		return fundinglotverify.LedgerRowsCheck{}
	}
	return f.snapshot.ledger.check
}
func (f FundingFinancialFacts) SourceLinksSummary() fundinglotverify.FundingSourceLinksCheck {
	if f.snapshot == nil {
		return fundinglotverify.FundingSourceLinksCheck{}
	}
	return f.snapshot.links
}
func (f FundingFinancialFacts) SourceGraphSummary() fundinglotverify.FundingSourceGraphCheck {
	if f.snapshot == nil {
		return fundinglotverify.FundingSourceGraphCheck{}
	}
	return f.snapshot.sourceCheck
}
func (f FundingFinancialFacts) CustomerKeySummary() fundinglotverify.CustomerKeyGraphCheck {
	if f.snapshot == nil {
		return fundinglotverify.CustomerKeyGraphCheck{}
	}
	return f.snapshot.customerCheck
}

var (
	ErrFundingFinancialFactsInput     = fundingSourceReadInput
	ErrFundingFinancialFactsResource  = fundingSourceReadResource
	ErrFundingFinancialFactsCanceled  = fundingSourceReadCanceled
	ErrFundingFinancialFactsTimeout   = fundingSourceReadTimeout
	ErrFundingFinancialFactsExecution = fundingSourceReadExecution
)

type fundingFinancialTable uint8

const (
	financialUsers fundingFinancialTable = iota
	financialTenants
	financialAccounts
	financialJournals
	financialLines
	financialCredits
	financialReceipts
	financialBatches
	financialVouchers
	financialRevocations
	financialDecisions
	financialRedemptions
	financialCommands
	financialCommerceReceipts
	financialAudits
	financialOutbox
	financialKeys
	financialRequests
	financialReservations
	financialSettlements
	financialRefunds
	financialTableCount
)

type fundingFinancialCompletion struct {
	eof  bool
	rows uint64
}

// No public constructor, callback, completion bit, or reusable session escapes.
type fundingFinancialSession struct {
	ctx        context.Context
	query      fundingSourceQuery
	schema     string
	budget     fundingSourceReadBudget
	completed  [financialTableCount]fundingFinancialCompletion
	algorithms [4]bool
	graph      fundingFinancialSnapshot
}

func fundingFinancialNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// The adapter observes cancellation during legacy paging without changing the
// legacy algorithm, cursor convention, SQL, error identities or public wrapper.
type fundingFinancialQuery struct {
	ctx   context.Context
	query fundingSourceQuery
}
type fundingFinancialRows struct {
	ctx  context.Context
	rows fundingSourceRows
}

func (q fundingFinancialQuery) Query(sql string, args ...any) (fundingSourceRows, error) {
	if q.ctx.Err() != nil {
		return nil, fundingSourceReadFailure(q.ctx)
	}
	rows, err := q.query.Query(sql, args...)
	if err != nil || fundingFinancialNil(rows) {
		return nil, fundingSourceReadFailure(q.ctx)
	}
	return &fundingFinancialRows{ctx: q.ctx, rows: rows}, nil
}
func (r *fundingFinancialRows) Next() (bool, error) {
	if r.ctx.Err() != nil {
		return false, fundingSourceReadFailure(r.ctx)
	}
	return r.rows.Next()
}
func (r *fundingFinancialRows) Scan(dest ...any) error {
	if r.ctx.Err() != nil {
		return fundingSourceReadFailure(r.ctx)
	}
	return r.rows.Scan(dest...)
}
func (r *fundingFinancialRows) Err() error   { return r.rows.Err() }
func (r *fundingFinancialRows) Close() error { return r.rows.Close() }

type fundingFinancialLedgerQuery struct{ query fundingSourceQuery }

func (q fundingFinancialLedgerQuery) Query(sql string, args ...any) (fundingLedgerRows, error) {
	return q.query.Query(sql, args...)
}

func fundingFinancialFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fundingSourceReadFailure(ctx)
	}
	switch err {
	case fundingSourceReadResource, fundingLedgerReadResource,
		fundinglotverify.ErrLedgerRowsResource, fundinglotverify.ErrFundingSourceRowsResource,
		fundinglotverify.ErrFundingSourceGraphRowsResource, fundinglotverify.ErrCustomerKeyGraphRowsResource:
		return fundingSourceReadResource
	default:
		return fundingSourceReadExecution
	}
}

func fundingFinancialSQL(schema, table, key string, columns ...string) string {
	return "SELECT " + strings.Join(columns, ",") + " FROM " + pgx.Identifier{schema, table}.Sanitize() +
		" WHERE NOT $1::pg_catalog.bool OR " + key + " COLLATE pg_catalog.\"C\" OPERATOR(pg_catalog.>) $2::pg_catalog.text COLLATE pg_catalog.\"C\"" +
		" ORDER BY " + key + " COLLATE pg_catalog.\"C\" USING OPERATOR(pg_catalog.<) LIMIT 128"
}

func (s *fundingFinancialSession) table(table fundingFinancialTable, name, key string, columns []string, scan func(fundingSourceRows) (string, []string, func(), error)) error {
	if table >= financialTableCount || s.completed[table].eof {
		return fundingSourceReadExecution
	}
	before := s.budget.rows
	if err := readFundingSourcePages(s.ctx, s.query, fundingFinancialSQL(s.schema, name, key, columns...), scan, &s.budget); err != nil {
		return err
	}
	if s.ctx.Err() != nil {
		return fundingSourceReadFailure(s.ctx)
	}
	s.completed[table] = fundingFinancialCompletion{eof: true, rows: uint64(s.budget.rows - before)}
	return nil
}

// ReadFundingFinancialFacts runs all 21 fixed readers, then four independent
// cores on the owned raw union. Fake queries can only produce local facts.
func ReadFundingFinancialFacts(ctx context.Context, query FundingSourceFactQuery, schema string) (FundingFinancialFacts, error) {
	if ctx == nil || fundingFinancialNil(query) || !validFundingSourceSchema(schema) {
		return FundingFinancialFacts{}, fundingSourceReadInput
	}
	if ctx.Err() != nil {
		return FundingFinancialFacts{}, fundingSourceReadFailure(ctx)
	}
	session := fundingFinancialSession{ctx: ctx, query: fundingFinancialQuery{ctx: ctx, query: query}, schema: schema}
	if err := session.read(); err != nil {
		return FundingFinancialFacts{}, fundingFinancialFailure(ctx, err)
	}
	if err := session.check(); err != nil {
		return FundingFinancialFacts{}, fundingFinancialFailure(ctx, err)
	}
	var total uint64
	for _, done := range session.completed {
		if !done.eof {
			return FundingFinancialFacts{}, fundingSourceReadExecution
		}
		total += done.rows
	}
	if total != uint64(session.budget.rows) {
		return FundingFinancialFacts{}, fundingSourceReadExecution
	}
	for _, done := range session.algorithms {
		if !done {
			return FundingFinancialFacts{}, fundingSourceReadExecution
		}
	}
	if ctx.Err() != nil {
		return FundingFinancialFacts{}, fundingSourceReadFailure(ctx)
	}
	return FundingFinancialFacts{snapshot: &session.graph}, nil
}

func (s *fundingFinancialSession) read() error {
	if err := s.readUsers(); err != nil {
		return err
	}
	if err := s.readTenants(); err != nil {
		return err
	}
	ledger, err := readFundingLedgerRows(s.ctx, fundingFinancialLedgerQuery{query: s.query}, s.schema, &s.budget)
	if err != nil {
		return err
	}
	if s.ctx.Err() != nil {
		return fundingSourceReadFailure(s.ctx)
	}
	s.graph.ledger = ledger
	s.completed[financialAccounts] = fundingFinancialCompletion{eof: true, rows: uint64(len(ledger.rows.Accounts))}
	s.completed[financialJournals] = fundingFinancialCompletion{eof: true, rows: uint64(len(ledger.rows.Journals))}
	s.completed[financialLines] = fundingFinancialCompletion{eof: true, rows: uint64(len(ledger.rows.Lines))}
	if err := s.readCredits(); err != nil {
		return err
	}
	if err := s.readReceipts(); err != nil {
		return err
	}
	if err := s.readBatches(); err != nil {
		return err
	}
	if err := s.readVouchers(); err != nil {
		return err
	}
	if err := s.readRevocations(); err != nil {
		return err
	}
	if err := s.readDecisions(); err != nil {
		return err
	}
	if err := s.readRedemptions(); err != nil {
		return err
	}
	if err := s.readCommands(); err != nil {
		return err
	}
	if err := s.readCommerceReceipts(); err != nil {
		return err
	}
	if err := s.readAudits(); err != nil {
		return err
	}
	if err := s.readOutbox(); err != nil {
		return err
	}
	if err := s.readKeys(); err != nil {
		return err
	}
	if err := s.readRequests(); err != nil {
		return err
	}
	if err := s.readReservations(); err != nil {
		return err
	}
	if err := s.readSettlements(); err != nil {
		return err
	}
	if err := s.readRefunds(); err != nil {
		return err
	}
	return nil
}

func (s *fundingFinancialSession) readUsers() error {
	return s.table(financialUsers, "users", "id", []string{"id"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementApprovalUserRow
		if err := rows.Scan(&row.ID); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID}
		return row.ID, fields, func() { s.graph.sources.Users = append(s.graph.sources.Users, row) }, nil
	})
}

func (s *fundingFinancialSession) readTenants() error {
	return s.table(financialTenants, "tenants", "id", []string{"id"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAttemptTenantRow
		if err := rows.Scan(&row.ID); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID}
		return row.ID, fields, func() { s.graph.sources.Tenants = append(s.graph.sources.Tenants, row) }, nil
	})
}

func (s *fundingFinancialSession) readCredits() error {
	return s.table(financialCredits, "credit_orders", "id", []string{"id", "tenant_id", "business_ref", "amount::pg_catalog.text", "state", "requester", "approver", "proof_ref", "journal_id", "created_at"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingGraphCreditRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.BusinessRef, &row.Amount, &row.State, &row.Requester, &row.Approver, &row.ProofRef, &row.JournalID, &row.CreatedAt); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.TenantID, row.BusinessRef, row.Amount, row.State, row.Requester, row.ProofRef}
		if row.Approver != nil {
			fields = append(fields, *row.Approver)
		}
		if row.JournalID != nil {
			fields = append(fields, *row.JournalID)
		}
		return row.ID, fields, func() { s.graph.sources.Credits = append(s.graph.sources.Credits, row) }, nil
	})
}

func (s *fundingFinancialSession) readReceipts() error {
	return s.table(financialReceipts, "funding_receipts", "id", []string{"id", "tenant_id", "source_kind", "source_id", "amount::pg_catalog.text", "currency", "journal_id", "created_at"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingGraphReceiptRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.SourceKind, &row.SourceID, &row.Amount, &row.Currency, &row.JournalID, &row.CreatedAt); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.TenantID, row.SourceKind, row.SourceID, row.Amount, row.Currency, row.JournalID}
		return row.ID, fields, func() { s.graph.sources.Receipts = append(s.graph.sources.Receipts, row) }, nil
	})
}

func (s *fundingFinancialSession) readBatches() error {
	return s.table(financialBatches, "voucher_batches", "id", []string{"id", "state", "face_amount::pg_catalog.text", "count::pg_catalog.text", "expires_at", "purpose_ref", "reason", "maker", "approver", "activated_at", "terminal_actor", "terminal_at", "revision::pg_catalog.text", "redeemed_count::pg_catalog.text", "created_at"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingGraphBatchRow
		if err := rows.Scan(&row.ID, &row.State, &row.FaceAmount, &row.Count, &row.ExpiresAt, &row.PurposeRef, &row.Reason, &row.Maker, &row.Approver, &row.ActivatedAt, &row.TerminalActor, &row.TerminalAt, &row.Revision, &row.RedeemedCount, &row.CreatedAt); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.State, row.FaceAmount, row.Count, row.PurposeRef, row.Reason, row.Maker, row.Revision, row.RedeemedCount}
		if row.Approver != nil {
			fields = append(fields, *row.Approver)
		}
		if row.TerminalActor != nil {
			fields = append(fields, *row.TerminalActor)
		}
		return row.ID, fields, func() { s.graph.sources.Batches = append(s.graph.sources.Batches, row) }, nil
	})
}

func (s *fundingFinancialSession) readVouchers() error {
	return s.table(financialVouchers, "vouchers", "id", []string{"id", "batch_id", "created_at"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingGraphVoucherRow
		if err := rows.Scan(&row.ID, &row.BatchID, &row.CreatedAt); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.BatchID}
		return row.ID, fields, func() { s.graph.sources.Vouchers = append(s.graph.sources.Vouchers, row) }, nil
	})
}

func (s *fundingFinancialSession) readRevocations() error {
	return s.table(financialRevocations, "voucher_revocations", "id", []string{"id", "batch_id", "operation_id", "maker", "expected_revision::pg_catalog.text", "expected_redeemed_count::pg_catalog.text", "reason", "created_at"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingGraphRevocationRow
		if err := rows.Scan(&row.ID, &row.BatchID, &row.OperationID, &row.Maker, &row.ExpectedRevision, &row.ExpectedRedeemedCount, &row.Reason, &row.CreatedAt); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.BatchID, row.OperationID, row.Maker, row.ExpectedRevision, row.ExpectedRedeemedCount, row.Reason}
		return row.ID, fields, func() { s.graph.sources.Revocations = append(s.graph.sources.Revocations, row) }, nil
	})
}

func (s *fundingFinancialSession) readDecisions() error {
	return s.table(financialDecisions, "voucher_revocation_decisions", "id", []string{"id", "proposal_id", "batch_id", "operation_id", "approver", "status", "reason", "decided_at"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingGraphRevocationDecisionRow
		if err := rows.Scan(&row.ID, &row.ProposalID, &row.BatchID, &row.OperationID, &row.Approver, &row.Status, &row.Reason, &row.DecidedAt); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.ProposalID, row.BatchID, row.OperationID, row.Approver, row.Status, row.Reason}
		return row.ID, fields, func() { s.graph.sources.Decisions = append(s.graph.sources.Decisions, row) }, nil
	})
}

func (s *fundingFinancialSession) readRedemptions() error {
	return s.table(financialRedemptions, "voucher_redemptions", "id", []string{"id", "tenant_id", "voucher_id", "batch_id", "operation_id", "actor", "amount::pg_catalog.text", "journal_id", "funding_receipt_id", "created_at"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingGraphRedemptionRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.VoucherID, &row.BatchID, &row.OperationID, &row.Actor, &row.Amount, &row.JournalID, &row.FundingReceiptID, &row.CreatedAt); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.TenantID, row.VoucherID, row.BatchID, row.OperationID, row.Actor, row.Amount, row.JournalID, row.FundingReceiptID}
		return row.ID, fields, func() { s.graph.sources.Redemptions = append(s.graph.sources.Redemptions, row) }, nil
	})
}

func (s *fundingFinancialSession) readCommands() error {
	return s.table(financialCommands, "commerce_commands", "operation_id", []string{"operation_id", "actor", "scope", "kind", "request_digest", "fingerprint_key_version", "created_at"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingGraphCommerceCommandRow
		if err := rows.Scan(&row.OperationID, &row.Actor, &row.Scope, &row.Kind, &row.RequestDigest, &row.FingerprintKeyVersion, &row.CreatedAt); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.OperationID, row.Actor, row.Scope, row.Kind, row.RequestDigest}
		if row.FingerprintKeyVersion != nil {
			fields = append(fields, *row.FingerprintKeyVersion)
		}
		return row.OperationID, fields, func() { s.graph.sources.Commands = append(s.graph.sources.Commands, row) }, nil
	})
}

func (s *fundingFinancialSession) readCommerceReceipts() error {
	return s.table(financialCommerceReceipts, "commerce_operation_receipts", "operation_id", []string{"operation_id", "kind", "scope", "actor", "status", "reason", "resource_id", "amount::pg_catalog.text", "funding_receipt_id", "journal_id", "created_at"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingGraphCommerceReceiptRow
		if err := rows.Scan(&row.OperationID, &row.Kind, &row.Scope, &row.Actor, &row.Status, &row.Reason, &row.ResourceID, &row.Amount, &row.FundingReceiptID, &row.JournalID, &row.CreatedAt); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.OperationID, row.Kind, row.Scope, row.Actor, row.Status, row.Reason}
		if row.ResourceID != nil {
			fields = append(fields, *row.ResourceID)
		}
		if row.Amount != nil {
			fields = append(fields, *row.Amount)
		}
		if row.FundingReceiptID != nil {
			fields = append(fields, *row.FundingReceiptID)
		}
		if row.JournalID != nil {
			fields = append(fields, *row.JournalID)
		}
		return row.OperationID, fields, func() { s.graph.sources.CommerceReceipts = append(s.graph.sources.CommerceReceipts, row) }, nil
	})
}

func (s *fundingFinancialSession) readAudits() error {
	return s.table(financialAudits, "audit_events", "id", []string{"id", "actor", "tenant_scope", "action", "target", "sanitized_diff::pg_catalog.text", "request_id", "created_at"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementAuditRow
		if err := rows.Scan(&row.ID, &row.Actor, &row.TenantScope, &row.Action, &row.Target, &row.Diff, &row.RequestID, &row.CreatedAt); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.Actor, row.Action, row.Target, row.Diff, row.RequestID}
		if row.TenantScope != nil {
			fields = append(fields, *row.TenantScope)
		}
		return row.ID, fields, func() { s.graph.sources.Audits = append(s.graph.sources.Audits, row) }, nil
	})
}

func (s *fundingFinancialSession) readOutbox() error {
	return s.table(financialOutbox, "projection_outbox", "event_id", []string{"event_id", "tenant_id", "operation_id", "kind", "schema_version", "sanitized_payload::pg_catalog.text", "created_at"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementOutboxRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.OperationID, &row.Kind, &row.SchemaVersion, &row.Payload, &row.CreatedAt); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.TenantID, row.OperationID, row.Kind, row.Payload}
		return row.ID, fields, func() { s.graph.sources.Outbox = append(s.graph.sources.Outbox, row) }, nil
	})
}

func (s *fundingFinancialSession) readKeys() error {
	return s.table(financialKeys, "api_keys", "id", []string{"id", "tenant_id", "owner_id", "held::pg_catalog.text", "spent::pg_catalog.text", "lifetime_limit::pg_catalog.text"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.CustomerKeyRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.OwnerID, &row.Held, &row.Spent, &row.LifetimeLimit); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.TenantID, row.OwnerID, row.Held, row.Spent}
		if row.LifetimeLimit != nil {
			fields = append(fields, *row.LifetimeLimit)
		}
		return row.ID, fields, func() { s.graph.customer.Keys = append(s.graph.customer.Keys, row) }, nil
	})
}

func (s *fundingFinancialSession) readRequests() error {
	return s.table(financialRequests, "request_records", "id", []string{"id", "tenant_id", "key_id", "owner_id", "billing_status", "input_rate::pg_catalog.text", "output_rate::pg_catalog.text", "input_upper_bound::pg_catalog.text", "output_limit::pg_catalog.text"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.CustomerKeyRequestRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.KeyID, &row.OwnerID, &row.BillingStatus, &row.InputRate, &row.OutputRate, &row.InputUpperBound, &row.OutputLimit); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.TenantID, row.KeyID, row.OwnerID, row.BillingStatus, row.InputRate, row.OutputRate, row.InputUpperBound, row.OutputLimit}
		return row.ID, fields, func() { s.graph.customer.Requests = append(s.graph.customer.Requests, row) }, nil
	})
}

func (s *fundingFinancialSession) readReservations() error {
	return s.table(financialReservations, "reservations", "id", []string{"id", "tenant_id", "request_id", "amount::pg_catalog.text", "status"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.CustomerKeyReservationRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.RequestID, &row.Amount, &row.Status); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.TenantID, row.RequestID, row.Amount, row.Status}
		return row.ID, fields, func() { s.graph.customer.Reservations = append(s.graph.customer.Reservations, row) }, nil
	})
}

func (s *fundingFinancialSession) readSettlements() error {
	return s.table(financialSettlements, "settlements", "id", []string{"id", "tenant_id", "request_id", "charged_amount::pg_catalog.text", "raw_retail_amount::pg_catalog.text", "waived_retail_amount::pg_catalog.text", "refunded_amount::pg_catalog.text", "status", "reason", "journal_id"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.CustomerKeySettlementRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.RequestID, &row.ChargedAmount, &row.RawRetailAmount, &row.WaivedRetailAmount, &row.RefundedAmount, &row.Status, &row.Reason, &row.JournalID); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.TenantID, row.RequestID, row.ChargedAmount, row.RawRetailAmount, row.WaivedRetailAmount, row.RefundedAmount, row.Status, row.Reason, row.JournalID}
		return row.ID, fields, func() { s.graph.customer.Settlements = append(s.graph.customer.Settlements, row) }, nil
	})
}

func (s *fundingFinancialSession) readRefunds() error {
	return s.table(financialRefunds, "refund_orders", "id", []string{"id", "tenant_id", "settlement_id", "business_ref", "amount::pg_catalog.text", "state", "requester", "approver", "journal_id"}, func(rows fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.CustomerKeyRefundRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.SettlementID, &row.BusinessRef, &row.Amount, &row.State, &row.Requester, &row.Approver, &row.JournalID); err != nil {
			return "", nil, nil, err
		}
		fields := []string{row.ID, row.TenantID, row.SettlementID, row.BusinessRef, row.Amount, row.State, row.Requester}
		if row.Approver != nil {
			fields = append(fields, *row.Approver)
		}
		if row.JournalID != nil {
			fields = append(fields, *row.JournalID)
		}
		return row.ID, fields, func() { s.graph.customer.Refunds = append(s.graph.customer.Refunds, row) }, nil
	})
}

func (s *fundingFinancialSession) check() error {
	for _, done := range s.completed {
		if !done.eof {
			return fundingSourceReadExecution
		}
	}
	g := &s.graph
	g.sources.Accounts, g.sources.Lines = g.ledger.rows.Accounts, g.ledger.rows.Lines
	for i, j := range g.ledger.journals {
		if i%256 == 0 && s.ctx.Err() != nil {
			return fundingSourceReadFailure(s.ctx)
		}
		g.sources.Journals = append(g.sources.Journals, fundinglotverify.FundingGraphJournalRow(j))
	}
	g.customer.Users, g.customer.Tenants = g.sources.Users, g.sources.Tenants
	g.customer.Accounts, g.customer.Journals, g.customer.Lines = g.sources.Accounts, g.sources.Journals, g.sources.Lines
	var links fundinglotverify.FundingSourceRows
	for i, r := range g.sources.Receipts {
		if i%256 == 0 && s.ctx.Err() != nil {
			return fundingSourceReadFailure(s.ctx)
		}
		links.Receipts = append(links.Receipts, fundinglotverify.FundingSourceReceiptRow{ID: r.ID, TenantID: r.TenantID, SourceKind: r.SourceKind, SourceID: r.SourceID, Amount: r.Amount, Currency: r.Currency, JournalID: r.JournalID})
	}
	for i, r := range g.sources.Credits {
		if i%256 == 0 && s.ctx.Err() != nil {
			return fundingSourceReadFailure(s.ctx)
		}
		links.Credits = append(links.Credits, fundinglotverify.FundingSourceCreditRow{ID: r.ID, TenantID: r.TenantID, Amount: r.Amount, State: r.State, Requester: r.Requester, Approver: r.Approver, JournalID: r.JournalID})
	}
	for i, r := range g.sources.Redemptions {
		if i%256 == 0 && s.ctx.Err() != nil {
			return fundingSourceReadFailure(s.ctx)
		}
		links.Redemptions = append(links.Redemptions, fundinglotverify.FundingSourceRedemptionRow{ID: r.ID, TenantID: r.TenantID, VoucherID: r.VoucherID, FundingReceiptID: r.FundingReceiptID, Amount: r.Amount, JournalID: r.JournalID})
	}
	var err error
	g.ledger.check, err = fundinglotverify.CheckLedgerRows(s.ctx, g.ledger.rows)
	if err != nil {
		return err
	}
	s.algorithms[0] = true
	g.links, err = fundinglotverify.CheckFundingSourceLinks(s.ctx, links)
	if err != nil {
		return err
	}
	s.algorithms[1] = true
	g.sourceCheck, err = fundinglotverify.CheckFundingSourceGraph(s.ctx, g.sources)
	if err != nil {
		return err
	}
	s.algorithms[2] = true
	g.customerCheck, err = fundinglotverify.CheckCustomerKeyGraph(s.ctx, g.customer)
	if err != nil {
		return err
	}
	s.algorithms[3] = true
	if s.ctx.Err() != nil {
		return fundingSourceReadFailure(s.ctx)
	}
	return nil
}
