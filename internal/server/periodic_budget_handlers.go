package server

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

type periodicBudgetResponseBuffer struct{ bytes.Buffer }

func (b *periodicBudgetResponseBuffer) Write(raw []byte) (int, error) {
	if len(raw) > (128<<10)-b.Len() {
		return 0, periodicbudget.ErrInvariant
	}
	return b.Buffer.Write(raw)
}

func (s *Server) InitializePeriodicBudgets() error {
	if s.Config.Secrets == nil {
		return periodicbudget.ErrUnavailable
	}
	reader, err := periodicbudget.NewReader(s.Pool, s.Config.Secrets.Pepper)
	if err != nil {
		return periodicbudget.ErrUnavailable
	}
	commander, err := periodicbudget.NewCommander(s.Pool)
	if err != nil {
		return periodicbudget.ErrUnavailable
	}
	s.PeriodicReader, s.PeriodicCommander = reader, commander
	return nil
}

func (s *Server) registerPeriodicBudgets(a, p *gin.RouterGroup) {
	register := func(group *gin.RouterGroup, path, scope string, writable bool) {
		group.GET(path, s.periodicBudgetGet(scope, ""))
		group.GET(path+"/windows", s.periodicBudgetGet(scope, "windows"))
		group.GET(path+"/allocations", s.periodicBudgetGet(scope, "allocations"))
		if writable {
			group.PUT(path, s.periodicBudgetPut(scope))
		}
	}
	register(p, "/tenants/:id/periodic-budget", "tenant", true)
	register(p, "/procurement-accounts/:id/periodic-budget", "procurement", true)
	register(a, "/wallet/periodic-budget", "wallet", false)
	register(a, "/keys/:id/periodic-budget", "key", true)
	p.GET("/periodic-budget-operations/:id", s.periodicBudgetOperationGet(true))
	a.GET("/periodic-budget-operations/:id", s.periodicBudgetOperationGet(false))
}

func (s *Server) periodicBudgetFailure(c *gin.Context, status int, code string) {
	c.Header("Cache-Control", "no-store")
	s.fail(c, status, code, "周期预算操作未完成；结果不明时请查询原操作编号")
}

func (s *Server) periodicBudgetError(c *gin.Context, err error) {
	code, status := periodicbudget.ErrorCode(err), http.StatusServiceUnavailable
	switch code {
	case "invalid_request", "invalid_cursor":
		status = 400
	case "forbidden":
		status = 403
	case "not_found":
		status = 404
	case "periodic_budget_conflict", "periodic_calendar_immutable", "periodic_legacy_import_requires_maintenance", "policy_operation_conflict", "periodic_revision_exhausted":
		status = 409
	case "request_too_large":
		status = 413
	case "rate_limited":
		status = 429
		c.Header("Retry-After", "1")
	case "periodic_clock_regression", "periodic_budget_invariant", "periodic_budget_outcome_unknown", "periodic_budget_unavailable":
	default:
		code = "periodic_budget_unavailable"
	}
	s.periodicBudgetFailure(c, status, code)
}

func (s *Server) periodicBudgetJSON(c *gin.Context, status int, value any) {
	// Encode before committing response headers. Repository transactions and
	// rows have already closed, so a bad/oversized response cannot leak a page.
	var buffer periodicBudgetResponseBuffer
	if err := json.NewEncoder(&buffer).Encode(value); err != nil {
		s.periodicBudgetFailure(c, 503, "periodic_budget_invariant")
		return
	}
	if c.Request.Context().Err() != nil {
		s.periodicBudgetFailure(c, 503, "periodic_budget_unavailable")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(status, "application/json; charset=utf-8", buffer.Bytes())
}

// The route chooses scope; only the actor and active tenant from the validated
// session are forwarded. Current database authority remains the repository's
// responsibility, including target ownership and revoked-Key write checks.
func (s *Server) periodicBudgetTarget(c *gin.Context, scope string) (id, tenant string, ok bool) {
	if scope == "wallet" || scope == "key" {
		t := sessionOf(c).Tenant
		if t == nil || !periodicbudget.ValidUUID(t.ID) {
			s.periodicBudgetFailure(c, 403, "forbidden")
			return "", "", false
		}
		tenant = t.ID
	}
	if scope == "wallet" {
		return tenant, tenant, true
	}
	id, ok = s.periodicBudgetID(c)
	return id, tenant, ok
}

func (s *Server) periodicBudgetGet(scope, page string) gin.HandlerFunc {
	return func(c *gin.Context) {
		cursor, ok := s.periodicBudgetQuery(c, page != "")
		if !ok {
			return
		}
		id, tenant, ok := s.periodicBudgetTarget(c, scope)
		if !ok {
			return
		}
		done, ok := s.admitPeriodicBudget(c, periodicBudgetRead)
		if !ok {
			return
		}
		defer done()
		reader := s.PeriodicReader
		if reader == nil {
			s.periodicBudgetError(c, periodicbudget.ErrUnavailable)
			return
		}
		ctx, actor := c.Request.Context(), sessionOf(c).User.ID
		var value any
		var next string
		var err error
		switch page {
		case "":
			var v periodicbudget.PeriodicBudgetView
			switch scope {
			case "tenant":
				v, err = reader.PlatformTenantBudget(ctx, actor, id)
			case "wallet":
				v, err = reader.WalletBudget(ctx, actor, tenant)
			case "key":
				v, err = reader.KeyBudget(ctx, actor, tenant, id)
			case "procurement":
				v, err = reader.ProcurementBudget(ctx, actor, id)
			}
			if err == nil {
				err = v.Validate()
			}
			value = v
		case "windows":
			var v periodicbudget.WindowPage
			switch scope {
			case "tenant":
				v, err = reader.PlatformTenantWindows(ctx, actor, id, cursor)
			case "wallet":
				v, err = reader.WalletWindows(ctx, actor, tenant, cursor)
			case "key":
				v, err = reader.KeyWindows(ctx, actor, tenant, id, cursor)
			case "procurement":
				v, err = reader.ProcurementWindows(ctx, actor, id, cursor)
			}
			if err == nil && v.Data == nil {
				err = periodicbudget.ErrInvariant
			}
			value, next = v.Data, v.NextCursor
		case "allocations":
			var v periodicbudget.AllocationPage
			switch scope {
			case "tenant":
				v, err = reader.PlatformTenantAllocations(ctx, actor, id, cursor)
			case "wallet":
				v, err = reader.WalletAllocations(ctx, actor, tenant, cursor)
			case "key":
				v, err = reader.KeyAllocations(ctx, actor, tenant, id, cursor)
			case "procurement":
				v, err = reader.ProcurementAllocations(ctx, actor, id, cursor)
			}
			if err == nil && v.Data == nil {
				err = periodicbudget.ErrInvariant
			}
			value, next = v.Data, v.NextCursor
		}
		if err != nil {
			s.periodicBudgetError(c, err)
			return
		}
		response := gin.H{"data": value, "request_id": c.GetString("request_id")}
		if page != "" {
			response["next_cursor"] = next
		}
		s.periodicBudgetJSON(c, 200, response)
	}
}

func (s *Server) periodicBudgetPut(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if (scope == "tenant" || scope == "procurement") && !s.admin(c) || !s.sensitive(c) {
			return
		}
		id, tenant, ok := s.periodicBudgetTarget(c, scope)
		if !ok {
			return
		}
		in, ok := s.periodicBudgetInput(c)
		if !ok {
			return
		}
		done, ok := s.admitPeriodicBudget(c, periodicBudgetWrite)
		if !ok {
			return
		}
		defer done()
		commander := s.PeriodicCommander
		if commander == nil {
			s.periodicBudgetError(c, periodicbudget.ErrUnavailable)
			return
		}
		ctx, actor := c.Request.Context(), sessionOf(c).User.ID
		var v periodicbudget.ConfigureResult
		var err error
		switch scope {
		case "tenant":
			v, err = commander.ConfigurePlatformTenant(ctx, actor, id, in)
		case "key":
			v, err = commander.ConfigureKey(ctx, actor, tenant, id, in)
		case "procurement":
			v, err = commander.ConfigureProcurement(ctx, actor, id, in)
		}
		if err == nil {
			err = v.Receipt.Validate()
		}
		if err != nil {
			s.periodicBudgetError(c, err)
			return
		}
		status := http.StatusOK
		if v.Created {
			status = http.StatusCreated
		}
		s.periodicBudgetJSON(c, status, gin.H{"data": v.Receipt, "request_id": c.GetString("request_id")})
	}
}

func (s *Server) periodicBudgetOperationGet(platform bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := s.periodicBudgetQuery(c, false); !ok {
			return
		}
		id, ok := s.periodicBudgetID(c)
		if !ok {
			return
		}
		tenant := ""
		if !platform {
			t := sessionOf(c).Tenant
			if t == nil || !periodicbudget.ValidUUID(t.ID) {
				s.periodicBudgetFailure(c, 403, "forbidden")
				return
			}
			tenant = t.ID
		}
		done, ok := s.admitPeriodicBudget(c, periodicBudgetOperation)
		if !ok {
			return
		}
		defer done()
		if s.PeriodicReader == nil {
			s.periodicBudgetError(c, periodicbudget.ErrUnavailable)
			return
		}
		var v periodicbudget.CommandReceipt
		var err error
		if platform {
			v, err = s.PeriodicReader.PlatformOperation(c.Request.Context(), sessionOf(c).User.ID, id)
		} else {
			v, err = s.PeriodicReader.TenantOperation(c.Request.Context(), sessionOf(c).User.ID, tenant, id)
		}
		if err == nil {
			err = v.Validate()
		}
		if err != nil {
			s.periodicBudgetError(c, err)
			return
		}
		s.periodicBudgetJSON(c, 200, gin.H{"data": v, "request_id": c.GetString("request_id")})
	}
}
