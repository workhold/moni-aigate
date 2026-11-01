package paymentrefundproto

var factFields = []string{"schema_version", "fact_id", "environment_id", "merchant_id", "local_refund_id", "command_id", "parameter_digest", "merchant_order_id", "provider_transaction_id", "original_capture_amount", "capture_digest", "amount", "currency", "outcome", "provider_refund_reference", "failure_code", "capture_refund_revision", "completed_at"}
var notificationFields = []string{"schema_version", "event_id", "event_type", "fact"}

func commandOf(f RefundFact) RefundRequest {
	return RefundRequest{f.SchemaVersion, f.EnvironmentID, f.MerchantID, "refund", f.CommandID,
		f.LocalRefundID, f.MerchantOrderID, f.ProviderTransactionID, f.OriginalCaptureAmount,
		f.CaptureDigest, f.Amount, f.Currency}
}
func factValid(f RefundFact) bool {
	if !uuidValid(f.FactID) || !positive(f.CaptureRefundRevision) {
		return false
	}
	if _, ok := instant(f.CompletedAt); !ok {
		return false
	}
	d, err := ParameterDigest(commandOf(f))
	if err != nil || d != f.ParameterDigest {
		return false
	}
	switch f.Outcome {
	case "confirmed":
		return f.ProviderRefundReference != nil && refundReference(*f.ProviderRefundReference) && f.FailureCode == nil
	case "final_no_effect":
		return f.ProviderRefundReference == nil && f.FailureCode != nil &&
			(*f.FailureCode == "refund_limit_exceeded" || *f.FailureCode == "refund_policy_rejected" || *f.FailureCode == "capture_not_refundable")
	}
	return false
}
func CanonicalRefundFact(f RefundFact) ([]byte, error) {
	if !factValid(f) {
		return nil, ErrInvalid
	}
	return encode(f, bodyLimit)
}
func ParseRefundFact(raw []byte) (RefundFact, error) {
	var f RefundFact
	if _, err := strictObject(raw, bodyLimit, factFields, []string{"provider_refund_reference", "failure_code"}, &f); err != nil || !factValid(f) {
		return RefundFact{}, ErrInvalid
	}
	return f, nil
}
func FactDigest(f RefundFact) (string, error) {
	b, err := CanonicalRefundFact(f)
	if err != nil {
		return "", ErrInvalid
	}
	return digest(factDomain, b), nil
}
func notificationValid(n Notification) bool {
	return n.SchemaVersion == 2 && uuidValid(n.EventID) && factValid(n.Fact) &&
		(n.EventType == "refund.confirmed" && n.Fact.Outcome == "confirmed" || n.EventType == "refund.final_no_effect" && n.Fact.Outcome == "final_no_effect")
}
func CanonicalNotification(n Notification) ([]byte, error) {
	if !notificationValid(n) {
		return nil, ErrInvalid
	}
	return encode(n, bodyLimit)
}
func ParseNotification(raw []byte) (Notification, error) {
	var n Notification
	m, err := strictObject(raw, bodyLimit, notificationFields, nil, &n)
	if err != nil {
		return Notification{}, ErrInvalid
	}
	n.Fact, err = ParseRefundFact(m["fact"])
	if err != nil || !notificationValid(n) {
		return Notification{}, ErrInvalid
	}
	return n, nil
}
