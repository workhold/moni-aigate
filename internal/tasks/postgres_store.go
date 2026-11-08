package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore is the current task writer. It keeps task facts and the
// existing request/reservation/attempt references in one database transaction.
// Artifact bytes stay outside PostgreSQL and are fetched through the adapter.
type PostgresStore struct {
	pool   *pgxpool.Pool
	schema string
}

func NewPostgresStore(pool *pgxpool.Pool, schema string) (*PostgresStore, error) {
	if pool == nil {
		return nil, ErrInvalidInput
	}
	return &PostgresStore{pool: pool, schema: schema}, nil
}

func (s *PostgresStore) table(name string) string {
	if s.schema == "" {
		return pgx.Identifier{name}.Sanitize()
	}
	return pgx.Identifier{s.schema, name}.Sanitize()
}

func (s *PostgresStore) CreateTask(ctx context.Context, input SubmitInput, refs References) (Task, error) {
	task, err := New(input)
	if err != nil {
		return Task{}, err
	}
	if err = refs.valid(); err != nil || input.RequestID != refs.RequestID || input.ReservationID != refs.ReservationID || input.SourceID != refs.SourceID || input.AttemptID != refs.AttemptID {
		return Task{}, ErrInvalidInput
	}
	if input.ModelID == "" || input.ProfileVersion == "" || input.SchemaID == "" || input.SchemaDigest.IsZero() {
		return Task{}, ErrInvalidInput
	}
	query := `INSERT INTO %s
 (tenant_id,id,request_id,owner_id,attempt_id,reservation_id,source_id,model_id,profile_version,schema_id,schema_digest,variant_key,idempotency_key,submit_fingerprint,input_digest,submit_deadline,observe_deadline,result_deadline,artifact_deadline,max_artifact_bytes)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`
	_, err = s.pool.Exec(ctx, fmt.Sprintf(query, s.table("media_tasks")), input.Owner.TenantID.String(), input.TaskID.String(), refs.RequestID.String(), input.Owner.SubjectID.String(), nullableID(refs.AttemptID), refs.ReservationID.String(), refs.SourceID.String(), input.ModelID.String(), input.ProfileVersion.String(), input.SchemaID.String(), input.SchemaDigest[:], defaultVariant(input.VariantKey), input.IdempotencyKey, input.RequestDigest[:], input.SubmitBy, input.ObserveUntil, input.ResultUntil, input.ResultUntil, input.MaxArtifactBytes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, err
	}
	return task, nil
}

func (s *PostgresStore) FindByIdempotency(ctx context.Context, owner Owner, key string) (Task, References, error) {
	return s.load(ctx, s.pool, `WHERE t.tenant_id=$1 AND t.owner_id=$2 AND t.idempotency_key=$3`, owner.TenantID.String(), owner.SubjectID.String(), key)
}

func (s *PostgresStore) Snapshot(ctx context.Context, id ID) (Task, References, error) {
	return s.load(ctx, s.pool, `WHERE t.id=$1`, id.String())
}

func (s *PostgresStore) WithTask(ctx context.Context, id ID, fn func(TaskTx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	task, refs, err := s.load(ctx, tx, `WHERE t.id=$1 FOR UPDATE`, id.String())
	if err != nil {
		return err
	}
	write := &postgresTaskTx{store: s, tx: tx, task: task, refs: refs}
	if err = fn(write); err != nil {
		return err
	}
	if write.saved {
		if err = s.updateTask(ctx, tx, write.task, write.refs); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) load(ctx context.Context, runner interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, where string, args ...any) (Task, References, error) {
	q := fmt.Sprintf(`SELECT t.tenant_id,t.id,t.request_id,t.owner_id,t.attempt_id,t.reservation_id,t.source_id,t.idempotency_key,t.submit_fingerprint,t.input_digest,t.status,t.observation_state,t.cancel_state,t.artifact_state,t.provider_reference,t.submit_deadline,t.observe_deadline,t.result_deadline,t.artifact_deadline,t.max_artifact_bytes,t.created_at,
 COALESCE(e.sequence,0),COALESCE(e.event_id,''),COALESCE(e.digest,''::bytea),a.artifact_reference,a.media_type,a.byte_length,a.sha256,a.expires_at
 FROM %s t
 LEFT JOIN LATERAL (SELECT event_id,sequence,digest FROM %s WHERE tenant_id=t.tenant_id AND task_id=t.id ORDER BY sequence DESC LIMIT 1) e ON true
 LEFT JOIN LATERAL (SELECT artifact_reference,media_type,byte_length,sha256,expires_at FROM %s WHERE tenant_id=t.tenant_id AND task_id=t.id AND status='available' ORDER BY created_at DESC LIMIT 1) a ON true %s`, s.table("media_tasks"), s.table("media_task_events"), s.table("media_task_artifacts"), where)
	var tenant, taskID, requestID, ownerID, attemptID, reservationID, sourceID, key string
	var submitFP, inputDigest, eventDigest, artifactDigest []byte
	var status, observation, cancel, artifactState, provider string
	var submitBy, observeUntil, resultUntil, artifactUntil, createdAt, artifactExpires time.Time
	var maxBytes, sequence, artifactBytes int64
	var eventID, artifactRef, mediaType *string
	row := runner.QueryRow(ctx, q, args...)
	err := row.Scan(&tenant, &taskID, &requestID, &ownerID, &attemptID, &reservationID, &sourceID, &key, &submitFP, &inputDigest, &status, &observation, &cancel, &artifactState, &provider, &submitBy, &observeUntil, &resultUntil, &artifactUntil, &maxBytes, &createdAt, &sequence, &eventID, &eventDigest, &artifactRef, &mediaType, &artifactBytes, &artifactDigest, &artifactExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, References{}, ErrNotFound
	}
	if err != nil {
		return Task{}, References{}, err
	}
	tid, err := ParseID(taskID)
	if err != nil {
		return Task{}, References{}, ErrInvalidTask
	}
	ownerTenant, err := ParseID(tenant)
	if err != nil {
		return Task{}, References{}, ErrInvalidTask
	}
	subject, err := ParseID(ownerID)
	if err != nil {
		return Task{}, References{}, ErrInvalidTask
	}
	_, err = digestFromBytes(submitFP)
	if err != nil {
		return Task{}, References{}, ErrInvalidTask
	}
	input, err := digestFromBytes(inputDigest)
	if err != nil {
		return Task{}, References{}, ErrInvalidTask
	}
	lastID := ID("")
	if eventID != nil && *eventID != "" {
		lastID, err = ParseID(*eventID)
		if err != nil {
			return Task{}, References{}, ErrInvalidTask
		}
	}
	lastDigest := Digest{}
	if len(eventDigest) != 0 {
		lastDigest, err = digestFromBytes(eventDigest)
		if err != nil {
			return Task{}, References{}, ErrInvalidTask
		}
	}
	task := Task{id: tid, owner: Owner{TenantID: ownerTenant, SubjectID: subject}, idempotencyKey: key, requestDigest: input, createdAt: createdAt.UTC(), submitBy: submitBy.UTC(), observeUntil: observeUntil.UTC(), resultUntil: resultUntil.UTC(), maxArtifactBytes: maxBytes, protocol: ProtocolState(status), observation: ObservationState(observation), cancel: CancelState(cancel), artifactState: ArtifactState(artifactState), providerReference: provider, lastEventID: lastID, lastEventDigest: lastDigest, lastSequence: uint64(sequence)}
	if artifactRef != nil && mediaType != nil && len(artifactDigest) == 32 {
		d, e := digestFromBytes(artifactDigest)
		if e != nil {
			return Task{}, References{}, e
		}
		task.artifact = Artifact{Reference: *artifactRef, MediaType: *mediaType, Size: artifactBytes, SHA256: d, ExpiresAt: artifactExpires.UTC()}
	}
	if err = task.valid(); err != nil {
		return Task{}, References{}, err
	}
	refs := References{RequestID: mustID(requestID), ReservationID: mustID(reservationID), SourceID: mustID(sourceID)}
	if attemptID != "" {
		refs.AttemptID = mustID(attemptID)
	}
	return task, refs, nil
}

func (s *PostgresStore) updateTask(ctx context.Context, tx pgx.Tx, task Task, refs References) error {
	_, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE %s SET status=$3,observation_state=$4,cancel_state=$5,artifact_state=$6,provider_reference=NULLIF($7,''),settlement_id=NULLIF($8,'') WHERE tenant_id=$1 AND id=$2`, s.table("media_tasks")), task.owner.TenantID.String(), task.id.String(), task.protocol, task.observation, task.cancel, task.artifactState, task.providerReference, refs.SettlementID.String())
	return err
}

type postgresTaskTx struct {
	store *PostgresStore
	tx    pgx.Tx
	task  Task
	refs  References
	saved bool
}

func (t *postgresTaskTx) Task() Task             { return t.task }
func (t *postgresTaskTx) References() References { return t.refs }
func (t *postgresTaskTx) SaveTask(task Task) error {
	if task.id != t.task.id {
		return ErrInvalidTask
	}
	t.task, t.saved = task, true
	return nil
}
func (t *postgresTaskTx) AppendEvent(event Event) error {
	canonical, err := event.CanonicalJSON()
	if err != nil {
		return err
	}
	artifact, ok := event.Artifact()
	var ref, mediaType, expires any
	var size any
	var artifactDigest any
	if ok {
		artifactDigest = artifact.SHA256[:]
		ref, mediaType, expires, size = artifact.Reference, artifact.MediaType, artifact.ExpiresAt, artifact.Size
	}
	failure, _ := nullableString(event.failureCode)
	eventDigest := event.Digest()
	_, err = t.tx.Exec(context.Background(), fmt.Sprintf(`INSERT INTO %s(tenant_id,task_id,event_id,sequence,source,kind,observed_at,provider_reference,artifact_reference,artifact_media_type,artifact_bytes,artifact_sha256,artifact_expires_at,failure_code,canonical,digest) VALUES($1,$2,$3,$4,'adapter',$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15)`, t.store.table("media_task_events")), t.task.owner.TenantID.String(), t.task.id.String(), event.eventID.String(), event.sequence, event.kind, event.observedAt, event.providerReference, ref, mediaType, size, artifactDigest, expires, failure, canonical, eventDigest[:])
	return err
}
func (t *postgresTaskTx) BindSettlement(id ID) error {
	if err := validateID(id); err != nil {
		return err
	}
	if t.refs.SettlementID != "" && t.refs.SettlementID != id {
		return ErrSettlementBound
	}
	t.refs.SettlementID = id
	return nil
}
func (t *postgresTaskTx) PutArtifact(_ []byte) error { return ErrArtifactMissing }

func (s *PostgresStore) Claim(context.Context, WorkKind, ID, time.Time, time.Duration, ID) (WorkerLease, Task, References, error) {
	return WorkerLease{}, Task{}, References{}, ErrNoWork
}
func (s *PostgresStore) RenewLease(context.Context, WorkerLease, time.Time, time.Duration) (WorkerLease, error) {
	return WorkerLease{}, ErrLeaseConflict
}
func (s *PostgresStore) ReleaseLease(context.Context, WorkerLease) error { return ErrLeaseConflict }
func (s *PostgresStore) IssueGrant(ctx context.Context, id ID, actor Actor, grantID ID, now time.Time, ttl time.Duration) (DownloadGrant, error) {
	task, _, err := s.Snapshot(ctx, id)
	if err != nil {
		return DownloadGrant{}, err
	}
	return task.IssueDownloadGrant(actor, grantID, now, ttl)
}
func (s *PostgresStore) ReadArtifact(context.Context, Actor, DownloadGrant, time.Time) ([]byte, error) {
	return nil, ErrArtifactMissing
}

func digestFromBytes(raw []byte) (Digest, error) {
	if len(raw) != 32 {
		return Digest{}, ErrInvalidDigest
	}
	var d Digest
	copy(d[:], raw)
	if d.IsZero() {
		return Digest{}, ErrInvalidDigest
	}
	return d, nil
}

func mustID(raw string) ID { return ID(raw) }
func nullableID(id ID) any {
	if id == "" {
		return nil
	}
	return id.String()
}
func defaultVariant(v string) string {
	if v == "" {
		return "default"
	}
	return v
}
