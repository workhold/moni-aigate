package paymentreceiver

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/paymentruntime"
)

type Repository struct{ runtime *paymentruntime.Runtime }

func newRepository(rt *paymentruntime.Runtime) (*Repository, error) {
	if rt == nil || rt.Component() != paymentruntime.Receiver || rt.Pool() == nil || rt.Keyring() == nil {
		return nil, failure("receiver_unavailable")
	}
	return &Repository{runtime: rt}, nil
}

// verifiedEnvelope can only be constructed by the transaction-local verifier.
type verifiedEnvelope struct {
	notice  paymentproto.VerifiedNotification
	headers paymentproto.SignedHeaders
	dbNow   time.Time
	tx      pgx.Tx
}

func verifyTx(ctx context.Context, tx pgx.Tx, env paymentproto.Environment, keys *paymentproto.Keyring, headers paymentproto.SignedHeaders, raw []byte) (verifiedEnvelope, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&now); err != nil {
		return verifiedEnvelope{}, err
	}
	v, err := keys.VerifyNotification(env, now.UTC(), headers, raw)
	if err != nil {
		return verifiedEnvelope{}, err
	}
	return verifiedEnvelope{notice: v, headers: headers, dbNow: now.UTC(), tx: tx}, nil
}
func receiverLockKey(env, event string) int32 {
	parts := []string{"moni-payment-lock-v1", "receiver", env, "receiver-event", event}
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return int32(binary.BigEndian.Uint32(h[:4]))
}
func rollback(tx pgx.Tx) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return tx.Rollback(ctx)
}
func retryable(err error) bool {
	var p *pgconn.PgError
	return errors.As(err, &p) && (p.Code == "40P01" || p.Code == "40001")
}

// Receive does not accept caller-constructed trusted notifications. Verification
// uses the very transaction clock that is saved with a new inbox identity.
func (r *Repository) Receive(ctx context.Context, h paymentproto.SignedHeaders, raw []byte) (ReceiveOutcome, error) {
	if r == nil || r.runtime == nil || len(raw) == 0 || len(raw) > 65536 {
		return ReceiveOutcome{}, failure("invalid_request")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body := append([]byte(nil), raw...)
	// Stable identities across a confirmed rollback retry of this logical call.
	inboxID, receiptID, conflictID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for attempt := 0; attempt < 3; attempt++ {
		if err := r.runtime.Check(ctx); err != nil {
			return ReceiveOutcome{}, failure("mock_instance_unavailable")
		}
		conn, err := r.runtime.Pool().Acquire(ctx)
		if err != nil {
			return ReceiveOutcome{}, failure("receiver_unavailable")
		}
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			conn.Release()
			return ReceiveOutcome{}, failure("receiver_unavailable")
		}
		v, err := verifyTx(ctx, tx, r.runtime.Environment(), r.runtime.Keyring(), h, body)
		var out ReceiveOutcome
		if err == nil {
			out, err = writeVerifiedTx(ctx, tx, v, inboxID, receiptID, conflictID)
		}
		if err != nil {
			cleanupErr := rollback(tx)
			clean := (cleanupErr == nil || errors.Is(cleanupErr, pgx.ErrTxClosed)) && !conn.Conn().IsClosed() && conn.Conn().PgConn().TxStatus() == 'I'
			if !clean {
				c := conn.Hijack()
				cleanup, stop := context.WithTimeout(context.Background(), time.Second)
				_ = c.Close(cleanup)
				stop()
				return ReceiveOutcome{}, failure("receiver_unavailable")
			}
			conn.Release()
			if retryable(err) && attempt < 2 {
				continue
			}
			var p *paymentproto.Error
			if errors.As(err, &p) {
				return ReceiveOutcome{}, p
			}
			return ReceiveOutcome{}, failure("receiver_unavailable")
		}
		err = tx.Commit(ctx)
		if err == nil {
			conn.Release()
			return out, nil
		}
		var p *pgconn.PgError
		if errors.Is(err, pgx.ErrTxCommitRollback) || (errors.As(err, &p) && len(p.Code) >= 2 && p.Code[:2] != "08") {
			cleanupErr := rollback(tx)
			clean := (cleanupErr == nil || errors.Is(cleanupErr, pgx.ErrTxClosed)) && !conn.Conn().IsClosed() && conn.Conn().PgConn().TxStatus() == 'I'
			if !clean {
				c := conn.Hijack()
				cleanup, stop := context.WithTimeout(context.Background(), time.Second)
				_ = c.Close(cleanup)
				stop()
				return ReceiveOutcome{}, failure("receiver_unavailable")
			}
			conn.Release()
			if retryable(err) && attempt < 2 {
				continue
			}
			return ReceiveOutcome{}, failure("receiver_unavailable")
		}
		// Never recycle a connection with an ambiguous COMMIT result.
		c := conn.Hijack()
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		_ = c.Close(cleanup)
		stop()
		return ReceiveOutcome{}, failure("receiver_outcome_unknown")
	}
	return ReceiveOutcome{}, failure("receiver_unavailable")
}

func writeVerifiedTx(ctx context.Context, tx pgx.Tx, v verifiedEnvelope, inboxID, receiptID, conflictID string) (ReceiveOutcome, error) {
	if v.tx != tx || v.dbNow.IsZero() {
		return ReceiveOutcome{}, failure("receiver_unavailable")
	}
	canonical, err := v.notice.Canonical()
	if err != nil {
		return ReceiveOutcome{}, err
	}
	n, err := paymentproto.ParseNotification(canonical)
	if err != nil {
		return ReceiveOutcome{}, err
	}
	digest := paymentproto.RequestDigest(canonical)
	// This gate is independent of mock ownership and all application lock spaces.
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(782193415,$1)`, receiverLockKey(n.EnvironmentID, n.MerchantID+"\x00"+n.EventID))
	if err != nil {
		return ReceiveOutcome{}, err
	}
	var oldID, oldDigest string
	var receipt []byte
	err = tx.QueryRow(ctx, `SELECT i.id,i.business_digest,r.receipt_data FROM receiver_inbox i JOIN receiver_receipts r ON r.inbox_id=i.id WHERE i.environment_id=$1 AND i.merchant_id=$2 AND i.event_id=$3`, n.EnvironmentID, n.MerchantID, n.EventID).Scan(&oldID, &oldDigest, &receipt)
	if err == nil {
		if oldDigest == digest {
			d, e := parseReceipt(receipt, n.EventID)
			return ReceiveOutcome{Receipt: d, Status: ReceiveDuplicate}, e
		}
		seconds, e := strconv.ParseInt(v.headers.DeliveryTime, 10, 64)
		if e != nil {
			return ReceiveOutcome{}, e
		}
		_, err = tx.Exec(ctx, `INSERT INTO receiver_conflicts(id,inbox_id,observed_digest,delivery_id,delivery_time,signing_key_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(inbox_id,delivery_id,observed_digest) DO NOTHING`, conflictID, oldID, digest, v.headers.DeliveryID, seconds, v.headers.KeyID)
		return ReceiveOutcome{Status: ReceiveConflict}, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ReceiveOutcome{}, err
	}
	seconds, err := strconv.ParseInt(v.headers.DeliveryTime, 10, 64)
	if err != nil {
		return ReceiveOutcome{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO receiver_inbox(id,environment_id,merchant_id,event_id,business_digest,canonical_body,first_delivery_id,delivery_time,signing_key_id,verified_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, inboxID, n.EnvironmentID, n.MerchantID, n.EventID, digest, canonical, v.headers.DeliveryID, seconds, v.headers.KeyID, v.dbNow)
	if err != nil {
		return ReceiveOutcome{}, err
	}
	d := ReceiptData{ReceiptID: receiptID, EventID: n.EventID, Status: "received", Environment: "sandbox", ReceivedAt: v.dbNow.Format(utc6)}
	data, err := marshalReceipt(d)
	if err != nil {
		return ReceiveOutcome{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO receiver_receipts(inbox_id,receipt_id,received_at,receipt_data,receipt_digest) VALUES($1,$2,$3,$4,$5)`, inboxID, receiptID, v.dbNow, data, paymentproto.RequestDigest(data))
	return ReceiveOutcome{Receipt: d, Status: ReceiveNew}, err
}
