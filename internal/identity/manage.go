package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func validRole(role string) bool {
	return role == "owner" || role == "member" || role == "billing_viewer"
}
func (s *Store) CreateUser(ctx context.Context, actor, email, password, role string) (string, error) {
	if role != "none" && role != "admin" && role != "finance_operator" && role != "finance_approver" {
		return "", ErrForbidden
	}
	hash, e := security.HashPassword(password)
	if e != nil {
		return "", e
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") || len(email) > 254 {
		return "", errors.New("invalid email")
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	id := uuid.NewString()
	if _, e = tx.Exec(ctx, "INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,$3,$4)", id, email, hash, role); e != nil {
		return "", e
	}
	if e = audit(ctx, tx, actor, "", "user.create", id); e != nil {
		return "", e
	}
	return id, tx.Commit(ctx)
}
func (s *Store) CreateTenant(ctx context.Context, actor, name, email, password string, models []string) (string, error) {
	if len(name) < 1 || len(name) > 200 {
		return "", errors.New("invalid name")
	}
	hash, e := security.HashPassword(password)
	if e != nil {
		return "", e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	tid, uid := uuid.NewString(), uuid.NewString()
	if _, e = tx.Exec(ctx, "INSERT INTO tenants(id,name) VALUES($1,$2)", tid, name); e != nil {
		return "", e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO users(id,email,password_hash) VALUES($1,$2,$3)", uid, strings.ToLower(strings.TrimSpace(email)), hash); e != nil {
		return "", e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", tid, uid); e != nil {
		return "", e
	}
	for _, m := range models {
		if _, e = tx.Exec(ctx, "INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", tid, m); e != nil {
			return "", e
		}
	}
	if e = audit(ctx, tx, actor, tid, "tenant.create", tid); e != nil {
		return "", e
	}
	return tid, tx.Commit(ctx)
}
func (s *Store) UpdateTenant(ctx context.Context, actor, tid string, status *string, models *[]string, regions *[]string) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var id string
	if e = tx.QueryRow(ctx, "SELECT id FROM tenants WHERE id=$1 FOR UPDATE", tid).Scan(&id); e != nil {
		return e
	}
	if status != nil {
		if *status != "active" && *status != "suspended" {
			return errors.New("invalid status")
		}
		if _, e = tx.Exec(ctx, "UPDATE tenants SET status=$2 WHERE id=$1", tid, *status); e != nil {
			return e
		}
	}
	if models != nil {
		if _, e = tx.Exec(ctx, "DELETE FROM tenant_model_grants WHERE tenant_id=$1", tid); e != nil {
			return e
		}
		for _, m := range *models {
			if _, e = tx.Exec(ctx, "INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", tid, m); e != nil {
				return e
			}
		}
	}
	if regions != nil {
		if _, e = tx.Exec(ctx, "UPDATE tenants SET allowed_regions=$2 WHERE id=$1", tid, *regions); e != nil {
			return e
		}
	}
	if e = audit(ctx, tx, actor, tid, "tenant.update", tid); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) AddMember(ctx context.Context, actor, tid, email, password, role string, all bool, models []string, ownerOnly ...bool) (string, error) {
	if !validRole(role) {
		return "", errors.New("invalid role")
	}
	hash := ""
	var e error
	if password != "" {
		hash, e = security.HashPassword(password)
		if e != nil {
			return "", e
		}
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	var id string
	if e = tx.QueryRow(ctx, "SELECT id FROM tenants WHERE id=$1 FOR SHARE", tid).Scan(&id); e != nil {
		return "", e
	}
	if len(ownerOnly) > 0 && ownerOnly[0] {
		if e = s.delegate(ctx, tx, actor, tid, role, all, models); e != nil {
			return "", e
		}
	}
	uid := ""
	email = strings.ToLower(strings.TrimSpace(email))
	e = tx.QueryRow(ctx, "SELECT id FROM users WHERE email=$1", email).Scan(&uid)
	if errors.Is(e, pgx.ErrNoRows) {
		if hash == "" {
			return "", errors.New("password required for new member")
		}
		uid = uuid.NewString()
		_, e = tx.Exec(ctx, "INSERT INTO users(id,email,password_hash) VALUES($1,$2,$3)", uid, email, hash)
	}
	if e != nil {
		return "", e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,$3,$4)", tid, uid, role, all); e != nil {
		return "", e
	}
	for _, m := range models {
		if _, e = tx.Exec(ctx, "INSERT INTO member_model_grants(tenant_id,user_id,model_id) VALUES($1,$2,$3)", tid, uid, m); e != nil {
			return "", e
		}
	}
	if e = audit(ctx, tx, actor, tid, "member.create", uid); e != nil {
		return "", e
	}
	return uid, tx.Commit(ctx)
}
func (s *Store) UpdateMember(ctx context.Context, actor, tid, uid string, role, status *string, all *bool, models *[]string, ownerOnly ...bool) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var id string
	if e = tx.QueryRow(ctx, "SELECT id FROM tenants WHERE id=$1 FOR SHARE", tid).Scan(&id); e != nil {
		return e
	}
	rows, err := tx.Query(ctx, "SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=ANY($2::text[]) ORDER BY user_id FOR UPDATE", tid, []string{actor, uid})
	if err != nil {
		return err
	}
	for rows.Next() {
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if e = tx.QueryRow(ctx, "SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2", tid, uid).Scan(&id); e != nil {
		return e
	}
	if len(ownerOnly) > 0 && ownerOnly[0] {
		if uid == actor {
			return ErrForbidden
		}
		var currentRole string
		if e = tx.QueryRow(ctx, "SELECT role FROM memberships WHERE tenant_id=$1 AND user_id=$2", tid, uid).Scan(&currentRole); e != nil {
			return e
		}
		if currentRole == "owner" {
			return ErrForbidden
		}
		newRole := currentRole
		if role != nil {
			newRole = *role
		}
		var newAll bool
		if e = tx.QueryRow(ctx, "SELECT allow_all_models FROM memberships WHERE tenant_id=$1 AND user_id=$2", tid, uid).Scan(&newAll); e != nil {
			return e
		}
		if all != nil {
			newAll = *all
		}
		newModels := []string{}
		rows, e := tx.Query(ctx, "SELECT model_id FROM member_model_grants WHERE tenant_id=$1 AND user_id=$2 ORDER BY model_id", tid, uid)
		if e != nil {
			return e
		}
		for rows.Next() {
			var m string
			if e = rows.Scan(&m); e != nil {
				rows.Close()
				return e
			}
			newModels = append(newModels, m)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		if models != nil {
			newModels = *models
		}
		if e = s.delegate(ctx, tx, actor, tid, newRole, newAll, newModels); e != nil {
			return e
		}
	}
	if role != nil {
		if !validRole(*role) {
			return errors.New("invalid role")
		}
		if _, e = tx.Exec(ctx, "UPDATE memberships SET role=$3 WHERE tenant_id=$1 AND user_id=$2", tid, uid, *role); e != nil {
			return e
		}
	}
	if status != nil {
		if *status != "active" && *status != "suspended" {
			return errors.New("invalid status")
		}
		if _, e = tx.Exec(ctx, "UPDATE memberships SET status=$3 WHERE tenant_id=$1 AND user_id=$2", tid, uid, *status); e != nil {
			return e
		}
	}
	if all != nil {
		if _, e = tx.Exec(ctx, "UPDATE memberships SET allow_all_models=$3 WHERE tenant_id=$1 AND user_id=$2", tid, uid, *all); e != nil {
			return e
		}
	}
	if models != nil {
		if _, e = tx.Exec(ctx, "DELETE FROM member_model_grants WHERE tenant_id=$1 AND user_id=$2", tid, uid); e != nil {
			return e
		}
		for _, m := range *models {
			if _, e = tx.Exec(ctx, "INSERT INTO member_model_grants(tenant_id,user_id,model_id) VALUES($1,$2,$3)", tid, uid, m); e != nil {
				return e
			}
		}
	}
	if e = audit(ctx, tx, actor, tid, "member.update", uid); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type KeyInput struct {
	Name               string     `json:"name"`
	Scopes             []string   `json:"scopes"`
	Limit              *string    `json:"lifetime_limit"`
	ExpiresAt          *time.Time `json:"expires_at"`
	AllowedRegions     []string   `json:"allowed_regions"`
	AllowedClientCIDRs []string   `json:"allowed_client_cidrs"`
	RPMLimit           *int       `json:"rpm_limit,omitempty"`
	BurstLimit         *int       `json:"burst_limit,omitempty"`
	ConcurrencyLimit   *int       `json:"concurrency_limit,omitempty"`
}

// Omitted creation limits select defaults, but explicit null is not an integer.
// Canonical names, duplicates and the outer request size remain enforced by the
// HTTP decoder; the presence check keeps null distinct from omission here too.
func (in *KeyInput) UnmarshalJSON(data []byte) error {
	type plainKeyInput KeyInput
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var expires *time.Time
	if raw, present := fields["expires_at"]; present {
		var err error
		expires, err = parseStrictKeyExpiryJSON(raw)
		if err != nil {
			return err
		}
	}
	var value plainKeyInput
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	for _, name := range []string{"rpm_limit", "burst_limit", "concurrency_limit"} {
		if raw, present := fields[name]; present && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return ErrKeyLimitsInvalid
		}
	}
	value.ExpiresAt = expires
	*in = KeyInput(value)
	return nil
}

func (s *Store) CreateKey(ctx context.Context, v *Session, in KeyInput) (map[string]any, error) {
	if v == nil || v.Tenant == nil || (v.Tenant.Role != "owner" && v.Tenant.Role != "member") {
		return nil, ErrForbidden
	}
	if len(in.Name) < 1 || len(in.Name) > 100 || len(in.Scopes) == 0 || len(in.Scopes) > 1000 {
		return nil, ErrKeyConfigurationInvalid
	}
	limit, e := parseKeyConfigurationLimit(in.Limit)
	if e != nil {
		return nil, e
	}
	in.ExpiresAt = keyConfigurationTime(in.ExpiresAt)
	if !serializableKeyConfigurationTime(in.ExpiresAt) || (in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now())) {
		return nil, ErrKeyConfigurationInvalid
	}
	if len(in.AllowedRegions) == 0 {
		in.AllowedRegions = []string{"global"}
	}
	clientCIDRs, e := security.NormalizeClientCIDRs(in.AllowedClientCIDRs)
	if e != nil {
		return nil, ErrKeyPolicyInvalid
	}
	runtimeLimits, e := keyRuntimeLimitValues(in.RPMLimit, in.BurstLimit, in.ConcurrencyLimit, true)
	if e != nil {
		return nil, e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	var id, status, role string
	if e = tx.QueryRow(ctx, "SELECT id FROM tenants WHERE id=$1 AND status='active' FOR SHARE", v.Tenant.ID).Scan(&id); e != nil {
		return nil, e
	}
	if e = tx.QueryRow(ctx, "SELECT status,role FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR SHARE", v.Tenant.ID, v.User.ID).Scan(&status, &role); e != nil {
		return nil, e
	}
	if status != "active" || (role != "owner" && role != "member") {
		return nil, ErrForbidden
	}
	raw := "mg_" + security.Random(32)
	id = uuid.NewString()
	prefix := raw[:11]
	if _, e = tx.Exec(ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit,expires_at,allowed_regions,allowed_client_cidrs,rpm_limit,burst_limit,concurrency_limit) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, id, v.Tenant.ID, v.User.ID, in.Name, prefix, s.Secrets.Digest(raw), in.Scopes, limit, in.ExpiresAt, in.AllowedRegions, clientCIDRs, runtimeLimits.RPMLimit, runtimeLimits.BurstLimit, runtimeLimits.ConcurrencyLimit); e != nil {
		return nil, e
	}
	if e = audit(ctx, tx, v.User.ID, v.Tenant.ID, "key.create", id); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return map[string]any{"id": id, "name": in.Name, "prefix": prefix, "key": raw, "scopes": in.Scopes, "allowed_regions": in.AllowedRegions, "lifetime_limit": in.Limit, "spent": "0", "held": "0", "expires_at": in.ExpiresAt, "configuration_revision": "1", "allowed_client_cidrs": clientCIDRs, "access_policy_revision": "1", "rpm_limit": runtimeLimits.RPMLimit, "burst_limit": runtimeLimits.BurstLimit, "concurrency_limit": runtimeLimits.ConcurrencyLimit, "limit_revision": "1"}, nil
}
func (s *Store) RevokeKey(ctx context.Context, v *Session, id string) error {
	if v.Tenant == nil {
		return ErrForbidden
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var t string
	if e = tx.QueryRow(ctx, "SELECT id FROM tenants WHERE id=$1 FOR SHARE", v.Tenant.ID).Scan(&t); e != nil {
		return e
	}
	var role string
	if e = tx.QueryRow(ctx, "SELECT role FROM memberships WHERE tenant_id=$1 AND user_id=$2 AND status='active' FOR SHARE", v.Tenant.ID, v.User.ID).Scan(&role); e != nil {
		return e
	}
	var owner string
	if e = tx.QueryRow(ctx, "SELECT owner_id FROM api_keys WHERE tenant_id=$1 AND id=$2 FOR UPDATE", v.Tenant.ID, id).Scan(&owner); e != nil {
		return e
	}
	if role != "owner" && owner != v.User.ID {
		return ErrForbidden
	}
	if _, e = tx.Exec(ctx, "UPDATE api_keys SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1", id); e != nil {
		return e
	}
	if e = audit(ctx, tx, v.User.ID, v.Tenant.ID, "key.revoke", id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Store) delegate(ctx context.Context, tx pgx.Tx, actor, tenant, role string, all bool, models []string) error {
	if role != "member" && role != "billing_viewer" {
		return ErrForbidden
	}
	var actorRole, status string
	var actorAll bool
	if e := tx.QueryRow(ctx, "SELECT role,status,allow_all_models FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR SHARE", tenant, actor).Scan(&actorRole, &status, &actorAll); e != nil {
		return e
	}
	if actorRole != "owner" || status != "active" || (all && !actorAll) {
		return ErrForbidden
	}
	for _, model := range models {
		var ok bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenant_model_grants g WHERE g.tenant_id=$1 AND g.model_id=$2 AND ($3 OR EXISTS(SELECT 1 FROM member_model_grants mg WHERE mg.tenant_id=$1 AND mg.user_id=$4 AND mg.model_id=$2)))`, tenant, model, actorAll, actor).Scan(&ok); e != nil {
			return e
		}
		if !ok {
			return ErrForbidden
		}
	}
	return nil
}
