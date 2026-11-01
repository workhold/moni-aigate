package paymentproto

import "time"

var checkoutFields = []string{"schema_version", "environment_id", "merchant_id", "command_id", "merchant_order_id", "amount", "currency", "created_at", "expires_at"}
var cancelFields = []string{"schema_version", "environment_id", "merchant_id", "command_id", "merchant_order_id"}
var queryFields = []string{"schema_version", "environment_id", "merchant_id", "query_id", "command_id", "merchant_order_id", "command_kind", "parameter_digest"}

func normalizeCheckout(r CheckoutRequest) (CheckoutRequest, error) {
	created, ok := instant(r.CreatedAt)
	expires, expiresOK := instant(r.ExpiresAt)
	if !identity(r.SchemaVersion, r.EnvironmentID, r.MerchantID, r.CommandID, r.MerchantOrderID) || !positive(r.Amount) || r.Currency != "CNY" || !ok || !expiresOK || expires.Sub(created) != 900*time.Second {
		return CheckoutRequest{}, fail("invalid_request")
	}
	r.CreatedAt, r.ExpiresAt = created.Format(timeLayout), expires.Format(timeLayout)
	return r, nil
}
func CanonicalCheckout(r CheckoutRequest) ([]byte, error) {
	r, e := normalizeCheckout(r)
	if e != nil {
		return nil, e
	}
	return encode(r, requestLimit)
}
func ParseCheckout(raw []byte) (CheckoutRequest, error) {
	var r CheckoutRequest
	if _, e := strictObject(raw, requestLimit, checkoutFields, nil, &r); e != nil {
		return CheckoutRequest{}, e
	}
	return normalizeCheckout(r)
}
func cancelValid(r CancelRequest) bool {
	return identity(r.SchemaVersion, r.EnvironmentID, r.MerchantID, r.CommandID, r.MerchantOrderID)
}
func CanonicalCancel(r CancelRequest) ([]byte, error) {
	if !cancelValid(r) {
		return nil, fail("invalid_request")
	}
	return encode(r, requestLimit)
}
func ParseCancel(raw []byte) (CancelRequest, error) {
	var r CancelRequest
	if _, e := strictObject(raw, requestLimit, cancelFields, nil, &r); e != nil {
		return CancelRequest{}, e
	}
	if !cancelValid(r) {
		return CancelRequest{}, fail("invalid_request")
	}
	return r, nil
}
func queryValid(r QueryRequest) bool {
	return identity(r.SchemaVersion, r.EnvironmentID, r.MerchantID, r.CommandID, r.MerchantOrderID) && uuidValid(r.QueryID) && commandKind(r.CommandKind) && lowerHex(r.ParameterDigest)
}
func CanonicalQuery(r QueryRequest) ([]byte, error) {
	if !queryValid(r) {
		return nil, fail("invalid_request")
	}
	return encode(r, requestLimit)
}
func ParseQuery(raw []byte) (QueryRequest, error) {
	var r QueryRequest
	if _, e := strictObject(raw, requestLimit, queryFields, nil, &r); e != nil {
		return QueryRequest{}, e
	}
	if !queryValid(r) {
		return QueryRequest{}, fail("invalid_request")
	}
	return r, nil
}
