package reporting

import (
	"errors"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/moni-ai/moni-aigate/internal/economics"
)

var errUnavailable = errors.New("report_unavailable")

type requestFact struct {
	row               RequestRow
	charged, refunded *string
	key               string
	source            factSource
	cache             bool
	economics         *requestEconomicsFact
}

type requestEconomicsFact struct {
	offerVersionID, taskClass string
	fxPolicyVersionID         string
	valuationCurrency         economics.Currency
	fx                        economics.FxPolicy
	createdAt                 time.Time
}

type attemptFact struct {
	id, request, channel, channelName, status string
	amount                                    *string
	source                                    factSource
	currency                                  string
	asOf                                      *time.Time
	revision                                  string
}

type attemptCostFact struct {
	tenant, attempt, component, id string
	currency, basis                string
	amount                         *string
	asOf                           time.Time
	revision                       int64
}

type settlementLotFact struct {
	key, request, currency, basisKind, buyRateID string
	tenant, lotID                                string
	charged, refunded, fiat, issued              int64
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

type marginAggregate struct {
	amount        big.Int
	complete      bool
	contributions int64
	currency      string
	valuation     string
	fxPolicies    map[string]struct{}
}

type totals struct {
	requests, unsettled, attempts, unknown int64
	charged, refunded, cost                big.Int
	paymentBasis, cashRefunded             map[string]*big.Int
	providerCost                           map[string]*big.Int
	basisUnknown                           bool
	allInvoice                             bool
	provisional, billConfirmed             *big.Int
	marginCurrency, marginValuation        string
	fxPolicyVersions                       []string
	costAsOf                               *time.Time
	costRevision                           string
	provisionalAggregate                   marginAggregate
	confirmedAggregate                     marginAggregate
}

func newMarginAggregate() marginAggregate {
	return marginAggregate{complete: true, fxPolicies: map[string]struct{}{}}
}

func newTotals() totals {
	return totals{
		paymentBasis: map[string]*big.Int{}, cashRefunded: map[string]*big.Int{}, providerCost: map[string]*big.Int{},
		allInvoice: true, provisionalAggregate: newMarginAggregate(), confirmedAggregate: newMarginAggregate(),
	}
}

func addMoney(values map[string]*big.Int, currency string, amount *big.Int) {
	if values[currency] == nil {
		values[currency] = new(big.Int)
	}
	values[currency].Add(values[currency], amount)
}

func observeCost(t *totals, asOf *time.Time, revision string) {
	if asOf == nil {
		return
	}
	at := asOf.UTC()
	if t.costAsOf == nil || at.After(*t.costAsOf) || at.Equal(*t.costAsOf) && revision > t.costRevision {
		t.costAsOf = &at
		t.costRevision = revision
	}
}

func (m *marginAggregate) add(value *big.Int, currency, valuation string, fxPolicies []string) {
	if value == nil {
		m.complete = false
		return
	}
	if !m.complete {
		return
	}
	if m.contributions == 0 {
		m.amount.Set(value)
		m.currency, m.valuation = currency, valuation
	} else {
		if m.currency != currency {
			m.complete = false
			return
		}
		m.amount.Add(&m.amount, value)
		if valuation == "fx_snapshot" {
			m.valuation = fxSnapshotValuation
		}
	}
	for _, id := range fxPolicies {
		m.fxPolicies[id] = struct{}{}
	}
	m.contributions++
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
		addMoney(t.paymentBasis, currency, amount)
	}
	for currency, amount := range other.cashRefunded {
		addMoney(t.cashRefunded, currency, amount)
	}
	for currency, amount := range other.providerCost {
		addMoney(t.providerCost, currency, amount)
	}
	observeCost(t, other.costAsOf, "")
	t.provisionalAggregate.add(other.provisional, other.marginCurrency, other.marginValuation, other.fxPolicyVersions)
	t.confirmedAggregate.add(other.billConfirmed, other.marginCurrency, other.marginValuation, other.fxPolicyVersions)
}

func moneyMap(values map[string]*big.Int) map[string]string {
	result := make(map[string]string, len(values))
	for currency, amount := range values {
		result[currency] = amount.String()
	}
	return result
}

const (
	originalCurrencyValuation = "original_currency"
	fxSnapshotValuation       = "fx_snapshot"
)

func optionalString(value string) *string {
	copy := value
	return &copy
}

func sortedPolicyIDs(values map[string]struct{}) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (t *totals) view(singleRequest bool) Totals {
	net := new(big.Int).Sub(&t.charged, &t.refunded)
	v := Totals{
		RequestCount: t.requests, UnsettledRequests: t.unsettled, AttemptCount: t.attempts, UnknownProviderCosts: t.unknown,
		Charged: t.charged.String(), Refunded: t.refunded.String(), NetRevenue: net.String(), KnownProviderCost: t.cost.String(),
		PaymentBasis: moneyMap(t.paymentBasis), CashRefunded: moneyMap(t.cashRefunded), ProviderCost: moneyMap(t.providerCost),
		FxPolicyVersions: []string{},
	}
	if t.costAsOf != nil {
		at := t.costAsOf.UTC()
		v.CostAsOf = &at
	}
	var provisional, confirmed *big.Int
	var currency, valuation string
	var policies []string
	if singleRequest {
		provisional, confirmed = t.provisional, t.billConfirmed
		currency, valuation = t.marginCurrency, t.marginValuation
		policies = append([]string(nil), t.fxPolicyVersions...)
		v.CostRevision = t.costRevision
	} else {
		if t.provisionalAggregate.complete {
			provisional = new(big.Int).Set(&t.provisionalAggregate.amount)
		}
		if t.confirmedAggregate.complete {
			confirmed = new(big.Int).Set(&t.confirmedAggregate.amount)
		}
		currency, valuation = t.provisionalAggregate.currency, t.provisionalAggregate.valuation
		policies = sortedPolicyIDs(t.provisionalAggregate.fxPolicies)
		if currency == "" && provisional != nil {
			currency, valuation = "CNY", originalCurrencyValuation
		}
	}
	if provisional != nil {
		value := provisional.String()
		v.ProvisionalGrossMargin = &value
	}
	if confirmed != nil {
		value := confirmed.String()
		v.BillConfirmedGrossMargin = &value
	}
	if provisional != nil || confirmed != nil {
		v.GrossMarginCurrency = optionalString(currency)
		v.MarginValuation = optionalString(valuation)
		v.FxPolicyVersions = append([]string{}, policies...)
	}
	// Compatibility consumers see bill-confirmed evidence when available, and
	// otherwise the explicitly provisional contribution margin.
	if confirmed != nil {
		value := confirmed.String()
		v.GrossMargin = &value
	} else if provisional != nil {
		value := provisional.String()
		v.GrossMargin = &value
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

func validCurrency(currency string) bool {
	return currency == string(economics.CurrencyCNY) || currency == string(economics.CurrencyUSD)
}

func nonZeroCurrencies(t *totals) []string {
	seen := map[string]struct{}{}
	// A known zero after a full refund still carries its original currency. Do
	// not drop that key and accidentally compare a foreign-currency cost as CNY.
	for currency := range t.paymentBasis {
		seen[currency] = struct{}{}
	}
	for currency := range t.providerCost {
		seen[currency] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for currency := range seen {
		result = append(result, currency)
	}
	sort.Strings(result)
	return result
}

func economicsVector(values map[string]*big.Int) (economics.MoneyVector, bool) {
	result := economics.MoneyVector{}
	for currency, value := range values {
		if value.Sign() == 0 {
			continue
		}
		if !validCurrency(currency) || !value.IsInt64() {
			return nil, false
		}
		result[economics.Currency(currency)] = economics.Money(value.Int64())
	}
	return result, true
}

func validateRequestEconomics(row RequestRow, snapshot *requestEconomicsFact) error {
	if snapshot == nil {
		if row.OfferVersionID != "" || row.TaskClass != "" {
			return errUnavailable
		}
		return nil
	}
	if row.OfferVersionID == "" || row.OfferVersionID != snapshot.offerVersionID || row.TaskClass != snapshot.taskClass ||
		economics.TaskClass(snapshot.taskClass).Validate() != nil || snapshot.createdAt.IsZero() ||
		snapshot.fx.Validate() != nil || snapshot.fx.ID != snapshot.fxPolicyVersionID ||
		snapshot.valuationCurrency != snapshot.fx.ValuationCurrency || snapshot.valuationCurrency.Validate() != nil {
		return errUnavailable
	}
	return nil
}

func (t *totals) calculateMargins(row RequestRow, snapshot *requestEconomicsFact) error {
	if err := validateRequestEconomics(row, snapshot); err != nil {
		return err
	}
	if t.unsettled != 0 || t.unknown != 0 || t.basisUnknown {
		return nil
	}
	currencies := nonZeroCurrencies(t)
	if len(currencies) <= 1 {
		currency := "CNY"
		if len(currencies) == 1 {
			currency = currencies[0]
		}
		basis := t.paymentBasis[currency]
		if basis == nil {
			basis = new(big.Int)
		}
		cost := t.providerCost[currency]
		if cost == nil {
			cost = new(big.Int)
		}
		t.provisional = new(big.Int).Sub(basis, cost)
		if t.allInvoice {
			t.billConfirmed = new(big.Int).Set(t.provisional)
		}
		t.marginCurrency, t.marginValuation = currency, originalCurrencyValuation
		return nil
	}
	// Legacy requests have no frozen FX and therefore cannot manufacture a
	// cross-currency margin. P0 requests use the snapshot observation time so a
	// later policy expiry does not rewrite historical valuation evidence.
	if snapshot == nil {
		return nil
	}
	basis, ok := economicsVector(t.paymentBasis)
	if !ok {
		return nil
	}
	cost, ok := economicsVector(t.providerCost)
	if !ok {
		return nil
	}
	income, err := snapshot.fx.IncomeLowerVector(basis, snapshot.createdAt)
	if err != nil {
		return nil
	}
	upper, err := snapshot.fx.CostUpperVector(cost, snapshot.createdAt)
	if err != nil {
		return nil
	}
	t.provisional = new(big.Int).Sub(big.NewInt(income.Int64()), big.NewInt(upper.Int64()))
	if t.allInvoice {
		t.billConfirmed = new(big.Int).Set(t.provisional)
	}
	t.marginCurrency, t.marginValuation = string(snapshot.valuationCurrency), fxSnapshotValuation
	t.fxPolicyVersions = []string{snapshot.fxPolicyVersionID}
	return nil
}

type requestTotal struct {
	row       RequestRow
	sum       totals
	source    factSource
	cache     bool
	economics *requestEconomicsFact
}

type channelTotal struct {
	name              string
	cost              big.Int
	attempts, unknown int64
	requests          map[string]struct{}
}

type lotTotal struct {
	issued, fiat        int64
	currency, basisKind string
	net, basis          big.Int
}

func aggregate(filter Filter, asOf time.Time, facts []requestFact, attempts []attemptFact, innerCosts []attemptCostFact, allocations []settlementLotFact, refunds []cashRefundFact) (Report, error) {
	if len(facts) > MaxRequests {
		return Report{}, ErrRangeTooLarge
	}
	report := Report{From: filter.From.UTC(), To: filter.To.UTC(), AsOf: asOf.UTC(), Currency: "COIN", AmountUnit: "micro_coin", Scope: "request_created_window",
		Models: []ModelReport{}, Channels: []ChannelReport{}, Requests: []RequestRow{}, OfferReports: []OfferReport{}}
	requests := make(map[string]*requestTotal, len(facts))
	for _, fact := range facts {
		key := fact.key
		if key == "" {
			key = sourceKey(fact.source, fact.row.RequestID)
		}
		if _, duplicate := requests[key]; duplicate {
			return Report{}, errUnavailable
		}
		entry := &requestTotal{row: fact.row, sum: newTotals(), source: fact.source, cache: fact.cache, economics: fact.economics}
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
	channels := map[string]*channelTotal{}
	seenAttempts := map[string]bool{}
	attemptOwners := map[string]*requestTotal{}
	attemptChannels := map[string]*channelTotal{}
	for _, a := range attempts {
		source := a.source
		request, exists := requests[factKey(source, a.request)]
		attemptKey := sourceKey(source, a.id)
		if !exists || seenAttempts[attemptKey] || source != mediaFactSource && request.sum.attempts >= 2 {
			return Report{}, errUnavailable
		}
		seenAttempts[attemptKey] = true
		attemptOwners[attemptKey] = request
		request.sum.attempts++
		channel := channels[a.channel]
		if channel == nil {
			channel = &channelTotal{name: a.channelName, requests: map[string]struct{}{}}
			channels[a.channel] = channel
		}
		attemptChannels[attemptKey] = channel
		channel.requests[a.request] = struct{}{}
		channel.attempts++
		observeCost(&request.sum, a.asOf, a.revision)
		if (a.status != "contract" && a.status != "invoice") || a.amount == nil {
			request.sum.unknown++
			channel.unknown++
			continue
		}
		cost, ok := amount(*a.amount)
		if !ok {
			return Report{}, errUnavailable
		}
		currency := a.currency
		if currency == "" {
			currency = "CNY"
		}
		if !validCurrency(currency) {
			return Report{}, errUnavailable
		}
		if currency == "CNY" {
			request.sum.cost.Add(&request.sum.cost, cost)
			channel.cost.Add(&channel.cost, cost)
		}
		addMoney(request.sum.providerCost, currency, cost)
		if a.status == "contract" {
			request.sum.allInvoice = false
		}
	}

	// SQL already selects the latest component revision. Repeating the selection
	// here keeps the aggregate fail-closed for alternate readers and fixtures.
	latestInner := map[string]attemptCostFact{}
	for _, component := range innerCosts {
		attemptKey := sourceKey(textFactSource, component.attempt)
		if attemptOwners[attemptKey] == nil || component.component == "" || component.revision <= 0 {
			return Report{}, errUnavailable
		}
		key := attemptKey + "|" + component.component
		if previous, exists := latestInner[key]; exists {
			if previous.revision == component.revision {
				return Report{}, errUnavailable
			}
			if previous.revision > component.revision {
				continue
			}
		}
		latestInner[key] = component
	}
	innerKeys := make([]string, 0, len(latestInner))
	for key := range latestInner {
		innerKeys = append(innerKeys, key)
	}
	sort.Strings(innerKeys)
	for _, key := range innerKeys {
		component := latestInner[key]
		request := attemptOwners[sourceKey(textFactSource, component.attempt)]
		channel := attemptChannels[sourceKey(textFactSource, component.attempt)]
		at := component.asOf.UTC()
		observeCost(&request.sum, &at, component.id)
		if (component.basis != "contract" && component.basis != "invoice") || component.amount == nil {
			request.sum.unknown++
			channel.unknown++
			continue
		}
		cost, ok := amount(*component.amount)
		if !ok || !validCurrency(component.currency) {
			return Report{}, errUnavailable
		}
		if component.currency == "CNY" {
			request.sum.cost.Add(&request.sum.cost, cost)
			channel.cost.Add(&channel.cost, cost)
		}
		addMoney(request.sum.providerCost, component.currency, cost)
		if component.basis == "contract" {
			request.sum.allInvoice = false
		}
	}
	for _, request := range requests {
		// Only an explicit cache finalization proves known-zero supplier cost.
		// Other settled no-attempt text requests remain unknown and suppress margin.
		if request.source != mediaFactSource && request.row.BillingStatus == "settled" && request.sum.attempts == 0 && !request.cache {
			request.sum.unknown++
		}
	}
	allocatedByRequest := map[string]*big.Int{}
	refundedByRequest := map[string]*big.Int{}
	seenAllocations := map[string]bool{}
	lots := map[string]*lotTotal{}
	for _, allocation := range allocations {
		if allocation.key == "" || seenAllocations[allocation.key] || allocation.lotID == "" {
			return Report{}, errUnavailable
		}
		seenAllocations[allocation.key] = true
		_, exists := requests[factKey("", allocation.request)]
		if !exists || allocation.charged < 0 || allocation.refunded < 0 || allocation.refunded > allocation.charged || allocation.fiat < 0 || allocation.issued <= 0 || allocation.charged > allocation.issued {
			return Report{}, errUnavailable
		}
		addMoney(allocatedByRequest, allocation.request, big.NewInt(allocation.charged))
		addMoney(refundedByRequest, allocation.request, big.NewInt(allocation.refunded))
		lotKey := allocation.tenant + "|" + allocation.lotID
		lot := lots[lotKey]
		if lot == nil {
			lot = &lotTotal{issued: allocation.issued, fiat: allocation.fiat, currency: allocation.currency, basisKind: allocation.basisKind}
			lots[lotKey] = lot
		} else if lot.issued != allocation.issued || lot.fiat != allocation.fiat || lot.currency != allocation.currency || lot.basisKind != allocation.basisKind {
			return Report{}, errUnavailable
		}
		lot.net.Add(&lot.net, big.NewInt(allocation.charged-allocation.refunded))
	}
	for _, allocation := range allocations {
		request := requests[factKey("", allocation.request)]
		netCharged := allocation.charged - allocation.refunded
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
		if allocation.buyRateID == "" || !validCurrency(allocation.currency) {
			request.sum.basisUnknown = true
			continue
		}
		// Recompute from cumulative net consumption on the original edge. Never
		// subtract separately floored refunds, which would destroy the remainder.
		numerator := new(big.Int).Mul(big.NewInt(netCharged), big.NewInt(allocation.fiat))
		basis := new(big.Int).Quo(numerator, big.NewInt(allocation.issued))
		addMoney(request.sum.paymentBasis, allocation.currency, basis)
		lots[allocation.tenant+"|"+allocation.lotID].basis.Add(&lots[allocation.tenant+"|"+allocation.lotID].basis, basis)
	}
	for _, lot := range lots {
		if lot.net.Sign() < 0 || lot.net.Cmp(big.NewInt(lot.issued)) > 0 ||
			lot.basisKind == "paid" && lot.basis.Cmp(big.NewInt(lot.fiat)) > 0 {
			return Report{}, errUnavailable
		}
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
		refunded := refundedByRequest[key]
		if refunded == nil {
			refunded = new(big.Int)
		}
		if refunded.Cmp(&request.sum.refunded) != 0 {
			return Report{}, errUnavailable
		}
		if err := request.sum.calculateMargins(request.row, request.economics); err != nil {
			return Report{}, err
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
		request.row.Totals = request.sum.view(true)
		report.Requests = append(report.Requests, request.row)
	}
	// Offer dimensions use the same request totals and margin accumulator as the
	// summary and model dimensions; no independent profit query is introduced.
	offerTotals := map[string]*totals{}
	for _, request := range requests {
		if request.row.OfferVersionID == "" {
			continue
		}
		key := request.row.OfferVersionID + "|" + request.row.TaskClass
		if offerTotals[key] == nil {
			offerTotals[key] = func() *totals { value := newTotals(); return &value }()
		}
		offerTotals[key].add(&request.sum)
	}
	for key, sum := range offerTotals {
		parts := strings.SplitN(key, "|", 2)
		report.OfferReports = append(report.OfferReports, OfferReport{OfferVersionID: parts[0], TaskClass: parts[1], Totals: sum.view(false)})
	}
	for id, model := range models {
		report.Models = append(report.Models, ModelReport{ModelID: id, ModelName: model.name, Totals: model.sum.view(false)})
	}
	for id, channel := range channels {
		report.Channels = append(report.Channels, ChannelReport{ChannelID: id, ChannelName: channel.name, RequestCount: int64(len(channel.requests)), AttemptCount: channel.attempts, UnknownProviderCosts: channel.unknown, KnownProviderCost: channel.cost.String()})
	}
	for _, refund := range refunds {
		value, ok := amount(refund.amount)
		if !ok || !validCurrency(refund.currency) {
			return Report{}, errUnavailable
		}
		addMoney(summary.cashRefunded, refund.currency, value)
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
	sort.Slice(report.OfferReports, func(i, j int) bool {
		if report.OfferReports[i].OfferVersionID == report.OfferReports[j].OfferVersionID {
			return report.OfferReports[i].TaskClass < report.OfferReports[j].TaskClass
		}
		return report.OfferReports[i].OfferVersionID < report.OfferReports[j].OfferVersionID
	})
	report.Summary = summary.view(false)
	return report, nil
}
