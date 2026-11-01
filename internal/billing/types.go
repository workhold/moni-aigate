package billing

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

type Money int64

func (m Money) MarshalJSON() ([]byte, error) { return json.Marshal(strconv.FormatInt(int64(m), 10)) }
func (m *Money) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("money must be a decimal string")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid money")
	}
	*m = Money(n)
	return nil
}

type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	ResourceID string `json:"resource_id,omitempty"`
}

func (e *Error) Error() string        { return e.Code + ": " + e.Message }
func fail(code, message string) error { return &Error{Code: code, Message: message} }

type Store struct {
	pool       *pgxpool.Pool
	instanceID string
	active     sync.Map
}

func New(pool *pgxpool.Pool) *Store          { return &Store{pool: pool, instanceID: uuid.NewString()} }
func (s *Store) EndRequest(requestID string) { s.active.Delete(requestID) }

type Principal struct {
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
	KeyID    string `json:"key_id"`
	ClientIP string `json:"-"`
}
type ReserveInput struct {
	RouteRevision                                                                  int64
	Principal                                                                      Principal
	RequestID, ModelID, IdempotencyKey, RequestHMAC, ProfileVersion, PolicyVersion string
	InputUpperBound, OutputLimit                                                   int64
	RequestDeadline                                                                time.Time
	Streaming                                                                      bool
}
type Reservation struct {
	ID                    string    `json:"id"`
	RequestID             string    `json:"request_id"`
	TenantID              string    `json:"tenant_id"`
	Amount                Money     `json:"amount"`
	Status                string    `json:"status"`
	Deadline              time.Time `json:"deadline"`
	PriceVersionID        string    `json:"price_version_id"`
	InputRate             Money     `json:"input_rate"`
	OutputRate            Money     `json:"output_rate"`
	PricingGroupID        *string   `json:"pricing_group_id"`
	TenantPricingRevision int64     `json:"tenant_pricing_revision,string"`
	RetailPricedAt        time.Time `json:"retail_priced_at"`
}
type CreditInput struct {
	IdempotencyKey string `json:"-"`
	RequestHMAC    string `json:"-"`
	TenantID       string `json:"tenant_id"`
	ActorID        string `json:"actor_id"`
	BusinessRef    string `json:"business_ref"`
	ProofRef       string `json:"proof_ref"`
	Amount         Money  `json:"amount"`
}
type ApprovalInput struct {
	TenantID string `json:"tenant_id"`
	OrderID  string `json:"order_id"`
	ActorID  string `json:"actor_id"`
}
type CreditOrder struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	BusinessRef string `json:"business_ref"`
	Amount      Money  `json:"amount"`
	State       string `json:"state"`
	Requester   string `json:"requester"`
	Approver    string `json:"approver,omitempty"`
	JournalID   string `json:"journal_id,omitempty"`
}
type AttemptInput struct {
	TenantID, RequestID, ChannelID, ChannelVersionID, CredentialID, UpstreamModel string
	Sequence                                                                      int
	RouteDecisions                                                                []routing.Decision
	CostQuote                                                                     *routing.CostQuote
}
type Attempt struct {
	ID            string `json:"id"`
	RequestID     string `json:"request_id"`
	Sequence      int    `json:"sequence"`
	DeliveryState string `json:"delivery_state"`
	CostStatus    string `json:"cost_status"`
}
type DispatchInput struct{ TenantID, AttemptID string }
type Usage struct {
	Status          string `json:"status"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
}
type FinalizationInput struct {
	ID, TenantID, RequestID, AttemptID, Source, SourceEventID        string
	Usage                                                            Usage
	ProtocolOutcome, TransportEndReason, DeliveryStage, FinishReason string
	ContentWritten                                                   bool
	ObservedAt                                                       time.Time
}
type Finalization struct {
	ID                 string `json:"id"`
	EventID            string `json:"event_id"`
	RequestID          string `json:"request_id"`
	Usage              Usage  `json:"usage"`
	ProtocolOutcome    string `json:"protocol_outcome"`
	TransportEndReason string `json:"transport_end_reason"`
	DeliveryStage      string `json:"delivery_stage"`
	ContentWritten     bool   `json:"content_written"`
	FinishReason       string `json:"finish_reason"`
}
type SettleInput struct{ TenantID, RequestID, Mode string }
type ReleaseInput struct{ TenantID, RequestID, Reason string }
type PendingInput struct{ TenantID, RequestID, Reason string }
type Settlement struct {
	ID                 string `json:"id"`
	RequestID          string `json:"request_id"`
	TenantID           string `json:"tenant_id"`
	ChargedAmount      Money  `json:"charged_amount"`
	RawRetailAmount    string `json:"raw_retail_amount"`
	WaivedRetailAmount string `json:"waived_retail_amount"`
	RefundedAmount     Money  `json:"refunded_amount"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	JournalID          string `json:"journal_id,omitempty"`
}
type RefundInput struct {
	IdempotencyKey string `json:"-"`
	RequestHMAC    string `json:"-"`
	TenantID       string `json:"tenant_id"`
	ActorID        string `json:"actor_id"`
	SettlementID   string `json:"settlement_id"`
	BusinessRef    string `json:"business_ref"`
	Reason         string `json:"reason"`
	Amount         Money  `json:"amount"`
}
type RefundOrder struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	SettlementID string `json:"settlement_id"`
	Amount       Money  `json:"amount"`
	State        string `json:"state"`
	Requester    string `json:"requester"`
	Approver     string `json:"approver,omitempty"`
	JournalID    string `json:"journal_id,omitempty"`
}
type ReconcileInput struct {
	TenantID, RequestID, ActorID, Action, EvidenceRef, Reason string
	Usage                                                     Usage
}
type Reconciliation struct {
	ID         string      `json:"id"`
	State      string      `json:"state"`
	Settlement *Settlement `json:"settlement,omitempty"`
}
type RecoverInput struct{ Limit int }
type RecoveryResult struct {
	Processed int `json:"processed"`
	Settled   int `json:"settled"`
	Released  int `json:"released"`
	Pending   int `json:"pending"`
}
type CostCorrectionInput struct {
	TenantID, AttemptID, OperationID, EvidenceRef, Basis, ActorID string
	Amount                                                        Money
}
type Query struct {
	TenantID string
	Limit    int
	Before   time.Time
}
type Wallet struct {
	TenantID  string `json:"tenant_id"`
	Available Money  `json:"available"`
	Held      Money  `json:"held"`
}
type Journal struct {
	ID          string        `json:"id"`
	OperationID string        `json:"operation_id"`
	Kind        string        `json:"kind"`
	Reference   string        `json:"reference"`
	CreatedAt   time.Time     `json:"created_at"`
	Lines       []JournalLine `json:"lines"`
}
type JournalLine struct {
	AccountKind string `json:"account_kind"`
	Delta       Money  `json:"delta"`
}
type RequestRecord struct {
	ID                string    `json:"id"`
	ModelID           string    `json:"model_id"`
	Status            string    `json:"status"`
	BillingStatus     string    `json:"billing_status"`
	CreatedAt         time.Time `json:"created_at"`
	ChargedAmount     Money     `json:"charged_amount"`
	ReservationAmount Money     `json:"reservation_amount"`
}
type Report struct {
	TenantID             string `json:"tenant_id"`
	Credited             string `json:"credited"`
	Charged              string `json:"charged"`
	Refunded             string `json:"refunded"`
	Released             string `json:"released"`
	Requests             int64  `json:"requests"`
	KnownProviderCost    string `json:"known_provider_cost"`
	UnknownProviderCosts int64  `json:"unknown_provider_costs"`
}
type ProjectInput struct {
	Consumer string
	Limit    int
}
type LedgerCheck struct {
	Balanced           bool `json:"balanced"`
	AccountMismatches  int  `json:"account_mismatches"`
	UnbalancedJournals int  `json:"unbalanced_journals"`
}
