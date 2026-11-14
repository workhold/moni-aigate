package billing

import (
	"math"

	"github.com/moni-ai/moni-aigate/internal/security"
)

func normalizeCreditInput(in *CreditInput) error {
	if in.BasisKind == "" {
		in.BasisKind = "paid"
	}
	if in.RequestedBy == "" {
		in.RequestedBy = in.ActorID
	}
	if in.RequestedBy != in.ActorID || in.RequestID != "" && !validWalletOperatorOperation(in.RequestID) {
		return fail("invalid_credit", "invalid credit requester or wallet request")
	}
	if !boundedString(in.IdempotencyKey, 200) || !boundedString(in.RequestHMAC, 256) || !boundedString(in.BusinessRef, 200) || !security.ValidReference(in.ProofRef, 256) || !boundedString(in.RequestedBy, 200) {
		return fail("invalid_credit", "invalid credit identity or proof")
	}
	if in.Amount < 0 || in.BonusAmount < 0 {
		return fail("invalid_credit", "credit amounts must be nonnegative")
	}
	if in.Amount > 1000000000000 || in.BonusAmount > 1000000000000 || in.Amount > Money(math.MaxInt64)-in.BonusAmount {
		return fail("amount_overflow", "credit amount exceeds supported range")
	}
	if in.Amount == 0 && in.BonusAmount == 0 {
		return fail("invalid_credit", "credit amount is empty")
	}
	if in.TransferRef != "" && !security.ValidReference(in.TransferRef, 256) || len(in.FxNote) > 500 {
		return fail("invalid_credit", "invalid transfer or FX reference")
	}
	switch in.BasisKind {
	case "paid":
		if in.Amount <= 0 || in.FiatAmount <= 0 || in.FiatCurrency == "" {
			return fail("invalid_credit", "paid credit requires fiat amount and currency")
		}
		if in.FiatCurrency == "USD" {
			return fail("invalid_credit", "USD coin rate version is not installed")
		}
		if in.FiatCurrency != "CNY" {
			return fail("invalid_credit", "unsupported fiat currency")
		}
	case "grant":
		if in.Amount != 0 || in.BonusAmount <= 0 || in.FiatAmount != 0 || in.FiatCurrency != "" {
			return fail("invalid_credit", "grant requires only a bonus amount")
		}
	case "unknown":
		if in.Amount <= 0 || in.BonusAmount != 0 || in.FiatAmount != 0 || in.FiatCurrency != "" {
			return fail("invalid_credit", "unknown basis forbids fiat facts and bonus")
		}
	default:
		return fail("invalid_credit", "invalid credit basis")
	}
	return nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func creditInputMatches(in CreditInput, o CreditOrder) bool {
	var fiat FiatMoney
	if o.FiatAmount != nil {
		fiat = *o.FiatAmount
	}
	requestedByMatches := o.RequestedBy == in.RequestedBy || in.RequestID != "" && o.RequestID == in.RequestID
	return o.Requester == in.ActorID && requestedByMatches && o.RequestID == in.RequestID && o.Amount == in.Amount && o.BasisKind == in.BasisKind && o.FiatCurrency == in.FiatCurrency && fiat == in.FiatAmount && o.FxNote == in.FxNote && o.BonusAmount == in.BonusAmount && o.TransferRef == in.TransferRef && o.ProofRef == in.ProofRef
}
