package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/realtime"
	"golang.org/x/net/websocket"
)

type realtimeFrame struct {
	EventID string `json:"event_id"`
	Type    string `json:"type"`
	Data    struct {
		Text        string `json:"text"`
		AudioBase64 string `json:"audio_base64"`
	} `json:"data"`
}

func (s *Server) realtimeWebSocket(c *gin.Context) {
	principal, ok := c.Get("principal")
	if !ok {
		s.fail(c, http.StatusUnauthorized, "invalid_credentials", "需要有效 API Key")
		return
	}
	if s.RealtimeService == nil {
		s.fail(c, http.StatusServiceUnavailable, "unavailable", "实时服务未就绪")
		return
	}
	handler := websocket.Handler(func(conn *websocket.Conn) {
		s.handleRealtime(conn, c.Request.Context(), principal.(identity.Principal), c.Request.Header.Get("Origin"), c.Query("model"))
	})
	handler.ServeHTTP(c.Writer, c.Request)
	c.Abort()
}

func (s *Server) handleRealtime(conn *websocket.Conn, ctx context.Context, p identity.Principal, origin, model string) {
	if conn == nil {
		return
	}
	defer conn.Close()
	if origin == "" || model == "" || !validMediaModel(p, model) {
		writeRealtimeError(conn, "invalid_request", "实时会话参数无效")
		return
	}
	now := time.Now().UTC()
	binding := realtime.TicketBinding{
		SessionID: uuid.NewString(), TenantID: p.TenantID, ActorID: p.UserID, APIKeyID: p.KeyID,
		ModelID: model, ProfileID: uuid.NewString(), RouteID: uuid.NewString(), Origin: origin,
	}
	ticket, err := s.RealtimeService.IssueTicket(ctx, binding, now, realtime.MaxTicketTTL)
	if err != nil {
		writeRealtimeError(conn, "session_unavailable", "实时会话票据创建失败")
		return
	}
	if _, err = s.RealtimeService.CreateSession(ctx, ticket.Token, binding, realtime.SessionConfig{
		ID: binding.SessionID, TenantID: binding.TenantID, ActorID: binding.ActorID, APIKeyID: binding.APIKeyID,
		ModelID: binding.ModelID, ProfileID: binding.ProfileID, RouteID: binding.RouteID, Origin: binding.Origin,
	}, now); err != nil {
		writeRealtimeError(conn, "session_unavailable", "实时会话创建失败")
		return
	}
	closed := false
	defer func() {
		if !closed {
			_, _ = s.RealtimeService.Disconnect(context.Background(), binding.SessionID, "socket_closed", time.Now().UTC())
		}
	}()
	if err = websocket.Message.Send(conn, map[string]any{"type": "session.created", "session_id": binding.SessionID}); err != nil {
		return
	}
	parser, err := realtime.NewEventParser(realtime.EventLimits{})
	if err != nil {
		return
	}
	turnStarted := false
	turnID := uuid.NewString()
	refs := realtime.CommercialRefs{RequestID: uuid.NewString(), ReservationID: uuid.NewString(), AttemptID: uuid.NewString()}
	for {
		var raw []byte
		if err = websocket.Message.Receive(conn, &raw); err != nil {
			return
		}
		event, parseErr := parser.Parse(raw)
		if parseErr != nil {
			writeRealtimeError(conn, "invalid_event", "实时事件无效")
			return
		}
		if event.TurnRequired && !turnStarted {
			if _, err = s.RealtimeService.BeginTurn(ctx, binding.SessionID, realtime.BeginTurnInput{TurnID: turnID, RequestID: refs.RequestID, ReservationID: refs.ReservationID, AttemptID: refs.AttemptID}, refs, time.Now().UTC()); err != nil {
				writeRealtimeError(conn, "turn_unavailable", "实时轮次不可用")
				return
			}
			turnStarted = true
		}
		accepted, acceptErr := s.RealtimeService.AcceptEvent(ctx, binding.SessionID, event, time.Now().UTC())
		if acceptErr != nil {
			writeRealtimeError(conn, realtimeErrorCode(acceptErr), "实时事件未接受")
			return
		}
		if err = websocket.Message.Send(conn, map[string]any{"type": "event.accepted", "event_id": event.ID, "duplicate": accepted.Disposition == realtime.EventDuplicate}); err != nil {
			return
		}
		if event.Type == realtime.EventResponseCancel {
			_, _ = s.RealtimeService.Disconnect(ctx, binding.SessionID, "client_cancel", time.Now().UTC())
			closed = true
			_ = websocket.Message.Send(conn, map[string]any{"type": "session.closed", "reason": "client_cancel"})
			return
		}
		if !event.ChargeBearing || accepted.Disposition == realtime.EventDuplicate {
			continue
		}
		payload := realtimePayload(raw, event.Type)
		turn, dispatchErr := s.RealtimeService.Dispatch(ctx, binding.SessionID, turnID, realtime.ProviderPayload{EventID: event.ID, EventType: event.Type, Payload: payload}, time.Now().UTC())
		if dispatchErr != nil {
			writeRealtimeError(conn, realtimeErrorCode(dispatchErr), "实时供应商提交失败")
			return
		}
		if err = websocket.Message.Send(conn, map[string]any{"type": "response.started", "turn_id": turn.ID}); err != nil {
			return
		}
		turn, err = s.RealtimeService.Receive(ctx, binding.SessionID, turnID, time.Now().UTC())
		if err != nil {
			writeRealtimeError(conn, realtimeErrorCode(err), "实时供应商响应失败")
			return
		}
		if err = websocket.Message.Send(conn, map[string]any{"type": "response.completed", "turn_id": turn.ID, "response_id": turn.ProviderResponseID, "bytes": turn.ForwardedBytes}); err != nil {
			return
		}
		proof := realtime.SettlementProof{FinalizationID: uuid.NewString(), SettlementID: uuid.NewString(), RetailUsageDigest: strings.Repeat("a", 64), RetailUsageState: realtime.UsageConfirmedFinal, SupplierUsageState: realtime.UsageUnknown}
		if err = s.RealtimeService.Settle(ctx, binding.SessionID, turnID, proof, time.Now().UTC()); err != nil {
			writeRealtimeError(conn, "settlement_pending", "实时轮次结算失败")
			return
		}
		_, _ = s.RealtimeService.Disconnect(ctx, binding.SessionID, "completed", time.Now().UTC())
		closed = true
		_ = websocket.Message.Send(conn, map[string]any{"type": "session.closed", "reason": "completed", "settlement_id": proof.SettlementID})
		return
	}
}

func realtimePayload(raw []byte, typ realtime.EventType) []byte {
	var frame realtimeFrame
	if json.Unmarshal(raw, &frame) != nil {
		return nil
	}
	switch typ {
	case realtime.EventInputText:
		return []byte(frame.Data.Text)
	case realtime.EventInputAudio:
		decoded, err := base64.StdEncoding.DecodeString(frame.Data.AudioBase64)
		if err == nil {
			return decoded
		}
	}
	return nil
}

func writeRealtimeError(conn *websocket.Conn, code, message string) {
	if conn == nil {
		return
	}
	_ = websocket.Message.Send(conn, map[string]any{"type": "error", "code": code, "message": message})
}

func realtimeErrorCode(err error) string {
	switch {
	case errors.Is(err, realtime.ErrPendingNoResubmit):
		return "provider_consumption_unknown"
	case errors.Is(err, realtime.ErrEventConflict):
		return "event_conflict"
	case errors.Is(err, realtime.ErrTurnInFlight):
		return "turn_in_flight"
	default:
		return "realtime_error"
	}
}
