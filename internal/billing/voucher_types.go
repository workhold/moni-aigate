package billing

import (
	"context"
	"errors"
	"github.com/moni-ai/moni-aigate/internal/security"
	"time"
)

type VoucherError struct{ Code string }

func (e *VoucherError) Error() string { return e.Code }
func voucherFail(code string) error   { return &VoucherError{Code: code} }
func voucherSafeError(err error) error {
	if err == nil {
		return nil
	}
	var v *VoucherError
	if errors.As(err, &v) {
		return v
	}
	return voucherFail("commerce_unavailable")
}

type VoucherOptions struct {
	RuntimeAllowed, RecoveryMode bool
	Keys                         *security.VoucherKeyring
}
type VoucherService struct {
	store   *Store
	options VoucherOptions
}
type VoucherKeyVersions struct{ Code, Fingerprint []string }

func NewVoucherService(ctx context.Context, s *Store, o VoucherOptions) (*VoucherService, error) {
	if s == nil || s.pool == nil {
		return nil, voucherFail("commerce_unavailable")
	}
	if o.RuntimeAllowed {
		if o.Keys == nil || !o.Keys.HasVersion(o.Keys.ActiveVersion()) {
			return nil, voucherFail("voucher_key_unavailable")
		}
		versions, e := s.VoucherKeyVersions(ctx)
		if e != nil {
			return nil, e
		}
		seen := map[string]bool{}
		for _, v := range append(versions.Code, versions.Fingerprint...) {
			seen[v] = true
			if !o.Keys.HasVersion(v) {
				return nil, voucherFail("voucher_key_unavailable")
			}
		}
		if len(seen) > 128 {
			return nil, voucherFail("voucher_key_unavailable")
		}
	}
	return &VoucherService{store: s, options: o}, nil
}

type VoucherCommandContext struct{ OperationID, ActorID, TenantID, RequestID string }
type CreateVoucherBatchInput struct {
	Command            VoucherCommandContext
	FaceAmount         Money
	Count              int
	ExpiresAt          time.Time
	PurposeRef, Reason string
}
type ApproveVoucherBatchInput struct {
	Command          VoucherCommandContext
	BatchID          string
	ExpectedRevision int64
}
type VoidVoucherBatchInput struct {
	Command          VoucherCommandContext
	BatchID          string
	ExpectedRevision int64
	Reason           string
}
type ProposeVoucherRevocationInput struct {
	Command                                 VoucherCommandContext
	BatchID                                 string
	ExpectedRevision, ExpectedRedeemedCount int64
	Reason                                  string
}
type ApproveVoucherRevocationInput struct {
	Command    VoucherCommandContext
	ProposalID string
}
type RedeemVoucherInput struct {
	Command VoucherCommandContext
	Code    string
}
type IssuedVoucherCode struct {
	VoucherID string `json:"voucher_id"`
	Code      string `json:"code"`
}
type CreateVoucherBatchResult struct {
	Receipt                   VoucherOperationReceipt
	Batch                     *VoucherBatchView
	Codes                     []IssuedVoucherCode
	SecretsAvailable, Created bool
}
type VoucherOperationReceipt struct {
	OperationID      string    `json:"operation_id"`
	Kind             string    `json:"kind"`
	Scope            string    `json:"scope"`
	Actor            string    `json:"actor"`
	Status           string    `json:"status"`
	Reason           string    `json:"reason"`
	ResourceID       *string   `json:"resource_id"`
	Amount           *Money    `json:"amount"`
	FundingReceiptID *string   `json:"funding_receipt_id"`
	JournalID        *string   `json:"journal_id"`
	CreatedAt        time.Time `json:"created_at"`
}
type VoucherPolicyView struct {
	Enabled          bool   `json:"enabled"`
	Revision         string `json:"revision"`
	EffectiveEnabled bool   `json:"effective_enabled"`
	RuntimeAllowed   bool   `json:"runtime_allowed"`
}
type CommerceCapabilitiesView struct {
	VouchersEnabled bool   `json:"vouchers_enabled"`
	PaymentsMode    string `json:"payments_mode"`
}
type VoucherBatchView struct {
	ID             string     `json:"id"`
	State          string     `json:"state"`
	FaceAmount     Money      `json:"face_amount"`
	Count          int        `json:"count"`
	ExpiresAt      time.Time  `json:"expires_at"`
	PurposeRef     string     `json:"purpose_ref"`
	Reason         string     `json:"reason"`
	Maker          string     `json:"maker"`
	Approver       *string    `json:"approver"`
	ActivatedAt    *time.Time `json:"activated_at"`
	TerminalActor  *string    `json:"terminal_actor"`
	TerminalAt     *time.Time `json:"terminal_at"`
	Revision       string     `json:"revision"`
	RedeemedCount  string     `json:"redeemed_count"`
	RemainingCount string     `json:"remaining_count"`
	CreatedAt      time.Time  `json:"created_at"`
	Expired        bool       `json:"expired"`
	ObservedAt     time.Time  `json:"observed_at"`
}
type VoucherDecisionView struct {
	ID          string    `json:"id"`
	OperationID string    `json:"operation_id"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason"`
	Approver    string    `json:"approver"`
	DecidedAt   time.Time `json:"decided_at"`
}
type VoucherRevocationView struct {
	ID                    string               `json:"id"`
	BatchID               string               `json:"batch_id"`
	ExpectedRevision      string               `json:"expected_revision"`
	ExpectedRedeemedCount string               `json:"expected_redeemed_count"`
	Reason                string               `json:"reason"`
	Maker                 string               `json:"maker"`
	CreatedAt             time.Time            `json:"created_at"`
	Decision              *VoucherDecisionView `json:"decision"`
}
type VoucherPage[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"next_cursor"`
}
type VoucherRedemptionView struct {
	ID               string    `json:"id"`
	OperationID      string    `json:"operation_id"`
	TenantID         string    `json:"tenant_id"`
	VoucherID        string    `json:"voucher_id"`
	BatchID          string    `json:"batch_id"`
	Actor            string    `json:"actor"`
	Amount           Money     `json:"amount"`
	Currency         string    `json:"currency"`
	FundingReceiptID string    `json:"funding_receipt_id"`
	JournalID        string    `json:"journal_id"`
	CreatedAt        time.Time `json:"created_at"`
}
type FundingReceiptView struct {
	ID         string            `json:"id"`
	TenantID   string            `json:"tenant_id"`
	SourceKind FundingSourceKind `json:"source_kind"`
	SourceID   string            `json:"source_id"`
	Amount     Money             `json:"amount"`
	Currency   string            `json:"currency"`
	JournalID  string            `json:"journal_id"`
	CreatedAt  time.Time         `json:"created_at"`
}
type FundingSummaryView struct {
	Currency             string    `json:"currency"`
	TotalAmount          string    `json:"total_amount"`
	ManualCreditAmount   string    `json:"manual_credit_amount"`
	VoucherGrantAmount   string    `json:"voucher_grant_amount"`
	SandboxCaptureAmount string    `json:"sandbox_capture_amount"`
	WalletOpeningAmount  string    `json:"wallet_opening_amount"`
	AsOf                 time.Time `json:"as_of"`
}
type VoucherReader struct{ ActorID, TenantID string }
type VoucherPageInput struct{ Cursor string }
type FundingReceiptPageInput struct {
	Cursor     string
	SourceKind FundingSourceKind
}
type UpdateVoucherPolicyInput struct {
	ActorID, RequestID string
	ExpectedRevision   int64
	Enabled            bool
}
