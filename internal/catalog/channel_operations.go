package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ChannelOperationTarget struct {
	ChannelID        string `json:"channel_id"`
	ExpectedVersion  string `json:"expected_version"`
	ExpectedRevision string `json:"expected_revision"`
}

type ChannelOperationInput struct {
	Action  string                   `json:"action"`
	Targets []ChannelOperationTarget `json:"targets"`
}

type ChannelOperationState struct {
	CurrentVersion     string `json:"current_version"`
	ManagementRevision string `json:"management_revision"`
	Enabled            bool   `json:"enabled"`
	Quarantined        bool   `json:"quarantined"`
}

type ChannelOperationItem struct {
	ChannelID            string                 `json:"channel_id"`
	ExpectedVersion      string                 `json:"expected_version"`
	ExpectedRevision     string                 `json:"expected_revision"`
	Before               *ChannelOperationState `json:"before"`
	After                *ChannelOperationState `json:"after,omitempty"`
	LiveCredentials      int                    `json:"live_credentials"`
	PublishedBindings    int                    `json:"published_bindings"`
	OlderVersionBindings int                    `json:"older_version_bindings"`
	Status               string                 `json:"status"`
	Reason               string                 `json:"reason"`
}

type ChannelOperationPreview struct {
	Action       string                 `json:"action"`
	CanApply     bool                   `json:"can_apply"`
	ChangedCount int                    `json:"changed_count"`
	Items        []ChannelOperationItem `json:"items"`
}

type ChannelOperationReceipt struct {
	OperationID  string                 `json:"operation_id"`
	Actor        string                 `json:"actor"`
	Action       string                 `json:"action"`
	Status       string                 `json:"status"`
	TargetCount  int                    `json:"target_count"`
	ChangedCount int                    `json:"changed_count"`
	CreatedAt    time.Time              `json:"created_at"`
	Items        []ChannelOperationItem `json:"items"`
}

func canonicalOperationUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}

func canonicalChannelOperation(in ChannelOperationInput) (ChannelOperationInput, string, error) {
	if (in.Action != "enable" && in.Action != "disable") || len(in.Targets) == 0 || len(in.Targets) > 100 {
		return ChannelOperationInput{}, "", ErrInvalid
	}
	in.Targets = append([]ChannelOperationTarget(nil), in.Targets...)
	for _, target := range in.Targets {
		revision, err := strconv.ParseInt(target.ExpectedRevision, 10, 64)
		if !canonicalOperationUUID(target.ChannelID) || !canonicalOperationUUID(target.ExpectedVersion) || err != nil || revision < 1 || strconv.FormatInt(revision, 10) != target.ExpectedRevision {
			return ChannelOperationInput{}, "", ErrInvalid
		}
	}
	sort.Slice(in.Targets, func(i, j int) bool { return in.Targets[i].ChannelID < in.Targets[j].ChannelID })
	for i := 1; i < len(in.Targets); i++ {
		if in.Targets[i-1].ChannelID == in.Targets[i].ChannelID {
			return ChannelOperationInput{}, "", ErrInvalid
		}
	}
	body, err := json.Marshal(in)
	if err != nil {
		return ChannelOperationInput{}, "", err
	}
	digest := sha256.Sum256(body)
	return in, hex.EncodeToString(digest[:]), nil
}

func operationTargetIDs(in ChannelOperationInput) []string {
	ids := make([]string, len(in.Targets))
	for i, target := range in.Targets {
		ids[i] = target.ChannelID
	}
	return ids
}

// One statement observes channel state, live pool membership and all currently
// published binding counts under the same PostgreSQL snapshot.
func readChannelOperation(ctx context.Context, q poolQuerier, in ChannelOperationInput, lockedIDs []string) (ChannelOperationPreview, error) {
	body, err := json.Marshal(in.Targets)
	if err != nil {
		return ChannelOperationPreview{}, err
	}
	rows, err := q.Query(ctx, `WITH targets AS (SELECT value->>'channel_id' AS channel_id,value->>'expected_version' AS expected_version,value->>'expected_revision' AS expected_revision FROM jsonb_array_elements($1::jsonb))
 SELECT t.channel_id,t.expected_version,t.expected_revision,c.current_version,c.management_revision::text,c.enabled,c.quarantined,
 (SELECT count(*)::int FROM channel_version_credentials p JOIN channel_credentials k ON k.id=p.credential_id WHERE p.channel_version_id=c.current_version AND k.revoked_at IS NULL),
 b.published_bindings,b.older_version_bindings
 FROM targets t LEFT JOIN channels c ON c.id=t.channel_id AND c.id=ANY($2::text[])
 LEFT JOIN LATERAL (SELECT count(*)::int AS published_bindings,count(*) FILTER(WHERE mb.channel_version_id<>c.current_version)::int AS older_version_bindings FROM model_bindings mb JOIN route_policy_versions p ON p.id=mb.route_policy_version_id JOIN models m ON m.id=p.model_id AND m.active_revision=p.revision WHERE mb.channel_id=c.id) b ON true ORDER BY t.channel_id`, body, lockedIDs)
	if err != nil {
		return ChannelOperationPreview{}, err
	}
	defer rows.Close()
	preview := ChannelOperationPreview{Action: in.Action, CanApply: true, Items: []ChannelOperationItem{}}
	for rows.Next() {
		item := ChannelOperationItem{}
		var version, revision *string
		var enabled, quarantined *bool
		if err = rows.Scan(&item.ChannelID, &item.ExpectedVersion, &item.ExpectedRevision, &version, &revision, &enabled, &quarantined, &item.LiveCredentials, &item.PublishedBindings, &item.OlderVersionBindings); err != nil {
			return ChannelOperationPreview{}, err
		}
		if version != nil {
			item.Before = &ChannelOperationState{CurrentVersion: *version, ManagementRevision: *revision, Enabled: *enabled, Quarantined: *quarantined}
		}
		switch {
		case item.Before == nil:
			item.Reason = "not_found"
		case item.Before.CurrentVersion != item.ExpectedVersion:
			item.Reason = "stale_version"
		case item.Before.ManagementRevision != item.ExpectedRevision:
			item.Reason = "stale_revision"
		case in.Action == "enable" && item.Before.Quarantined:
			item.Reason = "channel_quarantined"
		case in.Action == "enable" && item.LiveCredentials == 0:
			item.Reason = "no_live_credential"
		}
		if item.Reason != "" {
			item.Status, preview.CanApply = "blocked", false
		} else if item.Before.Enabled == (in.Action == "enable") {
			item.Status = "unchanged"
		} else {
			item.Status = "ready"
			preview.ChangedCount++
		}
		preview.Items = append(preview.Items, item)
	}
	if err = rows.Err(); err != nil {
		return ChannelOperationPreview{}, err
	}
	if !preview.CanApply {
		preview.ChangedCount = 0
	}
	return preview, nil
}

func (s *Store) PreviewChannelOperation(ctx context.Context, in ChannelOperationInput) (ChannelOperationPreview, error) {
	in, _, err := canonicalChannelOperation(in)
	if err != nil {
		return ChannelOperationPreview{}, err
	}
	return readChannelOperation(ctx, s.Pool, in, operationTargetIDs(in))
}

type operationRowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readChannelOperationReceipt(ctx context.Context, q operationRowQuerier, id string) (ChannelOperationReceipt, string, error) {
	var receipt ChannelOperationReceipt
	var digest string
	var body []byte
	err := q.QueryRow(ctx, `SELECT operation_id,actor,action,input_digest,status,target_count,changed_count,created_at,items FROM channel_batch_operations WHERE operation_id=$1`, id).Scan(&receipt.OperationID, &receipt.Actor, &receipt.Action, &digest, &receipt.Status, &receipt.TargetCount, &receipt.ChangedCount, &receipt.CreatedAt, &body)
	if err == nil {
		err = json.Unmarshal(body, &receipt.Items)
	}
	return receipt, digest, err
}

func (s *Store) ChannelOperation(ctx context.Context, id string) (ChannelOperationReceipt, error) {
	if !canonicalOperationUUID(id) {
		return ChannelOperationReceipt{}, ErrInvalid
	}
	receipt, _, err := readChannelOperationReceipt(ctx, s.Pool, id)
	return receipt, err
}

// ChannelOperationListSQL omits immutable item details and the input digest.
func ChannelOperationListSQL() string {
	return `SELECT operation_id AS id,operation_id,actor,action,status,target_count,changed_count,created_at FROM channel_batch_operations`
}

func (s *Store) ApplyChannelOperation(ctx context.Context, actor, operationID string, in ChannelOperationInput) (ChannelOperationReceipt, error) {
	if !canonicalOperationUUID(operationID) || actor == "" {
		return ChannelOperationReceipt{}, ErrInvalid
	}
	in, digest, err := canonicalChannelOperation(in)
	if err != nil {
		return ChannelOperationReceipt{}, err
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ChannelOperationReceipt{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,13313))`, "channel_batch:"+operationID); err != nil {
		return ChannelOperationReceipt{}, err
	}
	// This is deliberately a fresh statement after the advisory-lock wait.
	previous, previousDigest, err := readChannelOperationReceipt(ctx, tx, operationID)
	if err == nil {
		if previous.Actor != actor || previousDigest != digest {
			return ChannelOperationReceipt{}, ErrConflict
		}
		return previous, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ChannelOperationReceipt{}, err
	}
	ids := operationTargetIDs(in)
	rows, err := tx.Query(ctx, `SELECT id FROM channels WHERE id=ANY($1::text[]) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return ChannelOperationReceipt{}, err
	}
	lockedIDs := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return ChannelOperationReceipt{}, err
		}
		lockedIDs = append(lockedIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ChannelOperationReceipt{}, err
	}
	rows, err = tx.Query(ctx, `SELECT k.id FROM channel_credentials k WHERE EXISTS(SELECT 1 FROM channels c JOIN channel_version_credentials p ON p.channel_version_id=c.current_version WHERE c.id=ANY($1::text[]) AND p.credential_id=k.id) ORDER BY k.id FOR SHARE OF k`, lockedIDs)
	if err != nil {
		return ChannelOperationReceipt{}, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return ChannelOperationReceipt{}, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ChannelOperationReceipt{}, err
	}
	preview, err := readChannelOperation(ctx, tx, in, lockedIDs)
	if err != nil {
		return ChannelOperationReceipt{}, err
	}
	receipt := ChannelOperationReceipt{OperationID: operationID, Actor: actor, Action: in.Action, Status: "rejected", TargetCount: len(in.Targets), Items: preview.Items}
	if preview.CanApply {
		receipt.Status, receipt.ChangedCount = "applied", preview.ChangedCount
	}
	for i := range receipt.Items {
		item := &receipt.Items[i]
		if !preview.CanApply {
			if item.Status == "ready" {
				item.Status = "not_applied"
			}
			continue
		}
		after := *item.Before
		item.After = &after
		if item.Status == "ready" {
			err = tx.QueryRow(ctx, `UPDATE channels SET enabled=$2 WHERE id=$1 AND current_version=$3 AND management_revision=$4::bigint RETURNING current_version,management_revision::text,enabled,quarantined`, item.ChannelID, in.Action == "enable", item.ExpectedVersion, item.ExpectedRevision).Scan(&after.CurrentVersion, &after.ManagementRevision, &after.Enabled, &after.Quarantined)
			if err != nil {
				return ChannelOperationReceipt{}, err
			}
			item.Status = "changed"
		}
	}
	body, err := json.Marshal(receipt.Items)
	if err != nil {
		return ChannelOperationReceipt{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_batch_operations(operation_id,actor,action,input_digest,status,target_count,changed_count,items) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, operationID, actor, in.Action, digest, receipt.Status, receipt.TargetCount, receipt.ChangedCount, body); err != nil {
		return ChannelOperationReceipt{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT created_at FROM channel_batch_operations WHERE operation_id=$1`, operationID).Scan(&receipt.CreatedAt); err != nil {
		return ChannelOperationReceipt{}, err
	}
	action := "channel.batch.reject"
	if preview.CanApply {
		action = "channel.batch.apply"
	}
	if err = audit(ctx, tx, actor, action, operationID); err != nil {
		return ChannelOperationReceipt{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ChannelOperationReceipt{}, err
	}
	return receipt, nil
}
