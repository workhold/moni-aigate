package fundinglotverify

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"unicode/utf8"
)

var (
	ErrCustomerKeyGraphRowsInput    = errors.New("funding_lot_customer_key_graph_rows_input")
	ErrCustomerKeyGraphRowsResource = errors.New("funding_lot_customer_key_graph_rows_resource")
	ErrCustomerKeyGraphRowsCanceled = errors.New("funding_lot_customer_key_graph_rows_canceled")
	ErrCustomerKeyGraphRowsTimeout  = errors.New("funding_lot_customer_key_graph_rows_timeout")
)

type CustomerKeyRow struct {
	ID, TenantID, OwnerID, Held, Spent string
	LifetimeLimit                      *string
}
type CustomerKeyRequestRow struct {
	ID, TenantID, KeyID, OwnerID, BillingStatus         string
	InputRate, OutputRate, InputUpperBound, OutputLimit string
}
type CustomerKeyReservationRow struct{ ID, TenantID, RequestID, Amount, Status string }
type CustomerKeySettlementRow struct {
	ID, TenantID, RequestID, ChargedAmount, RawRetailAmount       string
	WaivedRetailAmount, RefundedAmount, Status, Reason, JournalID string
}
type CustomerKeyRefundRow struct {
	ID, TenantID, SettlementID, BusinessRef, Amount, State, Requester string
	Approver, JournalID                                               *string
}
type CustomerKeyGraphRows struct {
	Tenants      []ProcurementAttemptTenantRow
	Users        []ProcurementApprovalUserRow
	Keys         []CustomerKeyRow
	Requests     []CustomerKeyRequestRow
	Reservations []CustomerKeyReservationRow
	Settlements  []CustomerKeySettlementRow
	Refunds      []CustomerKeyRefundRow
	Accounts     []LedgerAccountRow
	Journals     []FundingGraphJournalRow
	Lines        []LedgerLineRow
}
type CustomerKeyGraphCounts struct {
	Tenants      uint64 `json:"tenants"`
	Users        uint64 `json:"users"`
	Keys         uint64 `json:"keys"`
	Requests     uint64 `json:"requests"`
	Reservations uint64 `json:"reservations"`
	Settlements  uint64 `json:"settlements"`
	Refunds      uint64 `json:"refunds"`
	Accounts     uint64 `json:"accounts"`
	Journals     uint64 `json:"journals"`
	Lines        uint64 `json:"lines"`
}
type CustomerKeyGraphMismatches struct {
	DuplicateTenantRows             uint64 `json:"duplicate_tenant_rows"`
	DuplicateUserRows               uint64 `json:"duplicate_user_rows"`
	DuplicateKeyRows                uint64 `json:"duplicate_key_rows"`
	DuplicateRequestRows            uint64 `json:"duplicate_request_rows"`
	DuplicateReservationRows        uint64 `json:"duplicate_reservation_rows"`
	DuplicateReservationRequestRows uint64 `json:"duplicate_reservation_request_rows"`
	DuplicateSettlementRows         uint64 `json:"duplicate_settlement_rows"`
	DuplicateSettlementRequestRows  uint64 `json:"duplicate_settlement_request_rows"`
	DuplicateRefundRows             uint64 `json:"duplicate_refund_rows"`
	DuplicateRefundBusinessRefRows  uint64 `json:"duplicate_refund_business_ref_rows"`
	DuplicateAccountRows            uint64 `json:"duplicate_account_rows"`
	DuplicateAccountKindRows        uint64 `json:"duplicate_account_kind_rows"`
	DuplicateJournalRows            uint64 `json:"duplicate_journal_rows"`
	DuplicateJournalOperationRows   uint64 `json:"duplicate_journal_operation_rows"`
	DuplicateLineRows               uint64 `json:"duplicate_line_rows"`
	DuplicateJournalAccountRows     uint64 `json:"duplicate_journal_account_rows"`
	InvalidTextRows                 uint64 `json:"invalid_text_rows"`
	InvalidAmountRows               uint64 `json:"invalid_amount_rows"`
	InvalidEnumRows                 uint64 `json:"invalid_enum_rows"`
	KeyIdentityRows                 uint64 `json:"key_identity_rows"`
	RequestIdentityRows             uint64 `json:"request_identity_rows"`
	RequestReservationRows          uint64 `json:"request_reservation_rows"`
	ReservationIdentityRows         uint64 `json:"reservation_identity_rows"`
	ReservationAmountRows           uint64 `json:"reservation_amount_rows"`
	RequestFinancialStateRows       uint64 `json:"request_financial_state_rows"`
	SettlementIdentityRows          uint64 `json:"settlement_identity_rows"`
	SettlementStateRows             uint64 `json:"settlement_state_rows"`
	SettlementAmountRows            uint64 `json:"settlement_amount_rows"`
	RefundIdentityRows              uint64 `json:"refund_identity_rows"`
	RefundShapeRows                 uint64 `json:"refund_shape_rows"`
	SettlementRefundedRows          uint64 `json:"settlement_refunded_rows"`
	KeyHeldRows                     uint64 `json:"key_held_rows"`
	KeySpentRows                    uint64 `json:"key_spent_rows"`
	KeyLimitRows                    uint64 `json:"key_limit_rows"`
	ReservationJournalRows          uint64 `json:"reservation_journal_rows"`
	SettlementJournalRows           uint64 `json:"settlement_journal_rows"`
	RefundJournalRows               uint64 `json:"refund_journal_rows"`
	PendingRefundEffectRows         uint64 `json:"pending_refund_effect_rows"`
	JournalSourceRows               uint64 `json:"journal_source_rows"`
	LineIdentityRows                uint64 `json:"line_identity_rows"`
}
type CustomerKeyGraphCheck struct {
	Scope      string                     `json:"scope"`
	Scanned    CustomerKeyGraphCounts     `json:"scanned"`
	Balanced   bool                       `json:"balanced"`
	Mismatches CustomerKeyGraphMismatches `json:"mismatches"`
}

type customerKeyGraphWork struct {
	ctx   context.Context
	steps uint16
	bytes int
}

func customerKeyGraphContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrCustomerKeyGraphRowsTimeout
	}
	return ErrCustomerKeyGraphRowsCanceled
}
func (w *customerKeyGraphWork) tick() error {
	w.steps++
	if w.steps&255 == 0 && w.ctx.Err() != nil {
		return customerKeyGraphContextError(w.ctx)
	}
	return nil
}
func (w *customerKeyGraphWork) text(fields []string, optional ...*string) (bool, error) {
	if err := w.tick(); err != nil {
		return false, err
	}
	valid := true
	consume := func(s string) error {
		if len(s) > fundingSourceByteLimit-w.bytes {
			return ErrCustomerKeyGraphRowsResource
		}
		w.bytes += len(s)
		valid = valid && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
		return nil
	}
	for _, s := range fields {
		if err := consume(s); err != nil {
			return false, err
		}
	}
	for _, s := range optional {
		if s != nil {
			if err := consume(*s); err != nil {
				return false, err
			}
		}
	}
	return valid, nil
}

// Validate the entire raw input before allocating relationship indexes. Empty
// TEXT identities remain identities; nullable fields preserve SQL NULL.
func customerKeyGraphBudget(w *customerKeyGraphWork, in CustomerKeyGraphRows) (uint64, error) {
	remaining := fundingSourceRowLimit
	for _, n := range []int{len(in.Tenants), len(in.Users), len(in.Keys), len(in.Requests), len(in.Reservations), len(in.Settlements), len(in.Refunds), len(in.Accounts), len(in.Journals), len(in.Lines)} {
		if n > remaining {
			return 0, ErrCustomerKeyGraphRowsResource
		}
		remaining -= n
	}
	var invalid uint64
	check := func(fields []string, optional ...*string) error {
		valid, err := w.text(fields, optional...)
		if !valid && err == nil {
			invalid++
		}
		return err
	}
	for _, r := range in.Tenants {
		if err := check([]string{r.ID}); err != nil {
			return 0, err
		}
	}
	for _, r := range in.Users {
		if err := check([]string{r.ID}); err != nil {
			return 0, err
		}
	}
	for _, r := range in.Keys {
		if err := check([]string{r.ID, r.TenantID, r.OwnerID, r.Held, r.Spent}, r.LifetimeLimit); err != nil {
			return 0, err
		}
	}
	for _, r := range in.Requests {
		if err := check([]string{r.ID, r.TenantID, r.KeyID, r.OwnerID, r.BillingStatus, r.InputRate, r.OutputRate, r.InputUpperBound, r.OutputLimit}); err != nil {
			return 0, err
		}
	}
	for _, r := range in.Reservations {
		if err := check([]string{r.ID, r.TenantID, r.RequestID, r.Amount, r.Status}); err != nil {
			return 0, err
		}
	}
	for _, r := range in.Settlements {
		if err := check([]string{r.ID, r.TenantID, r.RequestID, r.ChargedAmount, r.RawRetailAmount, r.WaivedRetailAmount, r.RefundedAmount, r.Status, r.Reason, r.JournalID}); err != nil {
			return 0, err
		}
	}
	for _, r := range in.Refunds {
		if err := check([]string{r.ID, r.TenantID, r.SettlementID, r.BusinessRef, r.Amount, r.State, r.Requester}, r.Approver, r.JournalID); err != nil {
			return 0, err
		}
	}
	for _, r := range in.Accounts {
		if err := check([]string{r.ID, r.Kind, r.Balance}, r.TenantID); err != nil {
			return 0, err
		}
	}
	for _, r := range in.Journals {
		if err := check([]string{r.ID, r.TenantID, r.OperationID, r.Kind, r.Reference, r.Actor, r.Currency, r.CreatedTXID}); err != nil {
			return 0, err
		}
	}
	for _, r := range in.Lines {
		if err := check([]string{r.ID, r.JournalID, r.AccountID, r.SignedDelta}); err != nil {
			return 0, err
		}
	}
	return invalid, nil
}

func customerKeyGraphIndex[T any, K comparable](w *customerKeyGraphWork, rows []T, key func(T) K, duplicates *uint64) ([]T, map[K]T, error) {
	kept := make([]T, 0, len(rows))
	index := make(map[K]T, len(rows))
	for _, row := range rows {
		if err := w.tick(); err != nil {
			return nil, nil, err
		}
		k := key(row)
		if _, exists := index[k]; exists {
			*duplicates++
			continue
		}
		index[k], kept = row, append(kept, row)
	}
	return kept, index, nil
}

func customerKeyGraphMoney(values ...string) bool {
	for _, s := range values {
		if _, ok := procurementAttemptInt64(s); !ok {
			return false
		}
	}
	return true
}
func customerKeyGraphSigned(s string) bool {
	_, ok := ledgerInteger(s)
	return ok
}
func customerKeyGraphState(s string) bool {
	return procurementAttemptStatus(s, "reserved", "pending_reconciliation", "settled", "released")
}
func customerKeyGraphUnfinished(s string) bool {
	return s == "reserved" || s == "pending_reconciliation"
}
func customerKeyGraphReleaseReason(s string) bool {
	return procurementAttemptStatus(s, "released_deadline", "released_no_delivery", "released_not_sent", "released_rejected_no_consumption", "released_no_capacity", "released_authorization_revoked", "released_platform_failure", "released_provider_failure", "released_reconciliation")
}

func customerKeyGraphValuePass(w *customerKeyGraphWork, in CustomerKeyGraphRows, m *CustomerKeyGraphMismatches) error {
	for _, r := range in.Keys {
		if err := w.tick(); err != nil {
			return err
		}
		if !customerKeyGraphMoney(r.Held, r.Spent) || r.LifetimeLimit != nil && !customerKeyGraphMoney(*r.LifetimeLimit) {
			m.InvalidAmountRows++
		}
	}
	for _, r := range in.Requests {
		if err := w.tick(); err != nil {
			return err
		}
		if !customerKeyGraphMoney(r.InputRate, r.OutputRate, r.InputUpperBound, r.OutputLimit) {
			m.InvalidAmountRows++
		}
		if !customerKeyGraphState(r.BillingStatus) {
			m.InvalidEnumRows++
		}
	}
	for _, r := range in.Reservations {
		if err := w.tick(); err != nil {
			return err
		}
		if !customerKeyGraphMoney(r.Amount) {
			m.InvalidAmountRows++
		}
		if !customerKeyGraphState(r.Status) {
			m.InvalidEnumRows++
		}
	}
	for _, r := range in.Settlements {
		if err := w.tick(); err != nil {
			return err
		}
		_, rawOK := procurementBudgetUnsigned(r.RawRetailAmount)
		_, waivedOK := procurementBudgetUnsigned(r.WaivedRetailAmount)
		if !customerKeyGraphMoney(r.ChargedAmount, r.RefundedAmount) || !rawOK || !waivedOK {
			m.InvalidAmountRows++
		}
		if !procurementAttemptStatus(r.Status, "settled", "released") {
			m.InvalidEnumRows++
		}
	}
	for _, r := range in.Refunds {
		if err := w.tick(); err != nil {
			return err
		}
		n, ok := procurementAttemptInt64(r.Amount)
		if !ok || n.Sign() == 0 {
			m.InvalidAmountRows++
		}
		if !procurementAttemptStatus(r.State, "pending", "approved") {
			m.InvalidEnumRows++
		}
	}
	for _, r := range in.Accounts {
		if err := w.tick(); err != nil {
			return err
		}
		if !customerKeyGraphSigned(r.Balance) {
			m.InvalidAmountRows++
		}
	}
	for _, r := range in.Lines {
		if err := w.tick(); err != nil {
			return err
		}
		if !customerKeyGraphSigned(r.SignedDelta) {
			m.InvalidAmountRows++
		}
	}
	return nil
}

type customerKeyGraphSum struct {
	value big.Int
	valid bool
}

func customerKeyGraphNewSum() *customerKeyGraphSum { return &customerKeyGraphSum{valid: true} }
func (s *customerKeyGraphSum) add(v string) {
	n, ok := procurementAttemptInt64(v)
	if !ok {
		s.valid = false
		return
	}
	s.value.Add(&s.value, n)
}
func (s *customerKeyGraphSum) equal(v string) bool {
	n, ok := procurementAttemptInt64(v)
	return !s.valid || !ok || s.value.Cmp(n) == 0
}

type customerKeyGraphData struct {
	w                     *customerKeyGraphWork
	m                     *CustomerKeyGraphMismatches
	in                    CustomerKeyGraphRows
	tenants               map[string]ProcurementAttemptTenantRow
	users                 map[string]ProcurementApprovalUserRow
	keys                  map[string]CustomerKeyRow
	requests              map[string]CustomerKeyRequestRow
	reservations          map[string]CustomerKeyReservationRow
	reservationRequests   map[string]CustomerKeyReservationRow
	settlements           map[string]CustomerKeySettlementRow
	settlementRequests    map[string]CustomerKeySettlementRow
	refunds               map[string]CustomerKeyRefundRow
	accounts              map[string]LedgerAccountRow
	journals              map[string]FundingGraphJournalRow
	operations            map[string]FundingGraphJournalRow
	lines                 map[string][]LedgerLineRow
	held, spent, refunded map[string]*customerKeyGraphSum
}

// All indexes retain the first row in the supplied order. Secondary unique
// conflicts are removed before graph traversal, so they cannot inflate sums.
func (g *customerKeyGraphData) index() (err error) {
	w, m, in := g.w, g.m, &g.in
	if in.Tenants, g.tenants, err = customerKeyGraphIndex(w, in.Tenants, func(r ProcurementAttemptTenantRow) string { return r.ID }, &m.DuplicateTenantRows); err != nil {
		return err
	}
	if in.Users, g.users, err = customerKeyGraphIndex(w, in.Users, func(r ProcurementApprovalUserRow) string { return r.ID }, &m.DuplicateUserRows); err != nil {
		return err
	}
	if in.Keys, g.keys, err = customerKeyGraphIndex(w, in.Keys, func(r CustomerKeyRow) string { return r.ID }, &m.DuplicateKeyRows); err != nil {
		return err
	}
	if in.Requests, g.requests, err = customerKeyGraphIndex(w, in.Requests, func(r CustomerKeyRequestRow) string { return r.ID }, &m.DuplicateRequestRows); err != nil {
		return err
	}
	if in.Reservations, _, err = customerKeyGraphIndex(w, in.Reservations, func(r CustomerKeyReservationRow) string { return r.ID }, &m.DuplicateReservationRows); err != nil {
		return err
	}
	if in.Reservations, g.reservationRequests, err = customerKeyGraphIndex(w, in.Reservations, func(r CustomerKeyReservationRow) string { return r.RequestID }, &m.DuplicateReservationRequestRows); err != nil {
		return err
	}
	g.reservations = make(map[string]CustomerKeyReservationRow, len(in.Reservations))
	for _, r := range in.Reservations {
		if err = w.tick(); err != nil {
			return err
		}
		g.reservations[r.ID] = r
	}
	if in.Settlements, _, err = customerKeyGraphIndex(w, in.Settlements, func(r CustomerKeySettlementRow) string { return r.ID }, &m.DuplicateSettlementRows); err != nil {
		return err
	}
	if in.Settlements, g.settlementRequests, err = customerKeyGraphIndex(w, in.Settlements, func(r CustomerKeySettlementRow) string { return r.RequestID }, &m.DuplicateSettlementRequestRows); err != nil {
		return err
	}
	g.settlements = make(map[string]CustomerKeySettlementRow, len(in.Settlements))
	for _, r := range in.Settlements {
		if err = w.tick(); err != nil {
			return err
		}
		g.settlements[r.ID] = r
	}
	if in.Refunds, _, err = customerKeyGraphIndex(w, in.Refunds, func(r CustomerKeyRefundRow) string { return r.ID }, &m.DuplicateRefundRows); err != nil {
		return err
	}
	if in.Refunds, _, err = customerKeyGraphIndex(w, in.Refunds, func(r CustomerKeyRefundRow) string { return r.BusinessRef }, &m.DuplicateRefundBusinessRefRows); err != nil {
		return err
	}
	g.refunds = make(map[string]CustomerKeyRefundRow, len(in.Refunds))
	for _, r := range in.Refunds {
		if err = w.tick(); err != nil {
			return err
		}
		g.refunds[r.ID] = r
	}
	if in.Accounts, _, err = customerKeyGraphIndex(w, in.Accounts, func(r LedgerAccountRow) string { return r.ID }, &m.DuplicateAccountRows); err != nil {
		return err
	}
	if in.Accounts, _, err = customerKeyGraphIndex(w, in.Accounts, customerKeyGraphAccountKind, &m.DuplicateAccountKindRows); err != nil {
		return err
	}
	g.accounts = make(map[string]LedgerAccountRow, len(in.Accounts))
	for _, r := range in.Accounts {
		if err = w.tick(); err != nil {
			return err
		}
		g.accounts[r.ID] = r
	}
	if in.Journals, _, err = customerKeyGraphIndex(w, in.Journals, func(r FundingGraphJournalRow) string { return r.ID }, &m.DuplicateJournalRows); err != nil {
		return err
	}
	if in.Journals, g.operations, err = customerKeyGraphIndex(w, in.Journals, func(r FundingGraphJournalRow) string { return r.OperationID }, &m.DuplicateJournalOperationRows); err != nil {
		return err
	}
	g.journals = make(map[string]FundingGraphJournalRow, len(in.Journals))
	for _, r := range in.Journals {
		if err = w.tick(); err != nil {
			return err
		}
		g.journals[r.ID] = r
	}
	if in.Lines, _, err = customerKeyGraphIndex(w, in.Lines, func(r LedgerLineRow) string { return r.ID }, &m.DuplicateLineRows); err != nil {
		return err
	}
	if in.Lines, _, err = customerKeyGraphIndex(w, in.Lines, func(r LedgerLineRow) journalAccountKey { return journalAccountKey{r.JournalID, r.AccountID} }, &m.DuplicateJournalAccountRows); err != nil {
		return err
	}
	g.lines = make(map[string][]LedgerLineRow)
	for _, r := range in.Lines {
		if err = w.tick(); err != nil {
			return err
		}
		g.lines[r.JournalID] = append(g.lines[r.JournalID], r)
		j, jok := g.journals[r.JournalID]
		a, aok := g.accounts[r.AccountID]
		if !jok || !aok || a.TenantID != nil && *a.TenantID != j.TenantID {
			m.LineIdentityRows++
		}
	}
	g.held, g.spent, g.refunded = make(map[string]*customerKeyGraphSum), make(map[string]*customerKeyGraphSum), make(map[string]*customerKeyGraphSum)
	for _, r := range in.Keys {
		if err = w.tick(); err != nil {
			return err
		}
		g.held[r.ID], g.spent[r.ID] = customerKeyGraphNewSum(), customerKeyGraphNewSum()
	}
	for _, r := range in.Settlements {
		if err = w.tick(); err != nil {
			return err
		}
		g.refunded[r.ID] = customerKeyGraphNewSum()
	}
	return nil
}

func customerKeyGraphAccountKind(a LedgerAccountRow) accountKindKey {
	k := accountKindKey{nullTenant: a.TenantID == nil, kind: a.Kind}
	if a.TenantID != nil {
		k.tenant = *a.TenantID
	}
	return k
}
func customerKeyGraphUpper(r CustomerKeyRequestRow) (*big.Int, bool) {
	if !customerKeyGraphMoney(r.InputRate, r.OutputRate, r.InputUpperBound, r.OutputLimit) {
		return nil, false
	}
	i, _ := procurementAttemptInt64(r.InputRate)
	o, _ := procurementAttemptInt64(r.OutputRate)
	ib, _ := procurementAttemptInt64(r.InputUpperBound)
	ob, _ := procurementAttemptInt64(r.OutputLimit)
	n := new(big.Int).Mul(i, ib)
	n.Add(n, new(big.Int).Mul(o, ob)).Add(n, big.NewInt(999999)).Quo(n, big.NewInt(1000000))
	return n, n.IsInt64()
}

func (g *customerKeyGraphData) requestPass() error {
	for _, r := range g.in.Keys {
		if err := g.w.tick(); err != nil {
			return err
		}
		_, tok := g.tenants[r.TenantID]
		_, uok := g.users[r.OwnerID]
		if !tok || !uok {
			g.m.KeyIdentityRows++
		}
	}
	for _, r := range g.in.Requests {
		if err := g.w.tick(); err != nil {
			return err
		}
		_, tok := g.tenants[r.TenantID]
		_, uok := g.users[r.OwnerID]
		k, kok := g.keys[r.KeyID]
		if !tok || !uok || !kok || k.TenantID != r.TenantID || k.OwnerID != r.OwnerID {
			g.m.RequestIdentityRows++
		}
		v, vok := g.reservationRequests[r.ID]
		if !vok || v.TenantID != r.TenantID {
			g.m.RequestReservationRows++
		}
		s, sok := g.settlementRequests[r.ID]
		valid := vok && v.TenantID == r.TenantID && customerKeyGraphState(r.BillingStatus) && v.Status == r.BillingStatus
		if customerKeyGraphUnfinished(r.BillingStatus) {
			valid = valid && !sok
		} else {
			valid = valid && sok && s.TenantID == r.TenantID && s.Status == r.BillingStatus
		}
		if !valid {
			g.m.RequestFinancialStateRows++
		}
	}
	for _, v := range g.in.Reservations {
		if err := g.w.tick(); err != nil {
			return err
		}
		r, ok := g.requests[v.RequestID]
		if !ok || r.TenantID != v.TenantID {
			g.m.ReservationIdentityRows++
		}
		if ok {
			upper, uok := customerKeyGraphUpper(r)
			amount, aok := procurementAttemptInt64(v.Amount)
			if !uok || !aok || upper.Cmp(amount) != 0 {
				g.m.ReservationAmountRows++
			}
		}
		if ok && r.TenantID == v.TenantID && customerKeyGraphUnfinished(v.Status) {
			if k, exists := g.keys[r.KeyID]; exists && k.TenantID == r.TenantID {
				g.held[k.ID].add(v.Amount)
			}
		}
		if !g.reservationJournal(v) {
			g.m.ReservationJournalRows++
		}
	}
	return nil
}

func (g *customerKeyGraphData) settlementAmount(s CustomerKeySettlementRow, v CustomerKeyReservationRow, vok bool) bool {
	c, cok := procurementAttemptInt64(s.ChargedAmount)
	refunded, fok := procurementAttemptInt64(s.RefundedAmount)
	raw, rok := procurementBudgetUnsigned(s.RawRetailAmount)
	waived, wok := procurementBudgetUnsigned(s.WaivedRetailAmount)
	r, rvok := procurementAttemptInt64(v.Amount)
	if !cok || !fok || !rok || !wok || !vok || !rvok {
		return false
	}
	if refunded.Cmp(c) > 0 {
		return false
	}
	if s.Status == "released" {
		return c.Sign() == 0 && raw.Sign() == 0 && waived.Sign() == 0 && refunded.Sign() == 0
	}
	if s.Status != "settled" {
		return false
	}
	want := new(big.Int).Set(raw)
	if want.Cmp(r) > 0 {
		want.Set(r)
	}
	return c.Cmp(want) == 0 && new(big.Int).Add(c, waived).Cmp(raw) == 0
}
func (g *customerKeyGraphData) settlementPass() error {
	for _, s := range g.in.Settlements {
		if err := g.w.tick(); err != nil {
			return err
		}
		r, rok := g.requests[s.RequestID]
		v, vok := g.reservationRequests[s.RequestID]
		if !rok || !vok || r.TenantID != s.TenantID || v.TenantID != s.TenantID {
			g.m.SettlementIdentityRows++
		}
		stateOK := rok && vok && r.BillingStatus == s.Status && v.Status == s.Status
		if s.Status == "settled" {
			stateOK = stateOK && s.Reason == "charged_usage"
		} else {
			stateOK = stateOK && s.Status == "released" && customerKeyGraphReleaseReason(s.Reason)
		}
		if !stateOK {
			g.m.SettlementStateRows++
		}
		if !g.settlementAmount(s, v, vok) {
			g.m.SettlementAmountRows++
		}
		if rok && r.TenantID == s.TenantID {
			if k, exists := g.keys[r.KeyID]; exists && k.TenantID == r.TenantID {
				g.spent[k.ID].add(s.ChargedAmount)
			}
		}
		if !g.settlementJournal(s) {
			g.m.SettlementJournalRows++
		}
	}
	return nil
}
func (g *customerKeyGraphData) refundShape(r CustomerKeyRefundRow) bool {
	if r.State == "pending" {
		return r.Approver == nil && r.JournalID == nil
	}
	if r.State != "approved" || r.Approver == nil || r.JournalID == nil || *r.Approver == r.Requester {
		return false
	}
	_, exists := g.users[*r.Approver]
	return exists
}
func (g *customerKeyGraphData) refundPass() error {
	for _, r := range g.in.Refunds {
		if err := g.w.tick(); err != nil {
			return err
		}
		s, sok := g.settlements[r.SettlementID]
		_, uok := g.users[r.Requester]
		if !sok || s.TenantID != r.TenantID || !uok {
			g.m.RefundIdentityRows++
		}
		amount, aok := procurementAttemptInt64(r.Amount)
		charge, cok := procurementAttemptInt64(s.ChargedAmount)
		if !g.refundShape(r) || !aok || amount.Sign() <= 0 || sok && cok && amount.Cmp(charge) > 0 {
			g.m.RefundShapeRows++
		}
		if r.State == "approved" {
			if sok && s.TenantID == r.TenantID {
				g.refunded[s.ID].add(r.Amount)
			}
			if !g.refundJournal(r) {
				g.m.RefundJournalRows++
			}
		} else if r.State == "pending" {
			if _, effect := g.operations[r.ID]; effect {
				g.m.PendingRefundEffectRows++
			}
		}
	}
	for _, s := range g.in.Settlements {
		if err := g.w.tick(); err != nil {
			return err
		}
		sum := g.refunded[s.ID]
		c, cok := procurementAttemptInt64(s.ChargedAmount)
		if !sum.equal(s.RefundedAmount) || sum.valid && cok && sum.value.Cmp(c) > 0 {
			g.m.SettlementRefundedRows++
		}
	}
	for _, k := range g.in.Keys {
		if err := g.w.tick(); err != nil {
			return err
		}
		if !g.held[k.ID].equal(k.Held) {
			g.m.KeyHeldRows++
		}
		if !g.spent[k.ID].equal(k.Spent) {
			g.m.KeySpentRows++
		}
		if k.LifetimeLimit != nil && customerKeyGraphMoney(k.Held, k.Spent, *k.LifetimeLimit) {
			h, _ := procurementAttemptInt64(k.Held)
			s, _ := procurementAttemptInt64(k.Spent)
			limit, _ := procurementAttemptInt64(*k.LifetimeLimit)
			if new(big.Int).Add(h, s).Cmp(limit) > 0 {
				g.m.KeyLimitRows++
			}
		}
	}
	return nil
}

func (g *customerKeyGraphData) journalMatches(j FundingGraphJournalRow, tenant, operation, kind, reference, actor string, deltas map[accountKindKey]*big.Int) bool {
	if j.TenantID != tenant || j.OperationID != operation || j.Kind != kind || j.Reference != reference || j.Actor != actor || j.Currency != "CNY" {
		return false
	}
	lines := g.lines[j.ID]
	if len(lines) != len(deltas) {
		return false
	}
	seen := make(map[accountKindKey]bool, len(deltas))
	for _, l := range lines {
		a, exists := g.accounts[l.AccountID]
		if !exists {
			return false
		}
		k := customerKeyGraphAccountKind(a)
		want, exists := deltas[k]
		got, ok := procurementBudgetSigned(l.SignedDelta)
		if !exists || !ok || !got.IsInt64() || seen[k] || got.Cmp(want) != 0 {
			return false
		}
		seen[k] = true
	}
	return true
}
func (g *customerKeyGraphData) reservationJournal(v CustomerKeyReservationRow) bool {
	r, exists := g.requests[v.RequestID]
	j, jok := g.operations[v.ID]
	amount, aok := procurementAttemptInt64(v.Amount)
	if !exists || !jok || !aok || r.TenantID != v.TenantID {
		return false
	}
	return g.journalMatches(j, v.TenantID, v.ID, "reserve", r.ID, r.OwnerID, map[accountKindKey]*big.Int{
		{tenant: v.TenantID, kind: "AVAILABLE"}: new(big.Int).Neg(amount),
		{tenant: v.TenantID, kind: "HELD"}:      amount,
	})
}
func (g *customerKeyGraphData) settlementJournal(s CustomerKeySettlementRow) bool {
	v, vok := g.reservationRequests[s.RequestID]
	r, rok := g.requests[s.RequestID]
	j, jok := g.journals[s.JournalID]
	amount, aok := procurementAttemptInt64(v.Amount)
	charge, cok := procurementAttemptInt64(s.ChargedAmount)
	if !vok || !rok || !jok || !aok || !cok || v.TenantID != s.TenantID || r.TenantID != s.TenantID || !procurementAttemptStatus(s.Status, "settled", "released") || charge.Cmp(amount) > 0 {
		return false
	}
	deltas := map[accountKindKey]*big.Int{
		{tenant: s.TenantID, kind: "HELD"}:      new(big.Int).Neg(amount),
		{tenant: s.TenantID, kind: "AVAILABLE"}: new(big.Int).Sub(amount, charge),
	}
	if charge.Sign() > 0 {
		deltas[accountKindKey{nullTenant: true, kind: "REVENUE"}] = charge
	}
	return g.journalMatches(j, s.TenantID, s.ID, s.Status, r.ID, "system", deltas)
}
func (g *customerKeyGraphData) refundJournal(r CustomerKeyRefundRow) bool {
	if !g.refundShape(r) || r.State != "approved" {
		return false
	}
	s, sok := g.settlements[r.SettlementID]
	j, jok := g.journals[*r.JournalID]
	amount, aok := procurementAttemptInt64(r.Amount)
	if !sok || !jok || !aok || amount.Sign() <= 0 || s.TenantID != r.TenantID {
		return false
	}
	return g.journalMatches(j, r.TenantID, r.ID, "refund", s.ID, *r.Approver, map[accountKindKey]*big.Int{
		{tenant: r.TenantID, kind: "AVAILABLE"}: amount,
		{nullTenant: true, kind: "REVENUE"}:     new(big.Int).Neg(amount),
	})
}
func (g *customerKeyGraphData) journalPass() error {
	for _, j := range g.in.Journals {
		if err := g.w.tick(); err != nil {
			return err
		}
		valid := true
		switch j.Kind {
		case "reserve":
			r, exists := g.reservations[j.OperationID]
			valid = exists && g.reservationJournal(r)
		case "settled", "released":
			s, exists := g.settlements[j.OperationID]
			valid = exists && s.JournalID == j.ID && g.settlementJournal(s)
		case "refund":
			r, exists := g.refunds[j.OperationID]
			valid = exists && r.JournalID != nil && *r.JournalID == j.ID && g.refundJournal(r)
		}
		if !valid {
			g.m.JournalSourceRows++
		}
	}
	return nil
}

// CheckCustomerKeyGraph compares supplied customer financial rows without
// authenticating completeness, repairing money or establishing historical
// authorization, delivery evidence, source lifetime or funding-lot coverage.
func CheckCustomerKeyGraph(ctx context.Context, in CustomerKeyGraphRows) (CustomerKeyGraphCheck, error) {
	if ctx == nil {
		return CustomerKeyGraphCheck{}, ErrCustomerKeyGraphRowsInput
	}
	if ctx.Err() != nil {
		return CustomerKeyGraphCheck{}, customerKeyGraphContextError(ctx)
	}
	w := &customerKeyGraphWork{ctx: ctx}
	invalid, err := customerKeyGraphBudget(w, in)
	if err != nil {
		return CustomerKeyGraphCheck{}, err
	}
	result := CustomerKeyGraphCheck{Scope: "customer_key_financial_graph_v1", Scanned: CustomerKeyGraphCounts{
		Tenants: uint64(len(in.Tenants)), Users: uint64(len(in.Users)), Keys: uint64(len(in.Keys)), Requests: uint64(len(in.Requests)),
		Reservations: uint64(len(in.Reservations)), Settlements: uint64(len(in.Settlements)), Refunds: uint64(len(in.Refunds)),
		Accounts: uint64(len(in.Accounts)), Journals: uint64(len(in.Journals)), Lines: uint64(len(in.Lines)),
	}}
	result.Mismatches.InvalidTextRows = invalid
	if err = customerKeyGraphValuePass(w, in, &result.Mismatches); err != nil {
		return CustomerKeyGraphCheck{}, err
	}
	g := customerKeyGraphData{w: w, m: &result.Mismatches, in: in}
	for _, pass := range []func() error{g.index, g.requestPass, g.settlementPass, g.refundPass, g.journalPass} {
		if err = pass(); err != nil {
			return CustomerKeyGraphCheck{}, err
		}
	}
	if ctx.Err() != nil {
		return CustomerKeyGraphCheck{}, customerKeyGraphContextError(ctx)
	}
	result.Balanced = result.Mismatches == (CustomerKeyGraphMismatches{})
	return result, nil
}
