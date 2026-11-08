package tasks

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

// Worker executes one leased task operation. It deliberately creates no
// financial fact by itself. Submit/observe/cancel must be connected to the
// existing request and settlement writer by the caller.
type Worker struct {
	Store   Store
	Adapter provider.TaskAdapter
	Now     func() time.Time
}

func (w Worker) RunOnce(ctx context.Context, kind WorkKind, workerID ID) error {
	if w.Store == nil || w.Adapter == nil || w.Now == nil {
		return ErrInvalidInput
	}
	now := w.Now().UTC()
	lease, task, _, err := w.Store.Claim(ctx, kind, workerID, now, 30*time.Second, ID(uuid.NewString()))
	if err != nil {
		return err
	}
	defer w.Store.ReleaseLease(ctx, lease)
	view, err := task.Snapshot()
	if err != nil {
		return err
	}
	permissions := PermissionTaskObserve | PermissionTaskRead | PermissionTaskCancel
	actor := Actor{TenantID: view.Owner.TenantID, SubjectID: view.Owner.SubjectID, Permission: permissions}
	service, err := NewService(ServiceOptions{Store: w.Store, Adapter: w.Adapter, NewID: func() ID { return ID(uuid.NewString()) }, Worker: actor})
	if err != nil {
		return err
	}
	switch kind {
	case WorkSubmit:
		_, err = service.Submit(ctx, task.ID(), now)
	case WorkObserve:
		_, err = service.Observe(ctx, task.ID(), now)
	case WorkCancel:
		_, err = service.Cancel(ctx, actor, task.ID(), now)
	case WorkExpire:
		_, err = service.Expire(ctx, task.ID(), now)
	}
	return err
}
