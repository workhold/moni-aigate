package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/realtime"
	"golang.org/x/net/websocket"
)

type realtimeWireEvent struct {
	EventID string `json:"event_id"`
	Type    string `json:"type"`
	Data    any    `json:"data"`
}

// realtime serves the deliberately narrow first WS slice. Authorization and
// model access are completed before the protocol upgrade. The connection
// itself then owns one in-memory session and one commercial-reference slot.
func (s *Server) realtime(c *gin.Context) {
	principal, ok := c.Get("principal")
	if !ok {
		s.fail(c, http.StatusUnauthorized, "invalid_credentials", "需要有效 API Key")
		return
	}
	p, ok := principal.(identity.Principal)
	if !ok {
		s.fail(c, http.StatusUnauthorized, "invalid_credentials", "需要有效 API Key")
		return
	}
	origin, err := s.realtimeOrigin(c.Request)
	if err != nil {
		s.fail(c, http.StatusForbidden, "origin_forbidden", "WebSocket来源未获授权")
		return
	}
	model, ok := s.realtimeModel(c, p)
	if !ok {
		return
	}
	if s.Realtime == nil {
		s.fail(c, http.StatusServiceUnavailable, "realtime_unavailable", "实时服务暂不可用")
		return
	}

	wsServer := websocket.Server{
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler: websocket.Handler(func(ws *websocket.Conn) {
			s.handleRealtimeConnection(c, ws, p, model, origin)
		}),
	}
	wsServer.ServeHTTP(c.Writer, c.Request)
}

func (s *Server) realtimeModel(c *gin.Context, p identity.Principal) (catalog.Model, bool) {
	if value, exists := c.Get("realtime_model"); exists {
		model, ok := value.(catalog.Model)
		if !ok || model.ID == "" || model.ProfileVersion == "" {
			s.fail(c, http.StatusServiceUnavailable, "realtime_unavailable", "实时模型配置不可用")
			return catalog.Model{}, false
		}
		return model, true
	}
	name := c.Query("model")
	if strings.TrimSpace(name) != name || name == "" {
		s.fail(c, http.StatusBadRequest, "invalid_request", "需要有效model参数")
		return catalog.Model{}, false
	}
	if s.Catalog == nil {
		s.fail(c, http.StatusServiceUnavailable, "realtime_unavailable", "实时目录暂不可用")
		return catalog.Model{}, false
	}
	model, err := s.Catalog.AuthorizedModel(c.Request.Context(), p.TenantID, p.UserID, p.KeyID, name)
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, pgx.ErrNoRows) {
		s.fail(c, http.StatusForbidden, "model_forbidden", "模型不存在或未获授权")
		return catalog.Model{}, false
	}
	if err != nil {
		s.error(c, err)
		return catalog.Model{}, false
	}
	return model, true
}

func (s *Server) realtimeOrigin(req *http.Request) (string, error) {
	raw := req.Header.Get("Origin")
	if raw == "" {
		raw = s.Config.Origin
	}
	origin, err := realtime.CanonicalOrigin(raw)
	if err != nil {
		return "", err
	}
	if s.Config.Origin != "" {
		expected, err := realtime.CanonicalOrigin(s.Config.Origin)
		if err != nil || origin != expected {
			return "", fmt.Errorf("realtime: origin mismatch")
		}
	}
	return origin, nil
}

func (s *Server) handleRealtimeConnection(c *gin.Context, ws *websocket.Conn, p identity.Principal, model catalog.Model, origin string) {
	ws.MaxPayloadBytes = realtime.DefaultMaxFrameBytes
	ctx := c.Request.Context()
	service, err := s.realtimeServiceForTenant(p.TenantID)
	if err != nil {
		s.writeRealtimeError(ws, err)
		return
	}
	sessionID := uuid.NewString()
	turnID := uuid.NewString()
	requestID := c.GetString("request_id")
	if requestID == "" {
		requestID = uuid.NewString()
	}
	refs := realtime.CommercialRefs{
		RequestID:     requestID,
		ReservationID: uuid.NewString(),
		AttemptID:     uuid.NewString(),
	}
	billingRefs, err := s.reserveMediaForRequest(ctx, p.TenantID, p.UserID, p.KeyID, refs.RequestID, "realtime:"+refs.RequestID, "realtime_turn", "mock-realtime", "44444444-4444-4444-8444-444444444444", sha256.Sum256([]byte(refs.RequestID)), 1)
	if err != nil {
		s.writeRealtimeError(ws, err)
		return
	}
	if billingRefs.enabled {
		refs.RequestID = billingRefs.requestID
		refs.ReservationID = billingRefs.reservationID
		refs.AttemptID = billingRefs.attemptID
	}
	if refs.ReservationID == "" {
		refs.ReservationID = uuid.NewString()
	}
	if refs.AttemptID == "" {
		refs.AttemptID = uuid.NewString()
	}
	if billingRefs.enabled && billingRefs.existing && billingRefs.state != "prepared" {
		_ = s.writeRealtime(ws, realtimeWireEvent{EventID: uuid.NewString(), Type: "response.pending", Data: map[string]any{"reason": "provider_consumption_unknown"}})
		return
	}
	if billingRefs.enabled {
		if err = s.Billing.MarkMediaAttemptSending(ctx, p.TenantID, billingRefs.attemptID); err != nil {
			s.writeRealtimeError(ws, err)
			return
		}
	}
	routeID := fmt.Sprintf("mvp-route-%d", model.Revision)
	if s.Pool != nil {
		routeID, err = s.realtimeRouteID(ctx, model)
		if err != nil {
			_ = s.finalizeMediaRequest(context.Background(), billingRefs, "unknown", "unknown", 0)
			s.writeRealtimeError(ws, err)
			return
		}
	} else if model.Revision < 1 {
		routeID = "mvp-route"
	}
	binding := realtime.TicketBinding{
		SessionID: sessionID,
		TenantID:  p.TenantID,
		ActorID:   p.UserID,
		APIKeyID:  p.KeyID,
		ModelID:   model.ID,
		ProfileID: model.ProfileVersion,
		RouteID:   routeID,
		Origin:    origin,
	}
	created := false
	mediaSettled := false
	defer func() {
		if created {
			s.disconnectRealtime(service, sessionID, "client_disconnect")
		}
		if billingRefs.enabled && !mediaSettled {
			_ = s.finalizeMediaRequest(context.Background(), billingRefs, "unknown", "unknown", 0)
		}
		_ = ws.Close()
	}()

	now := time.Now().UTC()
	limits := realtime.DefaultSessionLimits()
	limits.MaxTurns = 1
	ticket, err := service.IssueTicket(ctx, binding, now, realtime.MaxTicketTTL)
	if err != nil {
		s.writeRealtimeError(ws, err)
		return
	}
	_, err = service.CreateSession(ctx, ticket.Token, binding, realtime.SessionConfig{
		ID: sessionID, TenantID: p.TenantID, ActorID: p.UserID, APIKeyID: p.KeyID,
		ModelID: model.ID, ProfileID: model.ProfileVersion, RouteID: routeID, Origin: origin, Limits: limits,
	}, now)
	if err != nil {
		s.writeRealtimeError(ws, err)
		return
	}
	created = true
	if _, err = service.BeginTurn(ctx, sessionID, realtime.BeginTurnInput{
		TurnID: turnID, RequestID: refs.RequestID, ReservationID: refs.ReservationID, AttemptID: refs.AttemptID,
	}, refs, now); err != nil {
		s.writeRealtimeError(ws, err)
		return
	}
	if err = s.writeRealtime(ws, realtimeWireEvent{EventID: uuid.NewString(), Type: "session.created", Data: map[string]any{
		"session_id": sessionID, "turn_id": turnID, "model": model.Name,
	}}); err != nil {
		return
	}

	parser, err := realtime.NewEventParser(realtime.EventLimits{})
	if err != nil {
		s.writeRealtimeError(ws, err)
		return
	}
	for {
		timeout := s.RealtimeTimeout
		if timeout <= 0 {
			timeout = realtime.DefaultTurnTTL
		}
		if err = ws.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return
		}
		var raw string
		err = websocket.Message.Receive(ws, &raw)
		if err != nil {
			if !errors.Is(err, io.EOF) && isRealtimeTimeout(err) {
				s.writeRealtimeError(ws, context.DeadlineExceeded)
				s.disconnectRealtime(service, sessionID, "turn_timeout")
				created = false
			}
			return
		}
		event, parseErr := parser.Parse([]byte(raw))
		if parseErr != nil {
			s.writeRealtimeError(ws, parseErr)
			s.disconnectRealtime(service, sessionID, "protocol_error")
			created = false
			return
		}
		result, acceptErr := service.AcceptEvent(ctx, sessionID, event, time.Now().UTC())
		if acceptErr != nil {
			s.writeRealtimeError(ws, acceptErr)
			s.disconnectRealtime(service, sessionID, "protocol_error")
			created = false
			return
		}
		if err = s.writeRealtime(ws, realtimeWireEvent{EventID: event.ID, Type: "event." + string(result.Disposition), Data: map[string]any{
			"event_type": result.Type, "turn_id": result.TurnID, "charge_bearing": result.ChargeBearing,
		}}); err != nil {
			return
		}
		if result.Disposition == realtime.EventDuplicate {
			continue
		}
		switch event.Type {
		case realtime.EventResponseCreate:
			if err = s.completeRealtimeTurn(ctx, ws, service, sessionID, turnID, event, billingRefs); err != nil {
				s.writeRealtimeError(ws, err)
				s.disconnectRealtime(service, sessionID, "provider_error")
				created = false
				mediaSettled = billingRefs.enabled
				return
			}
		case realtime.EventResponseCancel:
			s.disconnectRealtime(service, sessionID, "client_cancel")
			created = false
			return
		}
	}
}

func (s *Server) completeRealtimeTurn(ctx context.Context, ws *websocket.Conn, service *realtime.Service, sessionID, turnID string, event realtime.ClientEvent, billingRefs mediaExecutionRefs) error {
	snapshot, err := service.Dispatch(ctx, sessionID, turnID, realtime.ProviderPayload{EventID: event.ID, EventType: event.Type}, time.Now().UTC())
	if err != nil {
		return err
	}
	if err = s.writeRealtime(ws, realtimeWireEvent{EventID: uuid.NewString(), Type: "response.created", Data: map[string]any{
		"turn_id": turnID, "response_id": snapshot.ProviderResponseID,
	}}); err != nil {
		return err
	}
	snapshot, err = service.Receive(ctx, sessionID, turnID, time.Now().UTC())
	if err != nil {
		return err
	}
	if err = s.writeRealtime(ws, realtimeWireEvent{EventID: uuid.NewString(), Type: "response.delta", Data: map[string]any{
		"turn_id": turnID, "response_id": snapshot.ProviderResponseID, "bytes": snapshot.ForwardedBytes,
	}}); err != nil {
		return err
	}
	if billingRefs.enabled {
		if err = s.finalizeMediaRequest(ctx, billingRefs, "completed", "confirmed_final", 1); err != nil {
			return err
		}
		return s.writeRealtime(ws, realtimeWireEvent{EventID: uuid.NewString(), Type: "response.done", Data: map[string]any{
			"turn_id": turnID, "response_id": snapshot.ProviderResponseID,
		}})
	}
	return s.writeRealtime(ws, realtimeWireEvent{EventID: uuid.NewString(), Type: "response.pending", Data: map[string]any{
		"turn_id": turnID, "response_id": snapshot.ProviderResponseID,
		"reason": "settlement_unwired",
	}})
}

func (s *Server) disconnectRealtime(service *realtime.Service, sessionID, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = service.Disconnect(ctx, sessionID, reason, time.Now().UTC())
}

func (s *Server) writeRealtime(ws *websocket.Conn, event realtimeWireEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err = ws.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return websocket.Message.Send(ws, string(data))
}

func (s *Server) writeRealtimeError(ws *websocket.Conn, err error) error {
	if err == nil {
		return nil
	}
	return s.writeRealtime(ws, realtimeWireEvent{EventID: uuid.NewString(), Type: "error", Data: map[string]any{
		"code": realtimeErrorCode(err),
	}})
}

func realtimeErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, realtime.ErrEventConflict):
		return "event_conflict"
	case errors.Is(err, realtime.ErrTurnInFlight):
		return "turn_in_flight"
	case errors.Is(err, realtime.ErrTurnRequired):
		return "turn_required"
	case errors.Is(err, realtime.ErrInvalidTransition):
		return "turn_required"
	case errors.Is(err, realtime.ErrLimitExceeded):
		return "limit_exceeded"
	case errors.Is(err, realtime.ErrBackpressure):
		return "backpressure"
	case errors.Is(err, realtime.ErrPendingNoResubmit):
		return "provider_pending"
	case errors.Is(err, realtime.ErrProviderProtocol):
		return "provider_protocol"
	case errors.Is(err, realtime.ErrSessionClosed):
		return "session_closed"
	default:
		return "invalid_event"
	}
}

func isRealtimeTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr interface{ Timeout() bool }
	return errors.As(err, &netErr) && netErr.Timeout()
}
