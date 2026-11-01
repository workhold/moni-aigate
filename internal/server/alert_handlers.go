package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/alerts"
)

type alertService interface {
	alerts.Queries
	alerts.Commands
}

func (s *Server) InitializeAlerts() error {
	store, err := alerts.NewStore(s.Pool, alerts.RuntimeOptions{DeploymentEnabled: s.Config.OperationsAlertsEnabled, RecoveryMode: s.Config.RecoveryMode})
	if err == nil {
		s.Alerts = store
	}
	return err
}
func (s *Server) registerAlerts(p *gin.RouterGroup) {
	p.GET("/alerts/capabilities", s.alertCapabilities)
	p.GET("/alert-rules", s.alertRules)
	p.POST("/alert-rules", s.createAlertRule)
	p.GET("/alert-rules/:id", s.alertRule)
	p.PUT("/alert-rules/:id", s.configureAlertRule)
	p.POST("/alert-rules/:id/retire", s.retireAlertRule)
	p.GET("/alerts", s.alertEpisodes)
	p.GET("/alerts/:id", s.alertEpisode)
	p.GET("/alerts/:id/events", s.alertEvents)
	p.POST("/alerts/:id/acknowledge", s.acknowledgeAlert)
	p.GET("/alert-operations/:id", s.alertOperation)
}
func (s *Server) alertFailure(c *gin.Context, status int, code string) {
	c.Header("Cache-Control", "no-store")
	s.fail(c, status, code, "告警操作未完成；结果不明时请查询原操作编号")
}
func (s *Server) alertError(c *gin.Context, err error) {
	code, status := "alert_unavailable", 503
	var ae *alerts.Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case "invalid_request":
			code, status = ae.Code, 400
		case "unauthorized":
			code, status = ae.Code, 401
		case "forbidden":
			code, status = ae.Code, 403
		case "alert_operation_conflict":
			code, status = ae.Code, 409
		case "alert_not_found":
			code, status = ae.Code, 404
		case "alert_rate_limited":
			code, status = ae.Code, 429
		}
	}
	s.alertFailure(c, status, code)
}
func (s *Server) alertJSON(c *gin.Context, value any) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 256<<10 {
		s.alertFailure(c, 503, "alert_unavailable")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
}
func (s *Server) alertOK(c *gin.Context, value any, err error) {
	if err != nil {
		s.alertError(c, err)
		return
	}
	s.alertJSON(c, gin.H{"data": value, "request_id": c.GetString("request_id")})
}
func alertPage[T any](s *Server, c *gin.Context, page alerts.Page[T], err error) {
	if err != nil {
		s.alertError(c, err)
		return
	}
	if page.Data == nil {
		s.alertFailure(c, 503, "alert_unavailable")
		return
	}
	s.alertJSON(c, gin.H{"data": page.Data, "next_cursor": page.NextCursor, "request_id": c.GetString("request_id")})
}
func (s *Server) alertReader(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	switch sessionOf(c).User.PlatformRole {
	case "admin", "finance_operator", "finance_approver":
	default:
		s.alertFailure(c, 403, "forbidden")
		return false
	}
	if s.Alerts == nil {
		s.alertFailure(c, 503, "alert_unavailable")
		return false
	}
	return true
}
func (s *Server) alertWriter(c *gin.Context, admin bool) bool {
	if !s.alertReader(c) {
		return false
	}
	if admin && sessionOf(c).User.PlatformRole != "admin" {
		s.alertFailure(c, 403, "forbidden")
		return false
	}
	if !s.sensitive(c) {
		return false
	}
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		s.commerceInvalid(c)
		return false
	}
	return true
}
func alertReaderOf(c *gin.Context) alerts.Reader { return alerts.Reader{ActorID: sessionOf(c).User.ID} }
func (s *Server) alertCapabilities(c *gin.Context) {
	if !s.alertReader(c) {
		return
	}
	if _, ok := s.alertQuery(c); !ok {
		return
	}
	done, ok := s.admitAlert(c, false)
	if !ok {
		return
	}
	defer done()
	v, e := s.Alerts.Capabilities(c.Request.Context(), alertReaderOf(c))
	s.alertOK(c, v, e)
}
func (s *Server) alertRules(c *gin.Context) {
	if !s.alertReader(c) {
		return
	}
	q, ok := s.alertQuery(c, "state", "kind", "scope_id", "cursor")
	if !ok {
		return
	}
	f := alerts.RuleFilter{}
	if v := q.Get("state"); v != "" {
		if v != "enabled" && v != "disabled" && v != "retired" {
			s.commerceInvalid(c)
			return
		}
		n := alerts.RuleState(v)
		f.State = &n
	}
	if v := q.Get("kind"); v != "" {
		if !alertKind(v) {
			s.commerceInvalid(c)
			return
		}
		n := alerts.Kind(v)
		f.Kind = &n
	}
	if v := q.Get("scope_id"); v != "" {
		if !procurementUUID(v) || f.Kind == nil {
			s.commerceInvalid(c)
			return
		}
		f.ScopeID = &v
	}
	f.Cursor, ok = alertCursor(q.Get("cursor"), alerts.RuleFilterDigest(f))
	if !ok {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitAlert(c, false)
	if !ok {
		return
	}
	defer done()
	v, e := s.Alerts.Rules(c.Request.Context(), alertReaderOf(c), f)
	alertPage(s, c, v, e)
}
func (s *Server) alertRule(c *gin.Context) {
	if !s.alertReader(c) {
		return
	}
	if _, ok := s.alertQuery(c); !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	done, ok := s.admitAlert(c, false)
	if !ok {
		return
	}
	defer done()
	v, e := s.Alerts.Rule(c.Request.Context(), alertReaderOf(c), id)
	s.alertOK(c, v, e)
}
func (s *Server) alertEpisodes(c *gin.Context) {
	if !s.alertReader(c) {
		return
	}
	q, ok := s.alertQuery(c, "status", "rule_id", "kind", "cursor")
	if !ok {
		return
	}
	f := alerts.EpisodeFilter{}
	if v := q.Get("status"); v != "" {
		if v != "open" && v != "acknowledged" && v != "resolved" {
			s.commerceInvalid(c)
			return
		}
		f.Status = &v
	}
	if v := q.Get("rule_id"); v != "" {
		if !procurementUUID(v) {
			s.commerceInvalid(c)
			return
		}
		f.RuleID = &v
	}
	if v := q.Get("kind"); v != "" {
		if !alertKind(v) {
			s.commerceInvalid(c)
			return
		}
		n := alerts.Kind(v)
		f.Kind = &n
	}
	f.Cursor, ok = alertCursor(q.Get("cursor"), alerts.EpisodeFilterDigest(f))
	if !ok {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitAlert(c, false)
	if !ok {
		return
	}
	defer done()
	v, e := s.Alerts.Episodes(c.Request.Context(), alertReaderOf(c), f)
	alertPage(s, c, v, e)
}
func (s *Server) alertEpisode(c *gin.Context) {
	if !s.alertReader(c) {
		return
	}
	if _, ok := s.alertQuery(c); !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	done, ok := s.admitAlert(c, false)
	if !ok {
		return
	}
	defer done()
	v, e := s.Alerts.Episode(c.Request.Context(), alertReaderOf(c), id)
	s.alertOK(c, v, e)
}
func (s *Server) alertEvents(c *gin.Context) {
	if !s.alertReader(c) {
		return
	}
	q, ok := s.alertQuery(c, "cursor")
	if !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	cursor, ok := alertCursor(q.Get("cursor"), alerts.EventFilterDigest(id))
	if !ok {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitAlert(c, false)
	if !ok {
		return
	}
	defer done()
	v, e := s.Alerts.Events(c.Request.Context(), alertReaderOf(c), id, alerts.EventFilter{Cursor: cursor})
	alertPage(s, c, v, e)
}
func (s *Server) alertOperation(c *gin.Context) {
	if !s.alertReader(c) {
		return
	}
	if _, ok := s.alertQuery(c); !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	done, ok := s.admitAlert(c, false)
	if !ok {
		return
	}
	defer done()
	v, e := s.Alerts.Operation(c.Request.Context(), alertReaderOf(c), id)
	s.alertOK(c, v, e)
}
func (s *Server) createAlertRule(c *gin.Context) {
	if !s.alertWriter(c, true) {
		return
	}
	o, ok := alertBody(c, "operation_id", "kind", "scope_type", "scope_id", "label", "state", "parameters")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	id, a := commerceID(o["operation_id"])
	kind, b := commerceString(o["kind"])
	scope, d := commerceString(o["scope_type"])
	target, e := commerceID(o["scope_id"])
	label, f := alertLabel(o["label"])
	state, g := alertState(o["state"])
	params, h := alertParameters(o["parameters"])
	if !a || !b || !d || !e || !f || !g || !h || !alertParametersMatch(alerts.Kind(kind), params) || !alertScopeMatches(alerts.Kind(kind), scope) {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitAlert(c, true)
	if !ok {
		return
	}
	defer done()
	v, err := s.Alerts.Create(c.Request.Context(), alertReaderOf(c), alerts.CreateRule{OperationID: id, Kind: alerts.Kind(kind), ScopeType: alerts.ScopeType(scope), ScopeID: target, Label: label, State: state, Parameters: params})
	s.alertOK(c, v, err)
}
func (s *Server) configureAlertRule(c *gin.Context) {
	if !s.alertWriter(c, true) {
		return
	}
	target, ok := s.commercePathID(c)
	if !ok {
		return
	}
	o, ok := alertBody(c, "operation_id", "expected_revision", "label", "state", "parameters")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	id, a := commerceID(o["operation_id"])
	rev, b := alertDecimal(o["expected_revision"], true)
	label, d := alertLabel(o["label"])
	state, e := alertState(o["state"])
	params, f := alertParameters(o["parameters"])
	if !a || !b || !d || !e || !f {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitAlert(c, true)
	if !ok {
		return
	}
	defer done()
	v, err := s.Alerts.Configure(c.Request.Context(), alertReaderOf(c), target, alerts.ConfigureRule{OperationID: id, ExpectedRevision: rev, Label: label, State: state, Parameters: params})
	s.alertOK(c, v, err)
}
func (s *Server) retireAlertRule(c *gin.Context) {
	if !s.alertWriter(c, true) {
		return
	}
	target, ok := s.commercePathID(c)
	if !ok {
		return
	}
	o, ok := alertBody(c, "operation_id", "expected_revision")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	id, a := commerceID(o["operation_id"])
	rev, b := alertDecimal(o["expected_revision"], true)
	if !a || !b {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitAlert(c, true)
	if !ok {
		return
	}
	defer done()
	v, e := s.Alerts.Retire(c.Request.Context(), alertReaderOf(c), target, alerts.RetireRule{OperationID: id, ExpectedRevision: rev})
	s.alertOK(c, v, e)
}
func (s *Server) acknowledgeAlert(c *gin.Context) {
	if !s.alertWriter(c, false) {
		return
	}
	target, ok := s.commercePathID(c)
	if !ok {
		return
	}
	o, ok := alertBody(c, "operation_id", "expected_action_revision")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	id, a := commerceID(o["operation_id"])
	rev, b := alertDecimal(o["expected_action_revision"], true)
	if !a || !b {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitAlert(c, true)
	if !ok {
		return
	}
	defer done()
	v, e := s.Alerts.Acknowledge(c.Request.Context(), alertReaderOf(c), target, alerts.Acknowledge{OperationID: id, ExpectedActionRevision: rev})
	s.alertOK(c, v, e)
}
