package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

// discoveryBindingsSQL is one statement snapshot, with stable array ordering.
// Include paused models: pausing does not remove a published configuration.
const discoveryBindingsSQL = `SELECT COALESCE(jsonb_agg(q.binding ORDER BY q.model_id,q.upstream_model,q.channel_version_id),'[]'::jsonb) FROM (
 SELECT m.id AS model_id,b.upstream_model,b.channel_version_id,jsonb_build_object('model_id',m.id,'public_name',m.public_name,'enabled',m.enabled,'upstream_model',b.upstream_model,'revision',m.active_revision,'channel_version_id',b.channel_version_id) AS binding
 FROM models m JOIN route_policy_versions p ON p.model_id=m.id AND p.revision=m.active_revision JOIN model_bindings b ON b.route_policy_version_id=p.id WHERE b.channel_id=$1 ORDER BY m.id,b.upstream_model,b.channel_version_id LIMIT 1001
) q`

type DiscoveryBinding struct {
	ModelID          string `json:"model_id"`
	PublicName       string `json:"public_name"`
	Enabled          bool   `json:"enabled"`
	UpstreamModel    string `json:"upstream_model"`
	Revision         int64  `json:"revision"`
	ChannelVersionID string `json:"channel_version_id"`
}

type DiscoveryTarget struct {
	ID         string
	Endpoint   provider.Endpoint
	VersionID  string
	Credential string
}

type DiscoveryView struct {
	ID               string             `json:"id"`
	ChannelID        string             `json:"channel_id"`
	ChannelVersionID string             `json:"channel_version_id"`
	CredentialID     string             `json:"credential_id"`
	Format           string             `json:"format"`
	Status           string             `json:"status"`
	CreatedAt        time.Time          `json:"created_at"`
	CompletedAt      *time.Time         `json:"completed_at,omitempty"`
	ErrorCode        string             `json:"error_code,omitempty"`
	Models           []string           `json:"models"`
	Pages            int                `json:"pages"`
	Stale            bool               `json:"stale"`
	Unbound          []string           `json:"unbound"`
	Present          []DiscoveryBinding `json:"present"`
	Missing          []DiscoveryBinding `json:"missing"`
}

func ValidDiscoveryFormat(format string) bool {
	return format == "openai" || format == "anthropic" || format == "gemini"
}

func (s *Store) BeginModelDiscovery(ctx context.Context, actor, channel, expected, format string) (DiscoveryTarget, error) {
	result := DiscoveryTarget{}
	if expected == "" || !ValidDiscoveryFormat(format) {
		return result, ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var version string
	var enabled, quarantined bool
	if err = tx.QueryRow(ctx, `SELECT current_version,enabled,quarantined FROM channels WHERE id=$1 FOR SHARE`, channel).Scan(&version, &enabled, &quarantined); err != nil {
		return result, err
	}
	if version != expected || !enabled || quarantined {
		return result, ErrConflict
	}
	var credential, base, ciphertext string
	var cidrs []string
	var revoked *time.Time
	if err = tx.QueryRow(ctx, `SELECT k.id,v.base_url,v.allowed_cidrs,k.secret_ciphertext,k.revoked_at FROM channel_versions v JOIN channel_version_credentials p ON p.channel_version_id=v.id AND p.channel_id=v.channel_id JOIN channel_credentials k ON k.id=p.credential_id AND k.channel_id=p.channel_id WHERE v.id=$1 AND v.channel_id=$2 AND k.revoked_at IS NULL ORDER BY p.position LIMIT 1 FOR SHARE OF k`, version, channel).Scan(&credential, &base, &cidrs, &ciphertext, &revoked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, ErrConflict
		}
		return result, err
	}
	if revoked != nil {
		return result, ErrConflict
	}
	var bindings []byte
	if err = tx.QueryRow(ctx, discoveryBindingsSQL, channel).Scan(&bindings); err != nil {
		return result, err
	}
	var snapshot []DiscoveryBinding
	if len(bindings) > 262144 || json.Unmarshal(bindings, &snapshot) != nil || len(snapshot) > 1000 {
		return result, ErrInvalid
	}
	secret, err := s.Secrets.OpenFor(ciphertext, "provider:platform", credential)
	if err != nil {
		return result, err
	}
	id := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO channel_model_discoveries(id,actor,channel_id,channel_version_id,credential_id,format,bindings) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, actor, channel, version, credential, format, bindings); err != nil {
		return result, err
	}
	if err = audit(ctx, tx, actor, "channel.discovery.start", id); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	return DiscoveryTarget{ID: id, VersionID: version, Credential: credential, Endpoint: provider.Endpoint{URL: base, APIKey: secret, Network: NetworkPolicy(base, cidrs)}}, nil
}

// RecheckModelDiscovery preserves a fresh admission barrier without holding a
// transaction during network I/O. Rotation after this point cannot unsend GET.
func (s *Store) RecheckModelDiscovery(ctx context.Context, id string) error {
	var valid bool
	err := s.Pool.QueryRow(ctx, `SELECT c.enabled AND NOT c.quarantined AND c.current_version=d.channel_version_id AND k.revoked_at IS NULL FROM channel_model_discoveries d JOIN channels c ON c.id=d.channel_id JOIN channel_version_credentials p ON p.channel_version_id=d.channel_version_id AND p.channel_id=d.channel_id AND p.credential_id=d.credential_id JOIN channel_credentials k ON k.id=p.credential_id WHERE d.id=$1`, id).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	return nil
}

var discoveryErrorCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,79}$`)

func (s *Store) FinishModelDiscovery(ctx context.Context, id string, result provider.DiscoveryResult, code string) error {
	status := "completed"
	models := append([]string{}, result.Models...)
	pages := result.Pages
	if code != "" {
		if !discoveryErrorCode.MatchString(code) {
			return ErrInvalid
		}
		status, models, pages = "failed", []string{}, 0
	} else {
		if len(models) > 1000 || pages < 1 || pages > 10 {
			return ErrInvalid
		}
		for _, model := range models {
			if !provider.ValidDiscoveredModelID(model) {
				return ErrInvalid
			}
		}
		sort.Strings(models)
		for i := 1; i < len(models); i++ {
			if models[i] == models[i-1] {
				return ErrInvalid
			}
		}
	}
	raw, err := json.Marshal(models)
	if err != nil || len(raw) > 262144 {
		return ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var actor, format string
	if err = tx.QueryRow(ctx, `SELECT actor,format FROM channel_model_discoveries WHERE id=$1`, id).Scan(&actor, &format); err != nil {
		return err
	}
	if format == "gemini" {
		for _, model := range models {
			if !provider.ValidGeminiModel(model) {
				return ErrInvalid
			}
		}
	}
	var inserted string
	err = tx.QueryRow(ctx, `INSERT INTO channel_model_discovery_results(discovery_id,status,model_ids,pages,error_code) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING discovery_id`, id, status, raw, pages, code).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		var same bool
		if err = tx.QueryRow(ctx, `SELECT status=$2 AND model_ids=$3::jsonb AND pages=$4 AND error_code=$5 FROM channel_model_discovery_results WHERE discovery_id=$1`, id, status, raw, pages, code).Scan(&same); err != nil {
			return err
		}
		if !same {
			return ErrConflict
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "channel.discovery."+status, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Read metadata and compare live configuration in the same statement snapshot.
func discoverySelect(channelArg string) string {
	return `SELECT d.id,d.channel_id,d.channel_version_id,d.credential_id,d.format,COALESCE(r.status,CASE WHEN d.created_at<clock_timestamp()-interval '30 seconds' THEN 'interrupted' ELSE 'running' END) AS status,d.created_at,r.completed_at,COALESCE(r.error_code,'') AS error_code,COALESCE(r.pages,0) AS pages,
 (c.current_version<>d.channel_version_id OR NOT c.enabled OR c.quarantined OR k.revoked_at IS NOT NULL OR d.bindings<>(` + strings.ReplaceAll(discoveryBindingsSQL, "$1", channelArg) + `)) AS stale`
}

const discoveryJoins = ` FROM channel_model_discoveries d JOIN channels c ON c.id=d.channel_id JOIN channel_credentials k ON k.id=d.credential_id LEFT JOIN channel_model_discovery_results r ON r.discovery_id=d.id `

// DiscoveryListSQL is consumed only by the existing bounded platform paginator.
func DiscoveryListSQL() string {
	return discoverySelect("d.channel_id") + discoveryJoins + ` WHERE d.channel_id=$1`
}

func (s *Store) ModelDiscovery(ctx context.Context, channel, id string) (DiscoveryView, error) {
	v := DiscoveryView{Models: []string{}, Unbound: []string{}, Present: []DiscoveryBinding{}, Missing: []DiscoveryBinding{}}
	var bindings, models []byte
	err := s.Pool.QueryRow(ctx, discoverySelect("d.channel_id")+`,d.bindings,COALESCE(r.model_ids,'[]'::jsonb)`+discoveryJoins+` WHERE d.channel_id=$1 AND d.id=$2`, channel, id).Scan(&v.ID, &v.ChannelID, &v.ChannelVersionID, &v.CredentialID, &v.Format, &v.Status, &v.CreatedAt, &v.CompletedAt, &v.ErrorCode, &v.Pages, &v.Stale, &bindings, &models)
	if err != nil {
		return v, err
	}
	var bound []DiscoveryBinding
	if json.Unmarshal(bindings, &bound) != nil || json.Unmarshal(models, &v.Models) != nil {
		return v, ErrInvalid
	}
	if v.Status != "completed" {
		return v, nil
	}
	present := map[string]bool{}
	for _, model := range v.Models {
		present[model] = true
	}
	configured := map[string]bool{}
	for _, b := range bound {
		configured[b.UpstreamModel] = true
		if present[b.UpstreamModel] {
			v.Present = append(v.Present, b)
		} else {
			v.Missing = append(v.Missing, b)
		}
	}
	for _, model := range v.Models {
		if !configured[model] {
			v.Unbound = append(v.Unbound, model)
		}
	}
	return v, nil
}
