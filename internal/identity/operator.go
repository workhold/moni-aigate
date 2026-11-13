package identity

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/security"
)

// OperatorActor is the audit actor for host-side account recovery. These
// operations exist because the console cannot recover an account whose owner has
// lost the password or the second factor, and self-service mail recovery is off
// by default. They are reached only through the `moni-aigate user` command by a
// person who already holds the application database credential.
const OperatorActor = "operator-cli"

var ErrLastAdmin = errors.New("refusing to suspend the last active administrator")
var ErrNotFound = errors.New("user not found")

type OperatorUser struct {
	ID, Email, PlatformRole, Status string
	MFAEnabled                      bool
}

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") || len(email) > 254 {
		return "", ErrInvalid
	}
	return email, nil
}

// lockUserByEmail returns the user id with the row locked for the transaction.
func lockUserByEmail(ctx context.Context, tx pgx.Tx, email string) (id, role, status string, err error) {
	err = tx.QueryRow(ctx, `SELECT id,platform_role,status FROM users WHERE email=$1 FOR UPDATE`, email).Scan(&id, &role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", ErrNotFound
	}
	return
}

func revokeAllSessions(ctx context.Context, tx pgx.Tx, user string) error {
	_, err := tx.Exec(ctx, `UPDATE session_families SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL`, user)
	return err
}

func (s *Store) OperatorListUsers(ctx context.Context) ([]OperatorUser, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,email,platform_role,status,mfa_enabled FROM users ORDER BY created_at,id LIMIT 1000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OperatorUser
	for rows.Next() {
		var u OperatorUser
		if err = rows.Scan(&u.ID, &u.Email, &u.PlatformRole, &u.Status, &u.MFAEnabled); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// OperatorSetPassword replaces the password and ends every session of the user.
func (s *Store) OperatorSetPassword(ctx context.Context, email, password string) error {
	email, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	if len(password) < 12 || len(password) > 72 {
		return ErrInvalid
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return ErrInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	id, _, _, err := lockUserByEmail(ctx, tx, email)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, id, hash); err != nil {
		return err
	}
	if err = revokeAllSessions(ctx, tx, id); err != nil {
		return err
	}
	if err = audit(ctx, tx, OperatorActor, "", "user.password_set_by_operator", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// OperatorResetMFA removes the second factor and its recovery codes and ends
// every session. When platform MFA is enforced the user must enrol again at the
// next login; otherwise MFA simply stays optional as for any new account.
func (s *Store) OperatorResetMFA(ctx context.Context, email string) error {
	email, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	id, _, _, err := lockUserByEmail(ctx, tx, email)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET mfa_enabled=false,mfa_secret='',mfa_pending_secret='',mfa_last_step=-1 WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id=$1`, id); err != nil {
		return err
	}
	if err = revokeAllSessions(ctx, tx, id); err != nil {
		return err
	}
	if err = audit(ctx, tx, OperatorActor, "", "user.mfa_reset_by_operator", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// OperatorSetStatus suspends or reactivates a user. Suspending also ends every
// session. The last active administrator can never be suspended, so an operator
// cannot lock the whole console out by mistake.
func (s *Store) OperatorSetStatus(ctx context.Context, email string, suspend bool) error {
	email, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Lock every active admin in id order first so two concurrent suspensions
	// cannot each see "another admin remains".
	if suspend {
		if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE platform_role='admin' AND status='active' ORDER BY id FOR UPDATE`); err != nil {
			return err
		}
	}
	id, role, status, err := lockUserByEmail(ctx, tx, email)
	if err != nil {
		return err
	}
	want := "active"
	if suspend {
		want = "suspended"
	}
	if status == want {
		return tx.Commit(ctx) // idempotent
	}
	if suspend && role == "admin" {
		var others int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE platform_role='admin' AND status='active' AND id<>$1`, id).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return ErrLastAdmin
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET status=$2 WHERE id=$1`, id, want); err != nil {
		return err
	}
	if suspend {
		if err = revokeAllSessions(ctx, tx, id); err != nil {
			return err
		}
	}
	action := "user.activate_by_operator"
	if suspend {
		action = "user.suspend_by_operator"
	}
	if err = audit(ctx, tx, OperatorActor, "", action, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
