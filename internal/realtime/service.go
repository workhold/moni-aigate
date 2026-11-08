package realtime

// This file is the application boundary around Session. It deliberately
// carries commercial references and settlement proofs without creating any
// wallet, reservation, meter, or settlement facts.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrNotFound            = errors.New("realtime: record not found")
	ErrIdempotencyConflict = errors.New("realtime: idempotency conflict")
	ErrPendingNoResubmit   = errors.New("realtime: pending turn cannot be resubmitted")
	ErrProviderUnknown     = errors.New("realtime: provider consumption is unknown")
	ErrProviderProtocol    = errors.New("realtime: provider protocol violation")
)

// CommercialRefs are created by the existing billing path before a turn is
// started. Realtime only stores and forwards them. It never creates or
// modifies the referenced records.
type CommercialRefs struct {
	RequestID     string
	ReservationID string
	AttemptID     string
}

func (r CommercialRefs) valid() bool {
	return validID(r.RequestID) && validID(r.ReservationID) && validID(r.AttemptID)
}

type SessionRecord struct {
	Config            SessionConfig
	Snapshot          SessionSnapshot
	ProviderSessionID string
}

type TurnRecord struct {
	SessionID       string
	Snapshot        TurnSnapshot
	Refs            CommercialRefs
	ChargeEventID   string
	ChargeEventType EventType
	ChargeDigest    string
	ProviderHandle  string
	DispatchUnknown bool
	CancelRequested bool
}

type EventRecord struct {
	SessionID   string
	EventID     string
	Fingerprint string
	TurnID      string
	Type        EventType
	AcceptedAt  time.Time
}

func (r EventRecord) valid() bool {
	return validID(r.SessionID) && validID(r.EventID) && validDigestHex(r.Fingerprint) &&
		validEventType(r.Type) && !r.AcceptedAt.IsZero()
}

// TicketStore is the durable boundary for one-use browser tickets.
type TicketStore interface {
	IssueTicket(context.Context, TicketBinding, time.Time, time.Duration) (IssuedTicket, error)
	RedeemTicket(context.Context, string, TicketBinding, time.Time) (TicketClaims, error)
	RevokeTicket(context.Context, string) error
}

type SessionStore interface {
	CreateSession(context.Context, SessionRecord) error
	LoadSession(context.Context, string) (SessionRecord, error)
	SaveSession(context.Context, SessionRecord) error
}

type TurnStore interface {
	SaveTurn(context.Context, TurnRecord) error
	LoadTurn(context.Context, string, string) (TurnRecord, error)
}

type EventStore interface {
	AppendEvent(context.Context, EventRecord) (EventDisposition, error)
	FindEvent(context.Context, string, string) (EventRecord, error)
}

type Persistence interface {
	TicketStore
	SessionStore
	TurnStore
	EventStore
}

// ProviderRealtimeAdapter is the only network boundary used by Service.
// Implementations must treat an error with Sent=true or a non-empty Handle as
// unknown consumption. Service records that turn as pending and never calls
// Send again for it.
type ProviderRealtimeAdapter interface {
	Open(context.Context, ProviderOpenInput) (ProviderSession, error)
	Send(context.Context, ProviderSendInput) (ProviderDispatch, error)
	Receive(context.Context, ProviderReceiveInput) (ProviderReceiveResult, error)
	Cancel(context.Context, ProviderCancelInput) error
}

type ProviderOpenInput struct {
	SessionID string
	TenantID  string
	ModelID   string
	ProfileID string
	RouteID   string
}

type ProviderSession struct {
	ID     string
	Handle string
}

type ProviderSendInput struct {
	ProviderSessionID string
	SessionID         string
	TurnID            string
	RequestID         string
	ReservationID     string
	AttemptID         string
	EventID           string
	EventType         EventType
	Payload           []byte
	PayloadDigest     string
}

type ProviderDispatch struct {
	Sent       bool
	Handle     string
	ResponseID string
}

type ProviderReceiveInput struct {
	ProviderSessionID string
	ProviderHandle    string
	SessionID         string
	TurnID            string
}

type ProviderReceiveResult struct {
	ResponseID string
	Bytes      int
	Final      bool
}

type ProviderCancelInput struct {
	ProviderSessionID string
	ProviderHandle    string
	SessionID         string
	TurnID            string
}

type ProviderPayload struct {
	EventID       string
	EventType     EventType
	Payload       []byte
	PayloadDigest string
}

type ServiceOptions struct {
	Store   Persistence
	Adapter ProviderRealtimeAdapter
}

type Service struct {
	store   Persistence
	adapter ProviderRealtimeAdapter
	mu      sync.Mutex
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Store == nil || options.Adapter == nil {
		return nil, fmt.Errorf("%w: realtime service dependencies", ErrInvalidInput)
	}
	return &Service{store: options.Store, adapter: options.Adapter}, nil
}

func contextOK(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidInput)
	}
	return ctx.Err()
}

func (s *Service) IssueTicket(ctx context.Context, binding TicketBinding, at time.Time, ttl time.Duration) (IssuedTicket, error) {
	if err := contextOK(ctx); err != nil {
		return IssuedTicket{}, err
	}
	return s.store.IssueTicket(ctx, binding, at, ttl)
}

// CreateSession consumes the one-use ticket and opens exactly one provider
// session. A provider open failure leaves no local session record to retry
// implicitly, so the caller chooses the recovery policy explicitly.
func (s *Service) CreateSession(ctx context.Context, token string, binding TicketBinding, cfg SessionConfig, at time.Time) (SessionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextOK(ctx); err != nil {
		return SessionSnapshot{}, err
	}
	if token == "" || cfg.ID != binding.SessionID {
		return SessionSnapshot{}, ErrTicketBinding
	}
	claims, err := s.store.RedeemTicket(ctx, token, binding, at)
	if err != nil {
		return SessionSnapshot{}, err
	}
	if claims.Binding != binding {
		return SessionSnapshot{}, ErrTicketBinding
	}
	session, err := NewSession(cfg, at)
	if err != nil {
		return SessionSnapshot{}, err
	}
	if err = session.Open(at); err != nil {
		return SessionSnapshot{}, err
	}
	providerSession, err := s.adapter.Open(ctx, ProviderOpenInput{SessionID: cfg.ID, TenantID: cfg.TenantID, ModelID: cfg.ModelID, ProfileID: cfg.ProfileID, RouteID: cfg.RouteID})
	if err != nil || !validID(providerSession.ID) {
		if err != nil {
			return SessionSnapshot{}, err
		}
		return SessionSnapshot{}, ErrProviderProtocol
	}
	snapshot := session.Snapshot()
	record := SessionRecord{Config: cfg, Snapshot: snapshot, ProviderSessionID: providerSession.ID}
	if err = s.store.CreateSession(ctx, record); err != nil {
		return SessionSnapshot{}, err
	}
	return snapshot, nil
}

func (s *Service) load(ctx context.Context, sessionID string) (*Session, SessionRecord, error) {
	record, err := s.store.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, SessionRecord{}, err
	}
	return restoreSession(record)
}

func (s *Service) save(ctx context.Context, session *Session, record SessionRecord) error {
	record.Snapshot = session.Snapshot()
	return s.store.SaveSession(ctx, record)
}

func restoreSession(record SessionRecord) (*Session, SessionRecord, error) {
	session, err := NewSession(record.Config, record.Snapshot.CreatedAt)
	if err != nil {
		return nil, SessionRecord{}, err
	}
	session.state = record.Snapshot.State
	session.updatedAt = record.Snapshot.UpdatedAt
	session.forwarded = record.Snapshot.ForwardedBytes
	session.activeTurn = record.Snapshot.ActiveTurnID
	for _, snapshot := range record.Snapshot.Turns {
		copySnapshot := snapshot
		session.turns[snapshot.ID] = &turnRecord{TurnSnapshot: copySnapshot, chargeEventSeen: snapshot.ChargeEventCount > 0}
		session.requestIDs[snapshot.RequestID] = snapshot.ID
		if snapshot.ProviderResponseID != "" {
			session.responses[snapshot.ProviderResponseID] = snapshot.ID
		}
	}
	return session, record, nil
}

func (s *Service) BeginTurn(ctx context.Context, sessionID string, input BeginTurnInput, refs CommercialRefs, at time.Time) (TurnSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextOK(ctx); err != nil {
		return TurnSnapshot{}, err
	}
	if !refs.valid() || input.RequestID != refs.RequestID || input.ReservationID != refs.ReservationID || input.AttemptID != refs.AttemptID {
		return TurnSnapshot{}, fmt.Errorf("%w: commercial references", ErrInvalidInput)
	}
	session, record, err := s.load(ctx, sessionID)
	if err != nil {
		return TurnSnapshot{}, err
	}
	snapshot, err := session.BeginTurn(input, at)
	if err != nil {
		return TurnSnapshot{}, err
	}
	if err = s.save(ctx, session, record); err != nil {
		return TurnSnapshot{}, err
	}
	if err = s.store.SaveTurn(ctx, TurnRecord{SessionID: sessionID, Snapshot: snapshot, Refs: refs}); err != nil {
		return TurnSnapshot{}, err
	}
	return snapshot, nil
}

func (s *Service) AcceptEvent(ctx context.Context, sessionID string, event ClientEvent, at time.Time) (EventResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextOK(ctx); err != nil {
		return EventResult{}, err
	}
	if !validID(sessionID) {
		return EventResult{}, ErrInvalidInput
	}
	if err := event.valid(); err != nil {
		return EventResult{}, err
	}
	if previous, err := s.store.FindEvent(ctx, sessionID, event.ID); err == nil {
		if previous.Fingerprint != event.FingerprintHex() {
			return EventResult{}, ErrEventConflict
		}
		return EventResult{Disposition: EventDuplicate, TurnID: previous.TurnID, Type: previous.Type, ChargeBearing: event.ChargeBearing}, nil
	} else if !errors.Is(err, ErrNotFound) {
		return EventResult{}, err
	}
	session, record, err := s.load(ctx, sessionID)
	if err != nil {
		return EventResult{}, err
	}
	result, err := session.AcceptEvent(event, at)
	if err != nil {
		return EventResult{}, err
	}
	var turn TurnRecord
	if result.TurnID != "" {
		turn, err = s.store.LoadTurn(ctx, sessionID, result.TurnID)
		if err != nil {
			return EventResult{}, err
		}
		if event.ChargeBearing {
			turn.ChargeEventID = event.ID
			turn.ChargeEventType = event.Type
			turn.ChargeDigest = event.PayloadDigest
		}
		turn.Snapshot = findTurnSnapshot(session.Snapshot(), result.TurnID)
	}
	entry := EventRecord{SessionID: sessionID, EventID: event.ID, Fingerprint: event.FingerprintHex(), TurnID: result.TurnID, Type: event.Type, AcceptedAt: at.UTC()}
	if _, err = s.store.AppendEvent(ctx, entry); err != nil {
		return EventResult{}, err
	}
	if err = s.save(ctx, session, record); err != nil {
		return EventResult{}, err
	}
	if result.TurnID != "" {
		if err := s.store.SaveTurn(ctx, turn); err != nil {
			return EventResult{}, err
		}
	}
	return result, nil
}

// Dispatch is the one provider generation entry point. A pending or unknown
// turn is terminal for generation and cannot be retried by this service.
func (s *Service) Dispatch(ctx context.Context, sessionID, turnID string, input ProviderPayload, at time.Time) (TurnSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextOK(ctx); err != nil {
		return TurnSnapshot{}, err
	}
	session, record, err := s.load(ctx, sessionID)
	if err != nil {
		return TurnSnapshot{}, err
	}
	turn, err := s.store.LoadTurn(ctx, sessionID, turnID)
	if err != nil {
		return TurnSnapshot{}, err
	}
	if turn.DispatchUnknown || turn.ProviderHandle != "" || turn.Snapshot.State == TurnPending {
		return turn.Snapshot, ErrPendingNoResubmit
	}
	if input.EventID != turn.ChargeEventID || input.EventType != turn.ChargeEventType || !validID(input.EventID) {
		return turn.Snapshot, fmt.Errorf("%w: provider payload event", ErrInvalidInput)
	}
	if turn.ChargeDigest != "" && digestHex(input.Payload) != turn.ChargeDigest {
		return turn.Snapshot, fmt.Errorf("%w: provider payload digest", ErrInvalidInput)
	}
	if err = session.StartSending(turnID, at); err != nil {
		return TurnSnapshot{}, err
	}
	dispatch, callErr := s.adapter.Send(ctx, ProviderSendInput{ProviderSessionID: record.ProviderSessionID, SessionID: sessionID, TurnID: turnID, RequestID: turn.Refs.RequestID, ReservationID: turn.Refs.ReservationID, AttemptID: turn.Refs.AttemptID, EventID: input.EventID, EventType: input.EventType, Payload: append([]byte(nil), input.Payload...), PayloadDigest: digestHex(input.Payload)})
	if callErr != nil || !dispatch.Sent || !validID(dispatch.Handle) || !validID(dispatch.ResponseID) {
		turn.DispatchUnknown = true
		_ = session.MarkPending(turnID, "provider_dispatch_unknown", at)
		turn.Snapshot = findTurnSnapshot(session.Snapshot(), turnID)
		_ = s.save(ctx, session, record)
		_ = s.store.SaveTurn(ctx, turn)
		if callErr != nil {
			return turn.Snapshot, callErr
		}
		return turn.Snapshot, ErrProviderProtocol
	}
	if err = session.BindProviderResponse(turnID, dispatch.ResponseID, at); err != nil {
		return TurnSnapshot{}, err
	}
	turn.ProviderHandle = dispatch.Handle
	turn.Snapshot = findTurnSnapshot(session.Snapshot(), turnID)
	if err = s.save(ctx, session, record); err != nil {
		return TurnSnapshot{}, err
	}
	if err = s.store.SaveTurn(ctx, turn); err != nil {
		return TurnSnapshot{}, err
	}
	return turn.Snapshot, nil
}

func findTurnSnapshot(snapshot SessionSnapshot, turnID string) TurnSnapshot {
	for _, turn := range snapshot.Turns {
		if turn.ID == turnID {
			return turn
		}
	}
	return TurnSnapshot{}
}

func (s *Service) Receive(ctx context.Context, sessionID, turnID string, at time.Time) (TurnSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextOK(ctx); err != nil {
		return TurnSnapshot{}, err
	}
	session, record, err := s.load(ctx, sessionID)
	if err != nil {
		return TurnSnapshot{}, err
	}
	turn, err := s.store.LoadTurn(ctx, sessionID, turnID)
	if err != nil {
		return TurnSnapshot{}, err
	}
	if turn.DispatchUnknown || turn.ProviderHandle == "" {
		return turn.Snapshot, ErrPendingNoResubmit
	}
	if turn.Snapshot.State == TurnFinalizing || turn.Snapshot.State == TurnSettled || turn.Snapshot.State == TurnReleased {
		return turn.Snapshot, nil
	}
	received, callErr := s.adapter.Receive(ctx, ProviderReceiveInput{ProviderSessionID: record.ProviderSessionID, ProviderHandle: turn.ProviderHandle, SessionID: sessionID, TurnID: turnID})
	if callErr != nil {
		_ = session.MarkPending(turnID, "provider_receive_unknown", at)
		turn.DispatchUnknown = true
		turn.Snapshot = findTurnSnapshot(session.Snapshot(), turnID)
		_ = s.save(ctx, session, record)
		_ = s.store.SaveTurn(ctx, turn)
		return turn.Snapshot, callErr
	}
	if received.ResponseID != turn.Snapshot.ProviderResponseID || received.Bytes <= 0 {
		return turn.Snapshot, ErrProviderProtocol
	}
	if err = session.MarkForwarded(turnID, received.Bytes, at); err != nil {
		return TurnSnapshot{}, err
	}
	if err = session.StartReceiving(turnID, at); err != nil {
		return TurnSnapshot{}, err
	}
	if received.Final {
		if err = session.BeginFinalizing(turnID, at); err != nil {
			return TurnSnapshot{}, err
		}
	}
	turn.Snapshot = findTurnSnapshot(session.Snapshot(), turnID)
	if err = s.save(ctx, session, record); err != nil {
		return TurnSnapshot{}, err
	}
	if err = s.store.SaveTurn(ctx, turn); err != nil {
		return TurnSnapshot{}, err
	}
	return turn.Snapshot, nil
}

func (s *Service) Settle(ctx context.Context, sessionID, turnID string, proof SettlementProof, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextOK(ctx); err != nil {
		return err
	}
	session, record, err := s.load(ctx, sessionID)
	if err != nil {
		return err
	}
	turn, err := s.store.LoadTurn(ctx, sessionID, turnID)
	if err != nil {
		return err
	}
	if turn.Snapshot.State == TurnSettled {
		if turn.Snapshot.SettlementID == proof.SettlementID && turn.Snapshot.FinalizationID == proof.FinalizationID {
			return nil
		}
		return ErrIdempotencyConflict
	}
	if err = session.SettleTurn(turnID, proof, at); err != nil {
		return err
	}
	turn.Snapshot = findTurnSnapshot(session.Snapshot(), turnID)
	if err = s.save(ctx, session, record); err != nil {
		return err
	}
	return s.store.SaveTurn(ctx, turn)
}

func (s *Service) Disconnect(ctx context.Context, sessionID, reason string, at time.Time) (CloseResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextOK(ctx); err != nil {
		return CloseResult{}, err
	}
	session, record, err := s.load(ctx, sessionID)
	if err != nil {
		return CloseResult{}, err
	}
	result, err := session.Disconnect(reason, at)
	if err != nil {
		return CloseResult{}, err
	}
	if result.CancelUpstream {
		turn, loadErr := s.store.LoadTurn(ctx, sessionID, result.TurnID)
		if loadErr == nil && !turn.CancelRequested && turn.ProviderHandle != "" {
			turn.CancelRequested = true
			_ = s.adapter.Cancel(ctx, ProviderCancelInput{ProviderSessionID: record.ProviderSessionID, ProviderHandle: turn.ProviderHandle, SessionID: sessionID, TurnID: result.TurnID})
			turn.Snapshot = findTurnSnapshot(session.Snapshot(), result.TurnID)
			_ = s.store.SaveTurn(ctx, turn)
		}
	}
	return result, s.save(ctx, session, record)
}
