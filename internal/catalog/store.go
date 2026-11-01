package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/routing"
	"github.com/moni-ai/moni-aigate/internal/security"
)

var ErrConflict = errors.New("revision_conflict")
var ErrInvalid = errors.New("invalid_configuration")

type Store struct {
	Pool    *pgxpool.Pool
	Secrets *security.Secrets
}

func New(p *pgxpool.Pool, s *security.Secrets) *Store { return &Store{p, s} }
func audit(ctx context.Context, tx pgx.Tx, actor, action, target string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(actor,action,target) VALUES($1,$2,$3)`, actor, action, target)
	return e
}

type ModelInput struct {
	PublicName         string            `json:"public_name"`
	Protocol           string            `json:"protocol"`
	ContextLimit       int64             `json:"context_limit"`
	OutputLimit        int64             `json:"output_limit"`
	DefaultOutputLimit int64             `json:"default_output_limit"`
	InputUpperBound    int64             `json:"input_token_upper_bound"`
	SupportsTools      bool              `json:"supports_tools"`
	Profile            *provider.Profile `json:"profile"`
}

func (s *Store) CreateModel(ctx context.Context, actor string, in ModelInput) (string, error) {
	if len(in.PublicName) < 1 || len(in.PublicName) > 100 || strings.ContainsAny(in.PublicName, " \r\n\t") {
		return "", ErrInvalid
	}
	p := provider.Profile{Version: uuid.NewString(), Model: in.PublicName, Protocol: in.Protocol, ContextLimit: in.ContextLimit, InputTokenUpperBound: in.InputUpperBound, OutputLimit: in.OutputLimit, DefaultOutputLimit: in.DefaultOutputLimit, SupportsTools: in.SupportsTools}
	if in.Profile != nil {
		p = *in.Profile
		p.Version = uuid.NewString()
		p.Model = in.PublicName
		if in.Protocol != "" && p.ProtocolName() != in.Protocol {
			return "", ErrInvalid
		}
	}
	if p.InputTokenUpperBound == 0 {
		p.InputTokenUpperBound = p.ContextLimit
	}
	if p.DefaultOutputLimit == 0 {
		p.DefaultOutputLimit = p.OutputLimit
	}
	if p.OutputLimitField == "" && !p.InputOnly() {
		p.OutputLimitField = "max_tokens"
		if p.ProtocolName() == provider.ProtocolResponses {
			p.OutputLimitField = "max_output_tokens"
		}
		if p.ProtocolName() == provider.ProtocolGemini {
			p.OutputLimitField = "maxOutputTokens"
		}
	}
	if p.ProtocolName() == provider.ProtocolGemini && p.UsageSemantics == "" {
		p.UsageSemantics = "cumulative"
	}
	if p.ContextLimit <= 0 || p.ContextLimit > 10_000_000 {
		return "", ErrInvalid
	}
	if e := p.Validate(); e != nil {
		return "", ErrInvalid
	}
	body, e := json.Marshal(p)
	if e != nil {
		return "", e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	id := uuid.NewString()
	if _, e = tx.Exec(ctx, "INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,$2,$3,$4)", id, in.PublicName, body, p.Version); e != nil {
		return "", e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO model_capability_versions(id,model_id,profile) VALUES($1,$2,$3)", p.Version, id, body); e != nil {
		return "", e
	}
	if e = audit(ctx, tx, actor, "model.create", id); e != nil {
		return "", e
	}
	return id, tx.Commit(ctx)
}
func (s *Store) SetModelEnabled(ctx context.Context, actor, id string, enabled bool) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, "UPDATE models SET enabled=$2 WHERE id=$1", id, enabled)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	if e = audit(ctx, tx, actor, "model.status", id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type ChannelInput struct {
	Name         string   `json:"name"`
	BaseURL      string   `json:"base_url"`
	Secret       string   `json:"secret"`
	Region       string   `json:"data_region"`
	RegionAlias  string   `json:"region"`
	Provider     string   `json:"provider"`
	AllowedCIDRs []string `json:"allowed_cidrs"`
	Status       string   `json:"status"`
	Enabled      *bool    `json:"enabled"`
}

func validURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" || u.Scheme == "http") && !strings.ContainsAny(raw, "\r\n")
}
func (s *Store) SaveChannel(ctx context.Context, actor, id string, in ChannelInput) (string, error) {
	if in.Provider != "" && in.Provider != "openai" {
		return "", ErrInvalid
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	oldURL, oldRegion, oldProvider := "", "global", "openai"
	oldVersion, oldName := "", ""
	oldCIDRs := []string{}
	enabled := true
	quarantine := false
	if id != "" {
		if e = tx.QueryRow(ctx, `SELECT c.name,c.enabled,c.quarantined,v.base_url,v.region,v.provider,v.allowed_cidrs,v.id FROM channels c JOIN channel_versions v ON v.id=c.current_version WHERE c.id=$1 FOR UPDATE OF c`, id).Scan(&oldName, &enabled, &quarantine, &oldURL, &oldRegion, &oldProvider, &oldCIDRs, &oldVersion); e != nil {
			return "", e
		}
	}
	if in.Name == "" && id != "" {
		in.Name = oldName
	}
	if in.BaseURL == "" {
		in.BaseURL = oldURL
	}
	if in.Region == "" {
		in.Region = in.RegionAlias
	}
	if in.Region == "" {
		in.Region = oldRegion
	}
	if in.AllowedCIDRs == nil {
		in.AllowedCIDRs = oldCIDRs
	}
	if in.Provider == "" {
		in.Provider = oldProvider
	}
	if len(in.Name) < 1 || len(in.Name) > 200 || len(in.Region) > 80 || !validURL(in.BaseURL) {
		return "", ErrInvalid
	}
	if in.Status != "" {
		if in.Status != "active" && in.Status != "disabled" && in.Status != "quarantined" {
			return "", ErrInvalid
		}
		enabled = in.Status == "active"
		quarantine = in.Status == "quarantined"
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if err := provider.ValidateEndpointConfig(provider.Endpoint{URL: strings.TrimRight(in.BaseURL, "/") + "/chat/completions", Network: NetworkPolicy(in.BaseURL, in.AllowedCIDRs)}); err != nil {
		return "", ErrInvalid
	}
	if in.Secret == "" && id == "" || in.Secret != "" && !validCredentialSecret(in.Secret) {
		return "", ErrInvalid
	}
	version := uuid.NewString()
	members := []CredentialPoolMember{{Position: 1, Label: "primary", Weight: 1, MaxInflight: 1000, Primary: true}}
	if id != "" {
		pool, err := credentialPoolView(ctx, tx, id, oldVersion)
		if err != nil {
			return "", err
		}
		members = pool.Members
	}
	if in.Secret != "" {
		if len(members) != 1 {
			return "", ErrInvalid
		}
		if id != "" {
			credentials, err := channelCredentialsForShare(ctx, tx, id)
			if err != nil {
				return "", err
			}
			historical, err := s.historicalSecrets(credentials)
			if err != nil {
				return "", err
			}
			for _, old := range historical {
				if in.Secret == old {
					return "", ErrInvalid
				}
			}
		}
		members[0].CredentialID = uuid.NewString()
	}
	var ciphertext string
	if in.Secret != "" {
		ciphertext, e = s.Secrets.SealFor(in.Secret, "provider:platform", members[0].CredentialID)
		if e != nil {
			return "", e
		}
	}

	if id == "" {
		id = uuid.NewString()
		_, e = tx.Exec(ctx, "INSERT INTO channels(id,name,current_version,enabled,quarantined) VALUES($1,$2,$3,$4,$5)", id, in.Name, version, enabled, quarantine)
	} else {
		_, e = tx.Exec(ctx, "UPDATE channels SET name=$2,current_version=$3,enabled=$4,quarantined=$5 WHERE id=$1", id, in.Name, version, enabled, quarantine)
	}
	if e != nil {
		return "", e
	}
	if in.Secret != "" {
		if _, e = tx.Exec(ctx, "INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,$3)", members[0].CredentialID, id, ciphertext); e != nil {
			return "", e
		}
	}
	if _, e = tx.Exec(ctx, "INSERT INTO channel_versions(id,channel_id,base_url,provider,region,allowed_cidrs,credential_id) VALUES($1,$2,$3,$4,$5,$6,$7)", version, id, strings.TrimRight(in.BaseURL, "/"), in.Provider, in.Region, in.AllowedCIDRs, members[0].CredentialID); e != nil {
		return "", e
	}
	for _, member := range members {
		if e = insertPoolMember(ctx, tx, id, version, member); e != nil {
			return "", e
		}
	}
	if e = audit(ctx, tx, actor, "channel.version", id); e != nil {
		return "", e
	}
	return id, tx.Commit(ctx)
}

type BindingInput struct {
	ChannelID         string  `json:"channel_id"`
	UpstreamModel     string  `json:"upstream_model"`
	Priority          int     `json:"priority"`
	Weight            int     `json:"weight"`
	MaxInflight       int     `json:"max_inflight"`
	MaxInputCostRate  *string `json:"max_input_cost_rate"`
	MaxOutputCostRate *string `json:"max_output_cost_rate"`
}

func (s *Store) Publish(ctx context.Context, actor, model string, expected int64, bindings []BindingInput) (int64, error) {
	return s.PublishWithCostPolicy(ctx, actor, model, expected, bindings, routing.DefaultCostPolicy())
}

func (s *Store) PublishWithCostPolicy(ctx context.Context, actor, model string, expected int64, bindings []BindingInput, costPolicy routing.CostPolicy) (int64, error) {
	if routing.ValidateCostPolicy(costPolicy) != nil {
		return 0, ErrInvalid
	}
	if len(bindings) == 0 || len(bindings) > 1000 {
		return 0, ErrInvalid
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ChannelID < bindings[j].ChannelID })
	seen := map[string]bool{}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	var revision int64
	var profileJSON []byte
	if e = tx.QueryRow(ctx, "SELECT active_revision,profile FROM models WHERE id=$1 FOR UPDATE", model).Scan(&revision, &profileJSON); e != nil {
		return 0, e
	}
	var profile provider.Profile
	if json.Unmarshal(profileJSON, &profile) != nil {
		return 0, ErrInvalid
	}
	if expected != revision {
		return 0, ErrConflict
	}
	policy := uuid.NewString()
	if _, e = tx.Exec(ctx, "INSERT INTO route_policy_versions(id,model_id,revision,cost_mode,unknown_cost_policy) VALUES($1,$2,$3,$4,$5)", policy, model, revision+1, costPolicy.Mode, costPolicy.UnknownCostPolicy); e != nil {
		return 0, e
	}
	for _, b := range bindings {
		maxInput, err := parseCostRate(b.MaxInputCostRate)
		if err != nil {
			return 0, err
		}
		maxOutput, err := parseCostRate(b.MaxOutputCostRate)
		if err != nil {
			return 0, err
		}
		key := b.ChannelID
		if seen[key] || b.Weight <= 0 || b.Weight > 1_000_000 || b.MaxInflight <= 0 || b.MaxInflight > 1000 || b.UpstreamModel == "" {
			return 0, ErrInvalid
		}
		if profile.ProtocolName() == provider.ProtocolGemini && !provider.ValidGeminiModel(b.UpstreamModel) {
			return 0, ErrInvalid
		}
		seen[key] = true
		var ver string
		var enabled, quarantined bool
		if e = tx.QueryRow(ctx, "SELECT current_version,enabled,quarantined FROM channels WHERE id=$1 FOR SHARE", b.ChannelID).Scan(&ver, &enabled, &quarantined); e != nil {
			return 0, e
		}
		if !enabled || quarantined {
			return 0, ErrInvalid
		}
		rows, err := tx.Query(ctx, `SELECT k.revoked_at FROM channel_version_credentials p JOIN channel_credentials k ON k.id=p.credential_id WHERE p.channel_version_id=$1 ORDER BY k.id FOR SHARE OF k`, ver)
		if err != nil {
			return 0, err
		}
		live := false
		for rows.Next() {
			var revoked *time.Time
			if err = rows.Scan(&revoked); err != nil {
				rows.Close()
				return 0, err
			}
			live = live || revoked == nil
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		if !live {
			return 0, ErrInvalid
		}
		var isolated bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM route_binding_isolations WHERE channel_version_id=$1 AND upstream_model=$2)", ver, b.UpstreamModel).Scan(&isolated); e != nil {
			return 0, e
		}
		if isolated {
			return 0, ErrInvalid
		}
		if _, e = tx.Exec(ctx, `INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,max_input_cost_rate,max_output_cost_rate) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, policy, b.ChannelID, ver, b.UpstreamModel, b.Priority, b.Weight, b.MaxInflight, maxInput, maxOutput); e != nil {
			return 0, e
		}
	}
	if _, e = tx.Exec(ctx, "UPDATE models SET active_revision=$2 WHERE id=$1", model, revision+1); e != nil {
		return 0, e
	}
	if e = audit(ctx, tx, actor, "route.publish", model); e != nil {
		return 0, e
	}
	return revision + 1, tx.Commit(ctx)
}
func (s *Store) PublishPrice(ctx context.Context, actor, model, input, output string, effective time.Time) (string, error) {
	in, e := strconv.ParseInt(input, 10, 64)
	if e != nil || in < 0 {
		return "", ErrInvalid
	}
	out, e := strconv.ParseInt(output, 10, 64)
	if e != nil || out < 0 {
		return "", ErrInvalid
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	var id string
	var profileJSON []byte
	if e = tx.QueryRow(ctx, "SELECT id,profile FROM models WHERE id=$1 FOR SHARE", model).Scan(&id, &profileJSON); e != nil {
		return "", e
	}
	var profile provider.Profile
	if json.Unmarshal(profileJSON, &profile) != nil || profile.Validate() != nil || profile.InputOnly() && out != 0 {
		return "", ErrInvalid
	}
	id = uuid.NewString()
	var effectiveArg any
	if !effective.IsZero() {
		effectiveArg = effective
	}
	if _, e = tx.Exec(ctx, `INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by,price_group_id,pricing_operation_id) VALUES($1,$2,$3,$4,COALESCE($5::timestamptz,clock_timestamp()),$6,NULL,NULL)`, id, model, in, out, effectiveArg, actor); e != nil {
		return "", e
	}
	if e = audit(ctx, tx, actor, "price.create", id); e != nil {
		return "", e
	}
	return id, tx.Commit(ctx)
}

type Model struct {
	ID, Name, ProfileVersion string
	Revision                 int64
	Profile                  provider.Profile
}

func (s *Store) AuthorizedModel(ctx context.Context, tenant, user, key, name string) (Model, error) {
	m := Model{}
	var raw []byte
	e := s.Pool.QueryRow(ctx, `SELECT m.id,m.public_name,m.profile_version,m.active_revision,m.profile FROM models m JOIN tenant_model_grants g ON g.model_id=m.id AND g.tenant_id=$1 JOIN memberships mb ON mb.tenant_id=$1 AND mb.user_id=$2 JOIN api_keys k ON k.id=$3 AND k.tenant_id=$1 WHERE m.public_name=$4 AND m.enabled AND (mb.allow_all_models OR EXISTS(SELECT 1 FROM member_model_grants mg WHERE mg.tenant_id=$1 AND mg.user_id=$2 AND mg.model_id=m.id)) AND (m.id=ANY(k.scopes) OR '*'=ANY(k.scopes))`, tenant, user, key, name).Scan(&m.ID, &m.Name, &m.ProfileVersion, &m.Revision, &raw)
	if e == nil {
		e = json.Unmarshal(raw, &m.Profile)
	}
	return m, e
}

type Candidate struct {
	BindingID, ChannelID, VersionID, CredentialID, UpstreamModel, URL, Region, Secret string
	ExclusionReason                                                                   string
	AllowedCIDRs                                                                      []string
	Priority, Weight, MaxInflight                                                     int
	CredentialWeight, CredentialMaxInflight                                           int
	CostPolicy                                                                        routing.CostPolicy
	CostQuote                                                                         routing.CostQuote
	MaxInputCostRate, MaxOutputCostRate                                               *int64
	ProcurementManaged                                                                bool
	ProcurementState, ProcurementAvailable                                            string
}

func (s *Store) Candidates(ctx context.Context, model string, revision int64) ([]Candidate, error) {
	all, err := s.CandidateSnapshot(ctx, model, revision)
	if err != nil {
		return nil, err
	}
	available := make([]Candidate, 0, len(all))
	for _, candidate := range all {
		if candidate.ExclusionReason == "" {
			available = append(available, candidate)
		}
	}
	return available, nil
}

// CandidateSnapshot preserves exclusion metadata for explaining an admitted
// choice. Unavailable credentials are never decrypted or made dispatchable.
func (s *Store) CandidateSnapshot(ctx context.Context, model string, revision int64) ([]Candidate, error) {
	rows, e := s.Pool.Query(ctx, `WITH quote_clock AS MATERIALIZED (SELECT clock_timestamp() AS at) SELECT b.id,b.channel_id,b.channel_version_id,cp.credential_id,b.upstream_model,v.base_url,v.region,v.allowed_cidrs,b.priority,b.weight,b.max_inflight,cp.weight,cp.max_inflight,k.secret_ciphertext,CASE WHEN NOT c.enabled THEN 'disabled' WHEN c.quarantined THEN 'channel_quarantined' WHEN k.revoked_at IS NOT NULL THEN 'credential_revoked' WHEN EXISTS(SELECT 1 FROM route_binding_isolations i WHERE i.channel_version_id=b.channel_version_id AND i.upstream_model=b.upstream_model) THEN 'binding_isolated' ELSE '' END,p.cost_mode,p.unknown_cost_policy,b.max_input_cost_rate,b.max_output_cost_rate,coalesce(q.id,''),q.input_rate,q.output_rate,coalesce(q.basis,'unknown'),(pb.id IS NOT NULL),CASE WHEN EXISTS(SELECT 1 FROM procurement_reservations pr WHERE pr.account_id=pa.id AND pr.anomaly AND pr.state IN ('unknown','disputed')) THEN 'paused' ELSE coalesce(pa.state,'') END,coalesce((pa.limit_amount::numeric-pa.spent_amount-pa.held_amount)::text,'') FROM route_policy_versions p JOIN model_bindings b ON b.route_policy_version_id=p.id JOIN channels c ON c.id=b.channel_id JOIN channel_versions v ON v.id=b.channel_version_id JOIN channel_version_credentials cp ON cp.channel_version_id=v.id JOIN channel_credentials k ON k.id=cp.credential_id LEFT JOIN credential_procurement_bindings pb ON pb.credential_id=k.id LEFT JOIN procurement_accounts pa ON pa.id=pb.account_id CROSS JOIN quote_clock clock LEFT JOIN LATERAL (SELECT cv.id,cv.input_rate,cv.output_rate,cv.basis FROM cost_versions cv WHERE cv.channel_id=b.channel_id AND cv.upstream_model=b.upstream_model AND cv.effective_at<=clock.at ORDER BY cv.effective_at DESC,cv.created_at DESC,cv.id DESC LIMIT 1) q ON true WHERE p.model_id=$1 AND p.revision=$2 ORDER BY b.channel_id,b.id,cp.position`, model, revision)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	all := []Candidate{}
	for rows.Next() {
		v := Candidate{}
		var ciphertext string
		if e = rows.Scan(&v.BindingID, &v.ChannelID, &v.VersionID, &v.CredentialID, &v.UpstreamModel, &v.URL, &v.Region, &v.AllowedCIDRs, &v.Priority, &v.Weight, &v.MaxInflight, &v.CredentialWeight, &v.CredentialMaxInflight, &ciphertext, &v.ExclusionReason, &v.CostPolicy.Mode, &v.CostPolicy.UnknownCostPolicy, &v.MaxInputCostRate, &v.MaxOutputCostRate, &v.CostQuote.VersionID, &v.CostQuote.InputRate, &v.CostQuote.OutputRate, &v.CostQuote.Basis, &v.ProcurementManaged, &v.ProcurementState, &v.ProcurementAvailable); e != nil {
			return nil, e
		}
		if v.ExclusionReason == "" {
			v.Secret, e = s.Secrets.OpenFor(ciphertext, "provider:platform", v.CredentialID)
			if e != nil {
				return nil, e
			}
		}
		all = append(all, v)
	}
	return all, rows.Err()
}

func NetworkPolicy(raw string, cidrs []string) provider.NetworkPolicy {
	p := provider.NetworkPolicy{AllowedCIDRs: cidrs}
	u, e := url.Parse(raw)
	if e == nil && len(cidrs) > 0 {
		p.AllowHTTP = u.Scheme == "http"
		if n, e := strconv.ParseUint(u.Port(), 10, 16); e == nil && n > 0 {
			p.AllowedPorts = []uint16{uint16(n)}
		}
	}
	return p
}
