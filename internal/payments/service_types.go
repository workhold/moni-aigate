package payments

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

// PaymentError contains a fixed, public-safe domain code, never driver details.
type PaymentError struct{ Code string }

func (e *PaymentError) Error() string { return e.Code }
func paymentFail(code string) error   { return &PaymentError{Code: code} }

type Service struct {
	pool    *pgxpool.Pool
	runtime *paymentRuntime
}

func NewService(ctx context.Context, pool *pgxpool.Pool, opts Options) (*Service, error) {
	r, e := newPaymentRuntime(ctx, pool, opts, applicationComponent)
	if e != nil {
		return nil, e
	}
	return &Service{pool: pool, runtime: r}, nil
}

type OperationReceipt = billing.VoucherOperationReceipt
type CommandMeta struct{ OperationID, ActorID, TenantID, RequestID string }
type PublishSKUInput struct {
	CommandMeta
	SKUID            string
	ExpectedRevision int64
	DisplayName      string
	Amount           billing.Money
}
type ActivateSKUInput struct {
	CommandMeta
	SKUID            string
	ExpectedRevision int64
	VersionID        *string
	Enabled          bool
}
type CreateOrderInput struct {
	CommandMeta
	SKUVersionID string
}
type CancelOrderInput struct {
	CommandMeta
	OrderID string
}
type UpdatePolicyInput struct {
	ActorID, RequestID string
	ExpectedRevision   int64
	Enabled            bool
}
type PublishSKUResult struct {
	Receipt OperationReceipt
	Created bool
}
type OrderResult struct {
	Receipt OperationReceipt
	Order   *OrderView
	Created bool
}
type Reader struct{ ActorID, TenantID string }
type Page struct{ Cursor string }
type PageResult[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}
type PolicyView struct {
	Enabled          bool   `json:"enabled"`
	Revision         string `json:"revision"`
	RuntimeAllowed   bool   `json:"runtime_allowed"`
	EffectiveEnabled bool   `json:"effective_enabled"`
}
type PaymentCapabilitiesView struct {
	Mode            string  `json:"mode"`
	Environment     *string `json:"environment"`
	PaymentsEnabled bool    `json:"payments_enabled"`
	LocalSimulation bool    `json:"local_simulation"`
}
type SKUVersionView struct {
	ID                 string        `json:"id"`
	SKUID              string        `json:"sku_id"`
	Version            string        `json:"version"`
	DisplayName        string        `json:"display_name"`
	Amount             billing.Money `json:"amount"`
	WalletCreditAmount billing.Money `json:"wallet_credit_amount"`
	Currency           string        `json:"currency"`
	Environment        string        `json:"environment"`
	MerchantID         string        `json:"merchant_id"`
	PayeeLabel         string        `json:"payee_label"`
	CheckoutTTLSeconds int           `json:"checkout_ttl_seconds"`
	CreatedAt          time.Time     `json:"created_at"`
}
type PlatformSKUView struct {
	ID               string          `json:"id"`
	Revision         string          `json:"revision"`
	Enabled          bool            `json:"enabled"`
	CurrentVersionID *string         `json:"current_version_id"`
	CurrentVersion   *SKUVersionView `json:"current_version"`
	CreatedAt        time.Time       `json:"created_at"`
}
type OrderView struct {
	ID                 string        `json:"id"`
	TenantID           string        `json:"tenant_id"`
	Creator            string        `json:"creator"`
	SKUVersionID       string        `json:"sku_version_id"`
	DisplayName        string        `json:"display_name"`
	Amount             billing.Money `json:"amount"`
	WalletCreditAmount billing.Money `json:"wallet_credit_amount"`
	Currency           string        `json:"currency"`
	Environment        string        `json:"environment"`
	MerchantID         string        `json:"merchant_id"`
	PayeeLabel         string        `json:"payee_label"`
	CreatedAt          time.Time     `json:"created_at"`
	ExpiresAt          time.Time     `json:"expires_at"`
	CheckoutState      string        `json:"checkout_state"`
	CreationState      string        `json:"creation_state"`
	CheckoutRef        *string       `json:"checkout_ref"`
	FundingState       string        `json:"funding_state"`
	ReviewRequired     bool          `json:"review_required"`
	ReviewReason       *string       `json:"review_reason"`
	FundingReceiptID   *string       `json:"funding_receipt_id"`
	JournalID          *string       `json:"journal_id"`
	CreditedAt         *time.Time    `json:"credited_at"`
	ObservedAt         time.Time     `json:"observed_at"`
}
type ObservationView struct {
	ID         string     `json:"id"`
	OrderID    string     `json:"order_id"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	Reason     *string    `json:"reason"`
	OccurredAt *time.Time `json:"occurred_at"`
	ReceivedAt time.Time  `json:"received_at"`
}
type NotificationInput struct {
	Headers p.SignedHeaders
	Body    []byte
}
type NotificationACK struct {
	ReceiptID, EventID, Status, Environment string
	ReceivedAt                              time.Time
}
type ProcessResult struct {
	Outcome                                              string
	CaptureID, FundingReceiptID, JournalID, ReviewReason *string
}
