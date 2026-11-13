package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrModelAliasInvalid           = errors.New("invalid_model_alias")
	ErrModelAliasConflict          = errors.New("model_name_conflict")
	ErrModelAliasRetired           = errors.New("model_alias_retired")
	ErrModelAliasTargetUnavailable = errors.New("model_alias_target_unavailable")
	ErrModelAliasTableUnavailable  = errors.New("model_alias_table_unavailable")
)

// ModelAlias is the customer-visible name mapped to one logical model. The
// state is terminal: a retired name is never reused for a different model.
type ModelAlias struct {
	Alias       string     `json:"alias"`
	ModelID     string     `json:"model_id"`
	State       string     `json:"state"`
	Revision    string     `json:"revision"`
	OperationID string     `json:"operation_id"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	RetiredAt   *time.Time `json:"retired_at"`
}

type ModelAliasCreateInput struct {
	OperationID string `json:"operation_id"`
	Alias       string `json:"alias"`
}

type ModelAliasRetireInput struct {
	OperationID      string
	Alias            string
	ExpectedRevision int64
}

func validModelAliasName(value string) bool {
	if len(value) < 1 || len(value) > 100 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func ValidModelAliasName(value string) bool { return validModelAliasName(value) }

func canonicalModelAliasOperation(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

// ResolveModelName is the pure parsing seam used by request admission. It
// returns the logical model ID while retaining the caller's requested name in
// the surrounding request snapshot/response contract.
func ResolveModelName(requested, publicName, modelID string, aliases []ModelAlias) (string, bool) {
	if requested == publicName {
		return modelID, true
	}
	for _, alias := range aliases {
		if alias.State == "active" && alias.Alias == requested && alias.ModelID == modelID {
			return modelID, true
		}
	}
	return "", false
}

func (s *Store) ModelAliasTableAvailable(ctx context.Context) (bool, error) {
	var available bool
	err := s.Pool.QueryRow(ctx, `SELECT to_regclass('model_aliases') IS NOT NULL`).Scan(&available)
	return available, err
}

func (s *Store) ActiveModelAliases(ctx context.Context, modelIDs []string) (map[string][]string, error) {
	result := make(map[string][]string, len(modelIDs))
	for _, id := range modelIDs {
		result[id] = []string{}
	}
	if len(modelIDs) == 0 {
		return result, nil
	}
	available, err := s.ModelAliasTableAvailable(ctx)
	if err != nil || !available {
		return result, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT model_id,alias FROM model_aliases WHERE state='active' AND model_id=ANY($1::text[]) ORDER BY model_id,alias COLLATE "C"`, modelIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var modelID, alias string
		if err := rows.Scan(&modelID, &alias); err != nil {
			return nil, err
		}
		result[modelID] = append(result[modelID], alias)
	}
	return result, rows.Err()
}

func (s *Store) ModelAliases(ctx context.Context, modelID string) ([]ModelAlias, error) {
	if !canonicalPublicUUID(modelID) {
		return nil, ErrModelAliasInvalid
	}
	available, err := s.ModelAliasTableAvailable(ctx)
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, ErrModelAliasTableUnavailable
	}
	rows, err := s.Pool.Query(ctx, `SELECT alias,model_id,state,revision::text,operation_id,created_by,created_at,retired_at FROM model_aliases WHERE model_id=$1 ORDER BY alias COLLATE "C"`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ModelAlias{}
	for rows.Next() {
		var item ModelAlias
		if err := rows.Scan(&item.Alias, &item.ModelID, &item.State, &item.Revision, &item.OperationID, &item.CreatedBy, &item.CreatedAt, &item.RetiredAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func modelAliasUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func insertModelAliasOperation(ctx context.Context, tx pgx.Tx, kind, actor, modelID, target, operationID string, revision int64, safe map[string]any, result ModelAlias) (time.Time, error) {
	safeJSON, err := json.Marshal(safe)
	if err != nil {
		return time.Time{}, err
	}
	snapshot, err := json.Marshal(map[string]any{
		"alias":        result.Alias,
		"model_id":     result.ModelID,
		"state":        result.State,
		"revision":     result.Revision,
		"operation_id": result.OperationID,
		"created_by":   result.CreatedBy,
		"created_at":   "0001-01-01T00:00:00.000000Z",
		"retired_at":   nil,
	})
	if err != nil {
		return time.Time{}, err
	}
	fingerprint := pricingFingerprint(kind, actor, "platform", target, safe)
	var createdAt time.Time
	err = tx.QueryRow(ctx, `INSERT INTO pricing_operations(operation_id,kind,actor,scope,target,resource_id,revision,safe_input,input_fingerprint,result_snapshot)
VALUES($1,$2,$3,'platform',$4,$5,$6,$7,$8,$9) RETURNING created_at`, operationID, kind, actor, target, modelID, revision, safeJSON, fingerprint, snapshot).Scan(&createdAt)
	return createdAt, err
}

func (s *Store) CreateModelAlias(ctx context.Context, actor, modelID string, in ModelAliasCreateInput) (ModelAlias, error) {
	var result ModelAlias
	if !canonicalPublicUUID(actor) || !canonicalPublicUUID(modelID) || !canonicalModelAliasOperation(in.OperationID) || !validModelAliasName(in.Alias) {
		return result, ErrModelAliasInvalid
	}
	available, err := s.ModelAliasTableAvailable(ctx)
	if err != nil {
		return result, err
	}
	if !available {
		return result, ErrModelAliasTableUnavailable
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('model-name:'||$1,0))`, in.Alias); err != nil {
		return result, err
	}
	// A repeated operation returns the original immutable mapping. Reusing an
	// operation ID with different input is a conflict, never a second write.
	var existing ModelAlias
	err = tx.QueryRow(ctx, `SELECT alias,model_id,state,revision::text,operation_id,created_by,created_at,retired_at FROM model_aliases WHERE operation_id=$1`, in.OperationID).Scan(&existing.Alias, &existing.ModelID, &existing.State, &existing.Revision, &existing.OperationID, &existing.CreatedBy, &existing.CreatedAt, &existing.RetiredAt)
	if err == nil {
		if existing.Alias != in.Alias || existing.ModelID != modelID || existing.State != "active" {
			return result, ErrModelAliasConflict
		}
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	var enabled bool
	var activeRevision int64
	if err = tx.QueryRow(ctx, `SELECT enabled,active_revision FROM models WHERE id=$1 FOR SHARE`, modelID).Scan(&enabled, &activeRevision); errors.Is(err, pgx.ErrNoRows) {
		return result, pgx.ErrNoRows
	} else if err != nil {
		return result, err
	}
	if !enabled || activeRevision < 1 {
		return result, ErrModelAliasTargetUnavailable
	}
	var publicConflict bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM models WHERE public_name=$1)`, in.Alias).Scan(&publicConflict); err != nil {
		return result, err
	}
	if publicConflict {
		return result, ErrModelAliasConflict
	}
	var retired bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM model_aliases WHERE alias=$1)`, in.Alias).Scan(&retired); err != nil {
		return result, err
	}
	if retired {
		return result, ErrModelAliasConflict
	}
	result = ModelAlias{Alias: in.Alias, ModelID: modelID, State: "active", Revision: "1", OperationID: in.OperationID, CreatedBy: actor}
	createdAt, err := insertModelAliasOperation(ctx, tx, "model_alias.create", actor, modelID, "/api/platform/models/"+modelID+"/aliases", in.OperationID, 1, map[string]any{"model_id": modelID, "alias": in.Alias}, result)
	if err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO model_aliases(alias,model_id,state,revision,operation_id,created_by,created_at) VALUES($1,$2,'active',1,$3,$4,$5) RETURNING alias,model_id,state,revision::text,operation_id,created_by,created_at,retired_at`, in.Alias, modelID, in.OperationID, actor, createdAt).Scan(&result.Alias, &result.ModelID, &result.State, &result.Revision, &result.OperationID, &result.CreatedBy, &result.CreatedAt, &result.RetiredAt); err != nil {
		if modelAliasUniqueViolation(err) {
			return ModelAlias{}, ErrModelAliasConflict
		}
		return result, err
	}
	if err = audit(ctx, tx, actor, "model_alias.create", in.Alias); err != nil {
		return ModelAlias{}, err
	}
	return result, tx.Commit(ctx)
}

func (s *Store) RetireModelAlias(ctx context.Context, actor string, in ModelAliasRetireInput) (ModelAlias, error) {
	var result ModelAlias
	if !canonicalPublicUUID(actor) || !canonicalModelAliasOperation(in.OperationID) || !validModelAliasName(in.Alias) || in.ExpectedRevision < 1 {
		return result, ErrModelAliasInvalid
	}
	available, err := s.ModelAliasTableAvailable(ctx)
	if err != nil {
		return result, err
	}
	if !available {
		return result, ErrModelAliasTableUnavailable
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('model-name:'||$1,0))`, in.Alias); err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `SELECT alias,model_id,state,revision::text,operation_id,created_by,created_at,retired_at FROM model_aliases WHERE alias=$1 FOR UPDATE`, in.Alias).Scan(&result.Alias, &result.ModelID, &result.State, &result.Revision, &result.OperationID, &result.CreatedBy, &result.CreatedAt, &result.RetiredAt); err != nil {
		return result, err
	}
	if result.OperationID == in.OperationID {
		return result, tx.Commit(ctx)
	}
	if result.State == "retired" {
		return ModelAlias{}, ErrModelAliasRetired
	}
	if result.Revision != formatInt64(in.ExpectedRevision) {
		return ModelAlias{}, ErrConflict
	}
	nextRevision := in.ExpectedRevision + 1
	result.State = "retired"
	result.Revision = formatInt64(nextRevision)
	result.OperationID = in.OperationID
	createdAt, err := insertModelAliasOperation(ctx, tx, "model_alias.retire", actor, result.ModelID, "/api/platform/model-aliases/"+in.Alias+"/retire", in.OperationID, nextRevision, map[string]any{"alias": in.Alias, "expected_revision": formatInt64(in.ExpectedRevision)}, result)
	if err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `UPDATE model_aliases SET state='retired',revision=revision+1,operation_id=$2,retired_at=$4 WHERE alias=$1 AND state='active' AND revision=$3 RETURNING alias,model_id,state,revision::text,operation_id,created_by,created_at,retired_at`, in.Alias, in.OperationID, in.ExpectedRevision, createdAt).Scan(&result.Alias, &result.ModelID, &result.State, &result.Revision, &result.OperationID, &result.CreatedBy, &result.CreatedAt, &result.RetiredAt); err != nil {
		return result, err
	}
	if err = audit(ctx, tx, actor, "model_alias.retire", in.Alias); err != nil {
		return ModelAlias{}, err
	}
	return result, tx.Commit(ctx)
}

func formatInt64(value int64) string { return strconv.FormatInt(value, 10) }
