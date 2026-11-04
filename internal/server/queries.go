package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
)

var monetary = map[string]bool{"input_rate": true, "output_rate": true, "amount": true, "balance": true, "available": true, "held": true, "spent": true, "lifetime_limit": true, "charged_amount": true, "reserved_amount": true, "refunded_amount": true, "waived_retail_amount": true, "raw_retail_amount": true, "delta": true}

func queryValue(name string, value any) any {
	// pgx may decode timestamptz in the process's local timezone. Keep the
	// instant and full precision while giving HTTP and cursors canonical UTC.
	if at, ok := value.(time.Time); ok {
		return at.UTC()
	}
	if monetary[name] && value != nil {
		return fmt.Sprint(value)
	}
	return value
}

func (s *Server) query(ctx context.Context, sql string, args ...any) ([]map[string]any, error) {
	rows, e := s.Pool.Query(ctx, sql, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	all := []map[string]any{}
	fields := rows.FieldDescriptions()
	for rows.Next() {
		vals, e := rows.Values()
		if e != nil {
			return nil, e
		}
		m := map[string]any{}
		for i, v := range vals {
			name := fields[i].Name
			m[name] = queryValue(name, v)
		}
		all = append(all, m)
	}
	return all, rows.Err()
}

type cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        string    `json:"id"`
}

func (s *Server) page(c *gin.Context, sql string, args ...any) {
	query := "SELECT * FROM (" + sql + ") AS q"
	if raw := c.Query("cursor"); raw != "" {
		if len(raw) > 512 {
			s.fail(c, 400, "invalid_cursor", "分页游标无效")
			return
		}
		b, e := base64.RawURLEncoding.DecodeString(raw)
		v := cursor{}
		if e != nil || json.Unmarshal(b, &v) != nil || v.ID == "" || v.CreatedAt.IsZero() {
			s.fail(c, 400, "invalid_cursor", "分页游标无效")
			return
		}
		n := len(args)
		query += fmt.Sprintf(" WHERE (created_at,id)<($%d,$%d)", n+1, n+2)
		args = append(args, v.CreatedAt, v.ID)
	}
	query += " ORDER BY created_at DESC,id DESC LIMIT 51"
	all, e := s.query(c.Request.Context(), query, args...)
	if e != nil {
		s.error(c, e)
		return
	}
	next := ""
	if len(all) > 50 {
		all = all[:50]
		v := all[49]
		if ts, ok := v["created_at"].(time.Time); ok {
			b, _ := json.Marshal(cursor{ts, fmt.Sprint(v["id"])})
			next = base64.RawURLEncoding.EncodeToString(b)
		}
	}
	c.JSON(200, gin.H{"data": all, "request_id": c.GetString("request_id"), "next_cursor": next})
}
func (s *Server) keys(c *gin.Context) {
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	v := sessionOf(c)
	s.page(c, `SELECT id,name,prefix,scopes,allowed_regions,lifetime_limit,spent,held,expires_at,revoked_at,created_at,configuration_revision::text AS configuration_revision,allowed_client_cidrs,access_policy_revision::text AS access_policy_revision,rpm_limit,burst_limit,concurrency_limit,limit_revision::text AS limit_revision FROM api_keys WHERE tenant_id=$1 AND ($2='owner' OR owner_id=$3)`, tid, v.Tenant.Role, v.User.ID)
}
func (s *Server) tenants(c *gin.Context) {
	if sessionOf(c).User.PlatformRole != "admin" {
		s.page(c, `SELECT id,name,status,created_at FROM tenants`)
		return
	}
	s.page(c, `SELECT t.id,t.name,t.status,t.allowed_regions,t.created_at,ARRAY(SELECT model_id FROM tenant_model_grants WHERE tenant_id=t.id) AS model_ids,(SELECT u.email FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.tenant_id=t.id AND m.role='owner' ORDER BY u.id LIMIT 1) AS owner_email FROM tenants t`)
}
func (s *Server) users(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	s.page(c, `SELECT id,email,platform_role,status,mfa_enabled,created_at FROM users`)
}
func (s *Server) members(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	v, e := s.query(c.Request.Context(), `SELECT m.user_id AS id,m.user_id,u.email,m.role,m.status,m.allow_all_models,ARRAY(SELECT model_id FROM member_model_grants g WHERE g.tenant_id=m.tenant_id AND g.user_id=m.user_id) AS model_ids FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.tenant_id=$1 ORDER BY u.email LIMIT 1000`, c.Param("id"))
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) channels(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	s.page(c, `SELECT c.id,c.name,c.enabled,c.quarantined,c.current_version,c.management_revision::text AS management_revision,c.created_at,v.base_url,v.provider,v.region AS data_region,v.region,v.allowed_cidrs,CASE WHEN c.quarantined THEN 'quarantined' WHEN c.enabled THEN 'active' ELSE 'disabled' END AS status FROM channels c JOIN channel_versions v ON v.id=c.current_version`)
}
func modelSelect() string {
	return modelSelectFor(false)
}
func modelSelectFor(publication bool) string {
	publicationColumns := ""
	if publication {
		publicationColumns = ",m.public_visible,m.public_revision::text AS public_revision"
	}
	return `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at) SELECT m.id` + publicationColumns + `,m.public_name,m.enabled,meter_model_profile(m.id) AS profile,m.profile_version,COALESCE(NULLIF(meter_model_profile(m.id)->>'protocol',''),'openai_chat') AS protocol,m.active_revision,m.active_revision AS revision,m.created_at,(meter_model_profile(m.id)->>'context_limit')::bigint AS context_limit,(meter_model_profile(m.id)->>'output_limit')::bigint AS output_limit,(meter_model_profile(m.id)->>'default_output_limit')::bigint AS default_output_limit,(meter_model_profile(m.id)->>'input_token_upper_bound')::bigint AS input_token_upper_bound,(meter_model_profile(m.id)->>'supports_tools')::boolean AS supports_tools,'profile_input_bound' AS reservation_strategy,meter_price_million(p.id,'text_input_token') AS input_rate,meter_price_million(p.id,'text_output_token') AS output_rate FROM instant CROSS JOIN models m LEFT JOIN LATERAL(SELECT id FROM price_versions WHERE model_id=m.id AND price_group_id IS NULL AND effective_at<=instant.at ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1)p ON true`
}
func customerModelSelect() string {
	return `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at) SELECT m.id,m.public_name,m.enabled,meter_model_profile(m.id) AS profile,m.profile_version,COALESCE(NULLIF(meter_model_profile(m.id)->>'protocol',''),'openai_chat') AS protocol,m.active_revision,m.active_revision AS revision,m.created_at,(meter_model_profile(m.id)->>'context_limit')::bigint AS context_limit,(meter_model_profile(m.id)->>'output_limit')::bigint AS output_limit,(meter_model_profile(m.id)->>'default_output_limit')::bigint AS default_output_limit,(meter_model_profile(m.id)->>'input_token_upper_bound')::bigint AS input_token_upper_bound,(meter_model_profile(m.id)->>'supports_tools')::boolean AS supports_tools,'profile_input_bound' AS reservation_strategy,meter_price_million(p.id,'text_input_token') AS input_rate,meter_price_million(p.id,'text_output_token') AS output_rate,jsonb_build_object('model_id',m.id,'source',CASE WHEN t.price_group_id IS NULL THEN 'default' ELSE 'group' END,'status',CASE WHEN p.id IS NULL THEN 'unavailable' ELSE 'available' END,'price_group_id',t.price_group_id,'price_version_id',p.id,'currency','CNY','input_rate',meter_price_million(p.id,'text_input_token')::text,'output_rate',meter_price_million(p.id,'text_output_token')::text,'effective_at',pricing_time(p.effective_at),'as_of',pricing_time(instant.at)) AS retail_price FROM instant CROSS JOIN tenants t JOIN memberships mb ON mb.tenant_id=t.id AND mb.user_id=$2 JOIN users u ON u.id=mb.user_id JOIN tenant_model_grants g ON g.tenant_id=t.id JOIN models m ON m.id=g.model_id LEFT JOIN LATERAL(SELECT id,effective_at FROM price_versions WHERE model_id=m.id AND price_group_id IS NOT DISTINCT FROM t.price_group_id AND effective_at<=instant.at ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1)p ON true WHERE t.id=$1 AND t.status='active' AND mb.status='active' AND u.status='active' AND m.enabled AND (mb.allow_all_models OR EXISTS(SELECT 1 FROM member_model_grants mg WHERE mg.tenant_id=t.id AND mg.user_id=$2 AND mg.model_id=m.id))`
}
func (s *Server) models(c *gin.Context) {
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	v, e := s.query(c.Request.Context(), customerModelSelect()+` ORDER BY m.public_name LIMIT 1000`, tid, sessionOf(c).User.ID)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) platformModels(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	v, e := s.query(c.Request.Context(), modelSelectFor(true)+` ORDER BY m.public_name LIMIT 1000`)
	if e != nil {
		s.error(c, e)
		return
	}
	for _, m := range v {
		revision, _ := strconv.ParseInt(fmt.Sprint(m["active_revision"]), 10, 64)
		bindings, e := s.query(c.Request.Context(), `SELECT b.channel_id,b.upstream_model,b.priority,b.weight,b.max_inflight,(SELECT ((numerator::numeric*1000000)/denominator)::text FROM binding_cost_rate_limits WHERE binding_id=b.id AND dimension='text_input_token') AS max_input_cost_rate,(SELECT ((numerator::numeric*1000000)/denominator)::text FROM binding_cost_rate_limits WHERE binding_id=b.id AND dimension='text_output_token') AS max_output_cost_rate FROM model_bindings b JOIN route_policy_versions p ON p.id=b.route_policy_version_id WHERE p.model_id=$1 AND p.revision=$2 ORDER BY b.channel_id`, m["id"], revision)
		if e != nil {
			s.error(c, e)
			return
		}
		m["bindings"] = bindings
		var mode, unknown string
		e = s.Pool.QueryRow(c.Request.Context(), `SELECT cost_mode,unknown_cost_policy FROM route_policy_versions WHERE model_id=$1 AND revision=$2`, m["id"], revision).Scan(&mode, &unknown)
		if errors.Is(e, pgx.ErrNoRows) {
			mode, unknown = "manual", "allow"
		} else if e != nil {
			s.error(c, e)
			return
		}
		m["cost_policy"] = gin.H{"mode": mode, "unknown_cost_policy": unknown}
	}
	s.ok(c, v)
}
func (s *Server) prices(c *gin.Context) {
	if sessionOf(c).User.PlatformRole == "admin" {
		s.page(c, `SELECT p.id,p.model_id,m.public_name AS model_name,meter_price_million(p.id,'text_input_token') AS input_rate,meter_price_million(p.id,'text_output_token') AS output_rate,p.effective_at,p.created_at FROM price_versions p JOIN models m ON m.id=p.model_id WHERE p.price_group_id IS NULL`)
		return
	}
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	s.page(c, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at) SELECT p.id,p.model_id,m.public_name AS model_name,meter_price_million(p.id,'text_input_token') AS input_rate,meter_price_million(p.id,'text_output_token') AS output_rate,p.effective_at,p.created_at FROM instant CROSS JOIN price_versions p JOIN models m ON m.id=p.model_id JOIN tenants t ON t.id=$1 JOIN memberships mb ON mb.tenant_id=t.id AND mb.user_id=$2 JOIN users u ON u.id=mb.user_id WHERE p.price_group_id IS NOT DISTINCT FROM t.price_group_id AND p.effective_at<=instant.at AND t.status='active' AND mb.status='active' AND u.status='active' AND m.enabled AND EXISTS(SELECT 1 FROM tenant_model_grants g WHERE g.tenant_id=t.id AND g.model_id=m.id) AND (mb.allow_all_models OR EXISTS(SELECT 1 FROM member_model_grants mg WHERE mg.tenant_id=t.id AND mg.user_id=$2 AND mg.model_id=m.id))`, tid, sessionOf(c).User.ID)
}
func (s *Server) audits(c *gin.Context) {
	s.page(c, `SELECT id,actor,tenant_scope,action,target,sanitized_diff,created_at FROM audit_events`)
}
