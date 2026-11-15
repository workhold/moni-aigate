package reporting

import (
	"errors"
	"math/big"
	"sort"
	"strings"
	"time"
)

var errUnavailable = errors.New("report_unavailable")

type requestFact struct {
	row               RequestRow
	charged, refunded *string
	key               string
	source            factSource
}

type attemptFact struct {
	id, request, channel, channelName, status string
	amount                                    *string
	source                                    factSource
}

type settlementLotFact struct {
	key, request, currency, basisKind, buyRateID string
	charged, fiat, issued                        int64
}

type cashRefundFact struct {
	currency, amount string
}

type factSource string

const (
	textFactSource  factSource = "text"
	mediaFactSource factSource = "media"
)

func sourceKey(source factSource, id string) string {
	if source != mediaFactSource {
		source = textFactSource
	}
	return string(source) + ":" + id
}

func factKey(source factSource, value string) string {
	if strings.HasPrefix(value, string(textFactSource)+":") || strings.HasPrefix(value, string(mediaFactSource)+":") {
		return value
	}
	return sourceKey(source, value)
}

type totals struct {
	requests, unsettled, attempts, unknown int64
	charged, refunded, cost                big.Int
	paymentBasis, cashRefunded             map[string]*big.Int
	basisUnknown                           bool
}

func newTotals() totals {
	return totals{paymentBasis: map[string]*big.Int{}, cashRefunded: map[string]*big.Int{}}
}

func (t *totals) add(other *totals) {
	t.requests += other.requests
	t.unsettled += other.unsettled
	t.attempts += other.attempts
	t.unknown += other.unknown
	t.charged.Add(&t.charged, &other.charged)
	t.refunded.Add(&t.refunded, &other.refunded)
	t.cost.Add(&t.cost, &other.cost)
	if other.basisUnknown {
		t.basisUnknown = true
	}
	for currency, amount := range other.paymentBasis {
		if t.paymentBasis[currency] == nil {
			t.paymentBasis[currency] = new(big.Int)
		}
		t.paymentBasis[currency].Add(t.paymentBasis[currency], amount)
	}
	for currency, amount := range other.cashRefunded {
		if t.cashRefunded[currency] == nil {
			t.cashRefunded[currency] = new(big.Int)
		}
		t.cashRefunded[currency].Add(t.cashRefunded[currency], amount)
	}
}

func moneyMap(values map[string]*big.Int) map[string]string {
	result := make(map[string]string, len(values))
	for currency, amount := range values {
		result[currency] = amount.String()
	}
	return result
}

func (t *totals) view() Totals {
	net := new(big.Int).Sub(&t.charged, &t.refunded)
	v := Totals{RequestCount: t.requests, UnsettledRequests: t.unsettled, AttemptCount: t.attempts, UnknownProviderCosts: t.unknown,
		Charged: t.charged.String(), Refunded: t.refunded.String(), NetRevenue: net.String(), KnownProviderCost: t.cost.String(), PaymentBasis: moneyMap(t.paymentBasis), CashRefunded: moneyMap(t.cashRefunded)}
	if t.unsettled == 0 && t.unknown == 0 && !t.basisUnknown && len(t.paymentBasis) <= 1 {
		basis := new(big.Int)
		currency := "CNY"
		for name, amount := range t.paymentBasis {
			currency = name
			basis = amount
		}
		if currency == "CNY" {
			margin := new(big.Int).Sub(basis, &t.cost).String()
			v.GrossMargin = &margin
		}
	}
	return v
}

func amount(raw string) (*big.Int, bool) {
	if raw == "" || len(raw) > 100 || (len(raw) > 1 && raw[0] == '0') {
		return nil, false
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(raw, 10)
	return n, ok
}

func aggregate(filter Filter, asOf time.Time, facts []requestFact, attempts []attemptFact, allocations []settlementLotFact, refunds []cashRefundFact) (Report, error) {
	if len(facts) > MaxRequests {
		return Report{}, ErrRangeTooLarge
	}
	report := Report{From: filter.From.UTC(), To: filter.To.UTC(), AsOf: asOf.UTC(), Currency: "COIN", AmountUnit: "micro_coin", Scope: "request_created_window",
		Models: []ModelReport{}, Channels: []ChannelReport{}, Requests: []RequestRow{}}
	type requestTotal struct {
		row RequestRow
		sum totals
	}
	requests := make(map[string]*requestTotal, len(facts))
	for _, fact := range facts {
		key := fact.key
		if key == "" {
			key = sourceKey(fact.source, fact.row.RequestID)
		}
		if _, duplicate := requests[key]; duplicate {
			return Report{}, errUnavailable
		}
		entry := &requestTotal{row: fact.row, sum: newTotals()}
		entry.row.CreatedAt = entry.row.CreatedAt.UTC()
		entry.sum.requests = 1
		if fact.charged == nil && fact.refunded == nil {
			entry.sum.unsettled = 1
		} else {
			if fact.charged == nil || fact.refunded == nil {
				return Report{}, errUnavailable
			}
			charged, ok := amount(*fact.charged)
			if !ok {
				return Report{}, errUnavailable
			}
			refunded, ok := amount(*fact.refunded)
			if !ok || refunded.Cmp(charged) > 0 {
				return Report{}, errUnavailable
			}
			entry.sum.charged.Set(charged)
			entry.sum.refunded.Set(refunded)
		}
		requests[key] = entry
	}
	type channelTotal struct {
		name              string
		cost              big.Int
		attempts, unknown int64
		requests          map[string]struct{}
	}
	channels := map[string]*channelTotal{}
	seenAttempts := map[string]bool{}
	for _, a := range attempts {
		source := a.source
		request, exists := requests[factKey(source, a.request)]
		attemptKey := sourceKey(source, a.id)
		if !exists || seenAttempts[attemptKey] || source != mediaFactSource && request.sum.attempts >= 2 {
			return Report{}, errUnavailable
		}
		seenAttempts[attemptKey] = true
		request.sum.attempts++
		channel := channels[a.channel]
		if channel == nil {
			channel = &channelTotal{name: a.channelName, requests: map[string]struct{}{}}
			channels[a.channel] = channel
		}
		channel.requests[a.request] = struct{}{}
		channel.attempts++
		if (a.status != "contract" && a.status != "invoice") || a.amount == nil {
			request.sum.unknown++
			channel.unknown++
			continue
		}
		cost, ok := amount(*a.amount)
		if !ok {
			return Report{}, errUnavailable
		}
		request.sum.cost.Add(&request.sum.cost, cost)
		channel.cost.Add(&channel.cost, cost)
	}
	allocatedByRequest := map[string]*big.Int{}
	seenAllocations := map[string]bool{}
	for _, allocation := range allocations {
		if allocation.key == "" || seenAllocations[allocation.key] {
			return Report{}, errUnavailable
		}
		seenAllocations[allocation.key] = true
		_, exists := requests[factKey("", allocation.request)]
		if !exists || allocation.charged < 0 || allocation.fiat < 0 || allocation.issued <= 0 || allocation.charged > allocation.issued {
			return Report{}, errUnavailable
		}
		if allocatedByRequest[allocation.request] == nil {
			allocatedByRequest[allocation.request] = new(big.Int)
		}
		allocatedByRequest[allocation.request].Add(allocatedByRequest[allocation.request], big.NewInt(allocation.charged))
	}
	for _, allocation := range allocations {
		request := requests[factKey("", allocation.request)]
		switch allocation.basisKind {
		case "grant", "opening":
			continue
		case "unknown":
			request.sum.basisUnknown = true
			continue
		case "paid":
			if allocation.fiat == 0 {
				request.sum.basisUnknown = true
				continue
			}
		default:
			return Report{}, errUnavailable
		}
		if allocation.buyRateID == "" || allocation.currency != "CNY" && allocation.currency != "USD" {
			request.sum.basisUnknown = true
			continue
		}
		numerator := new(big.Int).Mul(big.NewInt(allocation.charged), big.NewInt(allocation.fiat))
		amount := new(big.Int).Quo(numerator, big.NewInt(allocation.issued))
		if request.sum.paymentBasis[allocation.currency] == nil {
			request.sum.paymentBasis[allocation.currency] = new(big.Int)
		}
		request.sum.paymentBasis[allocation.currency].Add(request.sum.paymentBasis[allocation.currency], amount)
	}
	for key, request := range requests {
		allocated := allocatedByRequest[key]
		if allocated == nil {
			if request.sum.charged.Sign() > 0 {
				request.sum.basisUnknown = true
			}
		} else if allocated.Cmp(&request.sum.charged) != 0 {
			request.sum.basisUnknown = true
		}
	}
	type modelTotal struct {
		name string
		sum  totals
	}
	models := map[string]*modelTotal{}
	summary := newTotals()
	for _, request := range requests {
		model := models[request.row.ModelID]
		if model == nil {
			model = &modelTotal{name: request.row.ModelName, sum: newTotals()}
			models[request.row.ModelID] = model
		}
		model.sum.add(&request.sum)
		summary.add(&request.sum)
		request.row.Totals = request.sum.view()
		report.Requests = append(report.Requests, request.row)
	}
	for id, model := range models {
		report.Models = append(report.Models, ModelReport{ModelID: id, ModelName: model.name, Totals: model.sum.view()})
	}
	for id, channel := range channels {
		report.Channels = append(report.Channels, ChannelReport{ChannelID: id, ChannelName: channel.name, RequestCount: int64(len(channel.requests)), AttemptCount: channel.attempts, UnknownProviderCosts: channel.unknown, KnownProviderCost: channel.cost.String()})
	}
	for _, refund := range refunds {
		value, ok := amount(refund.amount)
		if !ok || refund.currency != "CNY" && refund.currency != "USD" {
			return Report{}, errUnavailable
		}
		if summary.cashRefunded[refund.currency] == nil {
			summary.cashRefunded[refund.currency] = new(big.Int)
		}
		summary.cashRefunded[refund.currency].Add(summary.cashRefunded[refund.currency], value)
	}
	sort.Slice(report.Models, func(i, j int) bool { return report.Models[i].ModelID < report.Models[j].ModelID })
	sort.Slice(report.Channels, func(i, j int) bool { return report.Channels[i].ChannelID < report.Channels[j].ChannelID })
	sort.Slice(report.Requests, func(i, j int) bool {
		a, b := report.Requests[i], report.Requests[j]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.RequestID > b.RequestID
		}
		return a.CreatedAt.After(b.CreatedAt)
	})
	report.Summary = summary.view()
	return report, nil
}
