// Package routing selects frozen candidates and accounts for execution capacity.
// Callers persist admission before sending, and finish a lease only after the
// corresponding provider execution has actually exited.
package routing

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/routecost"
)

var (
	ErrInvalidPolicy   = errors.New("invalid_route_policy")
	ErrUnavailable     = errors.New("no_available_channel")
	ErrAttemptBudget   = errors.New("attempt_budget_exhausted")
	ErrCommitted       = errors.New("downstream_already_committed")
	ErrAttemptInFlight = errors.New("attempt_still_in_flight")
)

type Candidate struct {
	BindingID             string
	PhysicalModelID       string
	PolicyVersionID       string
	ChannelID             string
	VersionID             string
	CredentialID          string
	Model                 string
	Region                string
	HealthKey             string
	Breaker               BreakerConfig
	Priority              int
	Weight                uint64
	MaxInflight           int
	CredentialWeight      uint64 // Zero defaults to one; independent of channel Weight.
	CredentialMaxInflight int    // Zero defaults to MaxInflight; shared across model bindings and versions.
	Enabled               bool
	ExclusionReason       string
	Decisions             []Decision
	RouteDecision         *RouteDecision
	Endpoint              provider.Endpoint
	CostPolicy            CostPolicy
	CostQuote             routecost.Quote
	CostLimits            routecost.Limits
	InputUpperBound       int64
	OutputLimit           int64
	RetailAmount          int64
	Physical              PhysicalCapability
	Stats                 StatsSnapshot
	Quality               QualityEvidenceSnapshot
	ProcurementManaged    bool
	ProcurementState      string
	ProcurementAvailable  string
}

// Decision is the safe, immutable explanation of one channel at acquisition.
// It intentionally excludes credentials, endpoint details and request content.
type Decision struct {
	ChannelID string `json:"channel_id"`
	VersionID string `json:"version_id"`
	Priority  int    `json:"priority"`
	Weight    uint64 `json:"weight"`
	Reason    string `json:"reason"`
}

func clone(c Candidate) Candidate {
	c.Endpoint.Network.AllowedCIDRs = append([]string(nil), c.Endpoint.Network.AllowedCIDRs...)
	c.Endpoint.Network.AllowedPorts = append([]uint16(nil), c.Endpoint.Network.AllowedPorts...)
	c.Decisions = append([]Decision(nil), c.Decisions...)
	c.RouteDecision = cloneDecision(c.RouteDecision)
	c.Physical.Modalities = append([]string(nil), c.Physical.Modalities...)
	c.Physical.SupportedParameters = append([]string(nil), c.Physical.SupportedParameters...)
	return c
}

func candidateKey(c Candidate) string {
	if c.BindingID != "" {
		return c.BindingID
	}
	return c.ChannelID
}

func exclusionReason(c Candidate) string {
	switch c.ExclusionReason {
	case "not_authorized", "disabled", "channel_quarantined", "credential_revoked", "binding_isolated", "region_forbidden", "capability_missing", "quality_below_floor", "context_too_small", "tools_unsupported", "output_limit_too_small", "capacity_full", "circuit_open", "half_open_busy", "quarantined", "credential_cooldown", "cost_unknown", "cost_limit_exceeded", "margin_below_floor", "margin_basis_unavailable", "cumulative_cost_unavailable", "procurement_paused", "procurement_budget_exceeded", "procurement_bound_invalid", "procurement_budget_unavailable", "task_not_allowed", "offer_inactive", "quality_evidence_missing", "quality_evidence_withdrawn", "quality_evidence_expired", "quality_scope_mismatch", "already_tried":
		return c.ExclusionReason
	default:
		return "disabled"
	}
}

type AdmitFunc func(context.Context, Candidate, int) error
type Plan struct {
	mu              sync.Mutex
	candidates      []Candidate
	max, attempts   int
	tried           map[string]bool
	committed       bool
	active          bool
	policy          DynamicPolicy
	request         RequestShape
	algorithm       string
	priorCostWaived bool
	lastDecision    *RouteDecision
}

func NewPlan(candidates []Candidate, maxAttempts int) (*Plan, error) {
	request := RequestShape{Preference: RoutePreference{AllowFallbacks: true}}
	if len(candidates) > 0 {
		request.InputUpperBound = candidates[0].InputUpperBound
		request.OutputLimit = candidates[0].OutputLimit
	}
	return newPlan(candidates, maxAttempts, DefaultDynamicPolicy(), normalizeRequestShape(request))
}

func NewDynamicPlan(candidates []Candidate, maxAttempts int, policy DynamicPolicy, request RequestShape) (*Plan, error) {
	return newPlan(candidates, maxAttempts, policy, normalizeRequestShape(request))
}

func newPlan(candidates []Candidate, maxAttempts int, policy DynamicPolicy, request RequestShape) (*Plan, error) {
	if maxAttempts < 1 || maxAttempts > 2 || len(candidates) == 0 || policy.Validate() != nil || request.Validate() != nil || policy.Execution != nil && int64(maxAttempts) > policy.Execution.MaxOuterAttempts {
		return nil, ErrInvalidPolicy
	}
	p := &Plan{max: maxAttempts, tried: map[string]bool{}, policy: policy, request: request, algorithm: DynamicAlgorithmVersion}
	type channelPolicy struct {
		candidate   Candidate
		credentials map[string]bool
		weight      uint64
	}
	seen := map[string]*channelPolicy{}
	var total uint64
	for _, c := range candidates {
		if c.CostPolicy == (CostPolicy{}) {
			c.CostPolicy = DefaultCostPolicy()
		}
		if ValidateCostPolicy(c.CostPolicy) != nil || c.InputUpperBound < 0 || c.OutputLimit < 0 || c.CostPolicy.Mode == "cost_first" && c.InputUpperBound == 0 || !c.CostQuote.Matches(c.ChannelID, c.Model) || !c.CostLimits.ValidFor(c.CostQuote) {
			return nil, ErrInvalidPolicy
		}
		if _, err := metering.ManagementTokenBounds(c.CostQuote.Schema(), metering.Supplier, supplierInputBound(c), c.OutputLimit); err != nil {
			return nil, ErrInvalidPolicy
		}
		if c.PhysicalModelID != "" && (c.BindingID == "" || c.Physical.Validate() != nil) {
			return nil, ErrInvalidPolicy
		}
		if len(p.candidates) > 0 {
			a, _ := c.CostQuote.Schema().Digest()
			b, _ := p.candidates[0].CostQuote.Schema().Digest()
			if a != b {
				return nil, ErrInvalidPolicy
			}
		}
		if len(p.candidates) > 0 && (c.CostPolicy != p.candidates[0].CostPolicy || c.OutputLimit != p.candidates[0].OutputLimit || c.RetailAmount != p.candidates[0].RetailAmount || normalizeBreaker(c.Breaker) != normalizeBreaker(p.candidates[0].Breaker)) {
			return nil, ErrInvalidPolicy
		}
		if c.Breaker != (BreakerConfig{}) && ValidateBreakerConfig(c.Breaker) != nil {
			return nil, ErrInvalidPolicy
		}
		if c.ChannelID == "" || c.VersionID == "" || c.CredentialID == "" || c.Model == "" || c.Weight == 0 || c.MaxInflight <= 0 || c.CredentialMaxInflight < 0 {
			return nil, ErrInvalidPolicy
		}
		c.CredentialWeight = credentialWeight(c)
		c.CredentialMaxInflight = credentialCapacity(c)
		group := seen[candidateKey(c)]
		if group == nil {
			if len(seen) >= 1000 || math.MaxUint64-total < c.Weight {
				return nil, ErrInvalidPolicy
			}
			total += c.Weight
			group = &channelPolicy{candidate: c, credentials: map[string]bool{}}
			seen[candidateKey(c)] = group
		} else {
			previous := group.candidate
			if c.CredentialID == "" || previous.CredentialID == "" || c.VersionID != previous.VersionID || c.Model != previous.Model || c.Region != previous.Region || c.Priority != previous.Priority || c.Weight != previous.Weight || c.MaxInflight != previous.MaxInflight || normalizeBreaker(c.Breaker) != normalizeBreaker(previous.Breaker) || !c.CostQuote.Equal(previous.CostQuote) || !c.CostLimits.Equal(previous.CostLimits) {
				return nil, ErrInvalidPolicy
			}
		}
		if len(group.credentials) >= 32 || group.credentials[c.CredentialID] || math.MaxUint64-group.weight < c.CredentialWeight {
			return nil, ErrInvalidPolicy
		}
		group.credentials[c.CredentialID] = true
		group.weight += c.CredentialWeight
		p.candidates = append(p.candidates, clone(c))
	}
	return p, nil
}
func (p *Plan) Attempts() int     { p.mu.Lock(); defer p.mu.Unlock(); return p.attempts }
func (p *Plan) CommitDownstream() { p.mu.Lock(); defer p.mu.Unlock(); p.committed = true }
func (p *Plan) LastDecision() *RouteDecision {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneDecision(p.lastDecision)
}
func (p *Plan) CanRetry(ctx context.Context, err error) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ctx.Err() == nil && !p.committed && !p.active && p.attempts < p.max && provider.SafeToRetry(err)
}

// Start reserves channel and credential capacity, then invokes the authoritative DB
// admission. A failed admission neither dispatches nor consumes an attempt.
func (p *Plan) Start(ctx context.Context, engine *Engine, admit AdmitFunc) (*Attempt, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.committed {
		return nil, ErrCommitted
	}
	if p.active {
		return nil, ErrAttemptInFlight
	}
	if p.attempts >= p.max {
		return nil, ErrAttemptBudget
	}
	if engine == nil || admit == nil {
		return nil, ErrInvalidPolicy
	}
	// Prior-supplier accounting is a plain dynamic-v2 concern. Offer-backed plans
	// keep cumulative cost under the Reserve-frozen economics snapshot authority.
	if p.attempts > 0 && p.algorithm != "dynamic-v1" && p.request.Economics == nil {
		if p.priorCostWaived {
			p.request.PriorSupplierAmount, p.request.PriorSupplierCurrency, p.request.PriorSupplierUnknown = "0", "CNY", false
			p.priorCostWaived = false
		} else {
			amount, currency, unknown := priorSupplierCost(p.lastDecision)
			p.request.PriorSupplierAmount, p.request.PriorSupplierCurrency, p.request.PriorSupplierUnknown = amount, currency, unknown
		}
	}
	c, generation, decision, err := engine.acquirePlan(p, p.attempts+1)
	p.lastDecision = cloneDecision(decision)
	if err != nil {
		return nil, err
	}
	a := &Attempt{Candidate: clone(c), Sequence: p.attempts + 1, Frozen: frozenExecution(ctx, p, c, p.attempts+1), engine: engine, candidate: clone(c), generation: generation}
	defer func() {
		// Admission can panic after capacity/half-open acquisition. Until
		// ownership transfers to the caller, every exit returns that lease.
		// Start still holds p.mu here: Finish must never run after a.plan is
		// bound, because it would try to lock that same mutex again.
		if a.plan == nil {
			a.Finish(Neutral)
		}
	}()
	if err := admit(ctx, clone(c), a.Sequence); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.attempts++
	p.tried[candidateKey(c)] = true
	p.active = true
	a.plan = p
	return a, nil
}

type State string

const (
	Closed      State = "closed"
	Open        State = "open"
	HalfOpen    State = "half_open"
	Quarantined State = "quarantined"
)

type Outcome string

const (
	Success    Outcome = "success"
	Failure    Outcome = "failure"
	Neutral    Outcome = "neutral"
	Quarantine Outcome = "quarantine"
	// Overloaded marks an upstream 529: the provider is busy, which is not an
	// account or channel defect. It applies a fixed short rest on the
	// channel+model and never contributes to the circuit ladder.
	Overloaded Outcome = "overloaded"
)

// Attribution windows for the 043 amendment. Fixed bounded constants, not
// route-version fields: circuit parameters stay versioned, these do not.
const (
	attributionWindow    = 60 * time.Second
	successRecencyWindow = 5 * time.Minute
	overloadedRest       = 30 * time.Second
	attributionLedgerMax = 64
)

// FailureDetail carries the machine signals (failure class and upstream HTTP
// status) needed to attribute a counted failure. It never contains error text,
// URLs or credentials.
type FailureDetail struct {
	Class          provider.FailureClass
	UpstreamStatus int
}

type BreakerConfig struct {
	FailureThreshold int
	OpenDuration     time.Duration
	HalfOpenTokens   int
}

func DefaultBreakerConfig() BreakerConfig {
	return BreakerConfig{FailureThreshold: 3, OpenDuration: 30 * time.Second, HalfOpenTokens: 1}
}

func normalizeBreaker(c BreakerConfig) BreakerConfig {
	if c == (BreakerConfig{}) {
		return DefaultBreakerConfig()
	}
	return c
}

func ValidateBreakerConfig(c BreakerConfig) error {
	if c.FailureThreshold < 2 || c.FailureThreshold > 20 || c.OpenDuration < time.Second || c.OpenDuration > 10*time.Minute || c.OpenDuration%time.Millisecond != 0 || c.HalfOpenTokens < 1 || c.HalfOpenTokens > 4 {
		return ErrInvalidPolicy
	}
	return nil
}

type Config struct {
	FailureThreshold int
	OpenDuration     time.Duration
	HalfOpenTokens   int
	Now              func() time.Time
	RandN            func(uint64) uint64
}
type health struct {
	state      State
	failures   int
	until      time.Time
	probes     int
	generation uint64
	successAt  time.Time     // Last Success at this key (043 attribution).
	ledger     []attribEntry // Recent attributable upstream failures.
}

// attribEntry is one counted bare-5xx failure that a later identical rejection
// from a different credential of the same channel+model may withdraw.
type attribEntry struct {
	class        provider.FailureClass
	status       int
	credentialID string
	at           time.Time
}

// cooldownKey scopes the 043 credential cooldown to one pool member of one
// channel+model. It is admission filtering, separate from the health state.
type cooldownKey struct {
	channelID    string
	model        string
	credentialID string
}

type Engine struct {
	mu                 sync.Mutex
	config             Config
	health             map[string]*health
	inflight           map[string]int
	credentialInflight map[credentialLeaseKey]int
	credentialCooldown map[cooldownKey]time.Time
}

type credentialLeaseKey struct {
	credentialID string
}

func credentialKey(c Candidate) credentialLeaseKey {
	return credentialLeaseKey{credentialID: c.CredentialID}
}

func credentialWeight(c Candidate) uint64 {
	if c.CredentialWeight == 0 {
		return 1
	}
	return c.CredentialWeight
}

func credentialCapacity(c Candidate) int {
	if c.CredentialMaxInflight == 0 {
		return c.MaxInflight
	}
	return c.CredentialMaxInflight
}

type HealthSnapshot struct {
	State               State
	ConsecutiveFailures int
	OpenUntil           time.Time
	ProbeInFlight       bool
	Inflight            int
}

func NewEngine(c Config) *Engine {
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = 3
	}
	if c.OpenDuration <= 0 {
		c.OpenDuration = 30 * time.Second
	}
	if c.HalfOpenTokens <= 0 {
		c.HalfOpenTokens = 1
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.RandN == nil {
		c.RandN = rand.Uint64N
	}
	return &Engine{config: c, health: map[string]*health{}, inflight: map[string]int{}, credentialInflight: map[credentialLeaseKey]int{}, credentialCooldown: map[cooldownKey]time.Time{}}
}
func key(c Candidate) string {
	if c.HealthKey != "" {
		return c.HealthKey
	}
	return c.ChannelID
}
func (e *Engine) breaker(c Candidate) BreakerConfig {
	if c.Breaker != (BreakerConfig{}) {
		return c.Breaker
	}
	return BreakerConfig{FailureThreshold: e.config.FailureThreshold, OpenDuration: e.config.OpenDuration, HalfOpenTokens: e.config.HalfOpenTokens}
}
func (e *Engine) get(k string) *health {
	h := e.health[k]
	if h == nil {
		h = &health{state: Closed}
		e.health[k] = h
	}
	return h
}

func (e *Engine) acquire(candidates []Candidate, tried map[string]bool) (Candidate, uint64, error) {
	request := normalizeRequestShape(RequestShape{Preference: RoutePreference{AllowFallbacks: true}})
	if len(candidates) > 0 {
		request.InputUpperBound = candidates[0].InputUpperBound
		request.OutputLimit = candidates[0].OutputLimit
	}
	plan := &Plan{candidates: candidates, tried: tried, policy: DefaultDynamicPolicy(), request: request}
	candidate, generation, _, err := e.acquirePlan(plan, 1)
	return candidate, generation, err
}

type Attempt struct {
	Candidate  Candidate
	Sequence   int
	Frozen     *FrozenExecution
	engine     *Engine
	once       sync.Once
	candidate  Candidate
	generation uint64
	plan       *Plan
}

// NoteNoSupplierConsumption records that this attempt proved it did not consume supplier budget.
// The next attempt then keeps a zero prior cost instead of the unused quote upper bound.
func (a *Attempt) NoteNoSupplierConsumption() {
	if a == nil || a.plan == nil {
		return
	}
	a.plan.mu.Lock()
	a.plan.priorCostWaived = true
	a.plan.mu.Unlock()
}

func (a *Attempt) Finish(outcome Outcome) {
	a.FinishWithFailure(outcome, FailureDetail{})
}

// FinishWithFailure carries the machine signals of an upstream failure so the
// 043 attribution can withdraw a counted failure when an identical
// class+status rejection is reproduced by another credential of the same
// channel+model. The detail never changes retry eligibility or billing.
func (a *Attempt) FinishWithFailure(outcome Outcome, detail FailureDetail) {
	a.once.Do(func() {
		defer func() {
			if a.plan != nil {
				a.plan.mu.Lock()
				a.plan.active = false
				a.plan.mu.Unlock()
			}
		}()
		e := a.engine
		e.mu.Lock()
		defer e.mu.Unlock()
		id := a.candidate.ChannelID
		if e.inflight[id] > 0 {
			e.inflight[id]--
		}
		credentialID := credentialKey(a.candidate)
		if e.credentialInflight[credentialID] > 0 {
			e.credentialInflight[credentialID]--
		}
		h := e.get(key(a.candidate))
		// An old completion must not undo a newer open/quarantine/half-open
		// decision or release the newer generation's probe token.
		if h.generation != a.generation {
			return
		}
		if h.state == Quarantined && outcome != Quarantine {
			return
		}
		half := h.state == HalfOpen
		if half && h.probes > 0 {
			h.probes--
		}
		breaker := e.breaker(a.candidate)
		now := e.config.Now()
		switch outcome {
		case Success:
			h.state = Closed
			h.failures = 0
			h.until = time.Time{}
			h.successAt = now
			if half {
				h.probes = 0
				h.generation++
			}
		case Failure:
			if attributed(h, detail, a.candidate.CredentialID, now) {
				// Request-shaped rejection: no ladder count, no rest. The
				// paired entry and its earlier increment are withdrawn inside
				// attributed; a half-open probe keeps its returned token.
				break
			}
			if attributable(detail) {
				h.pruneLedger(now)
				h.ledger = append(h.ledger, attribEntry{class: detail.Class, status: detail.UpstreamStatus, credentialID: a.candidate.CredentialID, at: now})
			}
			h.failures++
			if half || h.failures >= breaker.FailureThreshold {
				h.state = Open
				h.until = now.Add(breaker.OpenDuration)
				h.probes = 0
				h.generation++
			}
		case Quarantine:
			h.state = Quarantined
			h.probes = 0
			h.generation++
		case Overloaded:
			// Provider-busy rest: fixed, short, outside the ladder, in memory
			// only. It must not manufacture failure history.
			h.state = Open
			h.until = now.Add(overloadedRest)
			h.probes = 0
			h.generation++
		case Neutral: // In particular, a cancelled half-open customer request
			// releases its token without falsely proving success or failure.
		}
	})
}

func attributable(detail FailureDetail) bool {
	return detail.Class == provider.ClassUpstream && detail.UpstreamStatus > 0
}

// attributed implements the 043 rule: a bare-5xx failure is the request's own
// fault when the same channel+model saw the identical failure class and
// upstream status from a different credential within the attribution window
// while the channel also answered another request successfully recently. It
// consumes the paired entry and withdraws its earlier ladder increment. An
// open breaker is untouchable here: every open bumps the generation, so any
// completion that still lands belongs to the same generation and finds the
// breaker closed or half-open; a half-open probe keeps its returned token.
func attributed(h *health, detail FailureDetail, credentialID string, now time.Time) bool {
	if !attributable(detail) {
		return false
	}
	h.pruneLedger(now)
	match := -1
	for i, entry := range h.ledger {
		if entry.class == detail.Class && entry.status == detail.UpstreamStatus && entry.credentialID != credentialID {
			match = i
			break
		}
	}
	if match < 0 {
		return false
	}
	if h.successAt.IsZero() || now.Sub(h.successAt) > successRecencyWindow {
		return false
	}
	h.ledger = append(h.ledger[:match], h.ledger[match+1:]...)
	if h.failures > 0 {
		h.failures--
	}
	return true
}

func (h *health) pruneLedger(now time.Time) {
	kept := h.ledger[:0]
	for _, entry := range h.ledger {
		if now.Sub(entry.at) <= attributionWindow {
			kept = append(kept, entry)
		}
	}
	h.ledger = kept
	if len(h.ledger) > attributionLedgerMax {
		h.ledger = append([]attribEntry(nil), h.ledger[len(h.ledger)-attributionLedgerMax:]...)
	}
}

// SetQuarantined mirrors an authoritative persisted operating state. The caller
// must persist quarantine and an audited recovery; an Engine is not that store.
func (e *Engine) SetQuarantined(healthKey string, enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h := e.get(healthKey)
	h.generation++
	h.probes = 0
	if enabled {
		h.state = Quarantined
	} else {
		h.state = Closed
		h.failures = 0
		h.until = time.Time{}
	}
}
func (e *Engine) Cooldown(healthKey string, d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h := e.get(healthKey)
	if h.state != Quarantined {
		h.state = Open
		h.until = e.config.Now().Add(d)
		h.probes = 0
		h.generation++
	}
}

// CredentialCooldown rests one pool member (channel+model+credential) for the
// bounded rate-limit duration derived by the gateway. It is member-level
// admission filtering and never touches the channel-level health state, so a
// single throttled credential cannot cool the whole channel+model pool.
func (e *Engine) CredentialCooldown(channelID, model, credentialID string, d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if channelID == "" || model == "" || credentialID == "" || d <= 0 {
		return
	}
	e.credentialCooldown[cooldownKey{channelID: channelID, model: model, credentialID: credentialID}] = e.config.Now().Add(d)
}

func (e *Engine) credentialCooling(channelID, model, credentialID string, now time.Time) bool {
	k := cooldownKey{channelID: channelID, model: model, credentialID: credentialID}
	until, ok := e.credentialCooldown[k]
	if !ok {
		return false
	}
	if !now.Before(until) {
		delete(e.credentialCooldown, k)
		return false
	}
	return true
}
func (e *Engine) Snapshot(healthKey, channelID string) HealthSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	h := e.get(healthKey)
	return HealthSnapshot{State: h.state, ConsecutiveFailures: h.failures, OpenUntil: h.until, ProbeInFlight: h.probes > 0, Inflight: e.inflight[channelID]}
}

// CredentialInflight returns shared capacity in use by a real credential across
// all channels, model bindings and frozen versions handled by this Engine.
func (e *Engine) CredentialInflight(credentialID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if credentialID == "" {
		return 0
	}
	return e.credentialInflight[credentialLeaseKey{credentialID: credentialID}]
}
