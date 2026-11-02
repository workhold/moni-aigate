package fundinglotverify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"reflect"
	"strings"
	"time"
)

var (
	ErrFundingSourceGraphRowsInput    = errors.New("funding_lot_funding_source_graph_rows_input")
	ErrFundingSourceGraphRowsResource = errors.New("funding_lot_funding_source_graph_rows_resource")
	ErrFundingSourceGraphRowsCanceled = errors.New("funding_lot_funding_source_graph_rows_canceled")
	ErrFundingSourceGraphRowsTimeout  = errors.New("funding_lot_funding_source_graph_rows_timeout")
)

type FundingGraphReceiptRow struct {
	ID, TenantID, SourceKind, SourceID, Amount, Currency, JournalID string
	CreatedAt                                                       time.Time
}
type FundingGraphCreditRow struct {
	ID, TenantID, BusinessRef, Amount, State, Requester, ProofRef string
	Approver, JournalID                                           *string
	CreatedAt                                                     time.Time
}
type FundingGraphRedemptionRow struct {
	ID, TenantID, VoucherID, BatchID, OperationID, Actor, Amount, JournalID, FundingReceiptID string
	CreatedAt                                                                                 time.Time
}
type FundingGraphVoucherRow struct {
	ID, BatchID string
	CreatedAt   time.Time
}
type FundingGraphBatchRow struct {
	ID, State, FaceAmount, Count, PurposeRef, Reason, Maker, Revision, RedeemedCount string
	Approver, TerminalActor                                                          *string
	ExpiresAt, CreatedAt                                                             time.Time
	ActivatedAt, TerminalAt                                                          *time.Time
}
type FundingGraphRevocationRow struct {
	ID, BatchID, OperationID, Maker, ExpectedRevision, ExpectedRedeemedCount, Reason string
	CreatedAt                                                                        time.Time
}
type FundingGraphRevocationDecisionRow struct {
	ID, ProposalID, BatchID, OperationID, Approver, Status, Reason string
	DecidedAt                                                      time.Time
}
type FundingGraphCommerceCommandRow struct {
	OperationID, Actor, Scope, Kind, RequestDigest string
	FingerprintKeyVersion                          *string
	CreatedAt                                      time.Time
}
type FundingGraphCommerceReceiptRow struct {
	OperationID, Kind, Scope, Actor, Status, Reason string
	ResourceID, Amount, FundingReceiptID, JournalID *string
	CreatedAt                                       time.Time
}
type FundingSourceGraphRows struct {
	Users            []ProcurementApprovalUserRow
	Tenants          []ProcurementAttemptTenantRow
	Accounts         []LedgerAccountRow
	Journals         []FundingGraphJournalRow
	Lines            []LedgerLineRow
	Audits           []ProcurementAuditRow
	Outbox           []ProcurementOutboxRow
	Receipts         []FundingGraphReceiptRow
	Credits          []FundingGraphCreditRow
	Redemptions      []FundingGraphRedemptionRow
	Batches          []FundingGraphBatchRow
	Vouchers         []FundingGraphVoucherRow
	Revocations      []FundingGraphRevocationRow
	Decisions        []FundingGraphRevocationDecisionRow
	Commands         []FundingGraphCommerceCommandRow
	CommerceReceipts []FundingGraphCommerceReceiptRow
}
type FundingSourceGraphCounts struct {
	Users            uint64 `json:"users"`
	Tenants          uint64 `json:"tenants"`
	Accounts         uint64 `json:"accounts"`
	Journals         uint64 `json:"journals"`
	Lines            uint64 `json:"lines"`
	Audits           uint64 `json:"audits"`
	Outbox           uint64 `json:"outbox"`
	Receipts         uint64 `json:"receipts"`
	Credits          uint64 `json:"credits"`
	Redemptions      uint64 `json:"redemptions"`
	Batches          uint64 `json:"batches"`
	Vouchers         uint64 `json:"vouchers"`
	Revocations      uint64 `json:"revocations"`
	Decisions        uint64 `json:"decisions"`
	Commands         uint64 `json:"commands"`
	CommerceReceipts uint64 `json:"commerce_receipts"`
}
type FundingSourceGraphMismatches struct {
	DuplicatePrimaryRows             uint64 `json:"duplicate_primary_rows"`
	DuplicateReceiptSourceRows       uint64 `json:"duplicate_receipt_source_rows"`
	DuplicateReceiptJournalRows      uint64 `json:"duplicate_receipt_journal_rows"`
	DuplicateCreditBusinessRefRows   uint64 `json:"duplicate_credit_business_ref_rows"`
	DuplicateCreditJournalRows       uint64 `json:"duplicate_credit_journal_rows"`
	DuplicateRedemptionVoucherRows   uint64 `json:"duplicate_redemption_voucher_rows"`
	DuplicateRedemptionOperationRows uint64 `json:"duplicate_redemption_operation_rows"`
	DuplicateRedemptionJournalRows   uint64 `json:"duplicate_redemption_journal_rows"`
	DuplicateRedemptionReceiptRows   uint64 `json:"duplicate_redemption_receipt_rows"`
	DuplicateRevocationOperationRows uint64 `json:"duplicate_revocation_operation_rows"`
	DuplicateDecisionProposalRows    uint64 `json:"duplicate_decision_proposal_rows"`
	DuplicateDecisionOperationRows   uint64 `json:"duplicate_decision_operation_rows"`
	DuplicateAcceptedResourceRows    uint64 `json:"duplicate_accepted_resource_rows"`
	DuplicateAuditEffectRows         uint64 `json:"duplicate_audit_effect_rows"`
	DuplicateOutboxEffectRows        uint64 `json:"duplicate_outbox_effect_rows"`
	InvalidTextRows                  uint64 `json:"invalid_text_rows"`
	InvalidTimeRows                  uint64 `json:"invalid_time_rows"`
	InvalidNumberRows                uint64 `json:"invalid_number_rows"`
	InvalidEnumRows                  uint64 `json:"invalid_enum_rows"`
	InvalidJSONRows                  uint64 `json:"invalid_json_rows"`
	IdentityRows                     uint64 `json:"identity_rows"`
	CreditShapeRows                  uint64 `json:"credit_shape_rows"`
	CreditReceiptRows                uint64 `json:"credit_receipt_rows"`
	RedemptionReceiptRows            uint64 `json:"redemption_receipt_rows"`
	ReceiptSourceRows                uint64 `json:"receipt_source_rows"`
	FundingJournalRows               uint64 `json:"funding_journal_rows"`
	FundingLineRows                  uint64 `json:"funding_line_rows"`
	FundingJournalSourceRows         uint64 `json:"funding_journal_source_rows"`
	ClearingLineSourceRows           uint64 `json:"clearing_line_source_rows"`
	FundingOutboxRows                uint64 `json:"funding_outbox_rows"`
	OutboxSourceRows                 uint64 `json:"outbox_source_rows"`
	FundingAuditRows                 uint64 `json:"funding_audit_rows"`
	AuditSourceRows                  uint64 `json:"audit_source_rows"`
	BatchShapeRows                   uint64 `json:"batch_shape_rows"`
	BatchVoucherCountRows            uint64 `json:"batch_voucher_count_rows"`
	BatchRedemptionCountRows         uint64 `json:"batch_redemption_count_rows"`
	VoucherBatchRows                 uint64 `json:"voucher_batch_rows"`
	RedemptionEligibilityRows        uint64 `json:"redemption_eligibility_rows"`
	RevocationRows                   uint64 `json:"revocation_rows"`
	DecisionRows                     uint64 `json:"decision_rows"`
	CommerceShapeRows                uint64 `json:"commerce_shape_rows"`
	CommerceTerminalRows             uint64 `json:"commerce_terminal_rows"`
	CommerceSourceRows               uint64 `json:"commerce_source_rows"`
	UnknownReceiptKindRows           uint64 `json:"unknown_receipt_kind_rows"`
}

// This result describes the supplied manual/voucher graph only. It is neither
// a database completeness record nor a proof of historical authorization.
type FundingSourceGraphCheck struct {
	Scope                    string                       `json:"scope"`
	Scanned                  FundingSourceGraphCounts     `json:"scanned"`
	ScopedGraphConsistent    bool                         `json:"scoped_graph_consistent"`
	Mismatches               FundingSourceGraphMismatches `json:"mismatches"`
	UncoveredSandboxReceipts uint64                       `json:"uncovered_sandbox_receipts"`
}

type fundingGraphPair struct{ first, second string }
type fundingGraphAccountKind struct {
	tenant, kind string
	hasTenant    bool
}
type fundingGraphWork struct {
	ctx   context.Context
	steps uint16
}

func (w *fundingGraphWork) tick() error {
	w.steps++
	if w.steps == 256 {
		w.steps = 0
		return fundingGraphContext(w.ctx)
	}
	return nil
}
func fundingGraphContext(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrFundingSourceGraphRowsTimeout
	}
	return ErrFundingSourceGraphRowsCanceled
}
func fundingGraphID(s string) bool { return s != "" && ledgerText(s) }
func fundingGraphUUID(s string) bool {
	return s != "00000000-0000-0000-0000-000000000000" && procurementApprovalUUID(s)
}
func fundingGraphRequired(values ...string) bool {
	for _, s := range values {
		if !fundingGraphID(s) {
			return false
		}
	}
	return true
}
func fundingGraphOptional(values ...*string) bool {
	for _, p := range values {
		if p != nil && !fundingGraphID(*p) {
			return false
		}
	}
	return true
}
func fundingGraphNumber(s string, positive bool) bool {
	n, ok := ledgerInteger(s)
	return ok && (!positive || n > 0)
}
func fundingGraphNonnegative(s string) bool { n, ok := ledgerInteger(s); return ok && n >= 0 }
func fundingGraphTime(values ...time.Time) bool {
	for _, t := range values {
		if !procurementCommandFiniteTime(t) {
			return false
		}
	}
	return true
}
func fundingGraphOptionalTime(values ...*time.Time) bool {
	for _, t := range values {
		if t != nil && !fundingGraphTime(*t) {
			return false
		}
	}
	return true
}
func fundingGraphVoucherKind(k string) bool {
	switch k {
	case "voucher.batch.create", "voucher.batch.approve", "voucher.batch.void", "voucher.revocation.create", "voucher.revocation.approve", "voucher.redeem":
		return true
	}
	return false
}
func fundingGraphPaymentKind(k string) bool {
	switch k {
	case "payment.sku.publish", "payment.sku.activate", "payment.order.create", "payment.order.cancel":
		return true
	}
	return false
}
func fundingGraphAction(k string) bool {
	switch k {
	case "credit.created", "credit.approved", "voucher.batch.created", "voucher.batch.approved", "voucher.batch.voided", "voucher.revocation.created", "voucher.revocation.approved", "voucher.redeemed":
		return true
	}
	return false
}
func fundingGraphVersion(s string) bool {
	if len(s) < 1 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// All DTO text fields are direct string or *string fields. Reflection here is
// confined to these owned raw DTOs; time.Time internals and derived values are
// deliberately excluded. The complete budget precedes any relation indexes.
func fundingGraphRawStrings(row reflect.Value, consume func(string) bool) bool {
	for i := 0; i < row.NumField(); i++ {
		v := row.Field(i)
		if v.Kind() == reflect.String && !consume(v.String()) {
			return false
		}
		if v.Kind() == reflect.Pointer && !v.IsNil() && v.Type().Elem().Kind() == reflect.String && !consume(v.Elem().String()) {
			return false
		}
	}
	return true
}
func fundingGraphBudget(w *fundingGraphWork, in FundingSourceGraphRows) error {
	raw := reflect.ValueOf(in)
	left := ledgerRowLimit
	for i := 0; i < raw.NumField(); i++ {
		if raw.Field(i).Len() > left {
			return ErrFundingSourceGraphRowsResource
		}
		left -= raw.Field(i).Len()
	}
	bytes := ledgerByteLimit
	for i := 0; i < raw.NumField(); i++ {
		group := raw.Field(i)
		for j := 0; j < group.Len(); j++ {
			if e := w.tick(); e != nil {
				return e
			}
			if !fundingGraphRawStrings(group.Index(j), func(s string) bool {
				if len(s) > bytes {
					return false
				}
				bytes -= len(s)
				return true
			}) {
				return ErrFundingSourceGraphRowsResource
			}
		}
	}
	return fundingGraphContext(w.ctx)
}
func fundingGraphShapes[T any](w *fundingGraphWork, rows []T, m *FundingSourceGraphMismatches, text, clock, number, enums func(T) bool) error {
	for _, row := range rows {
		if e := w.tick(); e != nil {
			return e
		}
		valid := fundingGraphRawStrings(reflect.ValueOf(row), func(s string) bool { return ledgerText(s) })
		if !valid || text != nil && !text(row) {
			m.InvalidTextRows++
		}
		if clock != nil && !clock(row) {
			m.InvalidTimeRows++
		}
		if number != nil && !number(row) {
			m.InvalidNumberRows++
		}
		if enums != nil && !enums(row) {
			m.InvalidEnumRows++
		}
	}
	return nil
}
func fundingGraphIndex[T any](w *fundingGraphWork, rows []T, id func(T) string, uuid bool, duplicate *uint64) (map[string]T, error) {
	index := make(map[string]T, len(rows))
	for _, r := range rows {
		if e := w.tick(); e != nil {
			return nil, e
		}
		key := id(r)
		if !fundingGraphID(key) || uuid && !fundingGraphUUID(key) {
			continue
		}
		if _, ok := index[key]; ok {
			(*duplicate)++
			continue
		}
		index[key] = r
	}
	return index, nil
}
func fundingGraphGroup[K comparable, T any](w *fundingGraphWork, rows map[string]T, key func(T) (K, bool), duplicate *uint64) (map[K][]T, error) {
	groups := make(map[K][]T, len(rows))
	for _, r := range rows {
		if e := w.tick(); e != nil {
			return nil, e
		}
		k, ok := key(r)
		if !ok {
			continue
		}
		if len(groups[k]) != 0 && duplicate != nil {
			(*duplicate)++
		}
		groups[k] = append(groups[k], r)
	}
	return groups, nil
}

type fundingGraphJSONValue struct {
	value  string
	number bool
}

func fundingGraphJSON(raw string, keys []string, numberKey string) (map[string]fundingGraphJSONValue, bool) {
	if !ledgerText(raw) || !procurementCommandJSONUnicode(raw) {
		return nil, false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, false
	}
	allowed := make(map[string]bool, len(keys))
	for _, k := range keys {
		allowed[k] = true
	}
	values := make(map[string]fundingGraphJSONValue, len(keys))
	for d.More() {
		t, e = d.Token()
		if e != nil {
			return nil, false
		}
		k, ok := t.(string)
		if !ok || !allowed[k] {
			return nil, false
		}
		if _, ok = values[k]; ok {
			return nil, false
		}
		t, e = d.Token()
		if e != nil {
			return nil, false
		}
		v := fundingGraphJSONValue{}
		if k == numberKey {
			n, ok := t.(json.Number)
			if !ok || !fundingGraphNumber(string(n), true) {
				return nil, false
			}
			v.value, v.number = string(n), true
		} else {
			s, ok := t.(string)
			if !ok || !ledgerText(s) {
				return nil, false
			}
			v.value = s
		}
		if (k == "amount" || k == "face_amount") && !fundingGraphNumber(v.value, true) {
			return nil, false
		}
		values[k] = v
	}
	t, e = d.Token()
	if e != nil || t != json.Delim('}') || len(values) != len(keys) {
		return nil, false
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, false
	}
	return values, true
}
func fundingGraphJSONKeys(action string) ([]string, string) {
	switch action {
	case "credit.created", "credit.approved", "credited":
		return []string{"amount"}, ""
	case "voucher.batch.created":
		return []string{"face_amount", "count", "purpose_ref"}, "count"
	case "voucher.batch.approved":
		return []string{"maker"}, ""
	case "voucher.batch.voided":
		return []string{"reason"}, ""
	case "voucher.revocation.created":
		return []string{"batch_id", "reason"}, ""
	case "voucher.revocation.approved":
		return []string{"batch_id"}, ""
	case "voucher.redeemed":
		return []string{"amount", "funding_receipt_id"}, ""
	}
	return nil, ""
}
func fundingGraphProductFits(a, b string) bool {
	// Reject oversized/noncanonical literals before big.Int parses or allocates
	// for caller-controlled text. Only the bounded product needs wide arithmetic.
	if !fundingGraphNumber(a, true) || !fundingGraphNumber(b, true) { return false }
	x, ok := new(big.Int).SetString(a, 10)
	if !ok {
		return false
	}
	y, ok := new(big.Int).SetString(b, 10)
	if !ok {
		return false
	}
	return x.Sign() > 0 && y.Sign() > 0 && x.Mul(x, y).Cmp(big.NewInt(9223372036854775807)) <= 0
}

// CheckFundingSourceGraph evaluates complete supplied raw groups. A successful
// call does not establish that the caller supplied every row in a database.
func CheckFundingSourceGraph(ctx context.Context, in FundingSourceGraphRows) (FundingSourceGraphCheck, error) {
	if ctx == nil {
		return FundingSourceGraphCheck{}, ErrFundingSourceGraphRowsInput
	}
	if e := fundingGraphContext(ctx); e != nil {
		return FundingSourceGraphCheck{}, e
	}
	w := &fundingGraphWork{ctx: ctx}
	if e := fundingGraphBudget(w, in); e != nil {
		return FundingSourceGraphCheck{}, e
	}
	g := fundingGraphState{work: w, input: in}
	g.check.Scope = "manual_voucher_funding_graph_v1"
	g.check.Scanned = FundingSourceGraphCounts{uint64(len(in.Users)), uint64(len(in.Tenants)), uint64(len(in.Accounts)), uint64(len(in.Journals)), uint64(len(in.Lines)), uint64(len(in.Audits)), uint64(len(in.Outbox)), uint64(len(in.Receipts)), uint64(len(in.Credits)), uint64(len(in.Redemptions)), uint64(len(in.Batches)), uint64(len(in.Vouchers)), uint64(len(in.Revocations)), uint64(len(in.Decisions)), uint64(len(in.Commands)), uint64(len(in.CommerceReceipts))}
	for _, receipt := range in.Receipts {
		if e := w.tick(); e != nil {
			return FundingSourceGraphCheck{}, e
		}
		if receipt.SourceKind == "sandbox_capture" {
			g.check.UncoveredSandboxReceipts++
		}
	}
	if e := g.run(); e != nil {
		return FundingSourceGraphCheck{}, e
	}
	if e := fundingGraphContext(ctx); e != nil {
		return FundingSourceGraphCheck{}, e
	}
	g.check.ScopedGraphConsistent = g.check.Mismatches == (FundingSourceGraphMismatches{})
	return g.check, nil
}
