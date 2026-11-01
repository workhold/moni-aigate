package catalog

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type CredentialPoolMember struct {
	CredentialID         string     `json:"credential_id"`
	Position             int        `json:"position"`
	Label                string     `json:"label"`
	Weight               int        `json:"weight"`
	MaxInflight          int        `json:"max_inflight"`
	RevokedAt            *time.Time `json:"revoked_at"`
	Primary              bool       `json:"primary"`
	ProcurementMode      string     `json:"procurement_mode"`
	ProcurementBindingID *string    `json:"procurement_binding_id"`
	ProcurementAccountID *string    `json:"procurement_account_id"`
}

type CredentialPoolView struct {
	ChannelID        string                 `json:"channel_id"`
	ChannelVersionID string                 `json:"channel_version_id"`
	CurrentVersion   string                 `json:"current_version"`
	Stale            bool                   `json:"stale"`
	Members          []CredentialPoolMember `json:"members"`
}

type CredentialPoolInput struct {
	ExpectedVersion string                      `json:"expected_version"`
	Members         []CredentialPoolMemberInput `json:"members"`
}

type CredentialPoolMemberInput struct {
	CredentialID string `json:"credential_id"`
	Secret       string `json:"secret"`
	Label        string `json:"label"`
	Weight       int    `json:"weight"`
	MaxInflight  int    `json:"max_inflight"`
}

type poolQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func credentialPoolView(ctx context.Context, q poolQuerier, channel, version string) (CredentialPoolView, error) {
	v := CredentialPoolView{Members: []CredentialPoolMember{}}
	rows, err := q.Query(ctx, `SELECT c.id,v.id,c.current_version,(v.id<>c.current_version OR NOT c.enabled OR c.quarantined),p.credential_id,p.position,p.label,p.weight,p.max_inflight,k.revoked_at,(p.credential_id=v.credential_id),CASE WHEN b.id IS NULL THEN 'legacy_unmanaged' ELSE 'managed' END,b.id,b.account_id FROM channels c JOIN channel_versions v ON v.channel_id=c.id AND v.id=COALESCE(NULLIF($2,''),c.current_version) JOIN channel_version_credentials p ON p.channel_version_id=v.id JOIN channel_credentials k ON k.id=p.credential_id LEFT JOIN credential_procurement_bindings b ON b.credential_id=k.id WHERE c.id=$1 ORDER BY p.position`, channel, version)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		m := CredentialPoolMember{}
		if err = rows.Scan(&v.ChannelID, &v.ChannelVersionID, &v.CurrentVersion, &v.Stale, &m.CredentialID, &m.Position, &m.Label, &m.Weight, &m.MaxInflight, &m.RevokedAt, &m.Primary, &m.ProcurementMode, &m.ProcurementBindingID, &m.ProcurementAccountID); err != nil {
			return CredentialPoolView{}, err
		}
		v.Members = append(v.Members, m)
	}
	if err = rows.Err(); err != nil {
		return CredentialPoolView{}, err
	}
	if len(v.Members) == 0 {
		return CredentialPoolView{}, pgx.ErrNoRows
	}
	return v, nil
}

// CredentialPool exposes only membership metadata, including historical versions.
// Revocation is visible separately; stale describes the channel/version state.
func (s *Store) CredentialPool(ctx context.Context, channel, version string) (CredentialPoolView, error) {
	return credentialPoolView(ctx, s.Pool, channel, version)
}

func validCredentialSecret(secret string) bool {
	if len(secret) == 0 || len(secret) > 4096 || !utf8.ValidString(secret) {
		return false
	}
	for _, r := range secret {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validCredentialLabel(label string) bool {
	if len(label) == 0 || len(label) > 64 || !utf8.ValidString(label) || strings.TrimSpace(label) == "" {
		return false
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type storedCredential struct {
	id, ciphertext string
	revokedAt      *time.Time
}

// Management operations lock the channel first, then credentials by ID. Keeping
// the identity of a live secret stable also keeps its concurrency budget stable.
func channelCredentialsForShare(ctx context.Context, tx pgx.Tx, channel string) (map[string]storedCredential, error) {
	rows, err := tx.Query(ctx, `SELECT id,secret_ciphertext,revoked_at FROM channel_credentials WHERE channel_id=$1 ORDER BY id FOR SHARE`, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	credentials := map[string]storedCredential{}
	for rows.Next() {
		var c storedCredential
		if err = rows.Scan(&c.id, &c.ciphertext, &c.revokedAt); err != nil {
			return nil, err
		}
		credentials[c.id] = c
	}
	return credentials, rows.Err()
}

func (s *Store) historicalSecrets(credentials map[string]storedCredential) (map[string]string, error) {
	secrets := make(map[string]string, len(credentials))
	for id, credential := range credentials {
		plain, err := s.Secrets.OpenFor(credential.ciphertext, "provider:platform", id)
		if err != nil {
			return nil, err
		}
		secrets[id] = plain
	}
	return secrets, nil
}

func insertPoolMember(ctx context.Context, tx pgx.Tx, channel, version string, m CredentialPoolMember) error {
	_, err := tx.Exec(ctx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,$4,$5,$6,$7)`, version, channel, m.CredentialID, m.Position, m.Label, m.Weight, m.MaxInflight)
	return err
}

func (s *Store) UpdateCredentialPool(ctx context.Context, actor, channel string, in CredentialPoolInput) (CredentialPoolView, error) {
	if in.ExpectedVersion == "" || len(in.Members) == 0 || len(in.Members) > 32 {
		return CredentialPoolView{}, ErrInvalid
	}
	for _, m := range in.Members {
		if (m.CredentialID == "") == (m.Secret == "") || !validCredentialLabel(m.Label) || m.Weight < 1 || m.Weight > 1_000_000 || m.MaxInflight < 1 || m.MaxInflight > 1000 || m.Secret != "" && !validCredentialSecret(m.Secret) {
			return CredentialPoolView{}, ErrInvalid
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return CredentialPoolView{}, err
	}
	defer tx.Rollback(ctx)
	var current string
	if err = tx.QueryRow(ctx, `SELECT current_version FROM channels WHERE id=$1 FOR UPDATE`, channel).Scan(&current); err != nil {
		return CredentialPoolView{}, err
	}
	if current != in.ExpectedVersion {
		return CredentialPoolView{}, ErrConflict
	}
	credentials, err := channelCredentialsForShare(ctx, tx, channel)
	if err != nil {
		return CredentialPoolView{}, err
	}
	historical, err := s.historicalSecrets(credentials)
	if err != nil {
		return CredentialPoolView{}, err
	}
	seenIDs, seenSecrets := map[string]bool{}, map[string]bool{}
	members := make([]CredentialPoolMember, 0, len(in.Members))
	for i, m := range in.Members {
		id, plain := m.CredentialID, m.Secret
		if id != "" {
			stored, ok := credentials[id]
			if !ok || stored.revokedAt != nil || seenIDs[id] {
				return CredentialPoolView{}, ErrInvalid
			}
			plain = historical[id]
		} else {
			for _, old := range historical {
				if plain == old {
					// Use the original live ID; a revoked secret cannot return.
					return CredentialPoolView{}, ErrInvalid
				}
			}
			id = uuid.NewString()
			ciphertext, sealErr := s.Secrets.SealFor(plain, "provider:platform", id)
			if sealErr != nil {
				return CredentialPoolView{}, sealErr
			}
			if _, err = tx.Exec(ctx, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,$3)`, id, channel, ciphertext); err != nil {
				return CredentialPoolView{}, err
			}
		}
		if seenSecrets[plain] {
			return CredentialPoolView{}, ErrInvalid
		}
		seenIDs[id], seenSecrets[plain] = true, true
		members = append(members, CredentialPoolMember{CredentialID: id, Position: i + 1, Label: m.Label, Weight: m.Weight, MaxInflight: m.MaxInflight, Primary: i == 0})
	}
	version := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO channel_versions(id,channel_id,base_url,provider,region,allowed_cidrs,credential_id) SELECT $1,channel_id,base_url,provider,region,allowed_cidrs,$2 FROM channel_versions WHERE id=$3 AND channel_id=$4`, version, members[0].CredentialID, current, channel); err != nil {
		return CredentialPoolView{}, err
	}
	for _, member := range members {
		if err = insertPoolMember(ctx, tx, channel, version, member); err != nil {
			return CredentialPoolView{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE channels SET current_version=$2 WHERE id=$1`, channel, version); err != nil {
		return CredentialPoolView{}, err
	}
	if err = audit(ctx, tx, actor, "channel.credential_pool", channel); err != nil {
		return CredentialPoolView{}, err
	}
	view, err := credentialPoolView(ctx, tx, channel, version)
	if err != nil {
		return CredentialPoolView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CredentialPoolView{}, err
	}
	return view, nil
}

// RevokeCredential is permanent and covers historical route snapshots as well.
func (s *Store) RevokeCredential(ctx context.Context, actor, channel, credential string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, `SELECT id FROM channels WHERE id=$1 FOR UPDATE`, channel).Scan(&id); err != nil {
		return err
	}
	var revoked *time.Time
	if err = tx.QueryRow(ctx, `SELECT revoked_at FROM channel_credentials WHERE id=$1 AND channel_id=$2 FOR UPDATE`, credential, channel).Scan(&revoked); err != nil {
		return err
	}
	if revoked == nil {
		if _, err = tx.Exec(ctx, `UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE id=$1`, credential); err != nil {
			return err
		}
		if err = audit(ctx, tx, actor, "channel.credential_revoke", credential); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
