package catalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	ReleaseTestOnly   = "test_only"
	ReleaseCommercial = "commercial"
)

type ReleaseError struct{ Code string }

func (e *ReleaseError) Error() string { return e.Code }
func releaseError(code string) error  { return &ReleaseError{Code: code} }
func ReleaseErrorCode(err error) string {
	var target *ReleaseError
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

type ReleaseEvidenceInput struct {
	ChannelID        string   `json:"channel_id"`
	ChannelVersionID string   `json:"channel_version_id"`
	ModelID          string   `json:"model_id"`
	ProfileVersion   string   `json:"profile_version"`
	UpstreamModel    string   `json:"upstream_model"`
	VariantKey       string   `json:"variant_key"`
	Supplier         string   `json:"supplier"`
	Protocol         string   `json:"protocol"`
	Qualification    string   `json:"qualification"`
	ObservedAt       string   `json:"observed_at"`
	EvidenceCommit   string   `json:"evidence_commit"`
	EvidenceRef      string   `json:"evidence_ref"`
	EvidenceSHA256   string   `json:"evidence_sha256"`
	SourceMode       string   `json:"source_mode"`
	SourceRepository string   `json:"source_repository"`
	SourceCommit     string   `json:"source_commit"`
	LicenseRef       string   `json:"license_ref"`
	NoticeRef        string   `json:"notice_ref"`
	ModificationRef  string   `json:"modification_ref"`
	BoundaryRef      string   `json:"boundary_ref"`
	UsageRef         string   `json:"usage_ref"`
	RetryRef         string   `json:"retry_ref"`
	CostRef          string   `json:"cost_ref"`
	CostStatus       string   `json:"cost_status"`
	OpenIssueCodes   []string `json:"open_issue_codes"`
	CredentialIDs    []string `json:"credential_ids"`
}

type ReleaseEvidence struct {
	ID string `json:"id"`
	ReleaseEvidenceInput
	Withdrawn        bool   `json:"withdrawn"`
	WithdrawalReason string `json:"withdrawal_reason,omitempty"`
	WithdrawnAt      string `json:"withdrawn_at,omitempty"`
	Stale            bool   `json:"stale"`
	CreatedAt        string `json:"created_at"`
}

var (
	releaseHex40      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	releaseHex64      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	releaseRef        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@+-]{0,199}$`)
	releaseCode       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	releaseRepository = regexp.MustCompile(`^https://[A-Za-z0-9.-]+/[A-Za-z0-9._/-]+$`)
)

func releaseTime(value string) (time.Time, error) {
	t, err := time.Parse("2006-01-02T15:04:05.000000Z07:00", value)
	if err != nil || t.Location() != time.UTC || t.Format("2006-01-02T15:04:05.000000Z07:00") != value {
		return time.Time{}, releaseError("release_evidence_invalid")
	}
	return t, nil
}
func formatReleaseTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000Z07:00") }
func validReleaseProtocol(v string) bool {
	switch v {
	case "openai_chat", "openai_responses", "anthropic_messages", "openai_embeddings", "jina_rerank", "gemini_generate_content":
		return true
	}
	return false
}
func validReleaseRepository(v string) bool {
	return len(v) <= 240 && releaseRepository.MatchString(v)
}
func validateReleaseInput(in ReleaseEvidenceInput) (time.Time, error) {
	for _, id := range []string{in.ChannelID, in.ChannelVersionID, in.ModelID} {
		if !canonicalPublicUUID(id) {
			return time.Time{}, releaseError("release_evidence_invalid")
		}
	}
	if in.VariantKey == "" {
		in.VariantKey = "default"
	}
	if len(in.Supplier) == 0 || len(in.Supplier) > 120 || len(in.UpstreamModel) == 0 || len(in.UpstreamModel) > 200 || len(in.ProfileVersion) == 0 || len(in.ProfileVersion) > 200 || in.VariantKey != "default" || !validReleaseProtocol(in.Protocol) || strings.ContainsAny(in.Supplier+in.UpstreamModel+in.ProfileVersion, "\r\n\x00") {
		return time.Time{}, releaseError("release_evidence_invalid")
	}
	switch in.Qualification {
	case "mock_verified", "external_flow_verified", "supplier_profile_qualified":
	default:
		return time.Time{}, releaseError("release_evidence_invalid")
	}
	if in.SourceMode != "reference_only" && in.SourceMode != "vendored" || in.CostStatus != "known" && in.CostStatus != "unknown" || !validReleaseRepository(in.SourceRepository) || !releaseHex40.MatchString(in.EvidenceCommit) || !releaseHex40.MatchString(in.SourceCommit) || !releaseHex64.MatchString(in.EvidenceSHA256) {
		return time.Time{}, releaseError("release_evidence_invalid")
	}
	for _, ref := range []string{in.EvidenceRef, in.LicenseRef, in.NoticeRef, in.ModificationRef, in.BoundaryRef, in.UsageRef, in.RetryRef, in.CostRef} {
		if !releaseRef.MatchString(ref) {
			return time.Time{}, releaseError("release_evidence_invalid")
		}
	}
	if len(in.OpenIssueCodes) > 32 || len(in.CredentialIDs) > 32 {
		return time.Time{}, releaseError("release_evidence_invalid")
	}
	seen := map[string]bool{}
	for _, code := range in.OpenIssueCodes {
		if !releaseCode.MatchString(code) || seen[code] {
			return time.Time{}, releaseError("release_evidence_invalid")
		}
		seen[code] = true
	}
	seen = map[string]bool{}
	for _, id := range in.CredentialIDs {
		if !canonicalPublicUUID(id) || seen[id] {
			return time.Time{}, releaseError("release_evidence_invalid")
		}
		seen[id] = true
	}
	observed, err := releaseTime(in.ObservedAt)
	return observed, err
}

func (s *Store) RecordChannelProfileEvidence(ctx context.Context, actor string, in ReleaseEvidenceInput) (ReleaseEvidence, error) {
	observed, err := validateReleaseInput(in)
	if err != nil {
		return ReleaseEvidence{}, err
	}
	if in.VariantKey == "" {
		in.VariantKey = "default"
	}
	if in.OpenIssueCodes == nil {
		in.OpenIssueCodes = []string{}
	}
	if in.CredentialIDs == nil {
		in.CredentialIDs = []string{}
	}
	if len(actor) == 0 || len(actor) > 200 || strings.ContainsAny(actor, "\r\n\x00") {
		return ReleaseEvidence{}, releaseError("release_evidence_invalid")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ReleaseEvidence{}, err
	}
	defer tx.Rollback(ctx)
	var kind, protocol string
	var now time.Time
	var stale bool
	err = tx.QueryRow(ctx, `SELECT v.channel_kind,m.protocol,clock_timestamp(),(c.current_version<>v.id OR mo.profile_version<>m.id) FROM channel_versions v JOIN channels c ON c.id=v.channel_id JOIN model_capability_versions m ON m.id=$4 AND m.model_id=$3 JOIN models mo ON mo.id=m.model_id WHERE v.id=$2 AND v.channel_id=$1`, in.ChannelID, in.ChannelVersionID, in.ModelID, in.ProfileVersion).Scan(&kind, &protocol, &now, &stale)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseEvidence{}, releaseError("release_evidence_not_found")
	}
	if err != nil {
		return ReleaseEvidence{}, err
	}
	if protocol != in.Protocol || observed.After(now) {
		return ReleaseEvidence{}, releaseError("release_evidence_invalid")
	}
	if kind == "direct" && len(in.CredentialIDs) == 0 {
		return ReleaseEvidence{}, releaseError("release_credential_coverage")
	}
	if len(in.CredentialIDs) > 0 {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM channel_version_credentials WHERE channel_version_id=$1 AND credential_id=ANY($2)`, in.ChannelVersionID, in.CredentialIDs).Scan(&count); err != nil {
			return ReleaseEvidence{}, err
		}
		if count != len(in.CredentialIDs) {
			return ReleaseEvidence{}, releaseError("release_credential_coverage")
		}
	}
	id := uuid.NewString()
	var created time.Time
	err = tx.QueryRow(ctx, `INSERT INTO channel_profile_release_evidence(id,channel_id,channel_version_id,model_id,profile_version,upstream_model,variant_key,supplier,protocol,qualification,observed_at,evidence_commit,evidence_ref,evidence_sha256,source_mode,source_repository,source_commit,license_ref,notice_ref,modification_ref,boundary_ref,usage_ref,retry_ref,cost_ref,cost_status,open_issue_codes,actor) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27) RETURNING created_at`, id, in.ChannelID, in.ChannelVersionID, in.ModelID, in.ProfileVersion, in.UpstreamModel, in.VariantKey, in.Supplier, in.Protocol, in.Qualification, observed, in.EvidenceCommit, in.EvidenceRef, in.EvidenceSHA256, in.SourceMode, in.SourceRepository, in.SourceCommit, in.LicenseRef, in.NoticeRef, in.ModificationRef, in.BoundaryRef, in.UsageRef, in.RetryRef, in.CostRef, in.CostStatus, in.OpenIssueCodes, actor).Scan(&created)
	if err != nil {
		return ReleaseEvidence{}, err
	}
	sort.Strings(in.CredentialIDs)
	for _, credential := range in.CredentialIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO channel_profile_release_evidence_credentials(evidence_id,channel_version_id,credential_id) VALUES($1,$2,$3)`, id, in.ChannelVersionID, credential); err != nil {
			return ReleaseEvidence{}, err
		}
	}
	if err = audit(ctx, tx, actor, "release.evidence.record", id); err != nil {
		return ReleaseEvidence{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ReleaseEvidence{}, err
	}
	return ReleaseEvidence{ID: id, ReleaseEvidenceInput: in, Stale: stale, CreatedAt: formatReleaseTime(created)}, nil
}

func (s *Store) WithdrawChannelProfileEvidence(ctx context.Context, actor, id, reason string) error {
	if !canonicalPublicUUID(id) || !releaseCode.MatchString(reason) || len(actor) == 0 || len(actor) > 200 {
		return releaseError("release_evidence_invalid")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM channel_profile_release_evidence WHERE id=$1)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return releaseError("release_evidence_not_found")
	}
	tag, err := tx.Exec(ctx, `INSERT INTO channel_profile_release_withdrawals(evidence_id,reason_code,actor) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, reason, actor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if err = audit(ctx, tx, actor, "release.evidence.withdraw", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type releaseCursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func encodeReleaseCursor(t time.Time, id string) string {
	b, _ := json.Marshal(releaseCursor{CreatedAt: formatReleaseTime(t), ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeReleaseCursor(raw string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) > 300 || base64.RawURLEncoding.EncodeToString(b) != raw {
		return time.Time{}, "", releaseError("release_evidence_invalid")
	}
	var c releaseCursor
	if json.Unmarshal(b, &c) != nil || !canonicalPublicUUID(c.ID) {
		return time.Time{}, "", releaseError("release_evidence_invalid")
	}
	t, err := releaseTime(c.CreatedAt)
	return t, c.ID, err
}

const releaseEvidenceSelect = `SELECT e.id,e.channel_id,e.channel_version_id,e.model_id,e.profile_version,e.upstream_model,e.variant_key,e.supplier,e.protocol,e.qualification,e.observed_at,e.evidence_commit,e.evidence_ref,e.evidence_sha256,e.source_mode,e.source_repository,e.source_commit,e.license_ref,e.notice_ref,e.modification_ref,e.boundary_ref,e.usage_ref,e.retry_ref,e.cost_ref,e.cost_status,e.open_issue_codes,e.created_at,(c.current_version<>e.channel_version_id OR m.profile_version<>e.profile_version),w.reason_code,w.created_at FROM channel_profile_release_evidence e JOIN channels c ON c.id=e.channel_id JOIN models m ON m.id=e.model_id LEFT JOIN channel_profile_release_withdrawals w ON w.evidence_id=e.id `

type releaseScan struct {
	value             ReleaseEvidence
	created, observed time.Time
	withdrawnAt       *time.Time
	reason            *string
}

func scanRelease(row pgx.Row) (releaseScan, error) {
	var r releaseScan
	in := &r.value.ReleaseEvidenceInput
	err := row.Scan(&r.value.ID, &in.ChannelID, &in.ChannelVersionID, &in.ModelID, &in.ProfileVersion, &in.UpstreamModel, &in.VariantKey, &in.Supplier, &in.Protocol, &in.Qualification, &r.observed, &in.EvidenceCommit, &in.EvidenceRef, &in.EvidenceSHA256, &in.SourceMode, &in.SourceRepository, &in.SourceCommit, &in.LicenseRef, &in.NoticeRef, &in.ModificationRef, &in.BoundaryRef, &in.UsageRef, &in.RetryRef, &in.CostRef, &in.CostStatus, &in.OpenIssueCodes, &r.created, &r.value.Stale, &r.reason, &r.withdrawnAt)
	in.ObservedAt = formatReleaseTime(r.observed)
	r.value.CreatedAt = formatReleaseTime(r.created)
	r.value.Withdrawn = r.reason != nil
	if r.reason != nil {
		r.value.WithdrawalReason = *r.reason
	}
	if r.withdrawnAt != nil {
		r.value.WithdrawnAt = formatReleaseTime(*r.withdrawnAt)
	}
	return r, err
}
func (s *Store) attachReleaseCredentials(ctx context.Context, values []ReleaseEvidence) error {
	ids := make([]string, len(values))
	positions := map[string]int{}
	for i := range values {
		ids[i] = values[i].ID
		positions[values[i].ID] = i
		values[i].CredentialIDs = []string{}
	}
	rows, err := s.Pool.Query(ctx, `SELECT evidence_id,credential_id FROM channel_profile_release_evidence_credentials WHERE evidence_id=ANY($1) ORDER BY evidence_id,credential_id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var evidence, credential string
		if err = rows.Scan(&evidence, &credential); err != nil {
			return err
		}
		i := positions[evidence]
		values[i].CredentialIDs = append(values[i].CredentialIDs, credential)
	}
	return rows.Err()
}
func (s *Store) ChannelProfileEvidence(ctx context.Context, id string) (ReleaseEvidence, error) {
	if !canonicalPublicUUID(id) {
		return ReleaseEvidence{}, releaseError("release_evidence_not_found")
	}
	r, err := scanRelease(s.Pool.QueryRow(ctx, releaseEvidenceSelect+`WHERE e.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseEvidence{}, releaseError("release_evidence_not_found")
	}
	if err != nil {
		return ReleaseEvidence{}, err
	}
	values := []ReleaseEvidence{r.value}
	if err = s.attachReleaseCredentials(ctx, values); err != nil {
		return ReleaseEvidence{}, err
	}
	return values[0], nil
}
func (s *Store) ListChannelProfileEvidence(ctx context.Context, channelID, modelID, cursor string) ([]ReleaseEvidence, string, error) {
	if channelID != "" && !canonicalPublicUUID(channelID) || modelID != "" && !canonicalPublicUUID(modelID) {
		return nil, "", releaseError("release_evidence_invalid")
	}
	where := "WHERE TRUE"
	args := []any{}
	next := func() string { return fmt.Sprintf("$%d", len(args)) }
	if channelID != "" {
		args = append(args, channelID)
		where += " AND e.channel_id=" + next()
	}
	if modelID != "" {
		args = append(args, modelID)
		where += " AND e.model_id=" + next()
	}
	if cursor != "" {
		t, id, err := decodeReleaseCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, t)
		a := next()
		args = append(args, id)
		b := next()
		where += fmt.Sprintf(" AND (e.created_at,e.id)<(%s,%s)", a, b)
	}
	args = append(args, 51)
	query := releaseEvidenceSelect + where + " ORDER BY e.created_at DESC,e.id DESC LIMIT " + next()
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	values := []ReleaseEvidence{}
	scans := []releaseScan{}
	for rows.Next() {
		r, e := scanRelease(rows)
		if e != nil {
			rows.Close()
			return nil, "", e
		}
		scans = append(scans, r)
		values = append(values, r.value)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, "", err
	}
	rows.Close()
	nextCursor := ""
	if len(values) == 51 {
		last := scans[49]
		nextCursor = encodeReleaseCursor(last.created, last.value.ID)
		values = values[:50]
	}
	if len(values) == 0 {
		return values, nextCursor, nil
	}
	if err = s.attachReleaseCredentials(ctx, values); err != nil {
		return nil, "", err
	}
	return values, nextCursor, nil
}

func validateCommercialBinding(ctx context.Context, tx pgx.Tx, evidenceID, modelID, profileVersion, channelID, channelVersionID, upstreamModel, variantKey string) error {
	if !canonicalPublicUUID(evidenceID) {
		return releaseError("release_qualification_required")
	}
	var matches, withdrawn bool
	var kind string
	var live, covered int
	err := tx.QueryRow(ctx, `SELECT v.channel_kind,(e.channel_version_id=$5 AND e.model_id=$1 AND e.profile_version=$2 AND e.upstream_model=$6 AND e.variant_key=$7 AND e.protocol=m.protocol AND e.qualification='supplier_profile_qualified' AND e.cost_status='known' AND cardinality(e.open_issue_codes)=0),EXISTS(SELECT 1 FROM channel_profile_release_withdrawals w WHERE w.evidence_id=e.id),(SELECT count(*) FROM channel_version_credentials cp JOIN channel_credentials k ON k.id=cp.credential_id AND k.channel_id=cp.channel_id WHERE cp.channel_version_id=$5 AND k.revoked_at IS NULL),(SELECT count(*) FROM channel_version_credentials cp JOIN channel_credentials k ON k.id=cp.credential_id AND k.channel_id=cp.channel_id JOIN channel_profile_release_evidence_credentials c ON c.evidence_id=e.id AND c.channel_version_id=cp.channel_version_id AND c.credential_id=cp.credential_id WHERE cp.channel_version_id=$5 AND k.revoked_at IS NULL) FROM channel_profile_release_evidence e JOIN channel_versions v ON v.id=e.channel_version_id AND v.channel_id=e.channel_id JOIN model_capability_versions m ON m.id=$2 AND m.model_id=$1 WHERE e.id=$3 AND e.channel_id=$4`, modelID, profileVersion, evidenceID, channelID, channelVersionID, upstreamModel, variantKey).Scan(&kind, &matches, &withdrawn, &live, &covered)
	if errors.Is(err, pgx.ErrNoRows) {
		return releaseError("release_evidence_not_found")
	}
	if err != nil {
		return err
	}
	if withdrawn {
		return releaseError("release_evidence_withdrawn")
	}
	if kind != "direct" {
		return releaseError("release_external_commercial_unsupported")
	}
	if !matches {
		return releaseError("release_qualification_required")
	}
	if live == 0 || covered != live {
		return releaseError("release_credential_coverage")
	}
	return nil
}
