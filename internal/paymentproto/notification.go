package paymentproto

import "encoding/json"

type notificationCommon struct {
	SchemaVersion         int     `json:"schema_version"`
	EventID               string  `json:"event_id"`
	EventType             string  `json:"event_type"`
	EnvironmentID         string  `json:"environment_id"`
	MerchantID            string  `json:"merchant_id"`
	OrderID               string  `json:"order_id"`
	ProviderTransactionID *string `json:"provider_transaction_id"`
	Amount                string  `json:"amount"`
	Currency              string  `json:"currency"`
	OccurredAt            string  `json:"occurred_at"`
	ProviderOrderRevision string  `json:"provider_order_revision"`
}

var notificationFields = []string{"schema_version", "event_id", "event_type", "environment_id", "merchant_id", "order_id", "provider_transaction_id", "amount", "currency", "occurred_at", "provider_order_revision"}

func normalizeNotification(n Notification) (Notification, error) {
	bad := func() (Notification, error) { return Notification{}, fail("invalid_request") }
	t, ok := instant(n.OccurredAt)
	if n.SchemaVersion != 1 || !reference(n.EventID) || !uuidValid(n.EnvironmentID) || !reference(n.MerchantID) || !uuidValid(n.OrderID) || !positive(n.Amount) || n.Currency != "CNY" || !ok || !positive(n.ProviderOrderRevision) {
		return bad()
	}
	if n.EventType != "checkout.failed" && (n.ProviderTransactionID == nil || !reference(*n.ProviderTransactionID)) {
		return bad()
	}
	switch n.EventType {
	case "capture.confirmed":
		if n.FailureCode != nil || n.RefundID != nil || n.CaseID != nil {
			return bad()
		}
	case "checkout.failed":
		if n.ProviderTransactionID != nil || n.FailureCode == nil || !failureCode(*n.FailureCode) || n.RefundID != nil || n.CaseID != nil {
			return bad()
		}
	case "refund.confirmed", "refund.failed":
		if n.FailureCode != nil || n.RefundID == nil || !uuidValid(*n.RefundID) || n.CaseID != nil {
			return bad()
		}
	case "dispute.opened", "chargeback.debited", "chargeback.reversed":
		if n.FailureCode != nil || n.RefundID != nil || n.CaseID == nil || !reference(*n.CaseID) {
			return bad()
		}
	default:
		return bad()
	}
	n.OccurredAt = t.Format(timeLayout)
	return n, nil
}
func commonNotification(n Notification) notificationCommon {
	return notificationCommon{n.SchemaVersion, n.EventID, n.EventType, n.EnvironmentID, n.MerchantID, n.OrderID, n.ProviderTransactionID, n.Amount, n.Currency, n.OccurredAt, n.ProviderOrderRevision}
}
func CanonicalNotification(n Notification) ([]byte, error) {
	n, e := normalizeNotification(n)
	if e != nil {
		return nil, e
	}
	c := commonNotification(n)
	switch n.EventType {
	case "capture.confirmed":
		return encode(c, bodyLimit)
	case "checkout.failed":
		return encode(struct {
			notificationCommon
			FailureCode string `json:"failure_code"`
		}{c, *n.FailureCode}, bodyLimit)
	case "refund.confirmed", "refund.failed":
		return encode(struct {
			notificationCommon
			RefundID string `json:"refund_id"`
		}{c, *n.RefundID}, bodyLimit)
	default:
		return encode(struct {
			notificationCommon
			CaseID string `json:"case_id"`
		}{c, *n.CaseID}, bodyLimit)
	}
}
func ParseNotification(raw []byte) (Notification, error) {
	if len(raw) == 0 || len(raw) > bodyLimit {
		return Notification{}, fail("invalid_request")
	}
	// This is only a bounded discriminant peek. Nothing is trusted until the
	// complete document and exact union field set pass strictObject below.
	var peek map[string]json.RawMessage
	var eventType string
	if json.Unmarshal(raw, &peek) != nil || json.Unmarshal(peek["event_type"], &eventType) != nil {
		return Notification{}, fail("invalid_request")
	}
	fields := append([]string(nil), notificationFields...)
	switch eventType {
	case "capture.confirmed":
	case "checkout.failed":
		fields = append(fields, "failure_code")
	case "refund.confirmed", "refund.failed":
		fields = append(fields, "refund_id")
	case "dispute.opened", "chargeback.debited", "chargeback.reversed":
		fields = append(fields, "case_id")
	default:
		return Notification{}, fail("invalid_request")
	}
	var wire struct {
		notificationCommon
		FailureCode *string `json:"failure_code"`
		RefundID    *string `json:"refund_id"`
		CaseID      *string `json:"case_id"`
	}
	if _, e := strictObject(raw, bodyLimit, fields, []string{"provider_transaction_id"}, &wire); e != nil {
		return Notification{}, e
	}
	c := wire.notificationCommon
	return normalizeNotification(Notification{SchemaVersion: c.SchemaVersion, EventID: c.EventID, EventType: c.EventType, EnvironmentID: c.EnvironmentID, MerchantID: c.MerchantID, OrderID: c.OrderID, ProviderTransactionID: c.ProviderTransactionID, Amount: c.Amount, Currency: c.Currency, OccurredAt: c.OccurredAt, ProviderOrderRevision: c.ProviderOrderRevision, FailureCode: wire.FailureCode, RefundID: wire.RefundID, CaseID: wire.CaseID})
}
