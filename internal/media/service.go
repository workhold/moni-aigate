package media

import (
	"context"
	"errors"
	"io"
	"sync"
)

var (
	ErrInvalidReference = errors.New("media_invalid_reference")
	ErrAdapterProtocol  = errors.New("media_adapter_protocol")
)

// RequestAttemptRef identifies the immutable facts that the later writer must
// attach to this execution. The service never creates or guesses these IDs.
type RequestAttemptRef struct {
	TenantID  string
	RequestID string
	AttemptID string
}

// UsageRefs identify the observation and settlement facts created by the
// integration layer. They are deliberately supplied only at confirmation.
type UsageRefs struct {
	ObservationID string
	SettlementID  string
}

func (r RequestAttemptRef) valid() bool {
	return safeReference(r.TenantID) && safeReference(r.RequestID) && safeReference(r.AttemptID)
}

func (r UsageRefs) valid() bool {
	return safeReference(r.ObservationID) && safeReference(r.SettlementID)
}

type ProfileSelector struct {
	Version    string
	VariantKey string
}

func (s ProfileSelector) valid() bool {
	return validUUID(s.Version) && s.VariantKey == "default"
}

// SubmitInput is the only input accepted by an adapter. Input is already
// parsed and bound to Profile. It contains no price, wallet, or token fields.
type SubmitInput struct {
	Refs    RequestAttemptRef
	Profile Profile
	Input   Input
}

// Submission is returned only after the adapter has made its dispatch
// decision. Sent must be true for a nil error. A non-empty Handle or Sent on
// an error means that consumption is unknown and the operation is terminal.
type Submission struct {
	Handle string
	Sent   bool
}

// StreamEvent is opaque provider progress. Media does not interpret it as a
// result or as usage evidence.
type StreamEvent struct {
	Sequence uint64
	Kind     string
	Final    bool
	Data     []byte
}

type Stream interface {
	Next(context.Context) (StreamEvent, error)
	Close() error
}

// Adapter is the provider boundary used by Orchestrator. Implementations
// must perform at most one generation for a Submission. The service enforces
// this at the operation level as well, so recovery cannot silently resubmit.
type Adapter interface {
	Submit(context.Context, SubmitInput) (Submission, error)
	Stream(context.Context, Submission) (Stream, error)
	Result(context.Context, Submission) (InlineResult, error)
	Usage(context.Context, Submission) (UsageEvidence, error)
	Cancel(context.Context, Submission) error
}

type ProfileAuthorization struct {
	Profile         Profile
	Adapter         Adapter
	AuthorizationID string
	Published       bool
}

type ProfileRegistry interface {
	Resolve(context.Context, ProfileSelector) (ProfileAuthorization, error)
}

// Registry is an in-memory publication index for the service boundary. It is
// intentionally explicit. A candidate Profile is never dispatchable merely
// because it parses successfully.
type Registry struct {
	mu      sync.RWMutex
	entries map[ProfileSelector]ProfileAuthorization
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[ProfileSelector]ProfileAuthorization)}
}

// RegisterPublished is the single publication transition for this local
// integration contract. Publication is external to the pure candidate
// profile, and requires a non-empty authorization identity and adapter.
func (r *Registry) RegisterPublished(profile Profile, authorizationID string, adapter Adapter) error {
	if r == nil || !profile.valid || !safeReference(authorizationID) || adapter == nil {
		return ErrProfileNotPublished
	}
	version, err := profile.Version()
	if err != nil {
		return err
	}
	variant, err := profile.VariantKey()
	if err != nil {
		return err
	}
	selector := ProfileSelector{Version: version, VariantKey: variant}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[ProfileSelector]ProfileAuthorization)
	}
	if _, exists := r.entries[selector]; exists {
		return ErrInvalidState
	}
	r.entries[selector] = ProfileAuthorization{
		Profile:         profile,
		Adapter:         adapter,
		AuthorizationID: authorizationID,
		Published:       true,
	}
	return nil
}

func (r *Registry) Resolve(ctx context.Context, selector ProfileSelector) (ProfileAuthorization, error) {
	if err := contextError(ctx); err != nil {
		return ProfileAuthorization{}, err
	}
	if r == nil || !selector.valid() {
		return ProfileAuthorization{}, ErrProfileNotPublished
	}
	r.mu.RLock()
	entry, ok := r.entries[selector]
	r.mu.RUnlock()
	if !ok || !entry.Published || !entry.Profile.valid || entry.Adapter == nil || entry.AuthorizationID == "" {
		return ProfileAuthorization{}, ErrProfileNotPublished
	}
	return entry, nil
}

type Orchestrator struct{ registry ProfileRegistry }

func NewOrchestrator(registry ProfileRegistry) *Orchestrator {
	return &Orchestrator{registry: registry}
}

// Start authorizes and submits exactly once. Unpublished or unconfigured
// profiles fail before the adapter is called.
func (o *Orchestrator) Start(ctx context.Context, selector ProfileSelector, input SubmitInput) (*Execution, error) {
	if o == nil || o.registry == nil || !selector.valid() || !input.Input.valid || !input.Profile.valid {
		return nil, ErrInvalidValue
	}
	if !input.Refs.valid() {
		return nil, ErrInvalidReference
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	authorized, err := o.registry.Resolve(ctx, selector)
	if err != nil {
		return nil, err
	}
	if !authorized.Published || !safeReference(authorized.AuthorizationID) || authorized.Adapter == nil {
		return nil, ErrProfileNotPublished
	}
	if input.Profile.digest != authorized.Profile.digest || input.Profile.version != selector.Version || input.Profile.variant != selector.VariantKey || input.Input.profileDigest != authorized.Profile.digest {
		return nil, ErrBindingMismatch
	}
	lifecycle, err := NewLifecycle(authorized.Profile, input.Input)
	if err != nil {
		return nil, err
	}
	submission, err := authorized.Adapter.Submit(ctx, input)
	if err != nil {
		if submission.Sent || submission.Handle != "" {
			lifecycle, _ = lifecycle.ObserveDispatch()
			lifecycle, _ = lifecycle.MarkConsumptionUnknown()
			return &Execution{lifecycle: lifecycle, refs: input.Refs, adapter: authorized.Adapter, submission: submission, submitted: true}, err
		}
		lifecycle, _ = lifecycle.Fail(FailureTransport)
		return nil, err
	}
	if !submission.Sent || !safeReference(submission.Handle) {
		if submission.Sent || submission.Handle != "" {
			lifecycle, _ = lifecycle.ObserveDispatch()
			lifecycle, _ = lifecycle.MarkConsumptionUnknown()
			return &Execution{lifecycle: lifecycle, refs: input.Refs, adapter: authorized.Adapter, submission: submission, submitted: true}, ErrAdapterProtocol
		}
		return nil, ErrAdapterProtocol
	}
	lifecycle, _ = lifecycle.ObserveDispatch()
	return &Execution{lifecycle: lifecycle, refs: input.Refs, adapter: authorized.Adapter, submission: submission, submitted: true}, nil
}

type Execution struct {
	mu              sync.Mutex
	lifecycle       Lifecycle
	refs            RequestAttemptRef
	adapter         Adapter
	submission      Submission
	submitted       bool
	streamOpened    bool
	resultFetched   bool
	usageFetched    bool
	usageRefs       UsageRefs
	cancelRequested bool
}

func (e *Execution) State() (ExecutionState, error) {
	if e == nil {
		return "", ErrInvalidValue
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lifecycle.State()
}

func (e *Execution) Consumption() (ConsumptionStatus, error) {
	if e == nil {
		return "", ErrInvalidValue
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lifecycle.Consumption()
}

func (e *Execution) References() (RequestAttemptRef, error) {
	if e == nil {
		return RequestAttemptRef{}, ErrInvalidValue
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.refs.valid() {
		return RequestAttemptRef{}, ErrInvalidValue
	}
	return e.refs, nil
}

func (e *Execution) UsageReferences() (UsageRefs, error) {
	if e == nil {
		return UsageRefs{}, ErrInvalidValue
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.usageRefs.valid() {
		return UsageRefs{}, ErrInvalidValue
	}
	return e.usageRefs, nil
}

func (e *Execution) OpenStream(ctx context.Context) (Stream, error) {
	if e == nil {
		return nil, ErrInvalidValue
	}
	e.mu.Lock()
	if err := e.readyLocked(); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if e.lifecycle.state != StateSending {
		e.mu.Unlock()
		return nil, ErrInvalidState
	}
	if e.streamOpened {
		e.mu.Unlock()
		return nil, ErrInvalidState
	}
	e.streamOpened = true
	adapter, submission := e.adapter, e.submission
	e.mu.Unlock()
	stream, err := adapter.Stream(ctx, submission)
	if err != nil || stream == nil {
		e.unknown()
		if err != nil {
			return nil, err
		}
		return nil, ErrAdapterProtocol
	}
	return &executionStream{owner: e, stream: stream}, nil
}

func (e *Execution) Result(ctx context.Context) (InlineResult, error) {
	if e == nil {
		return InlineResult{}, ErrInvalidValue
	}
	e.mu.Lock()
	if err := e.readyLocked(); err != nil {
		e.mu.Unlock()
		return InlineResult{}, err
	}
	if e.resultFetched {
		e.mu.Unlock()
		return InlineResult{}, ErrInvalidState
	}
	e.resultFetched = true
	adapter, submission, profile, input := e.adapter, e.submission, e.lifecycle.profile, e.lifecycle.input
	e.mu.Unlock()
	result, err := adapter.Result(ctx, submission)
	if err != nil {
		e.unknown()
		return InlineResult{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if result.profileDigest != profile.digest || result.profile != profile.version || result.variant != profile.variant || result.size != input.size {
		e.markUnknownLocked()
		return InlineResult{}, ErrBindingMismatch
	}
	next, err := e.lifecycle.ObserveResult(result)
	if err != nil {
		e.markUnknownLocked()
		return InlineResult{}, err
	}
	e.lifecycle = next
	return result, nil
}

func (e *Execution) ConfirmUsage(ctx context.Context, refs UsageRefs) error {
	if e == nil {
		return ErrInvalidValue
	}
	e.mu.Lock()
	if err := e.readyLocked(); err != nil {
		e.mu.Unlock()
		return err
	}
	if !refs.valid() {
		e.mu.Unlock()
		return ErrInvalidReference
	}
	if e.lifecycle.state != StateResultReceived || e.usageFetched {
		e.mu.Unlock()
		return ErrInvalidState
	}
	e.usageFetched = true
	adapter, submission := e.adapter, e.submission
	e.mu.Unlock()
	evidence, err := adapter.Usage(ctx, submission)
	if err != nil {
		e.unknown()
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	next, err := e.lifecycle.ConfirmUsage(evidence)
	if err != nil {
		e.markUnknownLocked()
		return err
	}
	e.lifecycle = next
	e.usageRefs = refs
	return nil
}

func (e *Execution) Cancel(ctx context.Context) error {
	if e == nil {
		return ErrInvalidValue
	}
	e.mu.Lock()
	if err := e.readyLocked(); err != nil {
		e.mu.Unlock()
		return err
	}
	if e.cancelRequested || e.resultFetched || e.usageFetched {
		e.mu.Unlock()
		return ErrInvalidState
	}
	e.cancelRequested = true
	adapter, submission := e.adapter, e.submission
	e.mu.Unlock()
	if err := adapter.Cancel(ctx, submission); err != nil {
		e.unknown()
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	next, err := e.lifecycle.Cancel()
	if err != nil {
		return err
	}
	e.lifecycle = next
	return nil
}

func (e *Execution) unknown() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.markUnknownLocked()
}

func (e *Execution) markUnknownLocked() {
	if e.lifecycle.state == StateSending || e.lifecycle.state == StateResultReceived {
		e.lifecycle, _ = e.lifecycle.MarkConsumptionUnknown()
	}
}

func (e *Execution) readyLocked() error {
	if !e.submitted || e.adapter == nil {
		return ErrInvalidValue
	}
	if e.cancelRequested || e.lifecycle.state == StateConsumptionUnknown || e.lifecycle.state == StateFailed || e.lifecycle.state == StateCancelled || e.lifecycle.state == StateCompleted {
		return ErrInvalidState
	}
	return nil
}

type executionStream struct {
	owner  *Execution
	stream Stream
}

func (s *executionStream) Next(ctx context.Context) (StreamEvent, error) {
	if s == nil || s.owner == nil || s.stream == nil {
		return StreamEvent{}, ErrInvalidValue
	}
	event, err := s.stream.Next(ctx)
	if err != nil && !errors.Is(err, io.EOF) {
		s.owner.unknown()
	}
	return event, err
}

func (s *executionStream) Close() error {
	if s == nil || s.stream == nil {
		return ErrInvalidValue
	}
	return s.stream.Close()
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidValue
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
