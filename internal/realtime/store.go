package realtime

import (
	"context"
	"sync"
	"time"
)

// MemoryStore has the same separated contracts as a database-backed store.
// Its maps are guarded by one mutex so tests can exercise the service with
// concurrent duplicate events and retries without introducing a second
// financial implementation.
type MemoryStore struct {
	mu       sync.Mutex
	tickets  *TicketBook
	sessions map[string]SessionRecord
	turns    map[string]TurnRecord
	events   map[string]EventRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tickets: NewTicketBook(), sessions: make(map[string]SessionRecord), turns: make(map[string]TurnRecord), events: make(map[string]EventRecord)}
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	return ctx.Err()
}

func (s *MemoryStore) IssueTicket(ctx context.Context, binding TicketBinding, at time.Time, ttl time.Duration) (IssuedTicket, error) {
	if err := checkContext(ctx); err != nil {
		return IssuedTicket{}, err
	}
	return s.tickets.Issue(binding, at, ttl)
}

func (s *MemoryStore) RedeemTicket(ctx context.Context, token string, binding TicketBinding, at time.Time) (TicketClaims, error) {
	if err := checkContext(ctx); err != nil {
		return TicketClaims{}, err
	}
	return s.tickets.Redeem(token, binding, at)
}

func (s *MemoryStore) RevokeTicket(ctx context.Context, token string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	return s.tickets.Revoke(token)
}

func validSessionRecord(record SessionRecord) bool {
	return validID(record.Config.ID) && record.Snapshot.ID == record.Config.ID && validID(record.ProviderSessionID)
}

func (s *MemoryStore) CreateSession(ctx context.Context, record SessionRecord) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if !validSessionRecord(record) {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[record.Config.ID]; exists {
		return ErrIdempotencyConflict
	}
	s.sessions[record.Config.ID] = record
	return nil
}

func (s *MemoryStore) LoadSession(ctx context.Context, id string) (SessionRecord, error) {
	if err := checkContext(ctx); err != nil {
		return SessionRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.sessions[id]
	if !ok {
		return SessionRecord{}, ErrNotFound
	}
	return record, nil
}

func (s *MemoryStore) SaveSession(ctx context.Context, record SessionRecord) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if !validSessionRecord(record) {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[record.Config.ID]; !ok {
		return ErrNotFound
	}
	s.sessions[record.Config.ID] = record
	return nil
}

func turnKey(sessionID, turnID string) string   { return sessionID + "\x00" + turnID }
func eventKey(sessionID, eventID string) string { return sessionID + "\x00" + eventID }

func (s *MemoryStore) SaveTurn(ctx context.Context, record TurnRecord) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if !validID(record.SessionID) || !validID(record.Snapshot.ID) || record.Snapshot.ID == "" || !record.Refs.valid() || record.Snapshot.RequestID != record.Refs.RequestID || record.Snapshot.ReservationID != record.Refs.ReservationID || record.Snapshot.AttemptID != record.Refs.AttemptID {
		return ErrInvalidInput
	}
	if record.ChargeEventID != "" && (!validID(record.ChargeEventID) || !validEventType(record.ChargeEventType) || (record.ChargeDigest != "" && !validDigestHex(record.ChargeDigest))) {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[record.SessionID]; !ok {
		return ErrNotFound
	}
	key := turnKey(record.SessionID, record.Snapshot.ID)
	if prior, ok := s.turns[key]; ok && prior.Refs != record.Refs {
		return ErrIdempotencyConflict
	}
	s.turns[key] = record
	return nil
}

func (s *MemoryStore) LoadTurn(ctx context.Context, sessionID, turnID string) (TurnRecord, error) {
	if err := checkContext(ctx); err != nil {
		return TurnRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.turns[turnKey(sessionID, turnID)]
	if !ok {
		return TurnRecord{}, ErrNotFound
	}
	return record, nil
}

func (s *MemoryStore) AppendEvent(ctx context.Context, record EventRecord) (EventDisposition, error) {
	if err := checkContext(ctx); err != nil {
		return "", err
	}
	if !record.valid() {
		return "", ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[record.SessionID]; !ok {
		return "", ErrNotFound
	}
	key := eventKey(record.SessionID, record.EventID)
	if prior, ok := s.events[key]; ok {
		if prior.Fingerprint != record.Fingerprint {
			return "", ErrEventConflict
		}
		return EventDuplicate, nil
	}
	s.events[key] = record
	return EventAccepted, nil
}

func (s *MemoryStore) FindEvent(ctx context.Context, sessionID, eventID string) (EventRecord, error) {
	if err := checkContext(ctx); err != nil {
		return EventRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.events[eventKey(sessionID, eventID)]
	if !ok {
		return EventRecord{}, ErrNotFound
	}
	return record, nil
}

var _ Persistence = (*MemoryStore)(nil)
