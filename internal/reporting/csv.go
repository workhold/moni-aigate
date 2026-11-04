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
	trimmed := strings.TrimLeftFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\ufeff' })
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func WriteCSV(writer io.Writer, report Report) error {
	w := csv.NewWriter(writer)
	if err := w.Write([]string{"request_id", "tenant_id", "tenant_name", "model_id", "model_name", "created_at", "status", "billing_status", "charged", "refunded", "net_revenue", "attempt_count", "known_provider_cost", "unknown_provider_costs", "gross_margin"}); err != nil {
		return err
	}
	for _, r := range report.Requests {
		margin := ""
		if r.GrossMargin != nil {
			margin = *r.GrossMargin
		}
		if err := w.Write([]string{csvText(r.RequestID), csvText(r.TenantID), csvText(r.TenantName), csvText(r.ModelID), csvText(r.ModelName), r.CreatedAt.UTC().Format(time.RFC3339Nano), csvText(r.Status), csvText(r.BillingStatus), r.Charged, r.Refunded, r.NetRevenue, strconv.FormatInt(r.AttemptCount, 10), r.KnownProviderCost, strconv.FormatInt(r.UnknownProviderCosts, 10), margin}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
