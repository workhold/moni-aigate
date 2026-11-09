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
	ChannelID             string
	VersionID             string
	CredentialID          string
	Model                 string
	Region                string
	HealthKey             string
	Priority              int
	Weight                uint64
	MaxInflight           int
	CredentialWeight      uint64 // Zero defaults to one; independent of channel Weight.
	CredentialMaxInflight int    // Zero defaults to MaxInflight; shared across model bindings and versions.
	Enabled               bool
	ExclusionReason       string
	Decisions             []Decision
	Endpoint              provider.Endpoint
	CostPolicy            CostPolicy
	CostQuote             routecost.Quote
	CostLimits            routecost.Limits
	InputUpperBound       int64
	OutputLimit           int64
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
	return c
}

func exclusionReason(c Candidate) string {
	switch c.ExclusionReason {
	case "disabled", "channel_quarantined", "credential_revoked", "binding_isolated", "region_forbidden":
		return c.ExclusionReason
	default:
		return "disabled"
	}
}

type AdmitFunc func(context.Context, Candidate, int) error
type Plan struct {
	mu            sync.Mutex
	candidates    []Candidate
	max, attempts int
	tried         map[string]bool
	committed     bool
	active        bool
}

func NewPlan(candidates []Candidate, maxAttempts int) (*Plan, error) {
	if maxAttempts < 1 || maxAttempts > 2 || len(candidates) == 0 {
		return nil, ErrInvalidPolicy
	}
	p := &Plan{max: maxAttempts, tried: map[string]bool{}}
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
		if _, err := metering.ManagementTokenBounds(c.CostQuote.Schema(), metering.Supplier, c.InputUpperBound, c.OutputLimit); err != nil {
			return nil, ErrInvalidPolicy
		}
		if len(p.candidates) > 0 {
			a, _ := c.CostQuote.Schema().Digest()
			b, _ := p.candidates[0].CostQuote.Schema().Digest()
			if a != b {
				return nil, ErrInvalidPolicy
			}
		}
		if len(p.candidates) > 0 && (c.CostPolicy != p.candidates[0].CostPolicy || c.InputUpperBound != p.candidates[0].InputUpperBound || c.OutputLimit != p.candidates[0].OutputLimit) {
			return nil, ErrInvalidPolicy
		}
		if c.ChannelID == "" || c.VersionID == "" || c.CredentialID == "" || c.Model == "" || c.Weight == 0 || c.MaxInflight <= 0 || c.CredentialMaxInflight < 0 {
			return nil, ErrInvalidPolicy
		}
		c.CredentialWeight = credentialWeight(c)
		c.CredentialMaxInflight = credentialCapacity(c)
		group := seen[c.ChannelID]
		if group == nil {
			if len(seen) >= 1000 || math.MaxUint64-total < c.Weight {
				return nil, ErrInvalidPolicy
			}
			total += c.Weight
			group = &channelPolicy{candidate: c, credentials: map[string]bool{}}
			seen[c.ChannelID] = group
		} else {
			previous := group.candidate
			if c.CredentialID == "" || previous.CredentialID == "" || c.VersionID != previous.VersionID || c.Model != previous.Model || c.Region != previous.Region || c.Priority != previous.Priority || c.Weight != previous.Weight || c.MaxInflight != previous.MaxInflight || !c.CostQuote.Equal(previous.CostQuote) || !c.CostLimits.Equal(previous.CostLimits) {
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
	c, generation, err := engine.acquire(p.candidates, p.tried)
	if err != nil {
		return nil, err
	}
	a := &Attempt{Candidate: clone(c), Sequence: p.attempts + 1, engine: engine, candidate: clone(c), generation: generation}
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
	p.tried[c.ChannelID] = true
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
)

type Config struct {
	FailureThreshold int
	OpenDuration     time.Duration
	Now              func() time.Time
	RandN            func(uint64) uint64
}
type health struct {
	state      State
	failures   int
	until      time.Time
	probe      bool
	generation uint64
}
type Engine struct {
	mu                 sync.Mutex
	config             Config
	health             map[string]*health
	inflight           map[string]int
	credentialInflight map[credentialLeaseKey]int
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
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.RandN == nil {
		c.RandN = rand.Uint64N
	}
	return &Engine{config: c, health: map[string]*health{}, inflight: map[string]int{}, credentialInflight: map[credentialLeaseKey]int{}}
}
func key(c Candidate) string {
	if c.HealthKey != "" {
		return c.HealthKey
	}
	return c.ChannelID
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
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.config.Now()
	type channelPool struct {
		candidate Candidate
		members   []int
		weight    uint64
	}
	channels := []channelPool{}
	channelIndexes := map[string]int{}
	for i, c := range candidates {
		channelIndex, exists := channelIndexes[c.ChannelID]
		if !exists {
			channelIndex = len(channels)
			channelIndexes[c.ChannelID] = channelIndex
			channels = append(channels, channelPool{candidate: c})
		}
		channels[channelIndex].members = append(channels[channelIndex].members, i)
	}
	pool := []int{}
	decisions := make([]Decision, len(channels))
	highest := math.MinInt
	var weight uint64
	for i := range channels {
		group := &channels[i]
		c := group.candidate
		decisions[i] = Decision{ChannelID: c.ChannelID, VersionID: c.VersionID, Priority: c.Priority, Weight: c.Weight}
		eligible := group.members[:0]
		for _, member := range group.members {
			candidate := candidates[member]
			costReason, err := CostExclusion(candidate.CostPolicy, candidate.CostQuote, candidate.CostLimits)
			if err != nil {
				return Candidate{}, 0, err
			}
			reason := ""
			switch {
			case !candidate.Enabled:
				reason = exclusionReason(candidate)
			case tried[candidate.ChannelID]:
				reason = "already_tried"
			case costReason != "":
				reason = costReason
			case ProcurementExclusion(candidate) != "":
				reason = ProcurementExclusion(candidate)
			case e.inflight[candidate.ChannelID] >= candidate.MaxInflight || e.credentialInflight[credentialKey(candidate)] >= credentialCapacity(candidate):
				reason = "capacity_full"
			default:
				h := e.get(key(candidate))
				switch {
				case h.state == Quarantined:
					reason = "quarantined"
				case h.state == Open && now.Before(h.until):
					reason = "circuit_open"
				default:
					if h.state == Open {
						h.state = HalfOpen
						h.probe = false
						h.generation++
					}
					if h.state == HalfOpen && h.probe {
						reason = "half_open_busy"
					}
				}
			}
			if reason != "" {
				// An unavailable channel reports its first member's exclusion.
				// Frozen candidate order makes mixed member failures stable while
				// retaining the existing safe decision vocabulary.
				if decisions[i].Reason == "" {
					decisions[i].Reason = reason
				}
				continue
			}
			memberWeight := credentialWeight(candidate)
			if math.MaxUint64-group.weight < memberWeight {
				return Candidate{}, 0, ErrInvalidPolicy
			}
			group.weight += memberWeight
			eligible = append(eligible, member)
		}
		group.members = eligible
		if len(eligible) == 0 {
			continue
		}
		decisions[i].Reason = "lower_priority"
		if c.Priority > highest {
			highest = c.Priority
			pool = pool[:0]
			weight = 0
		}
		if c.Priority == highest {
			if math.MaxUint64-weight < c.Weight {
				return Candidate{}, 0, ErrInvalidPolicy
			}
			pool = append(pool, i)
			weight += c.Weight
		}
	}
	if len(pool) == 0 {
		return Candidate{}, 0, ErrUnavailable
	}
	if channels[pool[0]].candidate.CostPolicy.Mode == "cost_first" {
		var lowest *Candidate
		cheapest := make([]int, 0, len(pool))
		for _, i := range pool {
			c := channels[i].candidate
			bounds, err := metering.ManagementTokenBounds(c.CostQuote.Schema(), metering.Supplier, c.InputUpperBound, c.OutputLimit)
			if err != nil {
				return Candidate{}, 0, err
			}
			rates, err := c.CostQuote.Rates()
			if err != nil {
				return Candidate{}, 0, err
			}
			comparison := -1
			if lowest != nil {
				other, err := metering.ManagementTokenBounds(lowest.CostQuote.Schema(), metering.Supplier, lowest.InputUpperBound, lowest.OutputLimit)
				if err != nil {
					return Candidate{}, 0, err
				}
				otherRates, err := lowest.CostQuote.Rates()
				if err != nil {
					return Candidate{}, 0, err
				}
				comparison, err = metering.CompareExactCost(bounds, rates, other, otherRates)
				if err != nil {
					return Candidate{}, 0, err
				}
			}
			if lowest == nil || comparison < 0 {
				lowest = &channels[i].candidate
				cheapest = cheapest[:0]
			}
			if comparison <= 0 {
				cheapest = append(cheapest, i)
			}
		}
		for _, i := range pool {
			decisions[i].Reason = "higher_cost"
		}
		pool = cheapest
		weight = 0
		for _, i := range pool {
			if math.MaxUint64-weight < channels[i].candidate.Weight {
				return Candidate{}, 0, ErrInvalidPolicy
			}
			weight += channels[i].candidate.Weight
		}
	}
	for _, i := range pool {
		decisions[i].Reason = "weighted_not_selected"
	}
	roll := e.config.RandN(weight)
	if roll >= weight {
		return Candidate{}, 0, ErrInvalidPolicy
	}
	for _, i := range pool {
		group := channels[i]
		if roll < group.candidate.Weight {
			selected := group.members[0]
			if len(group.members) > 1 {
				memberRoll := e.config.RandN(group.weight)
				if memberRoll >= group.weight {
					return Candidate{}, 0, ErrInvalidPolicy
				}
				for _, member := range group.members {
					memberWeight := credentialWeight(candidates[member])
					if memberRoll < memberWeight {
						selected = member
						break
					}
					memberRoll -= memberWeight
				}
			}
			c := candidates[selected]
			e.inflight[c.ChannelID]++
			e.credentialInflight[credentialKey(c)]++
			h := e.get(key(c))
			if h.state == HalfOpen {
				h.probe = true
			}
			decisions[i].Reason = "selected"
			c.Decisions = decisions
			return c, h.generation, nil
		}
		roll -= group.candidate.Weight
	}
	return Candidate{}, 0, ErrUnavailable
}

type Attempt struct {
	Candidate  Candidate
	Sequence   int
	engine     *Engine
	once       sync.Once
	candidate  Candidate
	generation uint64
	plan       *Plan
}

func (a *Attempt) Finish(outcome Outcome) {
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
		h.probe = false
		switch outcome {
		case Success:
			h.state = Closed
			h.failures = 0
			h.until = time.Time{}
		case Failure:
			h.failures++
			if half || h.failures >= e.config.FailureThreshold {
				h.state = Open
				h.until = e.config.Now().Add(e.config.OpenDuration)
				h.generation++
			}
		case Quarantine:
			h.state = Quarantined
			h.generation++
		case Neutral: // In particular, a cancelled half-open customer request
			// releases its token without falsely proving success or failure.
		}
	})
}

// SetQuarantined mirrors an authoritative persisted operating state. The caller
// must persist quarantine and an audited recovery; an Engine is not that store.
func (e *Engine) SetQuarantined(healthKey string, enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h := e.get(healthKey)
	h.generation++
	h.probe = false
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
		h.probe = false
		h.generation++
	}
}
func (e *Engine) Snapshot(healthKey, channelID string) HealthSnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	h := e.get(healthKey)
	return HealthSnapshot{State: h.state, ConsecutiveFailures: h.failures, OpenUntil: h.until, ProbeInFlight: h.probe, Inflight: e.inflight[channelID]}
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
