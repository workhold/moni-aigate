package tasks

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"time"
)

// References binds a task to the already-created commercial records. The
// service never creates a wallet, reservation, attempt, settlement, or source
// row. SettlementID is empty until the existing settlement writer binds its
// own row.
type References struct {
	RequestID     ID
	ReservationID ID
	AttemptID     ID
	SettlementID  ID
	SourceID      ID
}

func (r References) valid() error {
	for _, id := range []ID{r.RequestID, r.ReservationID, r.AttemptID, r.SourceID} {
		if err := validateID(id); err != nil {
			return err
		}
	}
	if r.SettlementID != "" {
		if err := validateID(r.SettlementID); err != nil {
			return err
		}
	}
	return nil
}

var (
	ErrNoWork          = errors.New("task_no_work")
	ErrLeaseConflict   = errors.New("task_lease_conflict")
	ErrSettlementBound = errors.New("task_settlement_already_bound")
	ErrArtifactMissing = errors.New("task_artifact_missing")
)

type WorkKind string

const (
	WorkSubmit  WorkKind = "submit"
	WorkObserve WorkKind = "observe"
	WorkCancel  WorkKind = "cancel"
	WorkExpire  WorkKind = "expire"
)

type WorkerLease struct {
	LeaseID   ID
	TaskID    ID
	WorkerID  ID
	Kind      WorkKind
	ExpiresAt time.Time
}

func (l WorkerLease) valid() error {
	if validateID(l.LeaseID) != nil || validateID(l.TaskID) != nil || validateID(l.WorkerID) != nil || !validUTC(l.ExpiresAt) {
		return ErrInvalidInput
	}
	switch l.Kind {
	case WorkSubmit, WorkObserve, WorkCancel, WorkExpire:
		return nil
	default:
		return ErrInvalidInput
	}
}

// TaskTx is the write unit shared by task facts and their existing commercial
// references. A real implementation maps this callback to one database
// transaction. The memory implementation below intentionally has the same
// commit-or-rollback behavior.
type TaskTx interface {
	Task() Task
	References() References
	SaveTask(Task) error
	AppendEvent(Event) error
	BindSettlement(ID) error
	PutArtifact([]byte) error
}

type Store interface {
	CreateTask(context.Context, SubmitInput, References) (Task, error)
	FindByIdempotency(context.Context, Owner, string) (Task, References, error)
	Snapshot(context.Context, ID) (Task, References, error)
	WithTask(context.Context, ID, func(TaskTx) error) error
	Claim(context.Context, WorkKind, ID, time.Time, time.Duration, ID) (WorkerLease, Task, References, error)
	RenewLease(context.Context, WorkerLease, time.Time, time.Duration) (WorkerLease, error)
	ReleaseLease(context.Context, WorkerLease) error
	IssueGrant(context.Context, ID, Actor, ID, time.Time, time.Duration) (DownloadGrant, error)
	ReadArtifact(context.Context, Actor, DownloadGrant, time.Time) ([]byte, error)
}

type memoryLease struct {
	lease WorkerLease
}

type MemoryStore struct {
	mu       sync.Mutex
	tasks    map[ID]Task
	refs     map[ID]References
	byOwner  map[string]ID
	events   map[ID]Event
	contents map[ID][]byte
	leases   map[ID]memoryLease
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks: make(map[ID]Task), refs: make(map[ID]References),
		byOwner: make(map[string]ID), events: make(map[ID]Event),
		contents: make(map[ID][]byte), leases: make(map[ID]memoryLease),
	}
}

func ownerKey(owner Owner, key string) string {
	return owner.TenantID.String() + ":" + owner.SubjectID.String() + ":" + key
}

func digestBytes(body []byte) Digest { return Digest(sha256.Sum256(body)) }

func (s *MemoryStore) CreateTask(_ context.Context, input SubmitInput, refs References) (Task, error) {
	if err := refs.valid(); err != nil {
		return Task{}, ErrInvalidInput
	}
	task, err := New(input)
	if err != nil {
		return Task{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ownerKey(input.Owner, input.IdempotencyKey)
	if existingID, ok := s.byOwner[key]; ok {
		existing := s.tasks[existingID]
		if existing.requestDigest != input.RequestDigest || s.refs[existingID] != refs {
			return Task{}, ErrIdempotencyConflict
		}
		return existing, nil
	}
	if _, ok := s.tasks[input.TaskID]; ok {
		return Task{}, ErrIdempotencyConflict
	}
	s.tasks[input.TaskID], s.refs[input.TaskID], s.byOwner[key] = task, refs, input.TaskID
	return task, nil
}

func (s *MemoryStore) FindByIdempotency(_ context.Context, owner Owner, key string) (Task, References, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byOwner[ownerKey(owner, key)]
	if !ok {
		return Task{}, References{}, ErrNotFound
	}
	return s.tasks[id], s.refs[id], nil
}

func (s *MemoryStore) Snapshot(_ context.Context, id ID) (Task, References, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return Task{}, References{}, ErrNotFound
	}
	return task, s.refs[id], nil
}

type memoryTx struct {
	store      *MemoryStore
	task       Task
	refs       References
	events     map[ID]Event
	content    []byte
	putContent bool
}

func (tx *memoryTx) Task() Task             { return tx.task }
func (tx *memoryTx) References() References { return tx.refs }
func (tx *memoryTx) SaveTask(task Task) error {
	if err := task.valid(); err != nil {
		return err
	}
	if task.ID() != tx.task.ID() {
		return ErrInvalidTask
	}
	tx.task = task
	return nil
}
func (tx *memoryTx) AppendEvent(event Event) error {
	if event.TaskID() != tx.task.ID() {
		return ErrEventConflict
	}
	if old, ok := tx.events[event.EventID()]; ok {
		if old.Digest() != event.Digest() {
			return ErrEventConflict
		}
		return nil
	}
	tx.events[event.EventID()] = event
	return nil
}
func (tx *memoryTx) BindSettlement(id ID) error {
	if validateID(id) != nil {
		return ErrInvalidInput
	}
	if tx.refs.SettlementID != "" && tx.refs.SettlementID != id {
		return ErrSettlementBound
	}
	tx.refs.SettlementID = id
	return nil
}
func (tx *memoryTx) PutArtifact(body []byte) error {
	if tx.task.protocol != StateSucceeded || tx.task.artifactState != ArtifactAvailable {
		return ErrArtifactNotReady
	}
	if int64(len(body)) != tx.task.artifact.Size {
		return ErrArtifactDigest
	}
	d := digestBytes(body)
	if d != tx.task.artifact.SHA256 {
		return ErrArtifactDigest
	}
	tx.content = append([]byte(nil), body...)
	tx.putContent = true
	return nil
}

func (s *MemoryStore) WithTask(_ context.Context, id ID, fn func(TaskTx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return ErrNotFound
	}
	tx := &memoryTx{store: s, task: task, refs: s.refs[id], events: make(map[ID]Event, len(s.events))}
	for eventID, event := range s.events {
		tx.events[eventID] = event
	}
	if err := fn(tx); err != nil {
		return err
	}
	s.tasks[id], s.refs[id] = tx.task, tx.refs
	for eventID, event := range tx.events {
		s.events[eventID] = event
	}
	if tx.putContent {
		s.contents[id] = tx.content
	}
	return nil
}

func eligible(task Task, kind WorkKind) bool {
	switch kind {
	case WorkSubmit:
		return task.protocol == StatePrepared
	case WorkObserve:
		return !task.IsTerminal()
	case WorkCancel:
		return task.cancel == CancelRequested
	case WorkExpire:
		return !task.IsTerminal() || task.artifactState == ArtifactAvailable
	default:
		return false
	}
}

func (s *MemoryStore) Claim(_ context.Context, kind WorkKind, worker ID, now time.Time, ttl time.Duration, leaseID ID) (WorkerLease, Task, References, error) {
	if validateID(worker) != nil || validateID(leaseID) != nil || !validUTC(now) || ttl <= 0 || ttl > maxDownloadGrantTTL {
		return WorkerLease{}, Task{}, References{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, lease := range s.leases {
		if !now.Before(lease.lease.ExpiresAt) {
			delete(s.leases, id)
		}
	}
	for id, task := range s.tasks {
		if eligible(task, kind) {
			if _, held := s.leases[id]; held {
				continue
			}
			lease := WorkerLease{LeaseID: leaseID, TaskID: id, WorkerID: worker, Kind: kind, ExpiresAt: now.Add(ttl)}
			s.leases[id] = memoryLease{lease: lease}
			return lease, task, s.refs[id], nil
		}
	}
	return WorkerLease{}, Task{}, References{}, ErrNoWork
}

func (s *MemoryStore) RenewLease(_ context.Context, lease WorkerLease, now time.Time, ttl time.Duration) (WorkerLease, error) {
	if err := lease.valid(); err != nil || !validUTC(now) || ttl <= 0 {
		return WorkerLease{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.leases[lease.TaskID]
	if !ok || current.lease != lease || !now.Before(lease.ExpiresAt) {
		return WorkerLease{}, ErrLeaseConflict
	}
	lease.ExpiresAt = now.Add(ttl)
	s.leases[lease.TaskID] = memoryLease{lease: lease}
	return lease, nil
}

func (s *MemoryStore) ReleaseLease(_ context.Context, lease WorkerLease) error {
	if err := lease.valid(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.leases[lease.TaskID]
	if !ok || current.lease != lease {
		return ErrLeaseConflict
	}
	delete(s.leases, lease.TaskID)
	return nil
}

func (s *MemoryStore) IssueGrant(_ context.Context, id ID, actor Actor, grantID ID, now time.Time, ttl time.Duration) (DownloadGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok {
		return DownloadGrant{}, ErrNotFound
	}
	return task.IssueDownloadGrant(actor, grantID, now, ttl)
}

func (s *MemoryStore) ReadArtifact(_ context.Context, actor Actor, grant DownloadGrant, now time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[grant.TaskID]
	if !ok {
		return nil, ErrNotFound
	}
	if _, err := task.Download(actor, grant, now); err != nil {
		return nil, err
	}
	body, ok := s.contents[grant.TaskID]
	if !ok {
		return nil, ErrArtifactMissing
	}
	return append([]byte(nil), body...), nil
}
