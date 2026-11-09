package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/realtime"
)

// postgresRealtimePersistence is tenant-bound on purpose. The realtime
// service API carries session IDs, while the database contract carries
// composite tenant keys. Binding the store to the authenticated tenant keeps
// every load and write inside that tenant even when a caller supplies an ID
// that exists in another tenant.
//
// The database is authoritative for session, turn and event identity/state.
// Provider session and turn identities are persisted as execution facts. The
// in-memory maps retain optional queues and response details, so a stable
// provider adapter can resume receive, cancel, and settle after a restart
// without dispatching the model again.
type postgresRealtimePersistence struct {
	pool     *pgxpool.Pool
	tenantID string
	tickets  *realtime.TicketBook

	mu       sync.Mutex
	sessions map[string]realtime.SessionRecord
	turns    map[string]realtime.TurnRecord
}

func newPostgresRealtimePersistence(pool *pgxpool.Pool, tenantID string) *postgresRealtimePersistence {
	return &postgresRealtimePersistence{
		pool:     pool,
		tenantID: tenantID,
		tickets:  realtime.NewTicketBook(),
		sessions: make(map[string]realtime.SessionRecord),
		turns:    make(map[string]realtime.TurnRecord),
	}
}

func (p *postgresRealtimePersistence) validContext(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		if ctx == nil {
			return fmt.Errorf("%w: nil context", realtime.ErrInvalidInput)
		}
		return ctx.Err()
	}
	if p == nil || p.pool == nil || !realtimePersistenceID(p.tenantID) {
		return fmt.Errorf("%w: postgres persistence", realtime.ErrInvalidInput)
	}
	return nil
}

func realtimePersistenceID(value string) bool {
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

func (p *postgresRealtimePersistence) checkTenant(value string) error {
	if value != p.tenantID {
		return fmt.Errorf("%w: tenant binding", realtime.ErrInvalidInput)
	}
	return nil
}

func (p *postgresRealtimePersistence) IssueTicket(ctx context.Context, binding realtime.TicketBinding, at time.Time, ttl time.Duration) (realtime.IssuedTicket, error) {
	if err := p.validContext(ctx); err != nil {
		return realtime.IssuedTicket{}, err
	}
	if err := p.checkTenant(binding.TenantID); err != nil {
		return realtime.IssuedTicket{}, err
	}
	return p.tickets.Issue(binding, at, ttl)
}

func (p *postgresRealtimePersistence) RedeemTicket(ctx context.Context, token string, binding realtime.TicketBinding, at time.Time) (realtime.TicketClaims, error) {
	if err := p.validContext(ctx); err != nil {
		return realtime.TicketClaims{}, err
	}
	if err := p.checkTenant(binding.TenantID); err != nil {
		return realtime.TicketClaims{}, err
	}
	return p.tickets.Redeem(token, binding, at)
}

func (p *postgresRealtimePersistence) RevokeTicket(ctx context.Context, token string) error {
	if err := p.validContext(ctx); err != nil {
		return err
	}
	return p.tickets.Revoke(token)
}

func (p *postgresRealtimePersistence) CreateSession(ctx context.Context, record realtime.SessionRecord) error {
	if err := p.validContext(ctx); err != nil {
		return err
	}
	if err := p.checkTenant(record.Config.TenantID); err != nil {
		return err
	}
	if !realtimePersistenceID(record.Config.ID) || record.Snapshot.ID != record.Config.ID || record.Snapshot.State != realtime.SessionOpen {
		return fmt.Errorf("%w: session record", realtime.ErrInvalidInput)
	}
	limits := record.Config.Limits
	if limits == (realtime.SessionLimits{}) {
		limits = realtime.DefaultSessionLimits()
	}
	createdAt := record.Snapshot.CreatedAt.UTC()
	expiresAt := createdAt.Add(limits.SessionTTL)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackRealtimeTx(ctx, tx)
	var inserted string
	err = tx.QueryRow(ctx, `
INSERT INTO realtime_sessions(tenant_id,id,actor_id,api_key_id,model_id,profile_version,route_id,origin,state,provider_session_id,expires_at,max_turns,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,'open',$9,$10,$11,$12)
ON CONFLICT DO NOTHING
RETURNING id`, p.tenantID, record.Config.ID, record.Config.ActorID, record.Config.APIKeyID,
		record.Config.ModelID, record.Config.ProfileID, record.Config.RouteID, record.Config.Origin,
		record.ProviderSessionID, expiresAt, limits.MaxTurns, createdAt).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return realtime.ErrIdempotencyConflict
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	p.sessions[record.Config.ID] = record
	p.mu.Unlock()
	return nil
}

func (p *postgresRealtimePersistence) LoadSession(ctx context.Context, sessionID string) (realtime.SessionRecord, error) {
	if err := p.validContext(ctx); err != nil {
		return realtime.SessionRecord{}, err
	}
	if !realtimePersistenceID(sessionID) {
		return realtime.SessionRecord{}, realtime.ErrNotFound
	}
	var (
		id, actorID, apiKeyID, modelID, profileID, routeID, origin, sessionState, providerSessionID string
		expiresAt, createdAt                                                                        time.Time
		maxTurns                                                                                    int
	)
	err := p.pool.QueryRow(ctx, `
SELECT id,actor_id,api_key_id,model_id,profile_version,route_id,origin,state,COALESCE(provider_session_id,''),expires_at,max_turns,created_at
FROM realtime_sessions WHERE tenant_id=$1 AND id=$2`, p.tenantID, sessionID).
		Scan(&id, &actorID, &apiKeyID, &modelID, &profileID, &routeID, &origin, &sessionState, &providerSessionID, &expiresAt, &maxTurns, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return realtime.SessionRecord{}, realtime.ErrNotFound
	}
	if err != nil {
		return realtime.SessionRecord{}, err
	}
	rows, err := p.pool.Query(ctx, `
SELECT id,request_id,reservation_id,attempt_id,state,response_reference,created_at
FROM realtime_turns WHERE tenant_id=$1 AND session_id=$2 ORDER BY ordinal`, p.tenantID, sessionID)
	if err != nil {
		return realtime.SessionRecord{}, err
	}
	defer rows.Close()
	turns := make([]realtime.TurnSnapshot, 0)
	activeTurnID := ""
	for rows.Next() {
		var turn realtime.TurnSnapshot
		var turnState string
		var responseReference *string
		if err = rows.Scan(&turn.ID, &turn.RequestID, &turn.ReservationID, &turn.AttemptID, &turnState, &responseReference, &turn.CreatedAt); err != nil {
			return realtime.SessionRecord{}, err
		}
		turn.State = realtime.TurnState(turnState)
		turn.UpdatedAt = turn.CreatedAt
		turn.RetailUsageState = realtime.UsageMissing
		turn.SupplierUsageState = realtime.UsageMissing
		if responseReference != nil {
			turn.ProviderResponseID = *responseReference
		}
		p.mu.Lock()
		extra, exists := p.turns[realtimePersistenceTurnKey(sessionID, turn.ID)]
		p.mu.Unlock()
		if exists {
			merged := extra.Snapshot
			merged.ID = turn.ID
			merged.RequestID = turn.RequestID
			merged.ReservationID = turn.ReservationID
			merged.AttemptID = turn.AttemptID
			merged.State = turn.State
			merged.CreatedAt = turn.CreatedAt
			merged.UpdatedAt = turn.UpdatedAt
			merged.ProviderResponseID = turn.ProviderResponseID
			turn = merged
		}
		if turn.State == realtime.TurnReserved || turn.State == realtime.TurnSending || turn.State == realtime.TurnReceiving || turn.State == realtime.TurnFinalizing {
			activeTurnID = turn.ID
		}
		turns = append(turns, turn)
	}
	if err = rows.Err(); err != nil {
		return realtime.SessionRecord{}, err
	}
	limits := realtime.DefaultSessionLimits()
	if maxTurns > 0 {
		limits.MaxTurns = maxTurns
	}
	if ttl := expiresAt.Sub(createdAt); ttl > 0 {
		limits.SessionTTL = ttl
	}
	snapshot := realtime.SessionSnapshot{
		ID: id, TenantID: p.tenantID, ActorID: actorID, APIKeyID: apiKeyID,
		ModelID: modelID, ProfileID: profileID, RouteID: routeID, Origin: origin,
		State: realtime.SessionState(sessionState), CreatedAt: createdAt, UpdatedAt: createdAt,
		TurnCount: len(turns), ActiveTurnID: activeTurnID, Turns: turns,
	}
	p.mu.Lock()
	prior, exists := p.sessions[sessionID]
	if exists {
		snapshot.ForwardedBytes = prior.Snapshot.ForwardedBytes
		snapshot.QueuedEvents = prior.Snapshot.QueuedEvents
		snapshot.QueuedBytes = prior.Snapshot.QueuedBytes
	}
	record := realtime.SessionRecord{Config: realtime.SessionConfig{
		ID: id, TenantID: p.tenantID, ActorID: actorID, APIKeyID: apiKeyID,
		ModelID: modelID, ProfileID: profileID, RouteID: routeID, Origin: origin, Limits: limits,
	}, Snapshot: snapshot, ProviderSessionID: providerSessionID}
	p.sessions[sessionID] = record
	p.mu.Unlock()
	return record, nil
}

func (p *postgresRealtimePersistence) SaveSession(ctx context.Context, record realtime.SessionRecord) error {
	if err := p.validContext(ctx); err != nil {
		return err
	}
	if err := p.checkTenant(record.Config.TenantID); err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackRealtimeTx(ctx, tx)
	if err = advanceRealtimeSessionTx(ctx, tx, p.tenantID, record.Config.ID, record.Snapshot.State, record.ProviderSessionID); err != nil {
		return err
	}
	for _, turn := range record.Snapshot.Turns {
		if err = advanceRealtimeTurnIfPresentTx(ctx, tx, p.tenantID, record.Config.ID, turn.ID, turn.State, turn.ProviderResponseID); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	p.sessions[record.Config.ID] = record
	for _, turn := range record.Snapshot.Turns {
		key := realtimePersistenceTurnKey(record.Config.ID, turn.ID)
		if prior, ok := p.turns[key]; ok {
			prior.Snapshot.State = turn.State
			prior.Snapshot.ProviderResponseID = turn.ProviderResponseID
			p.turns[key] = prior
		}
	}
	p.mu.Unlock()
	return nil
}

func (p *postgresRealtimePersistence) SaveTurn(ctx context.Context, record realtime.TurnRecord) error {
	if err := p.validContext(ctx); err != nil {
		return err
	}
	if !realtimePersistenceID(record.SessionID) || !realtimePersistenceID(record.Snapshot.ID) || record.Snapshot.RequestID != record.Refs.RequestID || record.Snapshot.ReservationID != record.Refs.ReservationID || record.Snapshot.AttemptID != record.Refs.AttemptID {
		return fmt.Errorf("%w: turn record", realtime.ErrInvalidInput)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackRealtimeTx(ctx, tx)
	var sessionState string
	if err = tx.QueryRow(ctx, "SELECT state FROM realtime_sessions WHERE tenant_id=$1 AND id=$2 FOR UPDATE", p.tenantID, record.SessionID).Scan(&sessionState); errors.Is(err, pgx.ErrNoRows) {
		return realtime.ErrNotFound
	} else if err != nil {
		return err
	}
	var (
		currentState, requestID, reservationID, attemptID string
		currentResponseReference, currentProviderHandle   *string
		currentDispatchUnknown, currentCancelRequested    bool
	)
	err = tx.QueryRow(ctx, `SELECT request_id,reservation_id,attempt_id,state,response_reference,provider_handle,dispatch_unknown,cancel_requested
FROM realtime_turns WHERE tenant_id=$1 AND session_id=$2 AND id=$3 FOR UPDATE`, p.tenantID, record.SessionID, record.Snapshot.ID).
		Scan(&requestID, &reservationID, &attemptID, &currentState, &currentResponseReference, &currentProviderHandle, &currentDispatchUnknown, &currentCancelRequested)
	if errors.Is(err, pgx.ErrNoRows) {
		if sessionState != string(realtime.SessionOpen) {
			return fmt.Errorf("%w: turn requires open session", realtime.ErrInvalidTransition)
		}
		var ordinal int64
		if err = tx.QueryRow(ctx, "SELECT COALESCE(max(ordinal),0)+1 FROM realtime_turns WHERE tenant_id=$1 AND session_id=$2", p.tenantID, record.SessionID).Scan(&ordinal); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO realtime_turns(tenant_id,id,session_id,ordinal,request_id,reservation_id,attempt_id,state,response_reference,provider_handle,dispatch_unknown,cancel_requested,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,'reserved',$8,NULL,false,false,$9)`, p.tenantID, record.Snapshot.ID, record.SessionID, ordinal, record.Refs.RequestID, record.Refs.ReservationID, record.Refs.AttemptID, nullableRealtimeString(record.Snapshot.ProviderResponseID), record.Snapshot.CreatedAt.UTC()); err != nil {
			return err
		}
		currentState = string(realtime.TurnReserved)
	} else if err != nil {
		return err
	} else if requestID != record.Refs.RequestID || reservationID != record.Refs.ReservationID || attemptID != record.Refs.AttemptID {
		return realtime.ErrIdempotencyConflict
	}
	if err = advanceRealtimeTurnTx(ctx, tx, p.tenantID, record.SessionID, record.Snapshot.ID, currentState, string(record.Snapshot.State), record.Snapshot.ProviderResponseID, record.ProviderHandle, record.DispatchUnknown, record.CancelRequested); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	p.turns[realtimePersistenceTurnKey(record.SessionID, record.Snapshot.ID)] = record
	p.mu.Unlock()
	return nil
}

func (p *postgresRealtimePersistence) LoadTurn(ctx context.Context, sessionID, turnID string) (realtime.TurnRecord, error) {
	if err := p.validContext(ctx); err != nil {
		return realtime.TurnRecord{}, err
	}
	var (
		requestID, reservationID, attemptID, state string
		responseReference, providerHandle          *string
		dispatchUnknown, cancelRequested           bool
		createdAt                                  time.Time
	)
	err := p.pool.QueryRow(ctx, `SELECT request_id,reservation_id,attempt_id,state,response_reference,provider_handle,dispatch_unknown,cancel_requested,created_at
FROM realtime_turns WHERE tenant_id=$1 AND session_id=$2 AND id=$3`, p.tenantID, sessionID, turnID).
		Scan(&requestID, &reservationID, &attemptID, &state, &responseReference, &providerHandle, &dispatchUnknown, &cancelRequested, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return realtime.TurnRecord{}, realtime.ErrNotFound
	}
	if err != nil {
		return realtime.TurnRecord{}, err
	}
	snapshot := realtime.TurnSnapshot{ID: turnID, RequestID: requestID, ReservationID: reservationID, AttemptID: attemptID, State: realtime.TurnState(state), CreatedAt: createdAt, UpdatedAt: createdAt, RetailUsageState: realtime.UsageMissing, SupplierUsageState: realtime.UsageMissing}
	if responseReference != nil {
		snapshot.ProviderResponseID = *responseReference
	}
	p.mu.Lock()
	record, exists := p.turns[realtimePersistenceTurnKey(sessionID, turnID)]
	p.mu.Unlock()
	if exists {
		merged := record.Snapshot
		merged.ID = snapshot.ID
		merged.RequestID = snapshot.RequestID
		merged.ReservationID = snapshot.ReservationID
		merged.AttemptID = snapshot.AttemptID
		merged.State = snapshot.State
		merged.CreatedAt = snapshot.CreatedAt
		merged.UpdatedAt = snapshot.UpdatedAt
		merged.ProviderResponseID = snapshot.ProviderResponseID
		record.Snapshot = merged
		record.ProviderHandle = realtimeStringValue(providerHandle)
		record.DispatchUnknown = dispatchUnknown
		record.CancelRequested = cancelRequested
	} else {
		record = realtime.TurnRecord{SessionID: sessionID, Snapshot: snapshot, Refs: realtime.CommercialRefs{RequestID: requestID, ReservationID: reservationID, AttemptID: attemptID}, ProviderHandle: realtimeStringValue(providerHandle), DispatchUnknown: dispatchUnknown, CancelRequested: cancelRequested}
	}
	return record, nil
}

type realtimeEventCanonical struct {
	Fingerprint string `json:"fingerprint"`
	Type        string `json:"type"`
	TurnID      string `json:"turn_id,omitempty"`
}

func (p *postgresRealtimePersistence) AppendEvent(ctx context.Context, record realtime.EventRecord) (realtime.EventDisposition, error) {
	if err := p.validContext(ctx); err != nil {
		return "", err
	}
	if err := p.checkTenantFromSession(record.SessionID); err != nil {
		return "", err
	}
	if !realtimePersistenceID(record.SessionID) || !realtimePersistenceID(record.EventID) || !realtimePersistenceID(record.Fingerprint) || record.AcceptedAt.IsZero() {
		return "", fmt.Errorf("%w: event record", realtime.ErrInvalidInput)
	}
	canonical, err := json.Marshal(realtimeEventCanonical{Fingerprint: record.Fingerprint, Type: string(record.Type), TurnID: record.TurnID})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer rollbackRealtimeTx(ctx, tx)
	var inserted string
	err = tx.QueryRow(ctx, `INSERT INTO realtime_events(tenant_id,session_id,event_id,turn_id,type,payload_bytes,payload_digest,canonical,created_at)
VALUES($1,$2,$3,$4,$5,0,$6,$7,$8)
ON CONFLICT DO NOTHING RETURNING event_id`, p.tenantID, record.SessionID, record.EventID, nullableRealtimeString(record.TurnID), string(record.Type), digest[:], canonical, record.AcceptedAt.UTC()).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		prior, readErr := readStoredRealtimeEventTx(ctx, tx, p.tenantID, record.SessionID, record.EventID)
		if readErr != nil {
			return "", readErr
		}
		if prior.Fingerprint != record.Fingerprint {
			return "", realtime.ErrEventConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return "", err
		}
		return realtime.EventDuplicate, nil
	}
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return realtime.EventAccepted, nil
}

func (p *postgresRealtimePersistence) FindEvent(ctx context.Context, sessionID, eventID string) (realtime.EventRecord, error) {
	if err := p.validContext(ctx); err != nil {
		return realtime.EventRecord{}, err
	}
	if err := p.checkTenantFromSession(sessionID); err != nil {
		return realtime.EventRecord{}, err
	}
	return readStoredRealtimeEvent(ctx, p.pool, p.tenantID, sessionID, eventID)
}

func (p *postgresRealtimePersistence) checkTenantFromSession(sessionID string) error {
	if !realtimePersistenceID(sessionID) {
		return fmt.Errorf("%w: session identity", realtime.ErrInvalidInput)
	}
	return nil
}

func readStoredRealtimeEvent(ctx context.Context, pool *pgxpool.Pool, tenantID, sessionID, eventID string) (realtime.EventRecord, error) {
	var (
		canonical []byte
		createdAt time.Time
	)
	err := pool.QueryRow(ctx, `SELECT canonical,created_at FROM realtime_events WHERE tenant_id=$1 AND session_id=$2 AND event_id=$3`, tenantID, sessionID, eventID).Scan(&canonical, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return realtime.EventRecord{}, realtime.ErrNotFound
	}
	if err != nil {
		return realtime.EventRecord{}, err
	}
	return decodeRealtimeEventCanonical(sessionID, eventID, canonical, createdAt)
}

func readStoredRealtimeEventTx(ctx context.Context, tx pgx.Tx, tenantID, sessionID, eventID string) (realtime.EventRecord, error) {
	var (
		canonical []byte
		createdAt time.Time
	)
	err := tx.QueryRow(ctx, `SELECT canonical,created_at FROM realtime_events WHERE tenant_id=$1 AND session_id=$2 AND event_id=$3`, tenantID, sessionID, eventID).Scan(&canonical, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return realtime.EventRecord{}, realtime.ErrNotFound
	}
	if err != nil {
		return realtime.EventRecord{}, err
	}
	return decodeRealtimeEventCanonical(sessionID, eventID, canonical, createdAt)
}

func decodeRealtimeEventCanonical(sessionID, eventID string, canonical []byte, createdAt time.Time) (realtime.EventRecord, error) {
	var value realtimeEventCanonical
	if err := json.Unmarshal(canonical, &value); err != nil || value.Fingerprint == "" || value.Type == "" {
		return realtime.EventRecord{}, fmt.Errorf("%w: stored event canonical", realtime.ErrInvalidInput)
	}
	if _, err := hex.DecodeString(value.Fingerprint); err != nil || len(value.Fingerprint) != sha256.Size*2 || value.Fingerprint != strings.ToLower(value.Fingerprint) {
		return realtime.EventRecord{}, fmt.Errorf("%w: stored event fingerprint", realtime.ErrInvalidInput)
	}
	return realtime.EventRecord{SessionID: sessionID, EventID: eventID, Fingerprint: value.Fingerprint, TurnID: value.TurnID, Type: realtime.EventType(value.Type), AcceptedAt: createdAt}, nil
}

func realtimePersistenceTurnKey(sessionID, turnID string) string {
	return sessionID + "\x00" + turnID
}

func nullableRealtimeString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func realtimeStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func rollbackRealtimeTx(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(ctx)
}

func advanceRealtimeSessionTx(ctx context.Context, tx pgx.Tx, tenantID, sessionID string, target realtime.SessionState, providerSessionID string) error {
	var current string
	if err := tx.QueryRow(ctx, "SELECT state FROM realtime_sessions WHERE tenant_id=$1 AND id=$2 FOR UPDATE", tenantID, sessionID).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
		return realtime.ErrNotFound
	} else if err != nil {
		return err
	}
	if current == string(target) {
		return nil
	}
	var path []string
	switch current {
	case "open":
		switch target {
		case realtime.SessionDraining:
			path = []string{"draining"}
		case realtime.SessionClosed:
			path = []string{"draining", "closed"}
		default:
			return fmt.Errorf("%w: session %s to %s", realtime.ErrInvalidTransition, current, target)
		}
	case "draining":
		if target != realtime.SessionClosed {
			return fmt.Errorf("%w: session %s to %s", realtime.ErrInvalidTransition, current, target)
		}
		path = []string{"closed"}
	default:
		return fmt.Errorf("%w: session %s to %s", realtime.ErrInvalidTransition, current, target)
	}
	for _, next := range path {
		if _, err := tx.Exec(ctx, "UPDATE realtime_sessions SET state=$3,provider_session_id=COALESCE(provider_session_id,NULLIF($4,'')) WHERE tenant_id=$1 AND id=$2", tenantID, sessionID, next, providerSessionID); err != nil {
			return err
		}
	}
	return nil
}

func advanceRealtimeTurnIfPresentTx(ctx context.Context, tx pgx.Tx, tenantID, sessionID, turnID string, target realtime.TurnState, responseID string) error {
	var current string
	err := tx.QueryRow(ctx, "SELECT state FROM realtime_turns WHERE tenant_id=$1 AND session_id=$2 AND id=$3 FOR UPDATE", tenantID, sessionID, turnID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return advanceRealtimeTurnTx(ctx, tx, tenantID, sessionID, turnID, current, string(target), responseID, "", false, false)
}

func advanceRealtimeTurnTx(ctx context.Context, tx pgx.Tx, tenantID, sessionID, turnID, current, target, responseID, providerHandle string, dispatchUnknown, cancelRequested bool) error {
	if current == target {
		_, err := tx.Exec(ctx, `UPDATE realtime_turns SET response_reference=COALESCE(response_reference,$4),provider_handle=COALESCE(provider_handle,NULLIF($5,'')),dispatch_unknown=dispatch_unknown OR $6,cancel_requested=cancel_requested OR $7
WHERE tenant_id=$1 AND session_id=$2 AND id=$3`, tenantID, sessionID, turnID, nullableRealtimeString(responseID), providerHandle, dispatchUnknown, cancelRequested)
		return err
	}
	path := realtimeTurnTransitionPath(current, target)
	if len(path) == 0 {
		return fmt.Errorf("%w: turn %s to %s", realtime.ErrInvalidTransition, current, target)
	}
	for _, next := range path {
		if _, err := tx.Exec(ctx, `UPDATE realtime_turns SET state=$4,response_reference=COALESCE(response_reference,$5),provider_handle=COALESCE(provider_handle,NULLIF($6,'')),dispatch_unknown=dispatch_unknown OR $7,cancel_requested=cancel_requested OR $8
WHERE tenant_id=$1 AND session_id=$2 AND id=$3`, tenantID, sessionID, turnID, next, nullableRealtimeString(responseID), providerHandle, dispatchUnknown, cancelRequested); err != nil {
			return err
		}
	}
	return nil
}

func realtimeTurnTransitionPath(current, target string) []string {
	if current == target {
		return nil
	}
	switch current {
	case "reserved":
		switch target {
		case "sending":
			return []string{"sending"}
		case "receiving":
			return []string{"sending", "receiving"}
		case "finalizing":
			return []string{"sending", "receiving", "finalizing"}
		case "pending":
			return []string{"sending", "pending"}
		case "released":
			return []string{"released"}
		}
	case "sending":
		switch target {
		case "receiving":
			return []string{"receiving"}
		case "finalizing":
			return []string{"receiving", "finalizing"}
		case "pending":
			return []string{"pending"}
		case "settled":
			return []string{"receiving", "finalizing", "settled"}
		}
	case "receiving":
		switch target {
		case "finalizing":
			return []string{"finalizing"}
		case "pending":
			return []string{"pending"}
		case "settled":
			return []string{"finalizing", "settled"}
		}
	case "finalizing":
		switch target {
		case "settled", "pending":
			return []string{target}
		}
	case "pending":
		switch target {
		case "settled", "released":
			return []string{target}
		}
	}
	return nil
}

var _ realtime.Persistence = (*postgresRealtimePersistence)(nil)

func (s *Server) realtimeServiceForTenant(tenantID string) (*realtime.Service, error) {
	if s.Pool == nil {
		if s.Realtime == nil {
			return nil, fmt.Errorf("%w: realtime service", realtime.ErrInvalidInput)
		}
		return s.Realtime, nil
	}
	if s.RealtimeAdapter == nil {
		return nil, fmt.Errorf("%w: realtime provider adapter", realtime.ErrInvalidInput)
	}
	store := newPostgresRealtimePersistence(s.Pool, tenantID)
	return realtime.NewService(realtime.ServiceOptions{Store: store, Adapter: s.RealtimeAdapter})
}

func (s *Server) realtimeRouteID(ctx context.Context, model catalog.Model) (string, error) {
	if s.Pool == nil {
		return "", fmt.Errorf("%w: realtime route database", realtime.ErrInvalidInput)
	}
	var routeID string
	err := s.Pool.QueryRow(ctx, `SELECT id FROM route_policy_versions WHERE model_id=$1 AND revision=$2`, model.ID, model.Revision).Scan(&routeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: realtime route is unavailable", realtime.ErrNotFound)
	}
	if err != nil {
		return "", err
	}
	return routeID, nil
}
