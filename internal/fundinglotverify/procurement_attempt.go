package fundinglotverify

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrProcurementAttemptBindingRowsInput    = errors.New("funding_lot_procurement_attempt_binding_rows_input")
	ErrProcurementAttemptBindingRowsResource = errors.New("funding_lot_procurement_attempt_binding_rows_resource")
	ErrProcurementAttemptBindingRowsCanceled = errors.New("funding_lot_procurement_attempt_binding_rows_canceled")
	ErrProcurementAttemptBindingRowsTimeout  = errors.New("funding_lot_procurement_attempt_binding_rows_timeout")
)

type ProcurementAttemptChannelRow struct{ ID string }
type ProcurementAttemptCredentialRow struct{ ID, ChannelID string }
type ProcurementAttemptVersionRow struct{ ID, ChannelID, CredentialID string }
type ProcurementAttemptPoolMemberRow struct {
	ChannelVersionID, ChannelID, CredentialID string
	Position, Weight, MaxInflight             int32
}
type ProcurementAttemptTenantRow struct{ ID string }
type ProcurementAttemptRequestRow struct{ ID, TenantID, InputUpperBound, OutputLimit string }
type ProcurementAttemptCostVersionRow struct {
	ID, ChannelID, UpstreamModel string
	InputRate, OutputRate        *string
	Basis                        string
	EffectiveAt, CreatedAt       time.Time
}
type ProcurementAttemptBindingRow struct {
	ID, CredentialID, ChannelID, AccountID, OperationID string
	BoundAt                                             time.Time
}
type ProcurementAttemptRow struct {
	ID, TenantID, RequestID      string
	Sequence                     int32
	ChannelID, ChannelVersionID  string
	CredentialID, UpstreamModel  string
	DeliveryState                string
	CostVersionID, CostInputRate *string
	CostOutputRate               *string
	CostBasis, CostStatus        string
	CostAmount                   *string
	CreatedAt                    time.Time
}
type ProcurementAttemptReservationRow struct {
	AttemptID, AccountID, BindingID string
	InputBound, OutputBound         string
	UpperAmount, HeldAmount         string
	SpentAmount, State              string
	Anomaly                         bool
}
type ProcurementAttemptBindingRows struct {
	Channels        []ProcurementAttemptChannelRow
	Credentials     []ProcurementAttemptCredentialRow
	Versions        []ProcurementAttemptVersionRow
	PoolMemberships []ProcurementAttemptPoolMemberRow
	Tenants         []ProcurementAttemptTenantRow
	Requests        []ProcurementAttemptRequestRow
	CostVersions    []ProcurementAttemptCostVersionRow
	Accounts        []ProcurementBudgetAccountRow
	Bindings        []ProcurementAttemptBindingRow
	Attempts        []ProcurementAttemptRow
	Reservations    []ProcurementAttemptReservationRow
	BudgetEvents    []ProcurementBudgetEventRow
}

type ProcurementAttemptBindingCounts struct {
	Channels        uint64 `json:"channels"`
	Credentials     uint64 `json:"credentials"`
	Versions        uint64 `json:"versions"`
	PoolMemberships uint64 `json:"pool_memberships"`
	Tenants         uint64 `json:"tenants"`
	Requests        uint64 `json:"requests"`
	CostVersions    uint64 `json:"cost_versions"`
	Accounts        uint64 `json:"accounts"`
	Bindings        uint64 `json:"bindings"`
	Attempts        uint64 `json:"attempts"`
	Reservations    uint64 `json:"reservations"`
	BudgetEvents    uint64 `json:"budget_events"`
}

type ProcurementAttemptBindingMismatches struct {
	DuplicateChannelRows                uint64 `json:"duplicate_channel_rows"`
	DuplicateCredentialRows             uint64 `json:"duplicate_credential_rows"`
	DuplicateVersionRows                uint64 `json:"duplicate_version_rows"`
	DuplicatePoolMembershipRows         uint64 `json:"duplicate_pool_membership_rows"`
	DuplicatePoolPositionRows           uint64 `json:"duplicate_pool_position_rows"`
	DuplicateTenantRows                 uint64 `json:"duplicate_tenant_rows"`
	DuplicateRequestRows                uint64 `json:"duplicate_request_rows"`
	DuplicateCostVersionRows            uint64 `json:"duplicate_cost_version_rows"`
	DuplicateAccountRows                uint64 `json:"duplicate_account_rows"`
	DuplicateBindingRows                uint64 `json:"duplicate_binding_rows"`
	DuplicateBindingCredentialRows      uint64 `json:"duplicate_binding_credential_rows"`
	DuplicateBindingOperationRows       uint64 `json:"duplicate_binding_operation_rows"`
	DuplicateAttemptRows                uint64 `json:"duplicate_attempt_rows"`
	DuplicateAttemptRequestSequenceRows uint64 `json:"duplicate_attempt_request_sequence_rows"`
	DuplicateAttemptRequestChannelRows  uint64 `json:"duplicate_attempt_request_channel_rows"`
	DuplicateReservationRows            uint64 `json:"duplicate_reservation_rows"`
	DuplicateBudgetEventRows            uint64 `json:"duplicate_budget_event_rows"`
	InvalidTextRows                     uint64 `json:"invalid_text_rows"`
	InvalidAmountRows                   uint64 `json:"invalid_amount_rows"`
	InvalidEnumRows                     uint64 `json:"invalid_enum_rows"`
	InvalidAttemptSequenceRows          uint64 `json:"invalid_attempt_sequence_rows"`
	InvalidPoolBoundsRows               uint64 `json:"invalid_pool_bounds_rows"`
	CredentialChannelIdentityRows       uint64 `json:"credential_channel_identity_rows"`
	ChannelVersionIdentityRows          uint64 `json:"channel_version_identity_rows"`
	PoolMembershipIdentityRows          uint64 `json:"pool_membership_identity_rows"`
	VersionPrimaryMembershipRows        uint64 `json:"version_primary_membership_rows"`
	BindingIdentityRows                 uint64 `json:"binding_identity_rows"`
	AttemptRequestTenantRows            uint64 `json:"attempt_request_tenant_rows"`
	AttemptVersionIdentityRows          uint64 `json:"attempt_version_identity_rows"`
	AttemptPoolMembershipRows           uint64 `json:"attempt_pool_membership_rows"`
	AttemptCostQuoteRows                uint64 `json:"attempt_cost_quote_rows"`
	AttemptCostShapeRows                uint64 `json:"attempt_cost_shape_rows"`
	AttemptReservationMissingRows       uint64 `json:"attempt_reservation_missing_rows"`
	ReservationIdentityRows             uint64 `json:"reservation_identity_rows"`
	ReservationRequestBoundRows         uint64 `json:"reservation_request_bound_rows"`
	ReservationUpperBoundRows           uint64 `json:"reservation_upper_bound_rows"`
}

type ProcurementAttemptBindingCheck struct {
	Scope      string                              `json:"scope"`
	Scanned    ProcurementAttemptBindingCounts     `json:"scanned"`
	Balanced   bool                                `json:"balanced"`
	Mismatches ProcurementAttemptBindingMismatches `json:"mismatches"`
}

type procurementAttemptWork struct {
	ctx   context.Context
	steps uint16
	bytes int
}

func (w *procurementAttemptWork) tick() error {
	w.steps++
	if w.steps&255 == 0 && w.ctx.Err() != nil {
		return procurementAttemptContextError(w.ctx)
	}
	return nil
}

func procurementAttemptContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrProcurementAttemptBindingRowsTimeout
	}
	return ErrProcurementAttemptBindingRowsCanceled
}

func procurementAttemptValidText(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func procurementAttemptIndexableText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func (w *procurementAttemptWork) rowText(required []string, optional ...*string) (bool, error) {
	valid := true
	consume := func(value string) error {
		if len(value) > fundingSourceByteLimit-w.bytes {
			return ErrProcurementAttemptBindingRowsResource
		}
		w.bytes += len(value)
		if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			valid = false
		}
		return nil
	}
	for _, value := range required {
		if err := consume(value); err != nil {
			return false, err
		}
		if value == "" {
			valid = false
		}
	}
	for _, value := range optional {
		if value == nil {
			continue
		}
		if err := consume(*value); err != nil {
			return false, err
		}
		if *value == "" {
			valid = false
		}
	}
	return valid, nil
}

func (w *procurementAttemptWork) rowTenantText(required []string, tenant string, optional ...*string) (bool, error) {
	valid, err := w.rowText(required, optional...)
	if err != nil {
		return false, err
	}
	if len(tenant) > fundingSourceByteLimit-w.bytes {
		return false, ErrProcurementAttemptBindingRowsResource
	}
	w.bytes += len(tenant)
	return valid && legacyTenantText(tenant), nil
}

func procurementAttemptRowCount(rows ProcurementAttemptBindingRows) (uint64, bool) {
	remaining := uint64(fundingSourceRowLimit)
	counts := [...]int{
		len(rows.Channels), len(rows.Credentials), len(rows.Versions), len(rows.PoolMemberships),
		len(rows.Tenants), len(rows.Requests), len(rows.CostVersions), len(rows.Accounts),
		len(rows.Bindings), len(rows.Attempts), len(rows.Reservations), len(rows.BudgetEvents),
	}
	var total uint64
	for _, count := range counts {
		if count < 0 || uint64(count) > remaining {
			return 0, false
		}
		remaining -= uint64(count)
		total += uint64(count)
	}
	return total, true
}

func procurementAttemptInt64(value string) (*big.Int, bool) {
	n, ok := procurementBudgetUnsigned(value)
	return n, ok && n.IsInt64()
}

func procurementAttemptNumeric(value string) (*big.Int, bool) {
	return procurementBudgetUnsigned(value)
}

func procurementAttemptSignedNumeric(value string) (*big.Int, bool) {
	return procurementBudgetSigned(value)
}

func procurementAttemptOptionalInt64(value *string) bool {
	if value == nil {
		return true
	}
	_, ok := procurementAttemptInt64(*value)
	return ok
}

func procurementAttemptOptionalNumeric(value *string) bool {
	if value == nil {
		return true
	}
	_, ok := procurementAttemptNumeric(*value)
	return ok
}

func procurementAttemptStatus(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func procurementAttemptTextPass(w *procurementAttemptWork, rows ProcurementAttemptBindingRows) (uint64, error) {
	var invalid uint64
	check := func(required []string, optional ...*string) error {
		if err := w.tick(); err != nil {
			return err
		}
		ok, err := w.rowText(required, optional...)
		if err != nil {
			return err
		}
		if !ok {
			invalid++
		}
		return nil
	}
	checkTenant := func(required []string, tenant string, optional ...*string) error {
		if err := w.tick(); err != nil {
			return err
		}
		valid, err := w.rowTenantText(required, tenant, optional...)
		if err != nil {
			return err
		}
		if !valid {
			invalid++
		}
		return nil
	}
	for _, row := range rows.Channels {
		if err := check([]string{row.ID}); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Credentials {
		if err := check([]string{row.ID, row.ChannelID}); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Versions {
		if err := check([]string{row.ID, row.ChannelID, row.CredentialID}); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.PoolMemberships {
		if err := check([]string{row.ChannelVersionID, row.ChannelID, row.CredentialID}); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Tenants {
		if err := checkTenant(nil, row.ID); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Requests {
		if err := checkTenant([]string{row.ID, row.InputUpperBound, row.OutputLimit}, row.TenantID); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.CostVersions {
		if err := check([]string{row.ID, row.ChannelID, row.UpstreamModel, row.Basis}, row.InputRate, row.OutputRate); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Accounts {
		if err := check([]string{row.ID, row.HeldAmount, row.SpentAmount}); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Bindings {
		if err := check([]string{row.ID, row.CredentialID, row.ChannelID, row.AccountID, row.OperationID}); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Attempts {
		if err := checkTenant([]string{row.ID, row.RequestID, row.ChannelID, row.ChannelVersionID, row.CredentialID, row.UpstreamModel, row.DeliveryState, row.CostBasis, row.CostStatus}, row.TenantID, row.CostVersionID, row.CostInputRate, row.CostOutputRate, row.CostAmount); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.Reservations {
		if err := check([]string{row.AttemptID, row.AccountID, row.BindingID, row.InputBound, row.OutputBound, row.UpperAmount, row.HeldAmount, row.SpentAmount, row.State}); err != nil {
			return 0, err
		}
	}
	for _, row := range rows.BudgetEvents {
		if err := check([]string{row.ID, row.EventKey, row.AccountID, row.AttemptID, row.HeldDelta, row.SpentDelta}); err != nil {
			return 0, err
		}
	}
	return invalid, nil
}

func procurementAttemptAmountPass(w *procurementAttemptWork, rows ProcurementAttemptBindingRows) (uint64, uint64, error) {
	var invalidAmounts, invalidPoolBounds uint64
	for _, row := range rows.PoolMemberships {
		if err := w.tick(); err != nil {
			return 0, 0, err
		}
		if row.Position < 1 || row.Position > 32 || row.Weight < 1 || row.Weight > 1_000_000 || row.MaxInflight < 1 || row.MaxInflight > 1000 {
			invalidPoolBounds++
		}
	}
	for _, row := range rows.Requests {
		if err := w.tick(); err != nil {
			return 0, 0, err
		}
		_, inputOK := procurementAttemptInt64(row.InputUpperBound)
		_, outputOK := procurementAttemptInt64(row.OutputLimit)
		if !inputOK || !outputOK {
			invalidAmounts++
		}
	}
	for _, row := range rows.CostVersions {
		if err := w.tick(); err != nil {
			return 0, 0, err
		}
		if !procurementAttemptOptionalInt64(row.InputRate) || !procurementAttemptOptionalInt64(row.OutputRate) {
			invalidAmounts++
		}
	}
	for _, row := range rows.Accounts {
		if err := w.tick(); err != nil {
			return 0, 0, err
		}
		_, heldOK := procurementAttemptNumeric(row.HeldAmount)
		_, spentOK := procurementAttemptNumeric(row.SpentAmount)
		if !heldOK || !spentOK {
			invalidAmounts++
		}
	}
	for _, row := range rows.Attempts {
		if err := w.tick(); err != nil {
			return 0, 0, err
		}
		if !procurementAttemptOptionalInt64(row.CostInputRate) || !procurementAttemptOptionalInt64(row.CostOutputRate) || !procurementAttemptOptionalNumeric(row.CostAmount) {
			invalidAmounts++
		}
	}
	for _, row := range rows.Reservations {
		if err := w.tick(); err != nil {
			return 0, 0, err
		}
		_, inputOK := procurementAttemptPositiveInt64(row.InputBound)
		_, outputOK := procurementAttemptInt64(row.OutputBound)
		upper, upperOK := procurementAttemptNumeric(row.UpperAmount)
		_, heldOK := procurementAttemptNumeric(row.HeldAmount)
		_, spentOK := procurementAttemptNumeric(row.SpentAmount)
		if !inputOK || !outputOK || !upperOK || !upper.IsInt64() || !heldOK || !spentOK {
			invalidAmounts++
		}
	}
	for _, row := range rows.BudgetEvents {
		if err := w.tick(); err != nil {
			return 0, 0, err
		}
		_, heldOK := procurementAttemptSignedNumeric(row.HeldDelta)
		_, spentOK := procurementAttemptSignedNumeric(row.SpentDelta)
		if !heldOK || !spentOK {
			invalidAmounts++
		}
	}
	return invalidAmounts, invalidPoolBounds, nil
}

func procurementAttemptSequencePass(w *procurementAttemptWork, rows ProcurementAttemptBindingRows) (uint64, error) {
	var invalid uint64
	for _, row := range rows.Attempts {
		if err := w.tick(); err != nil {
			return 0, err
		}
		if _, ok := procurementAttemptSequence(row.Sequence); !ok {
			invalid++
		}
	}
	return invalid, nil
}

func procurementAttemptSequence(value int32) (int32, bool) { return value, value >= 1 && value <= 2 }

func procurementAttemptPositiveInt64(value string) (*big.Int, bool) {
	n, ok := procurementAttemptInt64(value)
	return n, ok && n.Sign() > 0
}

func procurementAttemptEnumPass(w *procurementAttemptWork, rows ProcurementAttemptBindingRows) (uint64, error) {
	var invalid uint64
	for _, row := range rows.CostVersions {
		if err := w.tick(); err != nil {
			return 0, err
		}
		if !procurementAttemptStatus(row.Basis, "contract", "invoice", "unknown") {
			invalid++
		}
	}
	for _, row := range rows.Attempts {
		if err := w.tick(); err != nil {
			return 0, err
		}
		if !procurementAttemptStatus(row.DeliveryState, "prepared", "sending", "completed", "failed", "unknown") ||
			!procurementAttemptStatus(row.CostBasis, "contract", "invoice", "unknown") ||
			!procurementAttemptStatus(row.CostStatus, "unknown", "contract", "invoice") {
			invalid++
		}
	}
	for _, row := range rows.Reservations {
		if err := w.tick(); err != nil {
			return 0, err
		}
		if !procurementAttemptStatus(row.State, "prepared", "sending", "unknown", "settled", "released", "disputed") {
			invalid++
		}
	}
	return invalid, nil
}

func procurementAttemptOptionalEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

type procurementAttemptPoolKey struct{ version, channel, credential string }
type procurementAttemptPoolPrimaryKey struct{ version, credential string }
type procurementAttemptPositionKey struct {
	version  string
	position int32
}
type procurementAttemptRequestSequenceKey struct {
	request  string
	sequence int32
}
type procurementAttemptRequestChannelKey struct{ request, channel string }

// CheckProcurementAttemptBindingGraph independently checks the supplied
// permanent binding, route snapshot, quote snapshot and reservation facts.
// It reports only this local graph; the fixed database reader owns EOF proof.
func CheckProcurementAttemptBindingGraph(ctx context.Context, rows ProcurementAttemptBindingRows) (ProcurementAttemptBindingCheck, error) {
	if ctx == nil {
		return ProcurementAttemptBindingCheck{}, ErrProcurementAttemptBindingRowsInput
	}
	if ctx.Err() != nil {
		return ProcurementAttemptBindingCheck{}, procurementAttemptContextError(ctx)
	}
	rowCount, withinLimit := procurementAttemptRowCount(rows)
	if !withinLimit {
		return ProcurementAttemptBindingCheck{}, ErrProcurementAttemptBindingRowsResource
	}
	w := procurementAttemptWork{ctx: ctx}
	invalidText, err := procurementAttemptTextPass(&w, rows)
	if err != nil {
		return ProcurementAttemptBindingCheck{}, err
	}
	invalidAmounts, invalidPoolBounds, err := procurementAttemptAmountPass(&w, rows)
	if err != nil {
		return ProcurementAttemptBindingCheck{}, err
	}
	invalidSequence, err := procurementAttemptSequencePass(&w, rows)
	if err != nil {
		return ProcurementAttemptBindingCheck{}, err
	}
	invalidEnums, err := procurementAttemptEnumPass(&w, rows)
	if err != nil {
		return ProcurementAttemptBindingCheck{}, err
	}
	if ctx.Err() != nil {
		return ProcurementAttemptBindingCheck{}, procurementAttemptContextError(ctx)
	}

	out := ProcurementAttemptBindingCheck{
		Scope: "procurement_attempt_binding_graph_v1",
		Scanned: ProcurementAttemptBindingCounts{
			Channels: uint64(len(rows.Channels)), Credentials: uint64(len(rows.Credentials)), Versions: uint64(len(rows.Versions)), PoolMemberships: uint64(len(rows.PoolMemberships)),
			Tenants: uint64(len(rows.Tenants)), Requests: uint64(len(rows.Requests)), CostVersions: uint64(len(rows.CostVersions)), Accounts: uint64(len(rows.Accounts)),
			Bindings: uint64(len(rows.Bindings)), Attempts: uint64(len(rows.Attempts)), Reservations: uint64(len(rows.Reservations)), BudgetEvents: uint64(len(rows.BudgetEvents)),
		},
		Mismatches: ProcurementAttemptBindingMismatches{InvalidTextRows: invalidText, InvalidAmountRows: invalidAmounts, InvalidEnumRows: invalidEnums, InvalidAttemptSequenceRows: invalidSequence, InvalidPoolBoundsRows: invalidPoolBounds},
	}
	_ = rowCount // The count was checked before any map or summary allocation.

	channels := make(map[string]struct{}, len(rows.Channels))
	credentials := make(map[string]ProcurementAttemptCredentialRow, len(rows.Credentials))
	versions := make(map[string]ProcurementAttemptVersionRow, len(rows.Versions))
	pools := make(map[procurementAttemptPoolKey]ProcurementAttemptPoolMemberRow, len(rows.PoolMemberships))
	tenants := make(map[string]struct{}, len(rows.Tenants))
	requests := make(map[string]ProcurementAttemptRequestRow, len(rows.Requests))
	costVersions := make(map[string]ProcurementAttemptCostVersionRow, len(rows.CostVersions))
	accounts := make(map[string]ProcurementBudgetAccountRow, len(rows.Accounts))
	bindings := make(map[string]ProcurementAttemptBindingRow, len(rows.Bindings))
	bindingsByCredential := make(map[string]ProcurementAttemptBindingRow, len(rows.Bindings))
	attempts := make(map[string]ProcurementAttemptRow, len(rows.Attempts))
	reservations := make(map[string]ProcurementAttemptReservationRow, len(rows.Reservations))

	canonicalCredentials := make([]ProcurementAttemptCredentialRow, 0, len(rows.Credentials))
	canonicalVersions := make([]ProcurementAttemptVersionRow, 0, len(rows.Versions))
	canonicalPoolRows := make([]ProcurementAttemptPoolMemberRow, 0, len(rows.PoolMemberships))
	channelIDs := make(map[string]struct{}, len(rows.Channels))
	for _, row := range rows.Channels {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptIndexableText(row.ID) {
			continue
		}
		if _, exists := channelIDs[row.ID]; exists {
			out.Mismatches.DuplicateChannelRows++
			continue
		}
		channelIDs[row.ID] = struct{}{}
		channels[row.ID] = struct{}{}
	}
	credentialIDs := make(map[string]struct{}, len(rows.Credentials))
	for _, row := range rows.Credentials {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptIndexableText(row.ID) {
			canonicalCredentials = append(canonicalCredentials, row)
			continue
		}
		if _, exists := credentialIDs[row.ID]; exists {
			out.Mismatches.DuplicateCredentialRows++
			continue
		}
		credentialIDs[row.ID] = struct{}{}
		credentials[row.ID] = row
		canonicalCredentials = append(canonicalCredentials, row)
	}
	versionIDs := make(map[string]struct{}, len(rows.Versions))
	for _, row := range rows.Versions {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptIndexableText(row.ID) {
			canonicalVersions = append(canonicalVersions, row)
			continue
		}
		if _, exists := versionIDs[row.ID]; exists {
			out.Mismatches.DuplicateVersionRows++
			continue
		}
		versionIDs[row.ID] = struct{}{}
		versions[row.ID] = row
		canonicalVersions = append(canonicalVersions, row)
	}
	poolPrimary := make(map[procurementAttemptPoolPrimaryKey]struct{}, len(rows.PoolMemberships))
	poolPositions := make(map[procurementAttemptPositionKey]struct{}, len(rows.PoolMemberships))
	for _, row := range rows.PoolMemberships {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptIndexableText(row.ChannelVersionID) || !procurementAttemptIndexableText(row.CredentialID) {
			canonicalPoolRows = append(canonicalPoolRows, row)
			continue
		}
		primary := procurementAttemptPoolPrimaryKey{row.ChannelVersionID, row.CredentialID}
		duplicatePrimary := false
		if _, exists := poolPrimary[primary]; exists {
			out.Mismatches.DuplicatePoolMembershipRows++
			duplicatePrimary = true
		} else {
			poolPrimary[primary] = struct{}{}
		}
		position := procurementAttemptPositionKey{row.ChannelVersionID, row.Position}
		if _, exists := poolPositions[position]; exists {
			out.Mismatches.DuplicatePoolPositionRows++
		} else {
			poolPositions[position] = struct{}{}
		}
		if duplicatePrimary {
			continue
		}
		canonicalPoolRows = append(canonicalPoolRows, row)
		if procurementAttemptIndexableText(row.ChannelID) {
			pools[procurementAttemptPoolKey{row.ChannelVersionID, row.ChannelID, row.CredentialID}] = row
		}
	}
	tenantIDs := make(map[string]struct{}, len(rows.Tenants))
	for _, row := range rows.Tenants {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptIndexableText(row.ID) {
			continue
		}
		if _, exists := tenantIDs[row.ID]; exists {
			out.Mismatches.DuplicateTenantRows++
			continue
		}
		tenantIDs[row.ID] = struct{}{}
		tenants[row.ID] = struct{}{}
	}
	requestIDs := make(map[string]struct{}, len(rows.Requests))
	for _, row := range rows.Requests {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptIndexableText(row.ID) {
			continue
		}
		if _, exists := requestIDs[row.ID]; exists {
			out.Mismatches.DuplicateRequestRows++
			continue
		}
		requestIDs[row.ID] = struct{}{}
		requests[row.ID] = row
	}
	costVersionIDs := make(map[string]struct{}, len(rows.CostVersions))
	for _, row := range rows.CostVersions {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptIndexableText(row.ID) {
			continue
		}
		if _, exists := costVersionIDs[row.ID]; exists {
			out.Mismatches.DuplicateCostVersionRows++
			continue
		}
		costVersionIDs[row.ID] = struct{}{}
		costVersions[row.ID] = row
	}
	accountIDs := make(map[string]struct{}, len(rows.Accounts))
	for _, row := range rows.Accounts {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptIndexableText(row.ID) {
			continue
		}
		if _, exists := accountIDs[row.ID]; exists {
			out.Mismatches.DuplicateAccountRows++
			continue
		}
		accountIDs[row.ID] = struct{}{}
		accounts[row.ID] = row
	}
	bindingIDs := make(map[string]struct{}, len(rows.Bindings))
	bindingCredentials := make(map[string]struct{}, len(rows.Bindings))
	bindingOperations := make(map[string]struct{}, len(rows.Bindings))
	canonicalBindings := make([]ProcurementAttemptBindingRow, 0, len(rows.Bindings))
	for _, row := range rows.Bindings {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		duplicatePrimary := false
		if procurementAttemptIndexableText(row.ID) {
			if _, exists := bindingIDs[row.ID]; exists {
				out.Mismatches.DuplicateBindingRows++
				duplicatePrimary = true
			} else {
				bindingIDs[row.ID] = struct{}{}
			}
		}
		duplicateUnique := false
		if procurementAttemptIndexableText(row.CredentialID) {
			if _, exists := bindingCredentials[row.CredentialID]; exists {
				out.Mismatches.DuplicateBindingCredentialRows++
				duplicateUnique = true
			}
			bindingCredentials[row.CredentialID] = struct{}{}
		}
		if procurementAttemptIndexableText(row.OperationID) {
			if _, exists := bindingOperations[row.OperationID]; exists {
				out.Mismatches.DuplicateBindingOperationRows++
				duplicateUnique = true
			}
			bindingOperations[row.OperationID] = struct{}{}
		}
		if duplicatePrimary || duplicateUnique {
			continue
		}
		if procurementAttemptIndexableText(row.ID) {
			bindings[row.ID] = row
		}
		canonicalBindings = append(canonicalBindings, row)
		if procurementAttemptIndexableText(row.CredentialID) {
			bindingsByCredential[row.CredentialID] = row
		}
	}
	attemptIDs := make(map[string]struct{}, len(rows.Attempts))
	attemptSequences := make(map[procurementAttemptRequestSequenceKey]struct{}, len(rows.Attempts))
	attemptChannels := make(map[procurementAttemptRequestChannelKey]struct{}, len(rows.Attempts))
	canonicalAttempts := make([]ProcurementAttemptRow, 0, len(rows.Attempts))
	for _, row := range rows.Attempts {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		duplicatePrimary := false
		if procurementAttemptIndexableText(row.ID) {
			if _, exists := attemptIDs[row.ID]; exists {
				out.Mismatches.DuplicateAttemptRows++
				duplicatePrimary = true
			} else {
				attemptIDs[row.ID] = struct{}{}
			}
		}
		duplicateUnique := false
		if procurementAttemptIndexableText(row.RequestID) {
			sequenceKey := procurementAttemptRequestSequenceKey{row.RequestID, row.Sequence}
			if _, exists := attemptSequences[sequenceKey]; exists {
				out.Mismatches.DuplicateAttemptRequestSequenceRows++
				duplicateUnique = true
			}
			attemptSequences[sequenceKey] = struct{}{}
			channelKey := procurementAttemptRequestChannelKey{row.RequestID, row.ChannelID}
			if procurementAttemptIndexableText(row.ChannelID) {
				if _, exists := attemptChannels[channelKey]; exists {
					out.Mismatches.DuplicateAttemptRequestChannelRows++
					duplicateUnique = true
				}
				attemptChannels[channelKey] = struct{}{}
			}
		}
		if duplicatePrimary || duplicateUnique {
			continue
		}
		if procurementAttemptIndexableText(row.ID) {
			attempts[row.ID] = row
		}
		canonicalAttempts = append(canonicalAttempts, row)
	}
	reservationIDs := make(map[string]struct{}, len(rows.Reservations))
	canonicalReservations := make([]ProcurementAttemptReservationRow, 0, len(rows.Reservations))
	for _, row := range rows.Reservations {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptIndexableText(row.AttemptID) {
			canonicalReservations = append(canonicalReservations, row)
			continue
		}
		if _, exists := reservationIDs[row.AttemptID]; exists {
			out.Mismatches.DuplicateReservationRows++
			continue
		}
		reservationIDs[row.AttemptID] = struct{}{}
		reservations[row.AttemptID] = row
		canonicalReservations = append(canonicalReservations, row)
	}
	eventIDs := make(map[string]struct{}, len(rows.BudgetEvents))
	for _, row := range rows.BudgetEvents {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if procurementAttemptIndexableText(row.ID) {
			if _, exists := eventIDs[row.ID]; exists {
				out.Mismatches.DuplicateBudgetEventRows++
				continue
			}
			eventIDs[row.ID] = struct{}{}
		}
	}

	for _, row := range canonicalCredentials {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptValidText(row.ChannelID) {
			out.Mismatches.CredentialChannelIdentityRows++
			continue
		}
		if _, exists := channels[row.ChannelID]; !exists {
			out.Mismatches.CredentialChannelIdentityRows++
		}
	}
	for _, row := range canonicalVersions {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		_, channelOK := channels[row.ChannelID]
		credential, credentialOK := credentials[row.CredentialID]
		if !procurementAttemptValidText(row.ChannelID) || !channelOK || !procurementAttemptValidText(row.CredentialID) || !credentialOK || credential.ChannelID != row.ChannelID {
			out.Mismatches.ChannelVersionIdentityRows++
		}
	}
	for _, row := range canonicalPoolRows {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptValidText(row.ChannelVersionID) || !procurementAttemptValidText(row.ChannelID) || !procurementAttemptValidText(row.CredentialID) {
			out.Mismatches.PoolMembershipIdentityRows++
			continue
		}
		version, versionOK := versions[row.ChannelVersionID]
		credential, credentialOK := credentials[row.CredentialID]
		if !versionOK || !credentialOK || version.ChannelID != row.ChannelID || credential.ChannelID != row.ChannelID {
			out.Mismatches.PoolMembershipIdentityRows++
		}
	}
	for _, row := range canonicalVersions {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		if !procurementAttemptValidText(row.ID) || !procurementAttemptValidText(row.ChannelID) || !procurementAttemptValidText(row.CredentialID) {
			out.Mismatches.VersionPrimaryMembershipRows++
		} else if _, exists := pools[procurementAttemptPoolKey{row.ID, row.ChannelID, row.CredentialID}]; !exists {
			out.Mismatches.VersionPrimaryMembershipRows++
		}
	}
	for _, row := range canonicalBindings {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		credential, credentialOK := credentials[row.CredentialID]
		_, channelOK := channels[row.ChannelID]
		_, accountOK := accounts[row.AccountID]
		if !procurementAttemptValidText(row.CredentialID) || !credentialOK || !procurementAttemptValidText(row.ChannelID) || !channelOK || !procurementAttemptValidText(row.AccountID) || !accountOK || credential.ChannelID != row.ChannelID {
			out.Mismatches.BindingIdentityRows++
		}
	}
	for _, row := range canonicalAttempts {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		request, requestOK := requests[row.RequestID]
		_, tenantOK := tenants[row.TenantID]
		if !procurementAttemptValidText(row.RequestID) || !requestOK || !legacyTenantText(row.TenantID) || !tenantOK || request.TenantID != row.TenantID {
			out.Mismatches.AttemptRequestTenantRows++
		}
		version, versionOK := versions[row.ChannelVersionID]
		_, channelOK := channels[row.ChannelID]
		if !procurementAttemptValidText(row.ChannelVersionID) || !versionOK || !procurementAttemptValidText(row.ChannelID) || !channelOK || version.ChannelID != row.ChannelID {
			out.Mismatches.AttemptVersionIdentityRows++
		}
		if !procurementAttemptValidText(row.ChannelVersionID) || !procurementAttemptValidText(row.ChannelID) || !procurementAttemptValidText(row.CredentialID) {
			out.Mismatches.AttemptPoolMembershipRows++
		} else if _, exists := pools[procurementAttemptPoolKey{row.ChannelVersionID, row.ChannelID, row.CredentialID}]; !exists {
			out.Mismatches.AttemptPoolMembershipRows++
		}
		if !procurementAttemptQuoteMatches(row, costVersions) {
			out.Mismatches.AttemptCostQuoteRows++
		}
		if !procurementAttemptCostShapeValid(row) {
			out.Mismatches.AttemptCostShapeRows++
		}
	}
	for _, row := range canonicalAttempts {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		binding, bound := bindingsByCredential[row.CredentialID]
		if bound && procurementAttemptValidText(row.CredentialID) && procurementAttemptValidText(row.ChannelID) && binding.ChannelID == row.ChannelID && !binding.BoundAt.After(row.CreatedAt) {
			if _, exists := reservations[row.ID]; !exists {
				out.Mismatches.AttemptReservationMissingRows++
			}
		}
	}
	for _, row := range canonicalReservations {
		if err = w.tick(); err != nil {
			return ProcurementAttemptBindingCheck{}, err
		}
		attempt, attemptOK := attempts[row.AttemptID]
		binding, bindingOK := bindings[row.BindingID]
		_, accountOK := accounts[row.AccountID]
		identityMismatch := !procurementAttemptValidText(row.AttemptID) || !procurementAttemptValidText(row.BindingID) || !procurementAttemptValidText(row.AccountID) || !attemptOK || !bindingOK || !accountOK
		if attemptOK && bindingOK && (binding.AccountID != row.AccountID || binding.CredentialID != attempt.CredentialID || binding.ChannelID != attempt.ChannelID || binding.BoundAt.After(attempt.CreatedAt)) {
			identityMismatch = true
		}
		if identityMismatch {
			out.Mismatches.ReservationIdentityRows++
		}
		if !attemptOK {
			out.Mismatches.ReservationUpperBoundRows++
			continue
		}
		input, inputOK := procurementAttemptPositiveInt64(row.InputBound)
		output, outputOK := procurementAttemptInt64(row.OutputBound)
		if !procurementAttemptReservationUpperMatches(row, attempt, costVersions, input, output, inputOK, outputOK) {
			out.Mismatches.ReservationUpperBoundRows++
		}
		request, requestOK := requests[attempt.RequestID]
		if requestOK && procurementAttemptValidText(attempt.RequestID) {
			requestInput, requestInputOK := procurementAttemptInt64(request.InputUpperBound)
			requestOutput, requestOutputOK := procurementAttemptInt64(request.OutputLimit)
			if inputOK && outputOK && requestInputOK && requestOutputOK && (input.Cmp(requestInput) != 0 || output.Cmp(requestOutput) != 0) {
				out.Mismatches.ReservationRequestBoundRows++
			}
		}
	}
	if ctx.Err() != nil {
		return ProcurementAttemptBindingCheck{}, procurementAttemptContextError(ctx)
	}
	out.Balanced = out.Mismatches == (ProcurementAttemptBindingMismatches{})
	return out, nil
}

func procurementAttemptQuoteMatches(row ProcurementAttemptRow, costs map[string]ProcurementAttemptCostVersionRow) bool {
	if row.CostVersionID == nil {
		return row.CostInputRate == nil && row.CostOutputRate == nil && row.CostBasis == "unknown"
	}
	if !procurementAttemptValidText(*row.CostVersionID) || !procurementAttemptValidText(row.ChannelID) || !procurementAttemptValidText(row.UpstreamModel) {
		return false
	}
	cost, exists := costs[*row.CostVersionID]
	if !exists {
		return false
	}
	return cost.ChannelID == row.ChannelID && cost.UpstreamModel == row.UpstreamModel &&
		procurementAttemptOptionalEqual(cost.InputRate, row.CostInputRate) &&
		procurementAttemptOptionalEqual(cost.OutputRate, row.CostOutputRate) && cost.Basis == row.CostBasis &&
		!cost.EffectiveAt.After(row.CreatedAt)
}

func procurementAttemptCostShapeValid(row ProcurementAttemptRow) bool {
	if row.CostVersionID == nil && (row.CostInputRate != nil || row.CostOutputRate != nil || row.CostBasis != "unknown") {
		return false
	}
	if !procurementAttemptStatus(row.CostStatus, "unknown", "contract", "invoice") {
		return false
	}
	if row.CostStatus == "unknown" {
		return row.CostAmount == nil
	}
	if row.CostAmount == nil {
		return false
	}
	_, ok := procurementAttemptNumeric(*row.CostAmount)
	return ok
}

func procurementAttemptKnownQuote(row ProcurementAttemptRow, costs map[string]ProcurementAttemptCostVersionRow) (*big.Int, *big.Int, bool) {
	if row.CostVersionID == nil || !procurementAttemptQuoteMatches(row, costs) ||
		(row.CostBasis != "contract" && row.CostBasis != "invoice") || row.CostInputRate == nil || row.CostOutputRate == nil {
		return nil, nil, false
	}
	inputRate, inputOK := procurementAttemptInt64(*row.CostInputRate)
	outputRate, outputOK := procurementAttemptInt64(*row.CostOutputRate)
	if !inputOK || !outputOK {
		return nil, nil, false
	}
	return inputRate, outputRate, true
}

func procurementAttemptReservationUpperMatches(reservation ProcurementAttemptReservationRow, attempt ProcurementAttemptRow, costs map[string]ProcurementAttemptCostVersionRow, inputBound, outputBound *big.Int, inputOK, outputOK bool) bool {
	inputRate, outputRate, quoteOK := procurementAttemptKnownQuote(attempt, costs)
	if !quoteOK {
		return false
	}
	if !inputOK || !outputOK {
		return true
	}
	expected := new(big.Int).Mul(inputBound, inputRate)
	expected.Add(expected, new(big.Int).Mul(outputBound, outputRate))
	expected.Add(expected, big.NewInt(999999))
	expected.Div(expected, big.NewInt(1_000_000))
	if !expected.IsInt64() {
		return false
	}
	actual, ok := procurementAttemptNumeric(reservation.UpperAmount)
	return ok && actual.IsInt64() && actual.Cmp(expected) == 0
}
