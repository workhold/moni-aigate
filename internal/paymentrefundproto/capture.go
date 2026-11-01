package paymentrefundproto

import p "github.com/moni-ai/moni-aigate/internal/paymentproto"

type captureValue struct {
	CaptureProtocolVersion int    `json:"capture_protocol_version"`
	EnvironmentID          string `json:"environment_id"`
	MerchantID             string `json:"merchant_id"`
	MerchantOrderID        string `json:"merchant_order_id"`
	ProviderTransactionID  string `json:"provider_transaction_id"`
	Amount                 string `json:"amount"`
	Currency               string `json:"currency"`
	OccurredAt             string `json:"occurred_at"`
	ProviderOrderRevision  string `json:"provider_order_revision"`
}

func captureBinding(c captureValue) (CaptureBinding, error) {
	_, timeOK := instant(c.OccurredAt)
	if c.CaptureProtocolVersion != 1 || !uuidValid(c.EnvironmentID) || !reference(c.MerchantID) ||
		!merchantOrderValid(c.MerchantOrderID, c.EnvironmentID) || !reference(c.ProviderTransactionID) ||
		!positive(c.Amount) || c.Currency != "CNY" || !timeOK || !positive(c.ProviderOrderRevision) {
		return CaptureBinding{}, ErrInvalid
	}
	b, err := encode(c, requestLimit)
	if err != nil {
		return CaptureBinding{}, ErrInvalid
	}
	return CaptureBinding{value: c, canonical: b}, nil
}

// CaptureFromV1Notification preserves the authenticated capture's environment;
// the application must separately authorize that environment and funding lot.
func CaptureFromV1Notification(v p.VerifiedNotification) (CaptureBinding, error) {
	b, err := v.Canonical()
	if err != nil {
		return CaptureBinding{}, ErrInvalid
	}
	n, err := p.ParseNotification(b)
	if err != nil || n.EventType != "capture.confirmed" || n.ProviderTransactionID == nil {
		return CaptureBinding{}, ErrInvalid
	}
	order, err := p.MerchantOrderID(n.EnvironmentID, n.OrderID)
	if err != nil {
		return CaptureBinding{}, ErrInvalid
	}
	return captureBinding(captureValue{1, n.EnvironmentID, n.MerchantID, order,
		*n.ProviderTransactionID, n.Amount, n.Currency, n.OccurredAt, n.ProviderOrderRevision})
}

func CaptureFromV1Query(v p.VerifiedQuery) (CaptureBinding, error) {
	b, err := v.Canonical()
	if err != nil {
		return CaptureBinding{}, ErrInvalid
	}
	r, err := p.ParseQueryReply(b)
	if err != nil || r.CommandState != "completed" || r.Result == nil || r.Result.Status != "captured" || r.Result.Capture == nil || r.ProviderOrderRevision == nil {
		return CaptureBinding{}, ErrInvalid
	}
	c := r.Result.Capture
	if c.ProviderOrderRevision != *r.ProviderOrderRevision {
		return CaptureBinding{}, ErrInvalid
	}
	return captureBinding(captureValue{1, r.EnvironmentID, r.MerchantID, r.MerchantOrderID,
		c.ProviderTransactionID, c.Amount, c.Currency, c.OccurredAt, c.ProviderOrderRevision})
}

func (c CaptureBinding) Canonical() ([]byte, error) { return copyCanonical(c.canonical) }
func (c CaptureBinding) Digest() (string, error) {
	if len(c.canonical) == 0 {
		return "", ErrInvalid
	}
	return digest(captureDomain, c.canonical), nil
}
