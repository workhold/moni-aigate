package fundinglotverify

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrSandboxSourceGraphRowsInput    = errors.New("funding_lot_sandbox_source_graph_rows_input")
	ErrSandboxSourceGraphRowsResource = errors.New("funding_lot_sandbox_source_graph_rows_resource")
	ErrSandboxSourceGraphRowsCanceled = errors.New("funding_lot_sandbox_source_graph_rows_canceled")
	ErrSandboxSourceGraphRowsTimeout  = errors.New("funding_lot_sandbox_source_graph_rows_timeout")
)

type SandboxGraphEnvironmentRow struct {
	Singleton                                                                                                            bool
	ID, MerchantID, PayeeLabel, DatabaseOID, SchemaOID, NonceDigest, AppRole, WorkerRole, ProjectorRole, ProtocolVersion string
	CreatedAt                                                                                                            time.Time
}
type SandboxGraphAttestationRow struct {
	Singleton                                                                                                                                           bool
	EnvironmentID, DatabaseOID, SchemaOID, SchemaOwnerOID, AppRoleOID, WorkerRoleOID, ProjectorRoleOID, AppNonceDigest, WorkerNonceDigest, ProofVersion string
	ProofCanonical                                                                                                                                      []byte
	ProofSHA256                                                                                                                                         string
	CreatedAt                                                                                                                                           time.Time
}
type SandboxGraphPolicyRow struct {
	Singleton, Enabled bool
	Revision           string
}
type SandboxGraphSKURow struct {
	ID, Revision, LastVersion string
	Enabled                   bool
	CurrentVersionID          *string
	CreatedBy                 string
	CreatedAt                 time.Time
	LastChangeOperationID     *string
}
type SandboxGraphSKUVersionRow struct {
	ID, SKUID, Version, DisplayName, Amount, WalletCreditAmount, Currency, EnvironmentID, MerchantID, PayeeLabel, CheckoutTTLSeconds, PublishOperationID, CreatedBy string
	CreatedAt                                                                                                                                                       time.Time
}
type SandboxGraphSKUChangeRow struct {
	OperationID, SKUID, Kind, BeforeRevision, AfterRevision string
	BeforeVersionID, AfterVersionID, PublishedVersionID     *string
	BeforeEnabled, AfterEnabled                             bool
	CreatedAt                                               time.Time
	BeforeOperationID                                       *string
	CreatedXID                                              string
}
type SandboxGraphOrderRow struct {
	ID, TenantID, Creator, SKUVersionID, DisplayName, Amount, WalletCreditAmount, Currency, EnvironmentID, MerchantID, PayeeLabel, MerchantOrderID string
	CreatedAt, ExpiresAt                                                                                                                           time.Time
}
type SandboxGraphCancelIntentRow struct {
	OrderID, OperationID, Actor string
	CreatedAt                   time.Time
}
type SandboxGraphCommandOutboxRow struct {
	ID, OrderID, Kind                   string
	ExternalCommandID, OriginalOutboxID *string
	CanonicalParameters                 *[]byte
	ParameterDigest                     *string
	State, Generation, AttemptCount     string
	CurrentQueryID                      *string
	NextAttemptAt                       time.Time
	LeaseUntil                          *time.Time
	LastErrorCode                       *string
	CreatedAt                           time.Time
	LastSubmissionUnknownAt             *time.Time
}
type SandboxGraphObservationRow struct {
	ID, OutboxID, Generation string
	CanonicalReply           []byte
	ReplyDigest              string
	ObservedAt               time.Time
}
type SandboxGraphQueryAttemptRow struct {
	QueryID, SupervisorID, CreatedGeneration string
	CanonicalRequest                         []byte
	RequestDigest                            string
	CreatedAt                                time.Time
	MutationGeneration                       string
}
type SandboxGraphInboxRow struct {
	ID, EnvironmentID, MerchantID, EventID string
	CanonicalBody                          []byte
	BusinessDigest, VerifiedKeyID          string
	ReceivedAt                             time.Time
}
type SandboxGraphEventConflictRow struct {
	ID, InboxID, ConflictingDigest string
	ReceivedAt                     time.Time
}
type SandboxGraphVerifiedQueryRow struct {
	ID, QueryID                   string
	CanonicalBody                 []byte
	SnapshotDigest, VerifiedKeyID string
	ReceivedAt                    time.Time
}
type SandboxGraphQueryConflictRow struct {
	ID, VerifiedQueryID, ConflictingDigest string
	ReceivedAt                             time.Time
}
type SandboxGraphEvidenceRow struct {
	ID                                       string
	InboxID, VerifiedQueryID                 *string
	EnvironmentID, MerchantID, OrderID, Kind string
	ProviderTransactionID, Amount, Currency  *string
	HappenedAt                               *time.Time
	ProviderOrderRevision                    *string
	FactDigest                               string
	VerifiedAt                               time.Time
}
type SandboxGraphProcessingRow struct {
	EvidenceID, State, Generation, AttemptCount string
	NextAttemptAt                               time.Time
	LeaseUntil                                  *time.Time
	LastErrorCode                               *string
}
type SandboxGraphProviderRevisionRow struct{ EnvironmentID, MerchantID, OrderID, ProviderOrderRevision, Kind, FactDigest, EvidenceID string }
type SandboxGraphIdentityRow struct {
	ID, EnvironmentID, MerchantID, ProviderTransactionID, OrderID, TenantID, Amount, Currency string
	CapturedAt                                                                                time.Time
	ProviderOrderRevision, EvidenceID                                                         string
	CreatedAt                                                                                 time.Time
	ProcessingGeneration                                                                      string
}
type SandboxGraphSelectionRow struct {
	OrderID, IdentityID, FirstEvidenceID string
	SelectedAt                           time.Time
}
type SandboxGraphCaptureRow struct {
	ID, OrderID, IdentityID, TenantID, EnvironmentID, MerchantID, ProviderTransactionID, Amount, WalletCreditAmount, Currency string
	CapturedAt                                                                                                                time.Time
	ProviderOrderRevision, VerifiedEvidenceID                                                                                 string
	CreatedAt                                                                                                                 time.Time
	ProcessingGeneration                                                                                                      string
}
type SandboxGraphEffectRow struct {
	CaptureID, IdentityID, EnvironmentID, MerchantID, ProviderTransactionID, EffectKind, EffectID string
	CreatedAt                                                                                     time.Time
}
type SandboxGraphReviewRow struct {
	ID, OrderID, EvidenceID      string
	IdentityID, SelectionOrderID *string
	Reason                       string
	CreatedAt                    time.Time
}

type SandboxSourceGraphRows struct {
	Environments      []SandboxGraphEnvironmentRow
	Attestations      []SandboxGraphAttestationRow
	Policies          []SandboxGraphPolicyRow
	SKUs              []SandboxGraphSKURow
	SKUVersions       []SandboxGraphSKUVersionRow
	SKUChanges        []SandboxGraphSKUChangeRow
	Orders            []SandboxGraphOrderRow
	CancelIntents     []SandboxGraphCancelIntentRow
	CommandOutbox     []SandboxGraphCommandOutboxRow
	Observations      []SandboxGraphObservationRow
	QueryAttempts     []SandboxGraphQueryAttemptRow
	Inbox             []SandboxGraphInboxRow
	EventConflicts    []SandboxGraphEventConflictRow
	VerifiedQueries   []SandboxGraphVerifiedQueryRow
	QueryConflicts    []SandboxGraphQueryConflictRow
	Evidence          []SandboxGraphEvidenceRow
	Processing        []SandboxGraphProcessingRow
	ProviderRevisions []SandboxGraphProviderRevisionRow
	Identities        []SandboxGraphIdentityRow
	Selections        []SandboxGraphSelectionRow
	Captures          []SandboxGraphCaptureRow
	Effects           []SandboxGraphEffectRow
	Reviews           []SandboxGraphReviewRow
	Users             []ProcurementApprovalUserRow
	Tenants           []ProcurementAttemptTenantRow
	Commands          []FundingGraphCommerceCommandRow
	CommerceReceipts  []FundingGraphCommerceReceiptRow
	Receipts          []FundingGraphReceiptRow
	Accounts          []LedgerAccountRow
	Journals          []FundingGraphJournalRow
	Lines             []LedgerLineRow
	Audits            []ProcurementAuditRow
	Outbox            []ProcurementOutboxRow
}
type SandboxSourceGraphCounts struct {
	Environments      uint64 `json:"environments"`
	Attestations      uint64 `json:"attestations"`
	Policies          uint64 `json:"policies"`
	SKUs              uint64 `json:"skus"`
	SKUVersions       uint64 `json:"sku_versions"`
	SKUChanges        uint64 `json:"sku_changes"`
	Orders            uint64 `json:"orders"`
	CancelIntents     uint64 `json:"cancel_intents"`
	CommandOutbox     uint64 `json:"command_outbox"`
	Observations      uint64 `json:"observations"`
	QueryAttempts     uint64 `json:"query_attempts"`
	Inbox             uint64 `json:"inbox"`
	EventConflicts    uint64 `json:"event_conflicts"`
	VerifiedQueries   uint64 `json:"verified_queries"`
	QueryConflicts    uint64 `json:"query_conflicts"`
	Evidence          uint64 `json:"evidence"`
	Processing        uint64 `json:"processing"`
	ProviderRevisions uint64 `json:"provider_revisions"`
	Identities        uint64 `json:"identities"`
	Selections        uint64 `json:"selections"`
	Captures          uint64 `json:"captures"`
	Effects           uint64 `json:"effects"`
	Reviews           uint64 `json:"reviews"`
	Users             uint64 `json:"users"`
	Tenants           uint64 `json:"tenants"`
	Commands          uint64 `json:"commands"`
	CommerceReceipts  uint64 `json:"commerce_receipts"`
	Receipts          uint64 `json:"receipts"`
	Accounts          uint64 `json:"accounts"`
	Journals          uint64 `json:"journals"`
	Lines             uint64 `json:"lines"`
	Audits            uint64 `json:"audits"`
	Outbox            uint64 `json:"outbox"`
}
type SandboxSourceGraphMismatches struct {
	DuplicatePrimaryRows    uint64 `json:"duplicate_primary_rows"`
	DuplicateUniqueRows     uint64 `json:"duplicate_unique_rows"`
	InvalidTextRows         uint64 `json:"invalid_text_rows"`
	InvalidNumberRows       uint64 `json:"invalid_number_rows"`
	InvalidTimeRows         uint64 `json:"invalid_time_rows"`
	InvalidEnumRows         uint64 `json:"invalid_enum_rows"`
	InvalidCanonicalRows    uint64 `json:"invalid_canonical_rows"`
	EnvironmentRows         uint64 `json:"environment_rows"`
	AttestationRows         uint64 `json:"attestation_rows"`
	PolicyRows              uint64 `json:"policy_rows"`
	IdentityRows            uint64 `json:"identity_rows"`
	CommerceShapeRows       uint64 `json:"commerce_shape_rows"`
	CommerceTerminalRows    uint64 `json:"commerce_terminal_rows"`
	CommerceResourceRows    uint64 `json:"commerce_resource_rows"`
	HumanAuditRows          uint64 `json:"human_audit_rows"`
	SKUVersionRows          uint64 `json:"sku_version_rows"`
	SKUChainRows            uint64 `json:"sku_chain_rows"`
	SKURootRows             uint64 `json:"sku_root_rows"`
	OrderRows               uint64 `json:"order_rows"`
	CancelIntentRows        uint64 `json:"cancel_intent_rows"`
	CommandParameterRows    uint64 `json:"command_parameter_rows"`
	CommandStateRows        uint64 `json:"command_state_rows"`
	ObservationRows         uint64 `json:"observation_rows"`
	QueryAttemptRows        uint64 `json:"query_attempt_rows"`
	QueryPointerRows        uint64 `json:"query_pointer_rows"`
	VerifiedQueryRows       uint64 `json:"verified_query_rows"`
	InboxRows               uint64 `json:"inbox_rows"`
	ConflictRows            uint64 `json:"conflict_rows"`
	EvidenceOriginalRows    uint64 `json:"evidence_original_rows"`
	EvidenceFactRows        uint64 `json:"evidence_fact_rows"`
	ProcessingRows          uint64 `json:"processing_rows"`
	ProcessingOutcomeRows   uint64 `json:"processing_outcome_rows"`
	ProviderRevisionRows    uint64 `json:"provider_revision_rows"`
	TransactionIdentityRows uint64 `json:"transaction_identity_rows"`
	SelectionRows           uint64 `json:"selection_rows"`
	ReviewRows              uint64 `json:"review_rows"`
	CaptureRows             uint64 `json:"capture_rows"`
	EffectRows              uint64 `json:"effect_rows"`
	ReceiptRows             uint64 `json:"receipt_rows"`
	JournalRows             uint64 `json:"journal_rows"`
	JournalLineRows         uint64 `json:"journal_line_rows"`
	CreditedRows            uint64 `json:"credited_rows"`
	CaptureAuditRows        uint64 `json:"capture_audit_rows"`
	ReverseSourceRows       uint64 `json:"reverse_source_rows"`
	LineIdentityRows        uint64 `json:"line_identity_rows"`
	UnknownSourceKindRows   uint64 `json:"unknown_source_kind_rows"`
}
type SandboxSourceGraphUnproven struct {
	ReservedReviewSemanticsUnproven uint64 `json:"reserved_review_semantics_unproven"`
}
type SandboxSourceGraphCheck struct {
	Scope                 string                       `json:"scope"`
	Scanned               SandboxSourceGraphCounts     `json:"scanned"`
	Status                string                       `json:"status"`
	ScopedGraphConsistent bool                         `json:"scoped_graph_consistent"`
	Mismatches            SandboxSourceGraphMismatches `json:"mismatches"`
	Unproven              SandboxSourceGraphUnproven   `json:"unproven"`
}

type sandboxGraphWork struct {
	ctx   context.Context
	steps uint16
	bytes int
}

func (w *sandboxGraphWork) tick() error {
	w.steps++
	if w.steps&255 == 0 {
		return sandboxGraphContext(w.ctx)
	}
	return nil
}
func sandboxGraphContext(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrSandboxSourceGraphRowsTimeout
	}
	return ErrSandboxSourceGraphRowsCanceled
}
func sandboxGraphTime(t time.Time) bool {
	return t.UTC().Year() >= 1 && t.UTC().Year() <= 9999 && t.Nanosecond()%1000 == 0
}
func sandboxGraphUint(s string, bits int, positive bool) bool {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	n, e := strconv.ParseUint(s, 10, bits)
	return e == nil && (!positive || n > 0)
}
func sandboxGraphMoney(s string) bool { n, ok := procurementAttemptInt64(s); return ok && n.Sign() > 0 }
func sandboxGraphN(s string) int64    { n, _ := strconv.ParseInt(s, 10, 64); return n }
func sandboxGraphHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := range s {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}
func sandboxGraphPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func sandboxGraphSame(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func sandboxGraphHas(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func sandboxGraphTerminal(p SandboxGraphProcessingRow) bool {
	return p.State == "processed" || p.State == "processing_review"
}

// All row keys are length framed, including NULL, and keep table namespaces.
func sandboxGraphKey(row any, names ...string) string {
	v := reflect.ValueOf(row)
	var b strings.Builder
	for _, name := range names {
		f := v.FieldByName(name)
		if f.Kind() == reflect.Pointer {
			if f.IsNil() {
				b.WriteString("N;")
				continue
			}
			f = f.Elem()
		}
		var s string
		if f.Kind() == reflect.String {
			s = f.String()
		} else if f.Kind() == reflect.Bool {
			s = strconv.FormatBool(f.Bool())
		} else {
			s = strconv.FormatInt(f.Int(), 10)
		}
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
		b.WriteByte(';')
	}
	return b.String()
}

type sandboxGraphTable struct {
	name    string
	primary []string
	unique  [][]string
}

var sandboxGraphTables = []sandboxGraphTable{
	{"Environments", []string{"Singleton"}, [][]string{{"ID"}, {"ID", "MerchantID"}}},
	{"Attestations", []string{"Singleton"}, [][]string{{"EnvironmentID"}}},
	{"Policies", []string{"Singleton"}, nil},
	{"SKUs", []string{"ID"}, nil},
	{"SKUVersions", []string{"ID"}, [][]string{{"SKUID", "Version"}, {"SKUID", "ID"}, {"PublishOperationID"}}},
	{"SKUChanges", []string{"OperationID"}, nil}, {"Orders", []string{"ID"}, [][]string{{"MerchantOrderID"}, {"TenantID", "ID"}, {"EnvironmentID", "MerchantID", "ID"}}},
	{"CancelIntents", []string{"OrderID"}, [][]string{{"OperationID"}}},
	{"CommandOutbox", []string{"ID"}, nil},
	{"Observations", []string{"ID"}, [][]string{{"OutboxID", "Generation", "ReplyDigest"}}},
	{"QueryAttempts", []string{"QueryID"}, [][]string{{"SupervisorID", "CreatedGeneration"}, {"SupervisorID", "QueryID"}}},
	{"Inbox", []string{"ID"}, [][]string{{"EnvironmentID", "MerchantID", "EventID"}}},
	{"EventConflicts", []string{"ID"}, [][]string{{"InboxID", "ConflictingDigest"}}},
	{"VerifiedQueries", []string{"ID"}, [][]string{{"QueryID"}}}, {"QueryConflicts", []string{"ID"}, [][]string{{"VerifiedQueryID", "ConflictingDigest"}}},
	{"Evidence", []string{"ID"}, nil}, {"Processing", []string{"EvidenceID"}, nil},
	{"ProviderRevisions", []string{"EnvironmentID", "MerchantID", "OrderID", "ProviderOrderRevision"}, nil},
	{"Identities", []string{"ID"}, [][]string{{"EnvironmentID", "MerchantID", "ProviderTransactionID"}, {"OrderID", "ID"}}},
	{"Selections", []string{"OrderID"}, [][]string{{"IdentityID"}}}, {"Captures", []string{"ID"}, [][]string{{"OrderID"}, {"IdentityID"}, {"TenantID", "ID"}}},
	{"Effects", []string{"CaptureID"}, [][]string{{"IdentityID"}, {"EnvironmentID", "MerchantID", "ProviderTransactionID", "EffectKind", "EffectID"}}},
	{"Reviews", []string{"ID"}, [][]string{{"EvidenceID", "Reason"}}}, {"Users", []string{"ID"}, nil}, {"Tenants", []string{"ID"}, nil},
	{"Commands", []string{"OperationID"}, nil}, {"CommerceReceipts", []string{"OperationID"}, nil},
	{"Receipts", []string{"ID"}, [][]string{{"SourceKind", "SourceID"}, {"JournalID"}}},
	{"Accounts", []string{"ID"}, [][]string{{"TenantID", "Kind"}}}, {"Journals", []string{"ID"}, [][]string{{"OperationID"}}},
	{"Lines", []string{"ID"}, [][]string{{"JournalID", "AccountID"}}}, {"Audits", []string{"ID"}, nil}, {"Outbox", []string{"ID"}, [][]string{{"OperationID", "Kind"}}},
}

type sandboxGraphData struct {
	w              *sandboxGraphWork
	result         SandboxSourceGraphCheck
	rows           map[string][]any
	index          map[string]map[string]any
	reported       map[string]map[string]bool
	groups         map[string]map[string][]any
	env            SandboxGraphEnvironmentRow
	keys           map[string]bool
	mutations      map[string]sandboxGraphRequest
	queries        map[string]sandboxGraphRequest
	replies        map[string]sandboxGraphReply
	observations   map[string]sandboxGraphReply
	facts          map[string]sandboxGraphFact
	identities     map[string]bool
	reviews        map[string]bool
	validCaptures  map[string]bool
	closedBounds   map[string]sandboxGraphBounds
	identityBounds map[string]sandboxGraphBounds
	err            error
}

func (g *sandboxGraphData) bad(field, table, key string) {
	if g.reported[field] == nil {
		g.reported[field] = map[string]bool{}
	}
	k := table + "\x00" + key
	if g.reported[field][k] {
		return
	}
	g.reported[field][k] = true
	v := reflect.ValueOf(&g.result.Mismatches).Elem().FieldByName(field)
	v.SetUint(v.Uint() + 1)
}
func sandboxGraphGet[T any](g *sandboxGraphData, table, id string) (T, bool) {
	var zero T
	row, ok := g.index[table][sandboxGraphKeyID(id)]
	if !ok {
		return zero, false
	}
	v, ok := row.(T)
	return v, ok
}
func sandboxGraphKeyID(id string) string { return strconv.Itoa(len(id)) + ":" + id + ";" }
func sandboxGraphRows[T any](g *sandboxGraphData, table string) []T {
	rows := make([]T, 0, len(g.rows[table]))
	for _, r := range g.rows[table] {
		rows = append(rows, r.(T))
	}
	return rows
}
func (g *sandboxGraphData) exists(table, id string) bool {
	_, ok := g.index[table][sandboxGraphKeyID(id)]
	return ok
}
func (g *sandboxGraphData) tick() bool {
	if g.err == nil {
		g.err = g.w.tick()
	}
	return g.err == nil
}
func sandboxGraphRelated[T any](g *sandboxGraphData, table string, fields []string, values ...string) []T {
	name := table + "/" + strings.Join(fields, ",")
	index, ok := g.groups[name]
	if !ok {
		index = map[string][]any{}
		for _, r := range g.rows[table] {
			if !g.tick() {
				return nil
			}
			k := sandboxGraphKey(r, fields...)
			index[k] = append(index[k], r)
		}
		g.groups[name] = index
	}
	var b strings.Builder
	for _, s := range values {
		b.WriteString(sandboxGraphKeyID(s))
	}
	raw := index[b.String()]
	out := make([]T, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(T))
	}
	return out
}

func (g *sandboxGraphData) raw(in SandboxSourceGraphRows) error {
	v := reflect.ValueOf(in)
	remaining := fundingSourceRowLimit
	for i := 0; i < v.NumField(); i++ {
		n := v.Field(i).Len()
		if n > remaining {
			return ErrSandboxSourceGraphRowsResource
		}
		remaining -= n
		reflect.ValueOf(&g.result.Scanned).Elem().FieldByName(v.Type().Field(i).Name).SetUint(uint64(n))
	}
	var inspect func(reflect.Value, *bool, *bool) error
	inspect = func(v reflect.Value, textBad, timeBad *bool) error {
		if err := g.w.tick(); err != nil {
			return err
		}
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return nil
			}
			return inspect(v.Elem(), textBad, timeBad)
		}
		if v.Type() == reflect.TypeOf(time.Time{}) {
			if !sandboxGraphTime(v.Interface().(time.Time)) {
				*timeBad = true
			}
			return nil
		}
		switch v.Kind() {
		case reflect.String:
			s := v.String()
			if len(s) > fundingSourceByteLimit-g.w.bytes {
				return ErrSandboxSourceGraphRowsResource
			}
			g.w.bytes += len(s)
			for at := 0; at < len(s); at += 256 {
				if err := g.w.tick(); err != nil {
					return err
				}
			}
			if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
				*textBad = true
			}
		case reflect.Slice:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				n := v.Len()
				if n > fundingSourceByteLimit-g.w.bytes {
					return ErrSandboxSourceGraphRowsResource
				}
				g.w.bytes += n
				for at := 0; at < n; at += 256 {
					if err := g.w.tick(); err != nil {
						return err
					}
				}
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if err := inspect(v.Field(i), textBad, timeBad); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for i := 0; i < v.NumField(); i++ {
		s := v.Field(i)
		for j := 0; j < s.Len(); j++ {
			var tb, tm bool
			if err := inspect(s.Index(j), &tb, &tm); err != nil {
				return err
			}
			if tb {
				g.result.Mismatches.InvalidTextRows++
			}
			if tm {
				g.result.Mismatches.InvalidTimeRows++
			}
		}
	}
	for i, def := range sandboxGraphTables {
		g.index[def.name] = map[string]any{}
		secondary := map[string]bool{}
		primarySeen := map[string]bool{}
		for j := 0; j < v.Field(i).Len(); j++ {
			if !g.tick() {
				return g.err
			}
			row := v.Field(i).Index(j).Interface()
			pk := sandboxGraphKey(row, def.primary...)
			rv := reflect.ValueOf(row)
			numericOK := true
			for f := 0; f < rv.NumField(); f++ {
				name := rv.Type().Field(f).Name
				value := rv.Field(f)
				if value.Kind() == reflect.Pointer {
					if value.IsNil() {
						continue
					}
					value = value.Elem()
				}
				if value.Kind() != reflect.String {
					continue
				}
				s := value.String()
				switch name {
				case "Amount", "WalletCreditAmount", "Revision", "LastVersion", "Version", "AfterRevision", "CreatedGeneration", "MutationGeneration", "ProviderOrderRevision", "ProcessingGeneration":
					numericOK = numericOK && sandboxGraphMoney(s)
				case "BeforeRevision", "Generation", "AttemptCount":
					numericOK = numericOK && sandboxGraphUint(s, 63, false)
				case "DatabaseOID", "SchemaOID", "SchemaOwnerOID", "AppRoleOID", "WorkerRoleOID", "ProjectorRoleOID":
					numericOK = numericOK && sandboxGraphUint(s, 32, true)
				case "CreatedXID":
					numericOK = numericOK && sandboxGraphUint(s, 64, true)
				case "CreatedTXID", "CheckoutTTLSeconds", "ProtocolVersion", "ProofVersion":
					numericOK = numericOK && sandboxGraphMoney(s)
				case "Balance", "SignedDelta":
					_, ok := ledgerInteger(s)
					numericOK = numericOK && ok
				}
			}
			if !numericOK {
				g.bad("InvalidNumberRows", def.name, pk)
			}
			g.rawShape(def.name, pk, row)
			if primarySeen[pk] {
				g.result.Mismatches.DuplicatePrimaryRows++
				continue
			}
			primarySeen[pk] = true
			duplicate := false
			unique := append([][]string(nil), def.unique...)
			switch r := row.(type) {
			case SandboxGraphCommandOutboxRow:
				if r.Kind == "query" {
					unique = append(unique, []string{"OriginalOutboxID"})
				} else if r.Kind == "checkout" || r.Kind == "cancel" {
					unique = append(unique, []string{"OrderID", "Kind"})
				}
				if r.ExternalCommandID != nil {
					unique = append(unique, []string{"ExternalCommandID"})
				}
			case SandboxGraphEvidenceRow:
				if r.InboxID != nil {
					unique = append(unique, []string{"InboxID"})
				}
				if r.VerifiedQueryID != nil {
					unique = append(unique, []string{"VerifiedQueryID"})
				}
			case ProcurementAuditRow:
				if strings.HasPrefix(r.Action, "payment.") && r.Action != "payment.policy.update" {
					unique = append(unique, []string{"Action", "Target"})
				}
			case FundingGraphCommerceReceiptRow:
				if r.Status == "accepted" && sandboxGraphHas(r.Kind, "voucher.batch.create", "voucher.batch.approve", "voucher.batch.void", "voucher.revocation.create", "voucher.revocation.approve") {
					unique = append(unique, []string{"Kind", "ResourceID"})
				}
			}
			for _, fields := range unique {
				k := strings.Join(fields, ",") + "\x00" + sandboxGraphKey(row, fields...)
				if secondary[k] {
					duplicate = true
				}
				secondary[k] = true
			}
			if duplicate {
				g.result.Mismatches.DuplicateUniqueRows++
				continue
			}
			g.index[def.name][pk] = row
			g.rows[def.name] = append(g.rows[def.name], row)
		}
	}
	return nil
}

// CheckSandboxSourceGraph describes supplied v1 stored-verified rows. It cannot
// authenticate installation, external payment, database completeness or proof.
func CheckSandboxSourceGraph(ctx context.Context, in SandboxSourceGraphRows) (SandboxSourceGraphCheck, error) {
	if ctx == nil {
		return SandboxSourceGraphCheck{}, ErrSandboxSourceGraphRowsInput
	}
	if err := sandboxGraphContext(ctx); err != nil {
		return SandboxSourceGraphCheck{}, err
	}
	g := sandboxGraphData{w: &sandboxGraphWork{ctx: ctx}, rows: map[string][]any{}, index: map[string]map[string]any{}, reported: map[string]map[string]bool{}, groups: map[string]map[string][]any{}, keys: map[string]bool{}, mutations: map[string]sandboxGraphRequest{}, queries: map[string]sandboxGraphRequest{}, replies: map[string]sandboxGraphReply{}, observations: map[string]sandboxGraphReply{}, facts: map[string]sandboxGraphFact{}, identities: map[string]bool{}, reviews: map[string]bool{}, validCaptures: map[string]bool{}}
	g.result.Scope = "sandbox_stored_verified_source_graph_v1"
	if err := g.raw(in); err != nil {
		return SandboxSourceGraphCheck{}, err
	}
	for _, pass := range []func(){g.environment, g.commerce, g.skusAndOrders, g.commandsAndOriginals, g.evidence, g.identitiesAndReviews, g.captureFunding, g.reverse} {
		pass()
		if g.err != nil {
			return SandboxSourceGraphCheck{}, g.err
		}
	}
	if err := sandboxGraphContext(ctx); err != nil {
		return SandboxSourceGraphCheck{}, err
	}
	g.result.Status = "consistent"
	if g.result.Mismatches != (SandboxSourceGraphMismatches{}) {
		g.result.Status = "mismatch"
	} else if g.result.Unproven != (SandboxSourceGraphUnproven{}) {
		g.result.Status = "indeterminate"
	}
	g.result.ScopedGraphConsistent = g.result.Status == "consistent"
	return g.result, nil
}

// The lexical checks precede duplicate filtering, so a conflicting row cannot
// hide malformed persisted values. Shared text identities are deliberately not
// assigned payment UUID constraints.
func (g *sandboxGraphData) rawShape(table, key string, row any) {
	field := ""
	ids := []string{}
	canonical := true
	switch r := row.(type) {
	case SandboxGraphEnvironmentRow:
		field, ids = "EnvironmentRows", []string{r.ID}
	case SandboxGraphAttestationRow:
		field, ids = "AttestationRows", []string{r.EnvironmentID}
		var p sandboxGraphProofWire
		canonical = g.strict(r.ProofCanonical, &p, 65536, 12, 4096)
	case SandboxGraphSKURow:
		field, ids = "SKURootRows", []string{r.ID}
	case SandboxGraphSKUVersionRow:
		field, ids = "SKUVersionRows", []string{r.ID}
	case SandboxGraphOrderRow:
		field, ids = "OrderRows", []string{r.ID}
	case SandboxGraphCommandOutboxRow:
		field, ids = "CommandStateRows", []string{r.ID}
		if r.ExternalCommandID != nil {
			ids = append(ids, *r.ExternalCommandID)
		}
		if r.CanonicalParameters != nil {
			_, canonical = g.request(*r.CanonicalParameters, r.Kind)
		}
		if !sandboxGraphHas(r.State, "pending", "retry_wait", "leased", "completed", "submission_unknown", "review_required") {
			g.bad("InvalidEnumRows", table, key)
		}
	case SandboxGraphObservationRow:
		field, ids = "ObservationRows", []string{r.ID}
		_, canonical = g.reply(r.CanonicalReply, false)
	case SandboxGraphQueryAttemptRow:
		field, ids = "QueryAttemptRows", []string{r.QueryID}
		_, canonical = g.request(r.CanonicalRequest, "query")
	case SandboxGraphInboxRow:
		field, ids = "InboxRows", []string{r.ID}
		_, canonical = g.notification(r.CanonicalBody)
	case SandboxGraphEventConflictRow:
		field, ids = "ConflictRows", []string{r.ID}
	case SandboxGraphVerifiedQueryRow:
		field, ids = "VerifiedQueryRows", []string{r.ID}
		_, canonical = g.reply(r.CanonicalBody, true)
	case SandboxGraphQueryConflictRow:
		field, ids = "ConflictRows", []string{r.ID}
	case SandboxGraphEvidenceRow:
		field, ids = "EvidenceFactRows", []string{r.ID, r.OrderID}
		if !sandboxGraphHas(r.Kind, "capture", "closed", "observation", "unsupported") {
			g.bad("InvalidEnumRows", table, key)
		}
	case SandboxGraphIdentityRow:
		field, ids = "TransactionIdentityRows", []string{r.ID}
	case SandboxGraphCaptureRow:
		field, ids = "CaptureRows", []string{r.ID}
	case SandboxGraphReviewRow:
		field, ids = "ReviewRows", []string{r.ID}
	case SandboxGraphProviderRevisionRow:
		if !sandboxGraphHas(r.Kind, "capture", "closed") {
			g.bad("InvalidEnumRows", table, key)
		}
	case SandboxGraphSKUChangeRow:
		if !sandboxGraphHas(r.Kind, "payment.sku.publish", "payment.sku.activate") {
			g.bad("InvalidEnumRows", table, key)
		}
	}
	for _, id := range ids {
		if !fundingGraphUUID(id) {
			g.bad(field, table, key)
		}
	}
	if !canonical {
		g.bad("InvalidCanonicalRows", table, key)
	}
}
