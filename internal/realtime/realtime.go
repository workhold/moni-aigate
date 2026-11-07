// Package realtime contains the provider-independent boundary for the first
// 028-R slice. It deliberately stores identities, state and bounded metadata;
// it does not persist content or perform financial or network operations.
package realtime

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxTicketTTL          = 2 * time.Minute
	DefaultMaxFrameBytes  = 1 << 20
	DefaultMaxTextBytes   = 256 << 10
	DefaultMaxAudioBytes  = 512 << 10
	DefaultMaxQueueEvents = 32
	DefaultMaxQueueBytes  = 4 << 20
	DefaultMaxForwarded   = 64 << 20
	DefaultMaxTurns       = 100
	DefaultSessionTTL     = 30 * time.Minute
	DefaultTurnTTL        = 120 * time.Second
)

var (
	ErrInvalidInput         = errors.New("realtime: invalid input")
	ErrInvalidTransition    = errors.New("realtime: invalid state transition")
	ErrInvalidTime          = errors.New("realtime: time moved backwards")
	ErrSessionClosed        = errors.New("realtime: session is closed")
	ErrSessionDraining      = errors.New("realtime: session is draining")
	ErrTurnInFlight         = errors.New("realtime: another turn is in flight")
	ErrTurnRequired         = errors.New("realtime: event requires an active turn")
	ErrEventConflict        = errors.New("realtime: event identity conflict")
	ErrResponseConflict     = errors.New("realtime: provider response identity conflict")
	ErrNoChargeEvent        = errors.New("realtime: no charge-bearing event accepted")
	ErrNotForwarded         = errors.New("realtime: no bytes have been forwarded")
	ErrBackpressure         = errors.New("realtime: outbound backpressure limit reached")
	ErrLimitExceeded        = errors.New("realtime: resource limit exceeded")
	ErrQueueEmpty           = errors.New("realtime: outbound queue is empty")
	ErrTicketInvalid        = errors.New("realtime: invalid ticket")
	ErrTicketUsed           = errors.New("realtime: ticket already used")
	ErrTicketExpired        = errors.New("realtime: ticket expired")
	ErrTicketBinding        = errors.New("realtime: ticket binding mismatch")
	ErrTicketRevoked        = errors.New("realtime: ticket revoked")
	ErrReconnectUnsupported = errors.New("realtime: reconnect creates a new session")
)

type SessionState string

const (
	SessionOpening  SessionState = "opening"
	SessionOpen     SessionState = "open"
	SessionDraining SessionState = "draining"
	SessionClosed   SessionState = "closed"
)

type TurnState string

const (
	TurnReserved   TurnState = "reserved"
	TurnSending    TurnState = "sending"
	TurnReceiving  TurnState = "receiving"
	TurnFinalizing TurnState = "finalizing"
	TurnSettled    TurnState = "settled"
	TurnReleased   TurnState = "released"
	TurnPending    TurnState = "pending"
)

type UsageState string

const (
	UsageConfirmedFinal UsageState = "confirmed_final"
	UsageUnknown        UsageState = "unknown"
	UsageMissing        UsageState = "missing"
)

type SessionLimits struct {
	MaxFrameBytes     int
	MaxQueueEvents    int
	MaxQueueBytes     int
	MaxForwardedBytes int
	MaxTurns          int
	SessionTTL        time.Duration
	TurnTTL           time.Duration
}

func DefaultSessionLimits() SessionLimits {
	return SessionLimits{
		MaxFrameBytes:     DefaultMaxFrameBytes,
		MaxQueueEvents:    DefaultMaxQueueEvents,
		MaxQueueBytes:     DefaultMaxQueueBytes,
		MaxForwardedBytes: DefaultMaxForwarded,
		MaxTurns:          DefaultMaxTurns,
		SessionTTL:        DefaultSessionTTL,
		TurnTTL:           DefaultTurnTTL,
	}
}

func (l SessionLimits) valid() bool {
	return l.MaxFrameBytes > 0 && l.MaxQueueEvents > 0 && l.MaxQueueBytes > 0 &&
		l.MaxForwardedBytes > 0 && l.MaxTurns > 0 && l.SessionTTL > 0 && l.TurnTTL > 0
}

type SessionConfig struct {
	ID        string
	TenantID  string
	ActorID   string
	APIKeyID  string
	ModelID   string
	ProfileID string
	RouteID   string
	Origin    string
	Limits    SessionLimits
}

type BeginTurnInput struct {
	TurnID        string
	RequestID     string
	ReservationID string
	AttemptID     string
}

type SettlementProof struct {
	FinalizationID     string
	SettlementID       string
	RetailUsageDigest  string
	RetailUsageState   UsageState
	SupplierUsageState UsageState
}

type TurnSnapshot struct {
	ID                 string
	RequestID          string
	ReservationID      string
	AttemptID          string
	State              TurnState
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ForwardedBytes     int
	ChargeEventCount   int
	ProviderResponseID string
	FinalizationID     string
	SettlementID       string
	RetailUsageState   UsageState
	SupplierUsageState UsageState
	TerminalReason     string
}

type SessionSnapshot struct {
	ID             string
	TenantID       string
	ActorID        string
	APIKeyID       string
	ModelID        string
	ProfileID      string
	RouteID        string
	Origin         string
	State          SessionState
	CreatedAt      time.Time
	UpdatedAt      time.Time
	TurnCount      int
	ActiveTurnID   string
	ForwardedBytes int
	QueuedEvents   int
	QueuedBytes    int
	Turns          []TurnSnapshot
}

type turnRecord struct {
	TurnSnapshot
	chargeEventSeen bool
}

type eventRecord struct {
	fingerprint [32]byte
	turnID      string
	typ         EventType
}

type outboundRecord struct {
	event OutboundEvent
}

type Session struct {
	mu sync.Mutex

	cfg       SessionConfig
	state     SessionState
	createdAt time.Time
	updatedAt time.Time

	turns       map[string]*turnRecord
	requestIDs  map[string]string
	activeTurn  string
	events      map[string]eventRecord
	responses   map[string]string
	outbound    []OutboundEvent
	outboundIDs map[string]outboundRecord
	queueBytes  int
	forwarded   int
}

func NewSession(cfg SessionConfig, at time.Time) (*Session, error) {
	if !validID(cfg.ID) || !validID(cfg.TenantID) || !validID(cfg.ActorID) ||
		!validID(cfg.APIKeyID) || !validID(cfg.ModelID) || !validID(cfg.ProfileID) || !validID(cfg.RouteID) {
		return nil, fmt.Errorf("%w: session identity", ErrInvalidInput)
	}
	origin, err := CanonicalOrigin(cfg.Origin)
	if err != nil {
		return nil, err
	}
	if at.IsZero() {
		return nil, fmt.Errorf("%w: zero creation time", ErrInvalidInput)
	}
	if cfg.Limits == (SessionLimits{}) {
		cfg.Limits = DefaultSessionLimits()
	}
	if !cfg.Limits.valid() {
		return nil, fmt.Errorf("%w: invalid session limits", ErrInvalidInput)
	}
	cfg.Origin = origin
	at = at.UTC()
	return &Session{
		cfg:         cfg,
		state:       SessionOpening,
		createdAt:   at,
		updatedAt:   at,
		turns:       make(map[string]*turnRecord),
		requestIDs:  make(map[string]string),
		events:      make(map[string]eventRecord),
		responses:   make(map[string]string),
		outboundIDs: make(map[string]outboundRecord),
	}, nil
}

func (s *Session) TicketBinding() TicketBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	return TicketBinding{
		SessionID: s.cfg.ID, TenantID: s.cfg.TenantID, ActorID: s.cfg.ActorID,
		APIKeyID: s.cfg.APIKeyID, ModelID: s.cfg.ModelID, ProfileID: s.cfg.ProfileID,
		RouteID: s.cfg.RouteID, Origin: s.cfg.Origin,
	}
}

func (s *Session) Open(at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return err
	}
	if s.state != SessionOpening {
		return fmt.Errorf("%w: open from %s", ErrInvalidTransition, s.state)
	}
	if s.sessionExpiredLocked(at) {
		return fmt.Errorf("%w: session already expired", ErrInvalidTransition)
	}
	s.state = SessionOpen
	return nil
}

func (s *Session) BeginDrain(reason string, at time.Time) error {
	if !validID(reason) {
		return fmt.Errorf("%w: drain reason", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return err
	}
	switch s.state {
	case SessionOpen:
		s.state = SessionDraining
		return nil
	case SessionDraining:
		return nil
	case SessionClosed:
		return ErrSessionClosed
	default:
		return fmt.Errorf("%w: drain from %s", ErrInvalidTransition, s.state)
	}
}

type CloseResult struct {
	TurnID                string
	TurnState             TurnState
	CancelUpstream        bool
	ReleasedBeforeForward bool
}

func (s *Session) Close(at time.Time) (CloseResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return CloseResult{}, err
	}
	if s.state == SessionClosed {
		return CloseResult{}, ErrSessionClosed
	}
	if s.state != SessionDraining {
		return CloseResult{}, fmt.Errorf("%w: close from %s", ErrInvalidTransition, s.state)
	}
	result := CloseResult{}
	if s.activeTurn != "" {
		tr := s.turns[s.activeTurn]
		result.TurnID = tr.ID
		if tr.State == TurnReserved {
			s.finishTurnLocked(tr, TurnReleased, "session_closed_before_forward")
			result.ReleasedBeforeForward = true
		} else {
			s.finishTurnLocked(tr, TurnPending, "session_closed_in_flight")
			result.CancelUpstream = true
		}
		result.TurnState = tr.State
	}
	s.state = SessionClosed
	return result, nil
}

func (s *Session) Disconnect(reason string, at time.Time) (CloseResult, error) {
	if !validID(reason) {
		return CloseResult{}, fmt.Errorf("%w: disconnect reason", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return CloseResult{}, err
	}
	if s.state == SessionClosed {
		return CloseResult{}, ErrSessionClosed
	}
	if s.state != SessionOpen && s.state != SessionDraining {
		return CloseResult{}, fmt.Errorf("%w: disconnect from %s", ErrInvalidTransition, s.state)
	}
	s.state = SessionDraining
	result := CloseResult{}
	if s.activeTurn != "" {
		tr := s.turns[s.activeTurn]
		result.TurnID = tr.ID
		if tr.State == TurnReserved {
			s.finishTurnLocked(tr, TurnReleased, "disconnect_before_forward")
			result.ReleasedBeforeForward = true
		} else {
			s.finishTurnLocked(tr, TurnPending, "disconnect_in_flight")
			result.CancelUpstream = true
		}
		result.TurnState = tr.State
	}
	s.state = SessionClosed
	return result, nil
}

func (s *Session) BeginTurn(input BeginTurnInput, at time.Time) (TurnSnapshot, error) {
	if !validID(input.TurnID) || !validID(input.RequestID) || !validID(input.ReservationID) || !validID(input.AttemptID) {
		return TurnSnapshot{}, fmt.Errorf("%w: turn requires request, reservation and attempt identities", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return TurnSnapshot{}, err
	}
	if s.state == SessionClosed {
		return TurnSnapshot{}, ErrSessionClosed
	}
	if s.state != SessionOpen {
		return TurnSnapshot{}, ErrSessionDraining
	}
	if s.sessionExpiredLocked(at) {
		s.state = SessionDraining
		return TurnSnapshot{}, fmt.Errorf("%w: session deadline", ErrLimitExceeded)
	}
	if s.activeTurn != "" {
		return TurnSnapshot{}, ErrTurnInFlight
	}
	if len(s.turns) >= s.cfg.Limits.MaxTurns {
		return TurnSnapshot{}, fmt.Errorf("%w: session turn limit", ErrLimitExceeded)
	}
	if _, ok := s.turns[input.TurnID]; ok {
		return TurnSnapshot{}, fmt.Errorf("%w: duplicate turn ID", ErrInvalidInput)
	}
	if old, ok := s.requestIDs[input.RequestID]; ok {
		return TurnSnapshot{}, fmt.Errorf("%w: request already bound to turn %s", ErrInvalidInput, old)
	}
	tr := &turnRecord{TurnSnapshot: TurnSnapshot{
		ID: input.TurnID, RequestID: input.RequestID, ReservationID: input.ReservationID,
		AttemptID: input.AttemptID, State: TurnReserved, CreatedAt: at.UTC(), UpdatedAt: at.UTC(),
		RetailUsageState: UsageMissing, SupplierUsageState: UsageMissing,
	}}
	s.turns[tr.ID] = tr
	s.requestIDs[tr.RequestID] = tr.ID
	s.activeTurn = tr.ID
	return tr.TurnSnapshot, nil
}

func (s *Session) StartSending(turnID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return err
	}
	if s.state == SessionClosed {
		return ErrSessionClosed
	}
	if s.state != SessionOpen {
		return ErrSessionDraining
	}
	tr, err := s.activeTurnLocked(turnID)
	if err != nil {
		return err
	}
	if tr.State != TurnReserved {
		return fmt.Errorf("%w: sending from %s", ErrInvalidTransition, tr.State)
	}
	if !tr.chargeEventSeen {
		return ErrNoChargeEvent
	}
	tr.State = TurnSending
	tr.UpdatedAt = at.UTC()
	return nil
}

func (s *Session) MarkForwarded(turnID string, bytes int, at time.Time) error {
	if bytes <= 0 {
		return fmt.Errorf("%w: forwarded bytes must be positive", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return err
	}
	if s.state == SessionClosed {
		return ErrSessionClosed
	}
	if s.state != SessionOpen {
		return ErrSessionDraining
	}
	tr, err := s.activeTurnLocked(turnID)
	if err != nil {
		return err
	}
	if tr.State != TurnSending && tr.State != TurnReceiving {
		return fmt.Errorf("%w: forward from %s", ErrInvalidTransition, tr.State)
	}
	if bytes > s.cfg.Limits.MaxFrameBytes || s.forwarded > s.cfg.Limits.MaxForwardedBytes-bytes {
		s.state = SessionDraining
		return fmt.Errorf("%w: forwarded byte limit", ErrLimitExceeded)
	}
	s.forwarded += bytes
	tr.ForwardedBytes += bytes
	tr.UpdatedAt = at.UTC()
	return nil
}

func (s *Session) StartReceiving(turnID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return err
	}
	tr, err := s.activeTurnLocked(turnID)
	if err != nil {
		return err
	}
	if tr.State != TurnSending {
		return fmt.Errorf("%w: receiving from %s", ErrInvalidTransition, tr.State)
	}
	if tr.ForwardedBytes == 0 {
		return ErrNotForwarded
	}
	tr.State = TurnReceiving
	tr.UpdatedAt = at.UTC()
	return nil
}

func (s *Session) BindProviderResponse(turnID, responseID string, at time.Time) error {
	if !validID(responseID) {
		return fmt.Errorf("%w: provider response ID", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return err
	}
	tr, err := s.activeTurnLocked(turnID)
	if err != nil {
		return err
	}
	if tr.State != TurnSending && tr.State != TurnReceiving && tr.State != TurnFinalizing {
		return fmt.Errorf("%w: response binding from %s", ErrInvalidTransition, tr.State)
	}
	if tr.ProviderResponseID != "" {
		if tr.ProviderResponseID == responseID {
			return nil
		}
		return ErrResponseConflict
	}
	if old, ok := s.responses[responseID]; ok && old != turnID {
		return ErrResponseConflict
	}
	tr.ProviderResponseID = responseID
	s.responses[responseID] = turnID
	tr.UpdatedAt = at.UTC()
	return nil
}

func (s *Session) BeginFinalizing(turnID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return err
	}
	tr, err := s.activeTurnLocked(turnID)
	if err != nil {
		return err
	}
	if tr.State != TurnReceiving {
		return fmt.Errorf("%w: finalizing from %s", ErrInvalidTransition, tr.State)
	}
	if tr.ProviderResponseID == "" {
		return fmt.Errorf("%w: provider response is not bound", ErrInvalidInput)
	}
	tr.State = TurnFinalizing
	tr.UpdatedAt = at.UTC()
	return nil
}

func (s *Session) SettleTurn(turnID string, proof SettlementProof, at time.Time) error {
	if !validID(proof.FinalizationID) || !validID(proof.SettlementID) || !validDigestHex(proof.RetailUsageDigest) || proof.RetailUsageState != UsageConfirmedFinal || !validUsageState(proof.SupplierUsageState) {
		return fmt.Errorf("%w: settlement proof", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return err
	}
	tr, err := s.activeTurnLocked(turnID)
	if err != nil {
		return err
	}
	if tr.State != TurnFinalizing {
		return fmt.Errorf("%w: settle from %s", ErrInvalidTransition, tr.State)
	}
	tr.State = TurnSettled
	tr.FinalizationID = proof.FinalizationID
	tr.SettlementID = proof.SettlementID
	tr.RetailUsageState = proof.RetailUsageState
	tr.SupplierUsageState = proof.SupplierUsageState
	tr.UpdatedAt = at.UTC()
	s.activeTurn = ""
	return nil
}

func (s *Session) ReleaseTurn(turnID, reason string, at time.Time) error {
	if !validID(reason) {
		return fmt.Errorf("%w: release reason", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return err
	}
	tr, err := s.activeTurnLocked(turnID)
	if err != nil {
		return err
	}
	if tr.State != TurnReserved || tr.ForwardedBytes != 0 {
		return fmt.Errorf("%w: release from %s", ErrInvalidTransition, tr.State)
	}
	s.finishTurnLocked(tr, TurnReleased, reason)
	return nil
}

func (s *Session) CancelTurn(turnID, reason string, at time.Time) error {
	if !validID(reason) {
		return fmt.Errorf("%w: cancel reason", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return err
	}
	tr, err := s.activeTurnLocked(turnID)
	if err != nil {
		return err
	}
	if tr.State == TurnReserved && tr.ForwardedBytes == 0 {
		s.finishTurnLocked(tr, TurnReleased, reason)
		return nil
	}
	if tr.State == TurnSending || tr.State == TurnReceiving || tr.State == TurnFinalizing {
		s.finishTurnLocked(tr, TurnPending, reason)
		return nil
	}
	return fmt.Errorf("%w: cancel from %s", ErrInvalidTransition, tr.State)
}

func (s *Session) MarkPending(turnID, reason string, at time.Time) error {
	if !validID(reason) {
		return fmt.Errorf("%w: pending reason", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return err
	}
	tr, err := s.activeTurnLocked(turnID)
	if err != nil {
		return err
	}
	if tr.State != TurnSending && tr.State != TurnReceiving && tr.State != TurnFinalizing {
		return fmt.Errorf("%w: pending from %s", ErrInvalidTransition, tr.State)
	}
	s.finishTurnLocked(tr, TurnPending, reason)
	return nil
}

func (s *Session) AcceptEvent(event ClientEvent, at time.Time) (EventResult, error) {
	if err := event.valid(); err != nil {
		return EventResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return EventResult{}, err
	}
	if prior, ok := s.events[event.ID]; ok {
		if prior.fingerprint != event.fingerprint {
			return EventResult{}, ErrEventConflict
		}
		return EventResult{Disposition: EventDuplicate, TurnID: prior.turnID, Type: prior.typ, ChargeBearing: event.ChargeBearing}, nil
	}
	if s.state == SessionClosed {
		return EventResult{}, ErrSessionClosed
	}
	if event.ChargeBearing || event.TurnRequired {
		if s.state != SessionOpen {
			return EventResult{}, ErrSessionDraining
		}
		if s.activeTurn == "" {
			return EventResult{}, ErrTurnRequired
		}
		tr := s.turns[s.activeTurn]
		if tr.State != TurnReserved && tr.State != TurnSending && tr.State != TurnReceiving {
			return EventResult{}, fmt.Errorf("%w: event for turn in %s", ErrInvalidTransition, tr.State)
		}
		tr.UpdatedAt = at.UTC()
		if event.ChargeBearing {
			tr.chargeEventSeen = true
			tr.ChargeEventCount++
		}
		s.events[event.ID] = eventRecord{fingerprint: event.fingerprint, turnID: tr.ID, typ: event.Type}
		return EventResult{Disposition: EventAccepted, TurnID: tr.ID, Type: event.Type, ChargeBearing: event.ChargeBearing}, nil
	}
	if s.state != SessionOpen && s.state != SessionDraining {
		return EventResult{}, ErrSessionClosed
	}
	s.events[event.ID] = eventRecord{fingerprint: event.fingerprint, typ: event.Type}
	return EventResult{Disposition: EventAccepted, Type: event.Type}, nil
}

type EventDisposition string

const (
	EventAccepted  EventDisposition = "accepted"
	EventDuplicate EventDisposition = "duplicate"
)

type EventResult struct {
	Disposition   EventDisposition
	TurnID        string
	Type          EventType
	ChargeBearing bool
}

type OutboundEvent struct {
	ID    string
	Type  string
	Bytes int
}

type QueueDisposition string

const (
	QueueAccepted  QueueDisposition = "accepted"
	QueueDuplicate QueueDisposition = "duplicate"
)

func (s *Session) EnqueueOutbound(event OutboundEvent, at time.Time) (QueueDisposition, error) {
	if !validID(event.ID) || !validID(event.Type) || event.Bytes <= 0 {
		return "", fmt.Errorf("%w: outbound event", ErrInvalidInput)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return "", err
	}
	if prior, ok := s.outboundIDs[event.ID]; ok {
		if prior.event == event {
			return QueueDuplicate, nil
		}
		return "", ErrEventConflict
	}
	if s.state == SessionClosed || s.state == SessionOpening {
		return "", ErrSessionClosed
	}
	if event.Bytes > s.cfg.Limits.MaxFrameBytes || len(s.outbound) >= s.cfg.Limits.MaxQueueEvents || s.queueBytes > s.cfg.Limits.MaxQueueBytes-event.Bytes {
		s.state = SessionDraining
		return "", ErrBackpressure
	}
	s.outbound = append(s.outbound, event)
	s.outboundIDs[event.ID] = outboundRecord{event: event}
	s.queueBytes += event.Bytes
	return QueueAccepted, nil
}

func (s *Session) DequeueOutbound(at time.Time) (OutboundEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return OutboundEvent{}, err
	}
	if s.state == SessionClosed {
		return OutboundEvent{}, ErrSessionClosed
	}
	if len(s.outbound) == 0 {
		return OutboundEvent{}, ErrQueueEmpty
	}
	event := s.outbound[0]
	s.outbound = s.outbound[1:]
	s.queueBytes -= event.Bytes
	return event, nil
}

type DeadlineResult struct {
	SessionExpired bool
	TurnID         string
	TurnState      TurnState
	CancelUpstream bool
	Released       bool
}

func (s *Session) EnforceDeadlines(at time.Time) (DeadlineResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceTimeLocked(at); err != nil {
		return DeadlineResult{}, err
	}
	if s.state == SessionClosed {
		return DeadlineResult{}, ErrSessionClosed
	}
	result := DeadlineResult{}
	if s.state == SessionOpen && s.sessionExpiredLocked(at) {
		s.state = SessionDraining
		result.SessionExpired = true
	}
	if s.activeTurn == "" {
		return result, nil
	}
	tr := s.turns[s.activeTurn]
	if at.UTC().Before(tr.CreatedAt.Add(s.cfg.Limits.TurnTTL)) {
		return result, nil
	}
	result.TurnID = tr.ID
	if tr.State == TurnReserved && tr.ForwardedBytes == 0 {
		s.finishTurnLocked(tr, TurnReleased, "turn_deadline_before_forward")
		result.TurnState = TurnReleased
		result.Released = true
		return result, nil
	}
	if tr.State == TurnSending || tr.State == TurnReceiving || tr.State == TurnFinalizing {
		s.finishTurnLocked(tr, TurnPending, "turn_deadline_in_flight")
		result.TurnState = TurnPending
		result.CancelUpstream = true
	}
	return result, nil
}

func (s *Session) Snapshot() SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	turns := make([]TurnSnapshot, 0, len(s.turns))
	for _, tr := range s.turns {
		turns = append(turns, tr.TurnSnapshot)
	}
	sort.Slice(turns, func(i, j int) bool { return turns[i].ID < turns[j].ID })
	return SessionSnapshot{
		ID: s.cfg.ID, TenantID: s.cfg.TenantID, ActorID: s.cfg.ActorID, APIKeyID: s.cfg.APIKeyID,
		ModelID: s.cfg.ModelID, ProfileID: s.cfg.ProfileID, RouteID: s.cfg.RouteID, Origin: s.cfg.Origin,
		State: s.state, CreatedAt: s.createdAt, UpdatedAt: s.updatedAt, TurnCount: len(turns),
		ActiveTurnID: s.activeTurn, ForwardedBytes: s.forwarded, QueuedEvents: len(s.outbound), QueuedBytes: s.queueBytes,
		Turns: turns,
	}
}

func (s *Session) advanceTimeLocked(at time.Time) error {
	if at.IsZero() {
		return fmt.Errorf("%w: zero time", ErrInvalidInput)
	}
	at = at.UTC()
	if at.Before(s.updatedAt) {
		return ErrInvalidTime
	}
	s.updatedAt = at
	return nil
}

func (s *Session) sessionExpiredLocked(at time.Time) bool {
	return !at.UTC().Before(s.createdAt.Add(s.cfg.Limits.SessionTTL))
}

func (s *Session) activeTurnLocked(turnID string) (*turnRecord, error) {
	if !validID(turnID) {
		return nil, fmt.Errorf("%w: turn ID", ErrInvalidInput)
	}
	if s.activeTurn == "" || s.activeTurn != turnID {
		return nil, fmt.Errorf("%w: turn %s is not active", ErrInvalidTransition, turnID)
	}
	tr := s.turns[turnID]
	if tr == nil {
		return nil, fmt.Errorf("%w: unknown turn", ErrInvalidInput)
	}
	return tr, nil
}

func (s *Session) finishTurnLocked(tr *turnRecord, state TurnState, reason string) {
	tr.State = state
	tr.TerminalReason = reason
	tr.UpdatedAt = s.updatedAt
	s.activeTurn = ""
}

func validUsageState(state UsageState) bool {
	return state == UsageConfirmedFinal || state == UsageUnknown || state == UsageMissing
}

func validDigestHex(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validID(value string) bool {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// TicketBinding is the complete audience and authorization binding of a
// short-lived browser ticket. The token itself is intentionally opaque.
type TicketBinding struct {
	SessionID string
	TenantID  string
	ActorID   string
	APIKeyID  string
	ModelID   string
	ProfileID string
	RouteID   string
	Origin    string
}

func (b TicketBinding) normalized() (TicketBinding, error) {
	if !validID(b.SessionID) || !validID(b.TenantID) || !validID(b.ActorID) || !validID(b.APIKeyID) || !validID(b.ModelID) || !validID(b.ProfileID) || !validID(b.RouteID) {
		return TicketBinding{}, fmt.Errorf("%w: ticket binding identity", ErrTicketInvalid)
	}
	origin, err := CanonicalOrigin(b.Origin)
	if err != nil {
		return TicketBinding{}, ErrTicketInvalid
	}
	b.Origin = origin
	return b, nil
}

func CanonicalOrigin(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || raw == "*" || raw == "null" {
		return "", fmt.Errorf("%w: origin", ErrInvalidInput)
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: origin", ErrInvalidInput)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: origin scheme", ErrInvalidInput)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("%w: origin host", ErrInvalidInput)
	}
	port := u.Port()
	if port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return "", fmt.Errorf("%w: origin port", ErrInvalidInput)
		}
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host + func() string {
		if port == "" {
			return ""
		}
		return ":" + port
	}(), nil
}

type IssuedTicket struct {
	Token  string
	Claims TicketClaims
}

type TicketClaims struct {
	TicketID  string
	Binding   TicketBinding
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type ticketRecord struct {
	claims  TicketClaims
	used    bool
	revoked bool
}

type TicketBook struct {
	mu      sync.Mutex
	tickets map[string]*ticketRecord
}

func NewTicketBook() *TicketBook {
	return &TicketBook{tickets: make(map[string]*ticketRecord)}
}

func (b *TicketBook) Issue(binding TicketBinding, at time.Time, ttl time.Duration) (IssuedTicket, error) {
	if b == nil {
		return IssuedTicket{}, ErrTicketInvalid
	}
	normalized, err := binding.normalized()
	if err != nil {
		return IssuedTicket{}, err
	}
	if at.IsZero() || ttl <= 0 || ttl > MaxTicketTTL {
		return IssuedTicket{}, fmt.Errorf("%w: ticket lifetime", ErrTicketInvalid)
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return IssuedTicket{}, fmt.Errorf("%w: ticket entropy: %v", ErrTicketInvalid, err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	ticketIDDigest := sha256.Sum256(raw[:])
	claims := TicketClaims{TicketID: hex.EncodeToString(ticketIDDigest[:]), Binding: normalized, IssuedAt: at.UTC(), ExpiresAt: at.UTC().Add(ttl)}
	b.mu.Lock()
	b.tickets[token] = &ticketRecord{claims: claims}
	b.mu.Unlock()
	return IssuedTicket{Token: token, Claims: claims}, nil
}

func (b *TicketBook) Redeem(token string, binding TicketBinding, at time.Time) (TicketClaims, error) {
	if b == nil || token == "" || at.IsZero() {
		return TicketClaims{}, ErrTicketInvalid
	}
	normalized, err := binding.normalized()
	if err != nil {
		return TicketClaims{}, ErrTicketBinding
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	record, ok := b.tickets[token]
	if !ok {
		return TicketClaims{}, ErrTicketInvalid
	}
	if record.revoked {
		return TicketClaims{}, ErrTicketRevoked
	}
	if record.used {
		return TicketClaims{}, ErrTicketUsed
	}
	if !at.UTC().Before(record.claims.ExpiresAt) {
		record.used = true
		return TicketClaims{}, ErrTicketExpired
	}
	if at.UTC().Before(record.claims.IssuedAt) {
		return TicketClaims{}, ErrTicketInvalid
	}
	if record.claims.Binding != normalized {
		return TicketClaims{}, ErrTicketBinding
	}
	record.used = true
	return record.claims, nil
}

func (b *TicketBook) Revoke(token string) error {
	if b == nil || token == "" {
		return ErrTicketInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	record, ok := b.tickets[token]
	if !ok {
		return ErrTicketInvalid
	}
	if record.used {
		return ErrTicketUsed
	}
	record.revoked = true
	return nil
}

type EventType string

const (
	EventInputText      EventType = "input.text"
	EventInputAudio     EventType = "input.audio"
	EventResponseCreate EventType = "response.create"
	EventResponseCancel EventType = "response.cancel"
	EventHeartbeat      EventType = "heartbeat"
	EventStatus         EventType = "status"
)

type EventLimits struct {
	MaxFrameBytes int
	MaxTextBytes  int
	MaxAudioBytes int
}

func DefaultEventLimits() EventLimits {
	return EventLimits{MaxFrameBytes: DefaultMaxFrameBytes, MaxTextBytes: DefaultMaxTextBytes, MaxAudioBytes: DefaultMaxAudioBytes}
}

func (l EventLimits) valid() bool {
	return l.MaxFrameBytes > 0 && l.MaxTextBytes > 0 && l.MaxAudioBytes > 0
}

type ClientEvent struct {
	ID            string
	Type          EventType
	PayloadBytes  int
	PayloadDigest string
	Fingerprint   [32]byte
	ChargeBearing bool
	TurnRequired  bool

	fingerprint [32]byte
}

func (e ClientEvent) FingerprintHex() string {
	return hex.EncodeToString(e.Fingerprint[:])
}

func (e ClientEvent) valid() error {
	if !validID(e.ID) || !validEventType(e.Type) || e.Fingerprint == ([32]byte{}) || e.fingerprint != e.Fingerprint || e.PayloadBytes < 0 {
		return fmt.Errorf("%w: client event", ErrInvalidInput)
	}
	return nil
}

func validEventType(typ EventType) bool {
	switch typ {
	case EventInputText, EventInputAudio, EventResponseCreate, EventResponseCancel, EventHeartbeat, EventStatus:
		return true
	default:
		return false
	}
}

type EventParser struct {
	limits EventLimits
}

func NewEventParser(limits EventLimits) (EventParser, error) {
	if limits == (EventLimits{}) {
		limits = DefaultEventLimits()
	}
	if !limits.valid() || limits.MaxTextBytes > limits.MaxFrameBytes || limits.MaxAudioBytes > limits.MaxFrameBytes {
		return EventParser{}, fmt.Errorf("%w: event limits", ErrInvalidInput)
	}
	return EventParser{limits: limits}, nil
}

func (p EventParser) Parse(frame []byte) (ClientEvent, error) {
	if !p.limits.valid() {
		return ClientEvent{}, fmt.Errorf("%w: uninitialized parser", ErrInvalidInput)
	}
	if len(frame) == 0 || len(frame) > p.limits.MaxFrameBytes {
		return ClientEvent{}, fmt.Errorf("%w: frame size", ErrLimitExceeded)
	}
	if !utf8.Valid(frame) {
		return ClientEvent{}, fmt.Errorf("%w: invalid UTF-8 frame", ErrInvalidInput)
	}
	root, err := parseObject(frame)
	if err != nil {
		return ClientEvent{}, err
	}
	if err := exactKeys(root, "event_id", "type", "data"); err != nil {
		return ClientEvent{}, err
	}
	var id string
	var typ string
	if err := json.Unmarshal(root["event_id"], &id); err != nil || !validID(id) {
		return ClientEvent{}, fmt.Errorf("%w: event_id", ErrInvalidInput)
	}
	if err := json.Unmarshal(root["type"], &typ); err != nil || !validEventType(EventType(typ)) {
		return ClientEvent{}, fmt.Errorf("%w: unsupported event type", ErrInvalidInput)
	}
	data, err := parseObject(root["data"])
	if err != nil {
		return ClientEvent{}, err
	}
	event := ClientEvent{ID: id, Type: EventType(typ)}
	canonicalData := []byte("{}")
	switch event.Type {
	case EventInputText:
		if err := exactKeys(data, "text"); err != nil {
			return ClientEvent{}, err
		}
		var text string
		if err := json.Unmarshal(data["text"], &text); err != nil || !utf8.ValidString(text) || text == "" {
			return ClientEvent{}, fmt.Errorf("%w: text payload", ErrInvalidInput)
		}
		if len(text) > p.limits.MaxTextBytes {
			return ClientEvent{}, fmt.Errorf("%w: text payload", ErrLimitExceeded)
		}
		canonicalData, _ = json.Marshal(struct {
			Text string `json:"text"`
		}{Text: text})
		event.PayloadBytes = len(text)
		event.PayloadDigest = digestHex([]byte(text))
		event.ChargeBearing, event.TurnRequired = true, true
	case EventInputAudio:
		if err := exactKeys(data, "audio_base64"); err != nil {
			return ClientEvent{}, err
		}
		var encoded string
		if err := json.Unmarshal(data["audio_base64"], &encoded); err != nil || encoded == "" {
			return ClientEvent{}, fmt.Errorf("%w: audio payload", ErrInvalidInput)
		}
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || base64.StdEncoding.EncodeToString(raw) != encoded {
			return ClientEvent{}, fmt.Errorf("%w: noncanonical audio base64", ErrInvalidInput)
		}
		if len(raw) > p.limits.MaxAudioBytes {
			return ClientEvent{}, fmt.Errorf("%w: audio payload", ErrLimitExceeded)
		}
		canonicalData, _ = json.Marshal(struct {
			AudioBase64 string `json:"audio_base64"`
		}{AudioBase64: encoded})
		event.PayloadBytes = len(raw)
		event.PayloadDigest = digestHex(raw)
		event.ChargeBearing, event.TurnRequired = true, true
	case EventResponseCreate:
		if len(data) != 0 {
			return ClientEvent{}, fmt.Errorf("%w: response.create data", ErrInvalidInput)
		}
		event.ChargeBearing, event.TurnRequired = true, true
	case EventResponseCancel:
		if len(data) != 0 {
			return ClientEvent{}, fmt.Errorf("%w: response.cancel data", ErrInvalidInput)
		}
		event.TurnRequired = true
	case EventHeartbeat, EventStatus:
		if len(data) != 0 {
			return ClientEvent{}, fmt.Errorf("%w: control data", ErrInvalidInput)
		}
	}
	h := sha256.New()
	h.Write([]byte("moni-aigate/realtime-event/v1\x00"))
	h.Write([]byte(event.Type))
	h.Write([]byte{0})
	h.Write(canonicalData)
	copy(event.Fingerprint[:], h.Sum(nil))
	event.fingerprint = event.Fingerprint
	return event, nil
}

func parseObject(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	token, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: JSON object: %v", ErrInvalidInput, err)
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, fmt.Errorf("%w: JSON object required", ErrInvalidInput)
	}
	values := make(map[string]json.RawMessage)
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: JSON key: %v", ErrInvalidInput, err)
		}
		name, ok := key.(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("%w: JSON key", ErrInvalidInput)
		}
		if _, exists := values[name]; exists {
			return nil, fmt.Errorf("%w: duplicate JSON key %q", ErrInvalidInput, name)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("%w: JSON value: %v", ErrInvalidInput, err)
		}
		values[name] = value
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("%w: JSON object close: %v", ErrInvalidInput, err)
	}
	if err := ensureEOF(dec); err != nil {
		return nil, err
	}
	return values, nil
}

func exactKeys(values map[string]json.RawMessage, want ...string) error {
	allowed := make(map[string]struct{}, len(want))
	for _, key := range want {
		allowed[key] = struct{}{}
		if _, ok := values[key]; !ok {
			return fmt.Errorf("%w: missing field %s", ErrInvalidInput, key)
		}
	}
	for key := range values {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%w: unknown field %s", ErrInvalidInput, key)
		}
	}
	return nil
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON", ErrInvalidInput)
		}
		return fmt.Errorf("%w: trailing JSON: %v", ErrInvalidInput, err)
	}
	return nil
}

func digestHex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
