package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

type RoutePublishInput struct {
	Expected   int64               `json:"expected_revision"`
	Bindings   []BindingInput      `json:"bindings"`
	CostPolicy *routing.CostPolicy `json:"cost_policy"`
}

func (in *RoutePublishInput) UnmarshalJSON(data []byte) error {
	type plain RoutePublishInput
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return ErrInvalid
	}
	if raw, exists := fields["cost_policy"]; exists && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ErrInvalid
	}
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	if value.CostPolicy != nil && routing.ValidateCostPolicy(*value.CostPolicy) != nil {
		return ErrInvalid
	}
	*in = RoutePublishInput(value)
	return nil
}

func parseCostRate(value *string) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	n, err := strconv.ParseInt(*value, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != *value {
		return nil, ErrInvalid
	}
	return &n, nil
}

type CostVersionInput struct {
	ChannelID               string     `json:"channel_id"`
	UpstreamModel           string     `json:"upstream_model"`
	InputRate               *string    `json:"input_rate"`
	OutputRate              *string    `json:"output_rate"`
	Basis                   string     `json:"basis"`
	EffectiveAt             *time.Time `json:"effective_at"`
	ExpectedChannelRevision string     `json:"expected_channel_revision"`
}

var costEffectiveRFC3339 = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](?:\.[0-9]{1,9})?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)

func (in *CostVersionInput) UnmarshalJSON(data []byte) error {
	type plain CostVersionInput
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return ErrInvalid
	}
	if raw, exists := fields["effective_at"]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var text string
		if json.Unmarshal(raw, &text) != nil || !costEffectiveRFC3339.MatchString(text) {
			return ErrInvalid
		}
	}
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	*in = CostVersionInput(value)
	return nil
}

type CostVersionPublished struct {
	ID              string `json:"id"`
	ChannelRevision string `json:"channel_revision"`
}

func (s *Store) PublishCostVersion(ctx context.Context, actor string, in CostVersionInput) (CostVersionPublished, error) {
	var result CostVersionPublished
	expected, err := strconv.ParseInt(in.ExpectedChannelRevision, 10, 64)
	if err != nil || expected <= 0 || strconv.FormatInt(expected, 10) != in.ExpectedChannelRevision || !canonicalOperationUUID(in.ChannelID) || len(in.UpstreamModel) < 1 || len(in.UpstreamModel) > 200 || (in.Basis != "unknown" && in.Basis != "contract" && in.Basis != "invoice") {
		return result, ErrInvalid
	}
	input, err := parseCostRate(in.InputRate)
	if err != nil {
		return result, err
	}
	output, err := parseCostRate(in.OutputRate)
	if err != nil {
		return result, err
	}
	if in.Basis == "unknown" && (input != nil || output != nil) || in.Basis != "unknown" && (input == nil || output == nil) {
		return result, ErrInvalid
	}
	var effective *time.Time
	if in.EffectiveAt != nil {
		normalized := in.EffectiveAt.UTC().Truncate(time.Microsecond)
		if normalized.Year() < 0 || normalized.Year() > 9999 {
			return result, ErrInvalid
		}
		effective = &normalized
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var current string
	if err = tx.QueryRow(ctx, `SELECT management_revision::text FROM channels WHERE id=$1 FOR UPDATE`, in.ChannelID).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, ErrInvalid
		}
		return result, err
	}
	if current != in.ExpectedChannelRevision {
		return result, ErrConflict
	}
	result.ID = uuid.NewString()
	if _, err = tx.Exec(ctx, `SELECT meter_insert_token_cost($1,$2,$3,$4,$5,$6,coalesce($7::timestamptz,clock_timestamp()))`, result.ID, in.ChannelID, in.UpstreamModel, input, output, in.Basis, effective); err != nil {
		var postgres *pgconn.PgError
		if errors.As(err, &postgres) && postgres.Code == "23505" {
			return CostVersionPublished{}, ErrConflict
		}
		return CostVersionPublished{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT management_revision::text FROM channels WHERE id=$1`, in.ChannelID).Scan(&result.ChannelRevision); err != nil {
		return CostVersionPublished{}, err
	}
	if err = audit(ctx, tx, actor, "cost.version", result.ID); err != nil {
		return CostVersionPublished{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CostVersionPublished{}, err
	}
	return result, nil
}
