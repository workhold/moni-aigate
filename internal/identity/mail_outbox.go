package identity

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrMailOutbox = errors.New("mail_outbox_unavailable")
var ErrMailOutboxInput = errors.New("mail_outbox_invalid")

const mailProofActiveSQL = `c.consumed_at IS NULL AND c.revoked_at IS NULL AND c.expires_at>clock_timestamp()
 AND (c.purpose='registration' OR (u.id IS NOT NULL AND u.status='active' AND u.password_revision=c.password_revision))`

func mailUUID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v != uuid.Nil && v.String() == id
}

// ClaimMail locks only outbox rows. In particular, its JOIN must not acquire
// challenge or user locks in the opposite order to proof consumption/cleanup.
func (s *Store) ClaimMail(ctx context.Context, enabled MailPurposes, limit int) ([]MailClaim, error) {
	if limit < 1 || limit > 2 {
		return nil, ErrMailOutboxInput
	}
	claims := []MailClaim{}
	if !enabled.Registration && !enabled.PasswordReset {
		return claims, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, ErrMailOutbox
	}
	defer rollbackMail(tx)
	rows, err := tx.Query(ctx, `SELECT o.id::text,c.purpose
 FROM identity_mail_outbox o JOIN identity_challenges c ON c.id=o.challenge_id
 LEFT JOIN users u ON u.id=c.user_id
 WHERE (($1 AND c.purpose='registration') OR ($2 AND c.purpose='password_reset'))
 AND o.attempt_count<5 AND ((o.state='pending' AND o.next_attempt_at<=clock_timestamp())
  OR (o.state='sending' AND o.lease_until<=clock_timestamp()))
 AND `+mailProofActiveSQL+`
 ORDER BY CASE WHEN o.state='sending' THEN o.lease_until ELSE o.next_attempt_at END,o.id
 LIMIT $3 FOR UPDATE OF o SKIP LOCKED`, enabled.Registration, enabled.PasswordReset, limit)
	if err != nil {
		return nil, ErrMailOutbox
	}
	for rows.Next() {
		var claim MailClaim
		if err = rows.Scan(&claim.ID, &claim.Purpose); err != nil {
			rows.Close()
			return nil, ErrMailOutbox
		}
		claims = append(claims, claim)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, ErrMailOutbox
	}
	for i := range claims {
		err = tx.QueryRow(ctx, `UPDATE identity_mail_outbox
 SET state='sending',attempt_count=attempt_count+1,claim_generation=claim_generation+1,
 lease_until=clock_timestamp()+interval '60 seconds'
 WHERE id=$1 RETURNING claim_generation,attempt_count,lease_until`, claims[i].ID).
			Scan(&claims[i].Generation, &claims[i].Attempt, &claims[i].LeaseUntil)
		if err != nil {
			return nil, ErrMailOutbox
		}
	}
	if tx.Commit(ctx) != nil {
		return nil, ErrMailOutbox
	}
	return claims, nil
}

type mailPayload struct {
	Version int    `json:"v"`
	To      string `json:"to"`
	Purpose string `json:"purpose"`
	Link    string `json:"link"`
}

func decodeMailPayload(raw string) (mailPayload, bool) {
	var payload mailPayload
	if len(raw) > 4096 || !utf8.ValidString(raw) {
		return payload, false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return payload, false
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] || (name != "v" && name != "to" && name != "purpose" && name != "link") {
			return payload, false
		}
		seen[name] = true
		var field json.RawMessage
		if d.Decode(&field) != nil {
			return payload, false
		}
	}
	if last, err := d.Token(); err != nil || last != json.Delim('}') || len(seen) != 4 {
		return payload, false
	}
	if _, err = d.Token(); err != io.EOF {
		return payload, false
	}
	if json.Unmarshal([]byte(raw), &payload) != nil || payload.Version != 1 || len(payload.To) > 254 || payload.To != strings.ToLower(strings.TrimSpace(payload.To)) {
		return payload, false
	}
	address, err := mail.ParseAddress(payload.To)
	if err != nil || address.Name != "" || address.Address != payload.To {
		return payload, false
	}
	for _, r := range payload.To {
		if r < 33 || r > 126 {
			return payload, false
		}
	}
	return payload, payload.Purpose == "registration" || payload.Purpose == "password_reset"
}

func mailPayloadToken(link string) (string, bool) {
	u, err := url.Parse(link)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.RawPath != "" || u.RawFragment != "" || !strings.HasPrefix(u.Fragment, "token=ic1_") {
		return "", false
	}
	token := strings.TrimPrefix(u.Fragment, "token=")
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "ic1_"))
	return token, err == nil && len(raw) == 32 && "ic1_"+base64.RawURLEncoding.EncodeToString(raw) == token
}

func (s *Store) PrepareMail(ctx context.Context, claim MailClaim) (MailPreparation, error) {
	if !mailUUID(claim.ID) || claim.Generation < 1 || claim.Generation > 5 {
		return MailPreparation{}, ErrMailOutboxInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var state, purpose, emailDigest, tokenDigest string
	var ciphertext, userEmail *string
	var generation, remainingMicros int64
	var active, bounded bool
	err := s.Pool.QueryRow(ctx, `SELECT o.state,o.claim_generation,c.purpose,
 (`+mailProofActiveSQL+`),
 coalesce(floor(extract(epoch FROM (o.lease_until-clock_timestamp()))*1000000)::bigint,0),
 CASE WHEN octet_length(o.payload_ciphertext)<=8192 THEN o.payload_ciphertext END,
 coalesce(octet_length(o.payload_ciphertext)<=8192,false),s.email_digest,c.token_digest,u.email
 FROM identity_mail_outbox o JOIN identity_challenges c ON c.id=o.challenge_id
 JOIN identity_mail_subjects s ON s.id=c.subject_id LEFT JOIN users u ON u.id=c.user_id
 WHERE o.id=$1`, claim.ID).Scan(&state, &generation, &purpose, &active, &remainingMicros, &ciphertext, &bounded, &emailDigest, &tokenDigest, &userEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return MailPreparation{Status: "stale"}, nil
	}
	if err != nil {
		return MailPreparation{}, ErrMailOutbox
	}
	if state != "sending" || generation != claim.Generation || purpose != claim.Purpose {
		return MailPreparation{Status: "stale"}, nil
	}
	if !active {
		return MailPreparation{Status: "cancelled", Code: "mail_proof_inactive"}, nil
	}
	if remainingMicros < int64(25*time.Second/time.Microsecond) {
		return MailPreparation{Status: "lease_short", Code: "mail_network"}, nil
	}
	if ciphertext == nil || !bounded || s.Secrets == nil {
		return MailPreparation{Status: "dead", Code: "mail_decrypt"}, nil
	}
	plaintext, err := s.Secrets.OpenFor(*ciphertext, "identity-mail-outbox", claim.ID)
	if err != nil {
		return MailPreparation{Status: "dead", Code: "mail_decrypt"}, nil
	}
	payload, valid := decodeMailPayload(plaintext)
	token, tokenValid := mailPayloadToken(payload.Link)
	if !valid || payload.Purpose != purpose || !tokenValid ||
		subtle.ConstantTimeCompare([]byte(s.Secrets.Digest("identity-mail-subject:v1:"+purpose+"\x00"+payload.To)), []byte(emailDigest)) != 1 ||
		subtle.ConstantTimeCompare([]byte(s.Secrets.Digest("identity-challenge:v1:"+purpose+":"+token)), []byte(tokenDigest)) != 1 ||
		(purpose == "password_reset" && (userEmail == nil || strings.ToLower(strings.TrimSpace(*userEmail)) != payload.To)) {
		return MailPreparation{Status: "dead", Code: "mail_payload_invalid"}, nil
	}
	return MailPreparation{Status: "ready", Delivery: MailDelivery{ID: claim.ID, To: payload.To, Purpose: purpose, Link: payload.Link}, RemainingLease: time.Duration(remainingMicros) * time.Microsecond}, nil
}

func mailSafeCode(code string) bool {
	switch code {
	case "mail_configuration", "mail_payload_invalid", "mail_rejected", "mail_temporary", "mail_network", "mail_tls", "mail_tls_required", "mail_decrypt", "mail_attempts_exhausted", "mail_proof_inactive":
		return true
	}
	return false
}

// FinishMail never obtains challenge/subject/user locks. A late ACK can only
// change its own still-current generation; successful SMTP does not prove inbox
// delivery, and a stale ACK never overwrites a reclaimed or terminal message.
func (s *Store) FinishMail(ctx context.Context, claim MailClaim, result MailResult) (bool, error) {
	if !mailUUID(claim.ID) || claim.Generation < 1 || claim.Generation > 5 ||
		(result.Outcome != "accepted" && result.Outcome != "retry" && result.Outcome != "dead" && result.Outcome != "cancelled") ||
		(result.Outcome == "accepted" && result.Code != "") || (result.Outcome != "accepted" && !mailSafeCode(result.Code)) {
		return false, ErrMailOutboxInput
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tag, err := s.Pool.Exec(ctx, `WITH proof AS MATERIALIZED (
 SELECT c.id,(`+mailProofActiveSQL+`) AS active FROM identity_challenges c
 LEFT JOIN users u ON u.id=c.user_id JOIN identity_mail_outbox candidate ON candidate.challenge_id=c.id
 WHERE candidate.id=$1)
 UPDATE identity_mail_outbox o SET
 state=CASE WHEN $3='accepted' THEN 'sent' WHEN $3='retry' AND NOT proof.active THEN 'cancelled'
  WHEN $3='retry' AND attempt_count<5 THEN 'pending'
  WHEN $3='cancelled' THEN 'cancelled' ELSE 'dead' END,
 payload_ciphertext=CASE WHEN $3='retry' AND attempt_count<5 AND proof.active THEN payload_ciphertext ELSE NULL END,
 lease_until=NULL,sent_at=CASE WHEN $3='accepted' THEN clock_timestamp() ELSE NULL END,
 next_attempt_at=CASE WHEN $3='retry' AND attempt_count<5 AND proof.active THEN clock_timestamp()+
  CASE attempt_count WHEN 1 THEN interval '5 seconds' WHEN 2 THEN interval '30 seconds'
   WHEN 3 THEN interval '2 minutes' ELSE interval '5 minutes' END ELSE next_attempt_at END,
 last_error_code=CASE WHEN $3='accepted' THEN '' WHEN $3='retry' AND NOT proof.active THEN 'mail_proof_inactive'
  WHEN $3='retry' AND attempt_count>=5 THEN 'mail_attempts_exhausted' ELSE $4 END
 FROM proof WHERE o.id=$1 AND o.challenge_id=proof.id AND o.state='sending' AND o.claim_generation=$2`, claim.ID, claim.Generation, result.Outcome, result.Code)
	if err != nil {
		return false, ErrMailOutbox
	}
	return tag.RowsAffected() == 1, nil
}
