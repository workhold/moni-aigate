package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var (
	ErrKeyConfigurationInvalid = errors.New("invalid_key_configuration")
	ErrKeyBudgetBelowUsage     = errors.New("key_budget_below_usage")
	keyExpiryRFC3339           = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](?:\.[0-9]{1,9})?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)
)

type KeyConfigurationInput struct {
	ExpectedRevision string     `json:"expected_revision"`
	Name             string     `json:"name"`
	Scopes           []string   `json:"scopes"`
	AllowedRegions   []string   `json:"allowed_regions"`
	LifetimeLimit    *string    `json:"lifetime_limit"`
	ExpiresAt        *time.Time `json:"expires_at"`
}

// A full replacement must explicitly include the two nullable fields. The
// HTTP decoder additionally bounds JSON and rejects duplicates and aliases.
func (in *KeyConfigurationInput) UnmarshalJSON(data []byte) error {
	type plain KeyConfigurationInput
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return ErrKeyConfigurationInvalid
	}
	for _, name := range []string{"expected_revision", "name", "scopes", "allowed_regions", "lifetime_limit", "expires_at"} {
		raw, ok := fields[name]
		if !ok || (name != "lifetime_limit" && name != "expires_at" && bytes.Equal(bytes.TrimSpace(raw), []byte("null"))) {
			return ErrKeyConfigurationInvalid
		}
	}
	if len(fields) != 6 {
		return ErrKeyConfigurationInvalid
	}
	// encoding/json otherwise accepts a null array element as the empty string.
	// Empty historical strings may round-trip, but null is never a string.
	for _, name := range []string{"scopes", "allowed_regions"} {
		var elements []json.RawMessage
		if err := json.Unmarshal(fields[name], &elements); err != nil {
			return ErrKeyConfigurationInvalid
		}
		for _, raw := range elements {
			trimmed := bytes.TrimSpace(raw)
			if len(trimmed) == 0 || trimmed[0] != '"' {
				return ErrKeyConfigurationInvalid
			}
		}
	}
	expires, err := parseStrictKeyExpiryJSON(fields["expires_at"])
	if err != nil {
		return err
	}
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return ErrKeyConfigurationInvalid
	}
	value.ExpiresAt = expires
	*in = KeyConfigurationInput(value)
	return nil
}

// Go's time parser accepts some non-RFC3339 forms, including comma fractions
// and out-of-range zone offsets. Validate the console wire format first, then
// let time.Parse validate the calendar date and preserve nanosecond precision.
func parseStrictKeyExpiryJSON(raw json.RawMessage) (*time.Time, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil || !keyExpiryRFC3339.MatchString(text) {
		return nil, ErrKeyConfigurationInvalid
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return nil, ErrKeyConfigurationInvalid
	}
	return &parsed, nil
}

type KeyConfigurationView struct {
	ID                    string     `json:"id"`
	Name                  string     `json:"name"`
	Scopes                []string   `json:"scopes"`
	AllowedRegions        []string   `json:"allowed_regions"`
	LifetimeLimit         *string    `json:"lifetime_limit"`
	ExpiresAt             *time.Time `json:"expires_at"`
	ConfigurationRevision string     `json:"configuration_revision"`
	Spent                 string     `json:"spent"`
	Held                  string     `json:"held"`
}

func (s *Store) KeyConfiguration(ctx context.Context, actor *Session, id string) (KeyConfigurationView, error) {
	var result KeyConfigurationView
	if actor == nil || actor.Tenant == nil {
		return result, ErrForbidden
	}
	if !canonicalKeyID(id) {
		return result, ErrKeyConfigurationInvalid
	}
	err := s.Pool.QueryRow(ctx, `SELECT k.id,k.name,k.scopes,k.allowed_regions,k.lifetime_limit::text,k.expires_at,k.configuration_revision::text,k.spent::text,k.held::text
 FROM api_keys k JOIN tenants t ON t.id=k.tenant_id
 JOIN memberships m ON m.tenant_id=t.id AND m.user_id=$2 JOIN users u ON u.id=m.user_id
 WHERE k.tenant_id=$1 AND k.id=$3 AND t.status='active' AND m.status='active' AND u.status='active'
 AND (m.role='owner' OR (m.role IN ('member','billing_viewer') AND k.owner_id=$2))`, actor.Tenant.ID, actor.User.ID, id).Scan(&result.ID, &result.Name, &result.Scopes, &result.AllowedRegions, &result.LifetimeLimit, &result.ExpiresAt, &result.ConfigurationRevision, &result.Spent, &result.Held)
	result.ExpiresAt = keyConfigurationTime(result.ExpiresAt)
	return result, keyPolicyPermissionError(err)
}

func (s *Store) UpdateKeyConfiguration(ctx context.Context, actor *Session, id string, in KeyConfigurationInput) (KeyConfigurationView, error) {
	var result KeyConfigurationView
	if actor == nil || actor.Tenant == nil {
		return result, ErrForbidden
	}
	if !canonicalKeyID(id) {
		return result, ErrKeyConfigurationInvalid
	}
	expected, err := strconv.ParseInt(in.ExpectedRevision, 10, 64)
	if err != nil || expected <= 0 || strconv.FormatInt(expected, 10) != in.ExpectedRevision {
		return result, ErrKeyConfigurationInvalid
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, err
	}
	defer rollbackKeyLimits(tx)
	var locked, role string
	if err = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id=$1 AND status='active' FOR SHARE`, actor.Tenant.ID).Scan(&locked); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	if err = tx.QueryRow(ctx, `SELECT role FROM memberships WHERE tenant_id=$1 AND user_id=$2 AND status='active' FOR SHARE`, actor.Tenant.ID, actor.User.ID).Scan(&role); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	if role != "owner" && role != "member" {
		return result, ErrForbidden
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' FOR SHARE`, actor.User.ID).Scan(&locked); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	var before KeyConfigurationView
	var owner string
	var revoked bool
	if err = tx.QueryRow(ctx, `SELECT id,owner_id,name,scopes,allowed_regions,lifetime_limit::text,expires_at,configuration_revision::text,spent::text,held::text,revoked_at IS NOT NULL FROM api_keys WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, actor.Tenant.ID, id).Scan(&before.ID, &owner, &before.Name, &before.Scopes, &before.AllowedRegions, &before.LifetimeLimit, &before.ExpiresAt, &before.ConfigurationRevision, &before.Spent, &before.Held, &revoked); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	if revoked || (role != "owner" && owner != actor.User.ID) {
		return result, ErrForbidden
	}
	if before.ConfigurationRevision != in.ExpectedRevision {
		return result, ErrConflict
	}
	before.ExpiresAt = keyConfigurationTime(before.ExpiresAt)
	desired, err := normalizeKeyConfiguration(before, in)
	if err != nil {
		return result, err
	}
	if !equalKeyConfigurationTime(before.ExpiresAt, desired.ExpiresAt) && desired.ExpiresAt != nil {
		var now time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return result, err
		}
		if !desired.ExpiresAt.After(now) {
			return result, ErrKeyConfigurationInvalid
		}
	}
	if desired.LifetimeLimit != nil && keyBudgetBelowUsage(*desired.LifetimeLimit, before.Spent, before.Held) {
		return result, ErrKeyBudgetBelowUsage
	}
	if sameKeyConfiguration(before, desired) {
		return before, tx.Commit(ctx)
	}
	if err = tx.QueryRow(ctx, `UPDATE api_keys SET name=$3,scopes=$4,allowed_regions=$5,lifetime_limit=$6,expires_at=$7 WHERE tenant_id=$1 AND id=$2 RETURNING id,name,scopes,allowed_regions,lifetime_limit::text,expires_at,configuration_revision::text,spent::text,held::text`, actor.Tenant.ID, id, desired.Name, desired.Scopes, desired.AllowedRegions, desired.LifetimeLimit, desired.ExpiresAt).Scan(&result.ID, &result.Name, &result.Scopes, &result.AllowedRegions, &result.LifetimeLimit, &result.ExpiresAt, &result.ConfigurationRevision, &result.Spent, &result.Held); err != nil {
		return KeyConfigurationView{}, err
	}
	result.ExpiresAt = keyConfigurationTime(result.ExpiresAt)
	diff := map[string]any{"before": keyConfigurationAudit(before), "after": keyConfigurationAudit(result)}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff) VALUES($1,$2,'key.configuration.update',$3,$4)`, actor.User.ID, actor.Tenant.ID, id, diff); err != nil {
		return KeyConfigurationView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return KeyConfigurationView{}, err
	}
	return result, nil
}

func normalizeKeyConfiguration(before KeyConfigurationView, in KeyConfigurationInput) (KeyConfigurationView, error) {
	desired := before
	desired.Name = in.Name
	if in.Name != before.Name {
		desired.Name = strings.TrimSpace(in.Name)
		if !validKeyConfigurationLabel(desired.Name, 100) {
			return KeyConfigurationView{}, ErrKeyConfigurationInvalid
		}
	}
	if !slices.Equal(in.Scopes, before.Scopes) {
		if len(in.Scopes) < 1 || len(in.Scopes) > 1000 {
			return KeyConfigurationView{}, ErrKeyConfigurationInvalid
		}
		desired.Scopes = slices.Clone(in.Scopes)
		for _, scope := range desired.Scopes {
			if scope == "*" && len(desired.Scopes) == 1 {
				continue
			}
			if !canonicalKeyID(scope) {
				return KeyConfigurationView{}, ErrKeyConfigurationInvalid
			}
		}
		slices.Sort(desired.Scopes)
		desired.Scopes = slices.Compact(desired.Scopes)
	}
	if !slices.Equal(in.AllowedRegions, before.AllowedRegions) {
		if len(in.AllowedRegions) < 1 || len(in.AllowedRegions) > 32 {
			return KeyConfigurationView{}, ErrKeyConfigurationInvalid
		}
		desired.AllowedRegions = slices.Clone(in.AllowedRegions)
		for i, region := range desired.AllowedRegions {
			region = strings.TrimSpace(region)
			if !validKeyConfigurationLabel(region, 80) || (region == "*" && len(desired.AllowedRegions) != 1) {
				return KeyConfigurationView{}, ErrKeyConfigurationInvalid
			}
			desired.AllowedRegions[i] = region
		}
		slices.Sort(desired.AllowedRegions)
		desired.AllowedRegions = slices.Compact(desired.AllowedRegions)
	}
	if _, err := parseKeyConfigurationLimit(in.LifetimeLimit); err != nil {
		return KeyConfigurationView{}, err
	}
	desired.LifetimeLimit = in.LifetimeLimit
	desired.ExpiresAt = keyConfigurationTime(in.ExpiresAt)
	if !equalKeyConfigurationTime(before.ExpiresAt, desired.ExpiresAt) && !serializableKeyConfigurationTime(desired.ExpiresAt) {
		return KeyConfigurationView{}, ErrKeyConfigurationInvalid
	}
	return desired, nil
}

func validKeyConfigurationLabel(label string, maxBytes int) bool {
	return len(label) > 0 && len(label) <= maxBytes && utf8.ValidString(label) && !strings.ContainsFunc(label, unicode.IsControl)
}

func parseKeyConfigurationLimit(value *string) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	n, err := strconv.ParseInt(*value, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != *value {
		return nil, ErrKeyConfigurationInvalid
	}
	return &n, nil
}

func keyBudgetBelowUsage(limit, spent, held string) bool {
	cap, capOK := new(big.Int).SetString(limit, 10)
	used, usedOK := new(big.Int).SetString(spent, 10)
	reserved, heldOK := new(big.Int).SetString(held, 10)
	return !capOK || !usedOK || !heldOK || cap.Cmp(used.Add(used, reserved)) < 0
}

func keyConfigurationTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC().Truncate(time.Microsecond)
	return &normalized
}

func serializableKeyConfigurationTime(value *time.Time) bool {
	// A valid RFC3339 input may cross its year range when converted to UTC.
	// Reject new values that cannot be encoded in subsequent JSON responses.
	return value == nil || (value.Year() >= 0 && value.Year() <= 9999)
}

func equalKeyConfigurationTime(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}

func sameKeyConfiguration(a, b KeyConfigurationView) bool {
	return a.Name == b.Name && slices.Equal(a.Scopes, b.Scopes) && slices.Equal(a.AllowedRegions, b.AllowedRegions) && ((a.LifetimeLimit == nil && b.LifetimeLimit == nil) || (a.LifetimeLimit != nil && b.LifetimeLimit != nil && *a.LifetimeLimit == *b.LifetimeLimit)) && equalKeyConfigurationTime(a.ExpiresAt, b.ExpiresAt)
}

func keyConfigurationAudit(v KeyConfigurationView) map[string]any {
	return map[string]any{"name": v.Name, "scopes": v.Scopes, "allowed_regions": v.AllowedRegions, "lifetime_limit": v.LifetimeLimit, "expires_at": v.ExpiresAt, "configuration_revision": v.ConfigurationRevision}
}
