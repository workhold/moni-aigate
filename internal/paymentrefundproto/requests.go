package paymentrefundproto

var refundRequestFields = []string{"schema_version", "environment_id", "merchant_id", "command_kind", "command_id", "local_refund_id", "merchant_order_id", "provider_transaction_id", "original_capture_amount", "capture_digest", "amount", "currency"}
var queryRequestFields = []string{"schema_version", "query_id", "mutation_generation", "command", "parameter_digest"}

func requestValid(r RefundRequest) bool {
	return r.SchemaVersion == 2 && uuidValid(r.EnvironmentID) && reference(r.MerchantID) &&
		r.CommandKind == "refund" && uuidValid(r.CommandID) && uuidValid(r.LocalRefundID) &&
		merchantOrderValid(r.MerchantOrderID, r.EnvironmentID) && reference(r.ProviderTransactionID) &&
		lowerHex(r.CaptureDigest) && amountValid(r.Amount, r.OriginalCaptureAmount) && r.Currency == "CNY"
}
func NewRefundRequest(capture CaptureBinding, commandID, localRefundID, amount string) (RefundRequest, error) {
	d, err := capture.Digest()
	if err != nil {
		return RefundRequest{}, ErrInvalid
	}
	c := capture.value
	r := RefundRequest{2, c.EnvironmentID, c.MerchantID, "refund", commandID, localRefundID,
		c.MerchantOrderID, c.ProviderTransactionID, c.Amount, d, amount, c.Currency}
	if !requestValid(r) {
		return RefundRequest{}, ErrInvalid
	}
	return r, nil
}
func CanonicalRefundRequest(r RefundRequest) ([]byte, error) {
	if !requestValid(r) {
		return nil, ErrInvalid
	}
	return encode(r, requestLimit)
}
func ParseRefundRequest(raw []byte) (RefundRequest, error) {
	var r RefundRequest
	if _, err := strictObject(raw, requestLimit, refundRequestFields, nil, &r); err != nil || !requestValid(r) {
		return RefundRequest{}, ErrInvalid
	}
	return r, nil
}
func ParameterDigest(r RefundRequest) (string, error) {
	b, err := CanonicalRefundRequest(r)
	if err != nil {
		return "", ErrInvalid
	}
	return digest(commandDomain, b), nil
}
func queryValid(q QueryRequest) bool {
	if q.SchemaVersion != 2 || !uuidValid(q.QueryID) || !positive(q.MutationGeneration) {
		return false
	}
	d, err := ParameterDigest(q.Command)
	return err == nil && q.ParameterDigest == d
}
func NewQueryRequest(command RefundRequest, queryID, mutationGeneration string) (QueryRequest, error) {
	d, err := ParameterDigest(command)
	if err != nil {
		return QueryRequest{}, ErrInvalid
	}
	q := QueryRequest{2, queryID, mutationGeneration, command, d}
	if !queryValid(q) {
		return QueryRequest{}, ErrInvalid
	}
	return q, nil
}
func CanonicalQueryRequest(q QueryRequest) ([]byte, error) {
	if !queryValid(q) {
		return nil, ErrInvalid
	}
	return encode(q, requestLimit)
}
func ParseQueryRequest(raw []byte) (QueryRequest, error) {
	var q QueryRequest
	m, err := strictObject(raw, requestLimit, queryRequestFields, nil, &q)
	if err != nil {
		return QueryRequest{}, ErrInvalid
	}
	q.Command, err = ParseRefundRequest(m["command"])
	if err != nil || !queryValid(q) {
		return QueryRequest{}, ErrInvalid
	}
	return q, nil
}
func QueryDigest(q QueryRequest) (string, error) {
	b, err := CanonicalQueryRequest(q)
	if err != nil {
		return "", ErrInvalid
	}
	return digest(queryDomain, b), nil
}
