package identity

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func rollbackMail(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// CleanupMail selects a bounded set without locks, then obtains every parent
// lock before its children: subjects, challenges, outboxes, each sorted by ID.
// It never locks a user/tenant or consumes/revokes an otherwise active proof.
func (s *Store) CleanupMail(ctx context.Context, limit int) (MailCleanup, error) {
	var result MailCleanup
	if limit < 1 || limit > 100 {
		return result, ErrMailOutboxInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return result, ErrMailOutbox
	}
	defer rollbackMail(tx)
	// Separate challenge and childless-subject bounds keep old/high-volume
	// subjects from causing an unbounded number of row locks or Go allocations.
	rows, err := tx.Query(ctx, `SELECT c.id::text,c.subject_id::text
 FROM identity_challenges c LEFT JOIN identity_mail_outbox o ON o.challenge_id=c.id
 LEFT JOIN users u ON u.id=c.user_id WHERE
 (c.registration_email_ciphertext IS NOT NULL AND
  (c.consumed_at IS NOT NULL OR c.revoked_at IS NOT NULL OR c.expires_at<=clock_timestamp()))
 OR (o.state IN ('pending','sending') AND (NOT (`+mailProofActiveSQL+`)
  OR (o.state='sending' AND o.attempt_count>=5 AND o.lease_until<=clock_timestamp())))
 OR (o.state IN ('sent','dead','cancelled') AND o.created_at<=clock_timestamp()-interval '7 days')
 OR (o.id IS NULL AND c.created_at<=clock_timestamp()-interval '7 days'
  AND (c.consumed_at IS NOT NULL OR c.revoked_at IS NOT NULL OR c.expires_at<=clock_timestamp()))
 ORDER BY c.subject_id,c.id LIMIT $1`, limit)
	if err != nil {
		return result, ErrMailOutbox
	}
	var candidateChallenges, candidateSubjects []string
	for rows.Next() {
		var challenge, subject string
		if rows.Scan(&challenge, &subject) != nil {
			rows.Close()
			return result, ErrMailOutbox
		}
		candidateChallenges = append(candidateChallenges, challenge)
		candidateSubjects = append(candidateSubjects, subject)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, ErrMailOutbox
	}
	rows, err = tx.Query(ctx, `SELECT s.id::text FROM identity_mail_subjects s
 WHERE s.last_seen_at<=clock_timestamp()-interval '24 hours'
 AND NOT EXISTS(SELECT 1 FROM identity_challenges c WHERE c.subject_id=s.id)
 ORDER BY s.id LIMIT $1`, limit)
	if err != nil {
		return result, ErrMailOutbox
	}
	for rows.Next() {
		var subject string
		if rows.Scan(&subject) != nil {
			rows.Close()
			return result, ErrMailOutbox
		}
		candidateSubjects = append(candidateSubjects, subject)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, ErrMailOutbox
	}
	lockedSubjects, err := mailLockIDs(ctx, tx, `SELECT id::text FROM identity_mail_subjects
 WHERE id=ANY($1::uuid[]) ORDER BY id FOR UPDATE SKIP LOCKED`, candidateSubjects)
	if err != nil {
		return result, ErrMailOutbox
	}
	rows, err = tx.Query(ctx, `SELECT id::text FROM identity_challenges
 WHERE id=ANY($1::uuid[]) AND subject_id=ANY($2::uuid[]) ORDER BY id FOR UPDATE SKIP LOCKED`, candidateChallenges, lockedSubjects)
	if err != nil {
		return result, ErrMailOutbox
	}
	var lockedChallenges []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return result, ErrMailOutbox
		}
		lockedChallenges = append(lockedChallenges, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, ErrMailOutbox
	}
	lockedOutboxes, err := mailLockIDs(ctx, tx, `SELECT id::text FROM identity_mail_outbox
 WHERE challenge_id=ANY($1::uuid[]) ORDER BY id FOR UPDATE SKIP LOCKED`, lockedChallenges)
	if err != nil {
		return result, ErrMailOutbox
	}
	// Locks are already held in parent-first order. Deletes must subsequently
	// run child-first to satisfy the original non-cascading foreign keys.
	tag, err := tx.Exec(ctx, `UPDATE identity_challenges SET registration_email_ciphertext=NULL
 WHERE id=ANY($1::uuid[]) AND registration_email_ciphertext IS NOT NULL
 AND (consumed_at IS NOT NULL OR revoked_at IS NOT NULL OR expires_at<=clock_timestamp())`, lockedChallenges)
	if err != nil {
		return result, ErrMailOutbox
	}
	result.SecretsCleared = int(tag.RowsAffected())
	rows, err = tx.Query(ctx, `UPDATE identity_mail_outbox o SET
 state=CASE WHEN NOT (`+mailProofActiveSQL+`) THEN 'cancelled' ELSE 'dead' END,
 payload_ciphertext=NULL,lease_until=NULL,
 last_error_code=CASE WHEN NOT (`+mailProofActiveSQL+`) THEN 'mail_proof_inactive' ELSE 'mail_attempts_exhausted' END
 FROM identity_challenges c LEFT JOIN users u ON u.id=c.user_id
 WHERE o.id=ANY($1::uuid[]) AND c.id=o.challenge_id AND o.state IN ('pending','sending')
 AND (NOT (`+mailProofActiveSQL+`) OR (o.state='sending' AND o.attempt_count>=5 AND o.lease_until<=clock_timestamp()))
 RETURNING o.state`, lockedOutboxes)
	if err != nil {
		return result, ErrMailOutbox
	}
	for rows.Next() {
		var state string
		if rows.Scan(&state) != nil {
			rows.Close()
			return result, ErrMailOutbox
		}
		if state == "cancelled" {
			result.Cancelled++
		} else {
			result.Dead++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, ErrMailOutbox
	}
	tag, err = tx.Exec(ctx, `DELETE FROM identity_mail_outbox WHERE id=ANY($1::uuid[])
 AND state IN ('sent','dead','cancelled') AND created_at<=clock_timestamp()-interval '7 days'`, lockedOutboxes)
	if err != nil {
		return result, ErrMailOutbox
	}
	result.OutboxesDeleted = int(tag.RowsAffected())
	tag, err = tx.Exec(ctx, `DELETE FROM identity_challenges c WHERE c.id=ANY($1::uuid[])
 AND c.created_at<=clock_timestamp()-interval '7 days'
 AND (c.consumed_at IS NOT NULL OR c.revoked_at IS NOT NULL OR c.expires_at<=clock_timestamp())
 AND NOT EXISTS(SELECT 1 FROM identity_mail_outbox o WHERE o.challenge_id=c.id)`, lockedChallenges)
	if err != nil {
		return result, ErrMailOutbox
	}
	result.ChallengesDeleted = int(tag.RowsAffected())
	tag, err = tx.Exec(ctx, `DELETE FROM identity_mail_subjects s WHERE s.id=ANY($1::uuid[])
 AND s.last_seen_at<=clock_timestamp()-interval '24 hours'
 AND NOT EXISTS(SELECT 1 FROM identity_challenges c WHERE c.subject_id=s.id)`, lockedSubjects)
	if err != nil {
		return result, ErrMailOutbox
	}
	result.SubjectsDeleted = int(tag.RowsAffected())
	if tx.Commit(ctx) != nil {
		return MailCleanup{}, ErrMailOutbox
	}
	return result, nil
}

func mailLockIDs(ctx context.Context, tx pgx.Tx, query string, candidates []string) ([]string, error) {
	rows, err := tx.Query(ctx, query, candidates)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
