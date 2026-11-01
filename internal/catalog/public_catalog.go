package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

var (
	ErrPublicCatalogInvalid     = errors.New("invalid_public_catalog_configuration")
	ErrPublicCatalogUnavailable = errors.New("public_catalog_unavailable")
	ErrPublicCatalogConflict    = errors.New("public_catalog_revision_conflict")
	ErrPublicCatalogForbidden   = errors.New("public_catalog_forbidden")
)

type PublicCatalogFilter struct {
	Prefix, Protocol, AfterName, AfterID string
}

type PublicCatalogPage struct {
	Data    []PublicModel
	AsOf    time.Time
	HasMore bool
}

type PublicModel struct {
	ID           string             `json:"id"`
	PublicName   string             `json:"public_name"`
	Protocol     string             `json:"protocol"`
	Capabilities PublicCapabilities `json:"capabilities"`
	Limits       PublicLimits       `json:"limits"`
	Pricing      PublicPricing      `json:"pricing"`
}

type PublicCapabilities struct {
	Streaming bool `json:"streaming"`
	Tools     bool `json:"tools"`
	InputOnly bool `json:"input_only"`
	TextOnly  bool `json:"text_only"`
}

type PublicLimits struct {
	ContextTokens        int64 `json:"context_tokens"`
	InputTokenUpperBound int64 `json:"input_token_upper_bound"`
	MaxOutputTokens      int64 `json:"max_output_tokens"`
	DefaultOutputTokens  int64 `json:"default_output_tokens"`
}

type PublicPricing struct {
	Status         string     `json:"status"`
	Currency       string     `json:"currency"`
	Unit           string     `json:"unit"`
	PriceVersionID *string    `json:"price_version_id"`
	InputRate      *string    `json:"input_rate"`
	OutputRate     *string    `json:"output_rate"`
	EffectiveAt    *time.Time `json:"effective_at"`
}

type PublicationView struct {
	ID             string `json:"id"`
	PublicName     string `json:"public_name"`
	Enabled        bool   `json:"enabled"`
	PublicVisible  bool   `json:"public_visible"`
	PublicRevision string `json:"public_revision"`
}

func canonicalPublicUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func validPublicName(name string) bool {
	if len(name) < 1 || len(name) > 100 || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func publicProfile(name string, body []byte) (provider.Profile, error) {
	var p provider.Profile
	if !validPublicName(name) || len(body) == 0 || len(body) > 16384 || json.Unmarshal(body, &p) != nil || p.Validate() != nil {
		return p, ErrPublicCatalogInvalid
	}
	if p.Model != name || p.ContextLimit > 10_000_000 || p.InputTokenUpperBound > 10_000_000 || p.OutputLimit > 10_000_000 || p.DefaultOutputLimit > 10_000_000 {
		return p, ErrPublicCatalogInvalid
	}
	return p, nil
}

func publicProtocol(protocol string) bool {
	switch protocol {
	case provider.ProtocolChat, provider.ProtocolResponses, provider.ProtocolMessages, provider.ProtocolEmbeddings, provider.ProtocolRerank, provider.ProtocolGemini:
		return true
	}
	return false
}

func normalizePublicFilter(in PublicCatalogFilter) (PublicCatalogFilter, error) {
	in.Prefix = strings.TrimSpace(in.Prefix)
	if len(in.Prefix) > 100 || !utf8.ValidString(in.Prefix) || (in.Protocol != "" && !publicProtocol(in.Protocol)) {
		return in, ErrPublicCatalogInvalid
	}
	for _, r := range in.Prefix {
		if unicode.IsControl(r) {
			return in, ErrPublicCatalogInvalid
		}
	}
	if (in.AfterName == "") != (in.AfterID == "") || (in.AfterName != "" && (!validPublicName(in.AfterName) || !canonicalPublicUUID(in.AfterID))) {
		return in, ErrPublicCatalogInvalid
	}
	return in, nil
}

func literalPublicPrefix(prefix string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
}

// One materialized instant is returned even for an empty page. Profiles larger
// than the publication bound never leave PostgreSQL, including malformed rows
// created outside the management API.
const publicCatalogSQL = `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at),
 selected AS MATERIALIZED (
  SELECT m.id AS lookup_id,true AS present,
   CASE WHEN octet_length(m.id)=36 THEN m.id END AS id,
   CASE WHEN octet_length(m.public_name) BETWEEN 1 AND 100 THEN m.public_name END AS public_name,
   CASE WHEN octet_length(m.profile::text)<=16384 THEN m.profile END AS profile,
   octet_length(m.profile::text)<=16384 AS profile_bounded
  FROM models m
  WHERE m.public_visible AND m.enabled
   AND m.public_name LIKE $1 ESCAPE '\'
   AND ($2='' OR COALESCE(NULLIF(m.profile->>'protocol',''),'openai_chat')=$2)
   AND ($3='' OR (m.public_name COLLATE "C",m.id COLLATE "C")>($3 COLLATE "C",$4 COLLATE "C"))
   AND ($5='' OR m.id=$5)
  ORDER BY m.public_name COLLATE "C",m.id COLLATE "C" LIMIT 51
 )
 SELECT instant.at,m.present,m.id,m.public_name,m.profile,m.profile_bounded,
  p.present,p.id,p.currency,p.input_rate::text,p.output_rate::text,p.effective_at
 FROM instant LEFT JOIN selected m ON true
 LEFT JOIN LATERAL (
  SELECT true AS present,CASE WHEN octet_length(id)=36 THEN id END AS id,
   CASE WHEN currency='CNY' THEN 'CNY' END AS currency,input_rate,output_rate,effective_at FROM price_versions
  WHERE model_id=m.lookup_id AND price_group_id IS NULL AND effective_at<=instant.at
  ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1
 ) p ON true
 ORDER BY m.public_name COLLATE "C",m.id COLLATE "C"`

func (s *Store) publicModels(ctx context.Context, filter PublicCatalogFilter, id string) (PublicCatalogPage, error) {
	page := PublicCatalogPage{Data: []PublicModel{}}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := s.Pool.Query(ctx, publicCatalogSQL, literalPublicPrefix(filter.Prefix), filter.Protocol, filter.AfterName, filter.AfterID, id)
	if err != nil {
		return page, ErrPublicCatalogUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var present, pricePresent *bool
		var modelID, name *string
		var body []byte
		var bounded *bool
		var priceID, currency, input, output *string
		var effective *time.Time
		if err = rows.Scan(&page.AsOf, &present, &modelID, &name, &body, &bounded, &pricePresent, &priceID, &currency, &input, &output, &effective); err != nil {
			return PublicCatalogPage{}, ErrPublicCatalogUnavailable
		}
		page.AsOf = page.AsOf.UTC()
		if present == nil {
			continue
		}
		if modelID == nil || name == nil || bounded == nil || !*bounded || !canonicalPublicUUID(*modelID) {
			return PublicCatalogPage{}, ErrPublicCatalogUnavailable
		}
		p, invalid := publicProfile(*name, body)
		if invalid != nil {
			return PublicCatalogPage{}, ErrPublicCatalogUnavailable
		}
		price := PublicPricing{Status: "unavailable", Currency: "CNY", Unit: "micro_cny_per_million_tokens"}
		if pricePresent != nil {
			if priceID == nil || !canonicalPublicUUID(*priceID) || currency == nil || *currency != "CNY" || input == nil || output == nil || effective == nil || !canonicalPublicRate(*input) || !canonicalPublicRate(*output) || (p.InputOnly() && *output != "0") || effective.Year() < 0 || effective.Year() > 9999 {
				return PublicCatalogPage{}, ErrPublicCatalogUnavailable
			}
			at := effective.UTC()
			price.Status, price.PriceVersionID, price.InputRate, price.OutputRate, price.EffectiveAt = "available", priceID, input, output, &at
		} else if currency != nil || input != nil || output != nil || effective != nil {
			return PublicCatalogPage{}, ErrPublicCatalogUnavailable
		}
		page.Data = append(page.Data, PublicModel{
			ID: *modelID, PublicName: *name, Protocol: p.ProtocolName(),
			Capabilities: PublicCapabilities{Streaming: !p.InputOnly(), Tools: p.SupportsTools, InputOnly: p.InputOnly(), TextOnly: true},
			Limits:       PublicLimits{ContextTokens: p.ContextLimit, InputTokenUpperBound: p.InputTokenUpperBound, MaxOutputTokens: p.OutputLimit, DefaultOutputTokens: p.DefaultOutputLimit},
			Pricing:      price,
		})
	}
	if rows.Err() != nil || page.AsOf.IsZero() {
		return PublicCatalogPage{}, ErrPublicCatalogUnavailable
	}
	page.HasMore = len(page.Data) > 50
	if page.HasMore {
		page.Data = page.Data[:50]
	}
	return page, nil
}

func canonicalPublicRate(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n >= 0 && strconv.FormatInt(n, 10) == value
}

func (s *Store) PublicModels(ctx context.Context, filter PublicCatalogFilter) (PublicCatalogPage, error) {
	filter, err := normalizePublicFilter(filter)
	if err != nil {
		return PublicCatalogPage{}, err
	}
	return s.publicModels(ctx, filter, "")
}

func (s *Store) PublicModel(ctx context.Context, id string) (PublicModel, time.Time, error) {
	if !canonicalPublicUUID(id) {
		return PublicModel{}, time.Time{}, ErrPublicCatalogInvalid
	}
	page, err := s.publicModels(ctx, PublicCatalogFilter{}, id)
	if err != nil {
		return PublicModel{}, time.Time{}, err
	}
	if len(page.Data) == 0 {
		return PublicModel{}, page.AsOf, pgx.ErrNoRows
	}
	return page.Data[0], page.AsOf, nil
}

func publicActor(ctx context.Context, tx pgx.Tx, actor string) error {
	var role, status string
	if !canonicalPublicUUID(actor) {
		return ErrPublicCatalogForbidden
	}
	err := tx.QueryRow(ctx, `SELECT platform_role,status FROM users WHERE id=$1 FOR SHARE`, actor).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (role != "admin" || status != "active")) {
		return ErrPublicCatalogForbidden
	}
	return err
}

func publicationRow(ctx context.Context, tx pgx.Tx, id string) (PublicationView, error) {
	var v PublicationView
	err := tx.QueryRow(ctx, `SELECT id,public_name,enabled,public_visible,public_revision::text FROM models WHERE id=$1`, id).Scan(&v.ID, &v.PublicName, &v.Enabled, &v.PublicVisible, &v.PublicRevision)
	return v, err
}

func (s *Store) ModelPublication(ctx context.Context, actor, id string) (PublicationView, error) {
	if !canonicalPublicUUID(id) {
		return PublicationView{}, ErrPublicCatalogInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return PublicationView{}, err
	}
	defer tx.Rollback(ctx)
	if err = publicActor(ctx, tx, actor); err != nil {
		return PublicationView{}, err
	}
	v, err := publicationRow(ctx, tx, id)
	if err != nil {
		return PublicationView{}, err
	}
	return v, tx.Commit(ctx)
}

func (s *Store) SetModelPublication(ctx context.Context, actor, id string, expected int64, visible bool) (PublicationView, error) {
	if !canonicalPublicUUID(id) || expected <= 0 {
		return PublicationView{}, ErrPublicCatalogInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return PublicationView{}, err
	}
	defer tx.Rollback(ctx)
	if err = publicActor(ctx, tx, actor); err != nil {
		return PublicationView{}, err
	}
	var revision int64
	var current bool
	if err = tx.QueryRow(ctx, `SELECT public_revision,public_visible FROM models WHERE id=$1 FOR UPDATE`, id).Scan(&revision, &current); err != nil {
		return PublicationView{}, err
	}
	if expected != revision {
		return PublicationView{}, ErrPublicCatalogConflict
	}
	if visible && visible != current {
		// This new statement observes any profile change committed while the
		// model lock was waiting. Oversized profiles stay in PostgreSQL.
		var name string
		var body []byte
		if err = tx.QueryRow(ctx, `SELECT public_name,CASE WHEN octet_length(profile::text)<=16384 THEN profile END FROM models WHERE id=$1`, id).Scan(&name, &body); err != nil {
			return PublicationView{}, err
		}
		if _, err = publicProfile(name, body); err != nil {
			return PublicationView{}, err
		}
	}
	if visible != current {
		if _, err = tx.Exec(ctx, `UPDATE models SET public_visible=$2 WHERE id=$1`, id, visible); err != nil {
			return PublicationView{}, err
		}
		if err = audit(ctx, tx, actor, "model.publication.update", id); err != nil {
			return PublicationView{}, err
		}
	}
	v, err := publicationRow(ctx, tx, id)
	if err != nil {
		return PublicationView{}, err
	}
	return v, tx.Commit(ctx)
}
