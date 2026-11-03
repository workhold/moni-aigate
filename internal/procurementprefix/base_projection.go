package procurementprefix

import (
	"context"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"strconv"
	"time"
)

// projectBase uses only this call's original rows. It preserves every field of
// the unchanged 23-array ABI without accepting a caller Base or coverage claim.
func projectBase(ctx context.Context, rows Rows) (fundinglotverify.ProcurementEvidenceGraphRows, error) {
	if _, err := budgetRows(ctx, rows); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, err
	}
	b := budget{ctx: ctx}
	b.deadline, _ = ctx.Deadline()
	var out fundinglotverify.ProcurementEvidenceGraphRows
	var err error
	if rows.OriginalRows.Channels != nil {
		out.Channels = make([]fundinglotverify.ProcurementAttemptChannelRow, len(rows.OriginalRows.Channels))
	}
	for i, row := range rows.OriginalRows.Channels {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementAttemptChannelRow
		next.ID = row.ID
		out.Channels[i] = next
	}
	if rows.OriginalRows.ChannelCredentials != nil {
		out.Credentials = make([]fundinglotverify.ProcurementAttemptCredentialRow, len(rows.OriginalRows.ChannelCredentials))
	}
	for i, row := range rows.OriginalRows.ChannelCredentials {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementAttemptCredentialRow
		next.ID = row.ID
		next.ChannelID = row.ChannelID
		out.Credentials[i] = next
	}
	if rows.OriginalRows.ChannelVersions != nil {
		out.Versions = make([]fundinglotverify.ProcurementAttemptVersionRow, len(rows.OriginalRows.ChannelVersions))
	}
	for i, row := range rows.OriginalRows.ChannelVersions {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementAttemptVersionRow
		next.ID = row.ID
		next.ChannelID = row.ChannelID
		next.CredentialID = row.CredentialID
		out.Versions[i] = next
	}
	if rows.OriginalRows.ChannelVersionCredentials != nil {
		out.PoolMemberships = make([]fundinglotverify.ProcurementAttemptPoolMemberRow, len(rows.OriginalRows.ChannelVersionCredentials))
	}
	for i, row := range rows.OriginalRows.ChannelVersionCredentials {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementAttemptPoolMemberRow
		next.ChannelVersionID = row.ChannelVersionID
		next.ChannelID = row.ChannelID
		next.CredentialID = row.CredentialID
		next.Position, err = projectInt32(row.Position)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		next.Weight, err = projectInt32(row.Weight)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		next.MaxInflight, err = projectInt32(row.MaxInflight)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.PoolMemberships[i] = next
	}
	if rows.OriginalRows.Tenants != nil {
		out.Tenants = make([]fundinglotverify.ProcurementAttemptTenantRow, len(rows.OriginalRows.Tenants))
	}
	for i, row := range rows.OriginalRows.Tenants {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementAttemptTenantRow
		next.ID = row.ID
		out.Tenants[i] = next
	}
	if rows.OriginalRows.RequestRecords != nil {
		out.Requests = make([]fundinglotverify.ProcurementEvidenceRequestRow, len(rows.OriginalRows.RequestRecords))
	}
	for i, row := range rows.OriginalRows.RequestRecords {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementEvidenceRequestRow
		next.ID = row.ID
		next.TenantID = row.TenantID
		next.InputUpperBound = row.InputUpperBound
		next.OutputLimit = row.OutputLimit
		next.PolicyVersion = row.PolicyVersion
		next.LatestFinalizationID = cloneString(row.LatestFinalizationID)
		next.RequestDeadline, err = projectTime(row.RequestDeadline)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Requests[i] = next
	}
	if rows.OriginalRows.CostVersions != nil {
		out.CostVersions = make([]fundinglotverify.ProcurementAttemptCostVersionRow, len(rows.OriginalRows.CostVersions))
	}
	for i, row := range rows.OriginalRows.CostVersions {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementAttemptCostVersionRow
		next.ID = row.ID
		next.ChannelID = row.ChannelID
		next.UpstreamModel = row.UpstreamModel
		next.InputRate = cloneString(row.InputRate)
		next.OutputRate = cloneString(row.OutputRate)
		next.Basis = row.Basis
		next.EffectiveAt, err = projectTime(row.EffectiveAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.CostVersions[i] = next
	}
	if rows.OriginalRows.ProcurementAccounts != nil {
		out.Accounts = make([]fundinglotverify.ProcurementEvidenceAccountRow, len(rows.OriginalRows.ProcurementAccounts))
	}
	for i, row := range rows.OriginalRows.ProcurementAccounts {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementEvidenceAccountRow
		next.ID = row.ID
		next.Label = row.Label
		next.Provider = row.Provider
		next.State = row.State
		next.LimitAmount = row.LimitAmount
		next.HeldAmount = row.HeldAmount
		next.SpentAmount = row.SpentAmount
		next.Revision = row.Revision
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Accounts[i] = next
	}
	if rows.OriginalRows.CredentialProcurementBindings != nil {
		out.Bindings = make([]fundinglotverify.ProcurementAttemptBindingRow, len(rows.OriginalRows.CredentialProcurementBindings))
	}
	for i, row := range rows.OriginalRows.CredentialProcurementBindings {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementAttemptBindingRow
		next.ID = row.ID
		next.CredentialID = row.CredentialID
		next.ChannelID = row.ChannelID
		next.AccountID = row.AccountID
		next.OperationID = row.OperationID
		next.BoundAt, err = projectTime(row.BoundAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Bindings[i] = next
	}
	if rows.OriginalRows.UpstreamAttempts != nil {
		out.Attempts = make([]fundinglotverify.ProcurementAttemptRow, len(rows.OriginalRows.UpstreamAttempts))
	}
	for i, row := range rows.OriginalRows.UpstreamAttempts {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementAttemptRow
		next.ID = row.ID
		next.TenantID = row.TenantID
		next.RequestID = row.RequestID
		next.Sequence, err = projectInt32(row.Sequence)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		next.ChannelID = row.ChannelID
		next.ChannelVersionID = row.ChannelVersionID
		next.CredentialID = row.CredentialID
		next.UpstreamModel = row.UpstreamModel
		next.DeliveryState = row.DeliveryState
		next.CostVersionID = cloneString(row.CostVersionID)
		next.CostInputRate = cloneString(row.CostInputRate)
		next.CostOutputRate = cloneString(row.CostOutputRate)
		next.CostBasis = row.CostBasis
		next.CostStatus = row.CostStatus
		next.CostAmount = cloneString(row.CostAmount)
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Attempts[i] = next
	}
	if rows.OriginalRows.ProcurementReservations != nil {
		out.Reservations = make([]fundinglotverify.ProcurementEvidenceReservationRow, len(rows.OriginalRows.ProcurementReservations))
	}
	for i, row := range rows.OriginalRows.ProcurementReservations {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementEvidenceReservationRow
		next.AttemptID = row.AttemptID
		next.AccountID = row.AccountID
		next.BindingID = row.BindingID
		next.InputBound = row.InputBound
		next.OutputBound = row.OutputBound
		next.UpperAmount = row.UpperAmount
		next.HeldAmount = row.HeldAmount
		next.SpentAmount = row.SpentAmount
		next.State = row.State
		next.Revision = row.Revision
		next.Anomaly = row.Anomaly
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		next.UpdatedAt, err = projectTime(row.UpdatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Reservations[i] = next
	}
	if rows.OriginalRows.ProcurementBudgetEvents != nil {
		out.BudgetEvents = make([]fundinglotverify.ProcurementEvidenceBudgetEventRow, len(rows.OriginalRows.ProcurementBudgetEvents))
	}
	for i, row := range rows.OriginalRows.ProcurementBudgetEvents {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementEvidenceBudgetEventRow
		next.ID = row.ID
		next.EventKey = row.EventKey
		next.AccountID = row.AccountID
		next.AttemptID = row.AttemptID
		next.HeldDelta = row.HeldDelta
		next.SpentDelta = row.SpentDelta
		next.Type = row.Type
		next.EvidenceRef = row.EvidenceRef
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.BudgetEvents[i] = next
	}
	if rows.OriginalRows.Users != nil {
		out.Users = make([]fundinglotverify.ProcurementApprovalUserRow, len(rows.OriginalRows.Users))
	}
	for i, row := range rows.OriginalRows.Users {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementApprovalUserRow
		next.ID = row.ID
		out.Users[i] = next
	}
	if rows.OriginalRows.ProviderCostCorrections != nil {
		out.Corrections = make([]fundinglotverify.ProcurementCostCorrectionRow, len(rows.OriginalRows.ProviderCostCorrections))
	}
	for i, row := range rows.OriginalRows.ProviderCostCorrections {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementCostCorrectionRow
		next.ID = row.ID
		next.TenantID = row.TenantID
		next.AttemptID = row.AttemptID
		next.OperationID = row.OperationID
		next.Amount = row.Amount
		next.Basis = row.Basis
		next.EvidenceRef = row.EvidenceRef
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Corrections[i] = next
	}
	if rows.OriginalRows.ProcurementCostProposals != nil {
		out.Proposals = make([]fundinglotverify.ProcurementCostProposalRow, len(rows.OriginalRows.ProcurementCostProposals))
	}
	for i, row := range rows.OriginalRows.ProcurementCostProposals {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementCostProposalRow
		next.ID = row.ID
		next.TenantID = row.TenantID
		next.AttemptID = row.AttemptID
		next.Amount = row.Amount
		next.Basis = row.Basis
		next.EvidenceRef = row.EvidenceRef
		next.Reason = row.Reason
		next.Requester = row.Requester
		next.ExpectedRevision = row.ExpectedReservationRevision
		next.ExpectedEvidenceCount = row.ExpectedEvidenceCount
		next.State = row.State
		next.Approver = cloneString(row.Approver)
		next.ApprovalOperationID = cloneString(row.ApprovalOperationID)
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		next.ApprovedAt, err = projectOptionalTime(row.ApprovedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Proposals[i] = next
	}
	if rows.OriginalRows.ProcurementCommands != nil {
		out.Commands = make([]fundinglotverify.ProcurementCommandRow, len(rows.OriginalRows.ProcurementCommands))
	}
	for i, row := range rows.OriginalRows.ProcurementCommands {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementCommandRow
		next.OperationID = row.OperationID
		next.Actor = row.Actor
		next.Kind = row.Kind
		next.TargetID = row.TargetID
		next.InputDigest = row.InputDigest
		next.Status = row.Status
		next.Reason = row.Reason
		next.Before = row.BeforeState
		next.After = row.AfterState
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Commands[i] = next
	}
	if rows.OriginalRows.ProcurementBalanceObservations != nil {
		out.Observations = make([]fundinglotverify.ProcurementObservationRow, len(rows.OriginalRows.ProcurementBalanceObservations))
	}
	for i, row := range rows.OriginalRows.ProcurementBalanceObservations {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementObservationRow
		next.ID = row.ID
		next.AccountID = row.AccountID
		next.OperationID = row.OperationID
		next.Actor = row.Actor
		next.Status = row.Status
		next.Amount = cloneString(row.Amount)
		next.Currency = row.Currency
		next.Source = row.Source
		next.Semantic = row.Semantic
		next.ObservedAt, err = projectTime(row.ObservedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		next.ProviderAsOf, err = projectOptionalTime(row.ProviderAsOf)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		next.EvidenceRef = row.EvidenceRef
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Observations[i] = next
	}
	if rows.OriginalRows.AuditEvents != nil {
		out.Audits = make([]fundinglotverify.ProcurementAuditRow, len(rows.OriginalRows.AuditEvents))
	}
	for i, row := range rows.OriginalRows.AuditEvents {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementAuditRow
		next.ID = row.ID
		next.Actor = row.Actor
		next.Action = row.Action
		next.Target = row.Target
		next.Diff = row.SanitizedDiff
		next.RequestID = row.RequestID
		next.TenantScope = cloneString(row.TenantScope)
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Audits[i] = next
	}
	if rows.OriginalRows.ProjectionOutbox != nil {
		out.Outbox = make([]fundinglotverify.ProcurementOutboxRow, len(rows.OriginalRows.ProjectionOutbox))
	}
	for i, row := range rows.OriginalRows.ProjectionOutbox {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementOutboxRow
		next.ID = row.EventID
		next.TenantID = row.TenantID
		next.OperationID = row.OperationID
		next.Kind = row.Kind
		next.Payload = row.SanitizedPayload
		next.SchemaVersion, err = projectInt32(row.SchemaVersion)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Outbox[i] = next
	}
	if rows.OriginalRows.ProcurementEvidenceIdentities != nil {
		out.Identities = make([]fundinglotverify.ProcurementEvidenceIdentityRow, len(rows.OriginalRows.ProcurementEvidenceIdentities))
	}
	for i, row := range rows.OriginalRows.ProcurementEvidenceIdentities {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementEvidenceIdentityRow
		next.AttemptID = row.AttemptID
		next.Digest = row.EvidenceDigest
		next.EventID = row.EventID
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Identities[i] = next
	}
	if rows.OriginalRows.MeteringEvents != nil {
		out.Metering = make([]fundinglotverify.ProcurementEvidenceMeterRow, len(rows.OriginalRows.MeteringEvents))
	}
	for i, row := range rows.OriginalRows.MeteringEvents {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementEvidenceMeterRow
		next.ID = row.ID
		next.TenantID = row.TenantID
		next.RequestID = row.RequestID
		next.Source = row.Source
		next.SourceEventID = row.SourceEventID
		next.UsageJSON = row.NormalizedUsage
		next.AttemptID = cloneString(row.AttemptID)
		next.SchemaVersion, err = projectInt32(row.SchemaVersion)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		next.HappenedAt, err = projectTime(row.HappenedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		next.ReceivedAt, err = projectTime(row.ReceivedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Metering[i] = next
	}
	if rows.OriginalRows.MeteringEventConflicts != nil {
		out.Conflicts = make([]fundinglotverify.ProcurementEvidenceConflictRow, len(rows.OriginalRows.MeteringEventConflicts))
	}
	for i, row := range rows.OriginalRows.MeteringEventConflicts {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementEvidenceConflictRow
		next.ID = row.ID
		next.EventID = row.EventID
		next.UsageJSON = row.NormalizedUsage
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Conflicts[i] = next
	}
	if rows.OriginalRows.FinalizationRecords != nil {
		out.Finalizations = make([]fundinglotverify.ProcurementEvidenceFinalizationRow, len(rows.OriginalRows.FinalizationRecords))
	}
	for i, row := range rows.OriginalRows.FinalizationRecords {
		if err := b.tick(); err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		var next fundinglotverify.ProcurementEvidenceFinalizationRow
		next.ID = row.ID
		next.TenantID = row.TenantID
		next.RequestID = row.RequestID
		next.EventID = row.EventID
		next.InputDigest = row.InputDigest
		next.ProtocolOutcome = row.ProtocolOutcome
		next.TransportEndReason = row.TransportEndReason
		next.DeliveryStage = row.DeliveryStage
		next.FinishReason = row.FinishReason
		next.PolicyVersion = row.PolicyVersion
		next.ContentWritten = row.ContentWritten
		next.CreatedAt, err = projectTime(row.CreatedAt)
		if err != nil {
			return fundinglotverify.ProcurementEvidenceGraphRows{}, err
		}
		out.Finalizations[i] = next
	}
	if err := b.check(); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, err
	}
	return out, nil
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func projectInt32(value string) (int32, error) {
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, ErrInput
	}
	return int32(n), nil
}

func projectTime(value Time) (time.Time, error) {
	if !finite(value) {
		return time.Time{}, ErrInput
	}
	return value.Time, nil
}

func projectOptionalTime(value Time) (*time.Time, error) {
	if nullTime(value) {
		return nil, nil
	}
	t, err := projectTime(value)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
