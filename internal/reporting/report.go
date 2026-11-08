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
}

func (t *totals) add(other *totals) {
	t.requests += other.requests
	t.unsettled += other.unsettled
	t.attempts += other.attempts
	t.unknown += other.unknown
	t.charged.Add(&t.charged, &other.charged)
	t.refunded.Add(&t.refunded, &other.refunded)
	t.cost.Add(&t.cost, &other.cost)
}

func (t *totals) view() Totals {
	net := new(big.Int).Sub(&t.charged, &t.refunded)
	v := Totals{RequestCount: t.requests, UnsettledRequests: t.unsettled, AttemptCount: t.attempts, UnknownProviderCosts: t.unknown,
		Charged: t.charged.String(), Refunded: t.refunded.String(), NetRevenue: net.String(), KnownProviderCost: t.cost.String()}
	if t.unsettled == 0 && t.unknown == 0 {
		margin := new(big.Int).Sub(net, &t.cost).String()
		v.GrossMargin = &margin
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

// aggregate keeps settlement values on the request side of the one-to-many
// attempt relation, so a fallback cannot duplicate customer consumption.
func aggregate(filter Filter, asOf time.Time, facts []requestFact, attempts []attemptFact) (Report, error) {
	if len(facts) > MaxRequests {
		return Report{}, ErrRangeTooLarge
	}
	report := Report{From: filter.From.UTC(), To: filter.To.UTC(), AsOf: asOf.UTC(), Currency: "CNY", AmountUnit: "micro_cny", Scope: "request_created_window",
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
		entry := &requestTotal{row: fact.row}
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
		if source == mediaFactSource || (a.status != "contract" && a.status != "invoice") || a.amount == nil {
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
	type modelTotal struct {
		name string
		sum  totals
	}
	models := map[string]*modelTotal{}
	var summary totals
	for _, request := range requests {
		model := models[request.row.ModelID]
		if model == nil {
			model = &modelTotal{name: request.row.ModelName}
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
