package reporting

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// csvText protects text cells after spreadsheet whitespace/control trimming.
// Actual numeric columns intentionally retain signed exact integer notation.
func csvText(value string) string {
	trimmed := strings.TrimLeftFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == rune(0xfeff) })
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func optionalAmount(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func WriteCSV(writer io.Writer, report Report) error {
	w := csv.NewWriter(writer)
	if err := w.Write([]string{"request_id", "tenant_id", "tenant_name", "model_id", "model_name", "created_at", "status", "billing_status", "charged", "refunded", "net_revenue", "attempt_count", "known_provider_cost", "unknown_provider_costs", "gross_margin", "payment_basis_cny", "payment_basis_usd", "cash_refunded_cny", "cash_refunded_usd", "protocol", "execution_kind", "profile_version", "schema_id", "schema_digest", "variant_key",
		"offer_version_id", "task_class", "provider_cost_cny", "provider_cost_usd", "cost_as_of", "cost_revision", "provisional_gross_margin", "bill_confirmed_gross_margin", "gross_margin_currency", "margin_valuation", "fx_policy_versions"}); err != nil {
		return err
	}
	for _, r := range report.Requests {
		basisCNY, basisUSD := r.PaymentBasis["CNY"], r.PaymentBasis["USD"]
		cashCNY, cashUSD := r.CashRefunded["CNY"], r.CashRefunded["USD"]
		providerCNY, providerUSD := r.ProviderCost["CNY"], r.ProviderCost["USD"]
		costAsOf := ""
		if r.CostAsOf != nil {
			costAsOf = r.CostAsOf.UTC().Format(time.RFC3339Nano)
		}
		currency, valuation := optionalAmount(r.GrossMarginCurrency), optionalAmount(r.MarginValuation)
		if err := w.Write([]string{csvText(r.RequestID), csvText(r.TenantID), csvText(r.TenantName), csvText(r.ModelID), csvText(r.ModelName), r.CreatedAt.UTC().Format(time.RFC3339Nano), csvText(r.Status), csvText(r.BillingStatus), r.Charged, r.Refunded, r.NetRevenue, strconv.FormatInt(r.AttemptCount, 10), r.KnownProviderCost, strconv.FormatInt(r.UnknownProviderCosts, 10), optionalAmount(r.GrossMargin), basisCNY, basisUSD, cashCNY, cashUSD, csvText(r.Protocol), csvText(r.ExecutionKind), csvText(r.ProfileVersion), csvText(r.SchemaID), csvText(r.SchemaDigest), csvText(r.VariantKey),
			csvText(r.OfferVersionID), csvText(r.TaskClass), providerCNY, providerUSD, costAsOf, csvText(r.CostRevision), optionalAmount(r.ProvisionalGrossMargin), optionalAmount(r.BillConfirmedGrossMargin), currency, valuation, csvText(strings.Join(r.FxPolicyVersions, ";"))}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
