package procurement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/security"
)

const accountColumns = `id,label,provider,state,limit_amount::text,held_amount::text,spent_amount::text,revision::text,created_at`
const commandColumns = `operation_id,actor,kind,target_id,status,reason,before_state,after_state,created_at`
const bindingColumns = `b.id,b.credential_id,b.channel_id,b.account_id,b.operation_id,b.bound_at,c.management_revision::text`

func canonicalID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}
func canonicalNumber(value string, positive bool) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n >= 0 && (!positive || n > 0) && strconv.FormatInt(n, 10) == value
}
func text(value string, max int) bool {
	if len(value) < 1 || len(value) > max || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func configuration(label, provider, state, limit string) bool {
	return text(label, 100) && text(provider, 100) && (state == "active" || state == "paused") && canonicalNumber(limit, false)
}
func scanAccount(row pgx.Row) (Account, error) {
	var value Account
	err := row.Scan(&value.ID, &value.Label, &value.Provider, &value.State, &value.LimitAmount, &value.HeldAmount, &value.SpentAmount, &value.Revision, &value.CreatedAt)
	return value, err
}
func scanCommand(row pgx.Row) (Command, error) {
	var value Command
	err := row.Scan(&value.OperationID, &value.Actor, &value.Kind, &value.TargetID, &value.Status, &value.Reason, &value.Before, &value.After, &value.CreatedAt)
	return value, err
}
func scanBinding(row pgx.Row) (Binding, error) {
	var value Binding
	err := row.Scan(&value.ID, &value.CredentialID, &value.ChannelID, &value.AccountID, &value.OperationID, &value.BoundAt, &value.ChannelRevision)
	return value, err
}
func (s *Store) Account(ctx context.Context, id string) (Account, error) {
	if !canonicalID(id) {
		return Account{}, ErrInvalid
	}
	return scanAccount(s.Pool.QueryRow(ctx, `SELECT `+accountColumns+` FROM procurement_accounts WHERE id=$1`, id))
}
func (s *Store) Command(ctx context.Context, id string) (Command, error) {
	if !canonicalID(id) {
		return Command{}, ErrInvalid
	}
	return scanCommand(s.Pool.QueryRow(ctx, `SELECT `+commandColumns+` FROM procurement_commands WHERE operation_id=$1`, id))
}

type commandRoot struct{ operation, actor, kind, digest string }

func lockCommand(ctx context.Context, tx pgx.Tx, actor, operation, kind, target string, payload any) (commandRoot, *Command, error) {
	root := commandRoot{operation: operation, actor: actor, kind: kind}
	encoded, err := json.Marshal(struct {
		Kind    string
		Target  string
		Payload any
	}{kind, target, payload})
	if err != nil {
		return root, nil, err
	}
	digest := sha256.Sum256(encoded)
	root.digest = hex.EncodeToString(digest[:])
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,19019))`, "procurement:"+operation); err != nil {
		return root, nil, err
	}
	var storedDigest string
	if err = tx.QueryRow(ctx, `SELECT input_digest FROM procurement_commands WHERE operation_id=$1`, operation).Scan(&storedDigest); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return root, nil, err
	}
	var previous *Command
	if err == nil {
		value, readErr := scanCommand(tx.QueryRow(ctx, `SELECT `+commandColumns+` FROM procurement_commands WHERE operation_id=$1`, operation))
		if readErr != nil {
			return root, nil, readErr
		}
		if value.Actor != actor || storedDigest != root.digest {
			return root, nil, ErrConflict
		}
		previous = &value
	}
	var role, status string
	if err = tx.QueryRow(ctx, `SELECT platform_role,status FROM users WHERE id=$1 FOR SHARE`, actor).Scan(&role, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return root, nil, ErrForbidden
		}
		return root, nil, err
	}
	if role != "admin" || status != "active" {
		return root, nil, ErrForbidden
	}
	return root, previous, nil
}
func saveCommand(ctx context.Context, tx pgx.Tx, root commandRoot, target, reason string, before, after any) (Command, error) {
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return Command{}, err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return Command{}, err
	}
	status := "applied"
	if reason != "" {
		status = "rejected"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO procurement_commands(operation_id,actor,kind,target_id,input_digest,status,reason,before_state,after_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, root.operation, root.actor, root.kind, target, root.digest, status, reason, beforeJSON, afterJSON); err != nil {
		return Command{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES($1,$2,$3,$4)`, root.actor, "procurement.command."+status, root.operation, json.RawMessage(`{"kind":"`+root.kind+`","status":"`+status+`"}`)); err != nil {
		return Command{}, err
	}
	return scanCommand(tx.QueryRow(ctx, `SELECT `+commandColumns+` FROM procurement_commands WHERE operation_id=$1`, root.operation))
}
func (s *Store) mutate(ctx context.Context, actor, operation, kind, target string, payload any, apply func(pgx.Tx, commandRoot) (Command, error)) (Command, error) {
	if !canonicalID(actor) || !canonicalID(operation) {
		return Command{}, ErrInvalid
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Command{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('lock_timeout','3s',true),set_config('statement_timeout','5s',true)`); err != nil {
		return Command{}, err
	}
	root, previous, err := lockCommand(ctx, tx, actor, operation, kind, target, payload)
	if err != nil {
		return Command{}, err
	}
	if previous != nil {
		return *previous, nil
	}
	result, err := apply(tx, root)
	if err != nil {
		return Command{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Command{}, err
	}
	return result, nil
}

func (s *Store) CreateAccount(ctx context.Context, actor string, in CreateInput) (Command, error) {
	if !configuration(in.Label, in.Provider, in.State, in.LimitAmount) {
		return Command{}, ErrInvalid
	}
	if s.prospective != nil {
		return s.prospective.create(ctx, actor, in)
	}
	return s.mutate(ctx, actor, in.OperationID, "account_create", "", in, func(tx pgx.Tx, root commandRoot) (Command, error) {
		id := uuid.NewString()
		value, err := scanAccount(tx.QueryRow(ctx, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount) VALUES($1,$2,$3,$4,$5::bigint) RETURNING `+accountColumns, id, in.Label, in.Provider, in.State, in.LimitAmount))
		if err != nil {
			return Command{}, err
		}
		return saveCommand(ctx, tx, root, id, "", nil, value)
	})
}
func (s *Store) ConfigureAccount(ctx context.Context, actor, id string, in ConfigurationInput) (Command, error) {
	if !canonicalID(id) || !canonicalNumber(in.ExpectedRevision, true) || !configuration(in.Label, in.Provider, in.State, in.LimitAmount) {
		return Command{}, ErrInvalid
	}
	if s.prospective != nil {
		return s.prospective.configure(ctx, actor, id, in)
	}
	return s.mutate(ctx, actor, in.OperationID, "account_configure", id, in, func(tx pgx.Tx, root commandRoot) (Command, error) {
		before, err := scanAccount(tx.QueryRow(ctx, `SELECT `+accountColumns+` FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return saveCommand(ctx, tx, root, id, "not_found", nil, nil)
		}
		if err != nil {
			return Command{}, err
		}
		if before.Revision != in.ExpectedRevision {
			return saveCommand(ctx, tx, root, id, "revision_conflict", before, nil)
		}
		after, err := scanAccount(tx.QueryRow(ctx, `UPDATE procurement_accounts SET label=$2,provider=$3,state=$4,limit_amount=$5::bigint WHERE id=$1 RETURNING `+accountColumns, id, in.Label, in.Provider, in.State, in.LimitAmount))
		if err != nil {
			return Command{}, err
		}
		return saveCommand(ctx, tx, root, id, "", before, after)
	})
}
func (s *Store) BindCredential(ctx context.Context, actor string, in BindingInput) (Command, error) {
	if !canonicalID(in.ChannelID) || !canonicalID(in.CredentialID) || !canonicalID(in.AccountID) || !canonicalNumber(in.ExpectedChannelRevision, true) {
		return Command{}, ErrInvalid
	}
	return s.mutate(ctx, actor, in.OperationID, "credential_bind", in.CredentialID, in, func(tx pgx.Tx, root commandRoot) (Command, error) {
		var revision string
		err := tx.QueryRow(ctx, `SELECT management_revision::text FROM channels WHERE id=$1 FOR UPDATE`, in.ChannelID).Scan(&revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return saveCommand(ctx, tx, root, in.CredentialID, "not_found", nil, nil)
		}
		if err != nil {
			return Command{}, err
		}
		if revision != in.ExpectedChannelRevision {
			return saveCommand(ctx, tx, root, in.CredentialID, "revision_conflict", nil, nil)
		}
		var revoked bool
		err = tx.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM channel_credentials WHERE id=$1 AND channel_id=$2 FOR SHARE`, in.CredentialID, in.ChannelID).Scan(&revoked)
		if errors.Is(err, pgx.ErrNoRows) {
			return saveCommand(ctx, tx, root, in.CredentialID, "not_found", nil, nil)
		}
		if err != nil {
			return Command{}, err
		}
		if revoked {
			return saveCommand(ctx, tx, root, in.CredentialID, "credential_revoked", nil, nil)
		}
		existing, err := scanBinding(tx.QueryRow(ctx, `SELECT `+bindingColumns+` FROM credential_procurement_bindings b JOIN channels c ON c.id=b.channel_id WHERE b.credential_id=$1`, in.CredentialID))
		if err == nil {
			return saveCommand(ctx, tx, root, in.CredentialID, "already_bound", existing, nil)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Command{}, err
		}
		var account string
		err = tx.QueryRow(ctx, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, in.AccountID).Scan(&account)
		if errors.Is(err, pgx.ErrNoRows) {
			return saveCommand(ctx, tx, root, in.CredentialID, "not_found", nil, nil)
		}
		if err != nil {
			return Command{}, err
		}
		id := uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO credential_procurement_bindings(id,credential_id,channel_id,account_id,operation_id) VALUES($1,$2,$3,$4,$5)`, id, in.CredentialID, in.ChannelID, in.AccountID, in.OperationID); err != nil {
			return Command{}, err
		}
		after, err := scanBinding(tx.QueryRow(ctx, `SELECT `+bindingColumns+` FROM credential_procurement_bindings b JOIN channels c ON c.id=b.channel_id WHERE b.id=$1`, id))
		if err != nil {
			return Command{}, err
		}
		return saveCommand(ctx, tx, root, in.CredentialID, "", nil, after)
	})
}
func normalizedTime(value time.Time) (time.Time, bool) {
	value = value.UTC().Truncate(time.Microsecond)
	return value, value.Year() >= 1 && value.Year() <= 9999
}
func (s *Store) AddObservation(ctx context.Context, actor, account string, in ObservationInput) (Command, error) {
	if !canonicalID(account) || !text(in.Source, 100) || !text(in.Semantic, 100) || !security.ValidReference(in.EvidenceRef, 256) || in.Currency != "CNY" || in.ObservedAt.IsZero() {
		return Command{}, ErrInvalid
	}
	if in.Status != "known" && in.Status != "unknown" && in.Status != "unsupported" && in.Status != "error" {
		return Command{}, ErrInvalid
	}
	if (in.Status == "known") != (in.Amount != nil) || in.Amount != nil && !canonicalNumber(*in.Amount, false) {
		return Command{}, ErrInvalid
	}
	var valid bool
	in.ObservedAt, valid = normalizedTime(in.ObservedAt)
	if !valid {
		return Command{}, ErrInvalid
	}
	if in.ProviderAsOf != nil {
		value, ok := normalizedTime(*in.ProviderAsOf)
		if !ok {
			return Command{}, ErrInvalid
		}
		in.ProviderAsOf = &value
	}
	return s.mutate(ctx, actor, in.OperationID, "balance_observe", account, in, func(tx pgx.Tx, root commandRoot) (Command, error) {
		var id string
		err := tx.QueryRow(ctx, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, account).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return saveCommand(ctx, tx, root, account, "not_found", nil, nil)
		}
		if err != nil {
			return Command{}, err
		}
		value := Observation{ID: uuid.NewString(), AccountID: account, OperationID: in.OperationID, Actor: actor, Status: in.Status, Amount: in.Amount, Currency: in.Currency, Source: in.Source, Semantic: in.Semantic, ObservedAt: in.ObservedAt, ProviderAsOf: in.ProviderAsOf, EvidenceRef: in.EvidenceRef}
		if err = tx.QueryRow(ctx, `INSERT INTO procurement_balance_observations(id,account_id,operation_id,actor,status,amount,currency,source,semantic,observed_at,provider_as_of,evidence_ref) VALUES($1,$2,$3,$4,$5,$6::bigint,$7,$8,$9,$10,$11,$12) RETURNING created_at`, value.ID, account, in.OperationID, actor, in.Status, in.Amount, in.Currency, in.Source, in.Semantic, in.ObservedAt, in.ProviderAsOf, in.EvidenceRef).Scan(&value.CreatedAt); err != nil {
			return Command{}, err
		}
		return saveCommand(ctx, tx, root, account, "", nil, value)
	})
}

func AccountListSQL() string { return `SELECT ` + accountColumns + ` FROM procurement_accounts` }
func BindingListSQL() string {
	return `SELECT b.id,b.credential_id,b.channel_id,b.account_id,b.operation_id,b.bound_at,b.bound_at AS created_at,c.management_revision::text AS channel_revision FROM credential_procurement_bindings b JOIN channels c ON c.id=b.channel_id`
}
func CommandListSQL() string {
	return `SELECT operation_id AS id,operation_id,actor,kind,target_id,status,reason,before_state AS before,after_state AS after,created_at FROM procurement_commands`
}
func ObservationListSQL() string {
	return `SELECT id,account_id,operation_id,actor,status,amount::text AS amount,currency,source,semantic,observed_at,provider_as_of,evidence_ref,created_at FROM procurement_balance_observations WHERE account_id=$1`
}
func EventsListSQL() string {
	return `SELECT id,event_key,account_id,attempt_id,held_delta::text AS held_delta,spent_delta::text AS spent_delta,type,evidence_ref,created_at FROM procurement_budget_events WHERE account_id=$1`
}
func ReservationsListSQL() string {
	return `SELECT p.attempt_id AS id,p.attempt_id,p.account_id,p.binding_id,p.input_bound::text AS input_bound,p.output_bound::text AS output_bound,p.upper_amount::text AS upper_amount,p.held_amount::text AS held_amount,p.spent_amount::text AS spent_amount,p.state,p.anomaly,p.revision::text AS revision,p.created_at,p.updated_at,a.tenant_id,a.request_id,a.channel_id FROM procurement_reservations p JOIN upstream_attempts a ON a.id=p.attempt_id WHERE p.account_id=$1`
}
