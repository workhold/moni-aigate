package fundinglotverify

import "time"

func (w *pbGraphWork) index(r PeriodicBudgetGraphRows) {
	w.controls = pbPrimary(w, r.Controls, "controls", func(v PeriodicBudgetControlRow) string { return v.ID }, func(v PeriodicBudgetControlRow) pbGraphObject { return pbObj("controls", v.ID) })
	w.windows = pbPrimary(w, r.Windows, "windows", func(v PeriodicBudgetWindowRow) string { return v.ID }, func(v PeriodicBudgetWindowRow) pbGraphObject { return pbObj("windows", v.ID) })
	w.allocs = pbPrimary(w, r.Allocations, "allocations", func(v PeriodicBudgetAllocationRow) string { return v.ID }, func(v PeriodicBudgetAllocationRow) pbGraphObject { return pbObj("allocations", v.ID) })
	w.events = pbPrimary(w, r.Events, "events", func(v PeriodicBudgetEventRow) string { return v.ID }, func(v PeriodicBudgetEventRow) pbGraphObject { return pbObj("events", v.ID) })
	w.ops = pbPrimary(w, r.Operations, "operations", func(v PeriodicBudgetOperationRow) string { return v.OperationID }, func(v PeriodicBudgetOperationRow) pbGraphObject { return pbObj("operations", v.OperationID) })
	w.versions = pbPrimary(w, r.Versions, "versions", func(v PeriodicBudgetVersionRow) pbGraphPair { return pbGraphPair{v.ControlID, v.Revision} }, func(v PeriodicBudgetVersionRow) pbGraphObject {
		return pbGraphObject{"versions", v.ControlID, v.Revision}
	})
	w.tenants = pbPrimary(w, r.Tenants, "tenants", func(v ProcurementAttemptTenantRow) string { return v.ID }, func(v ProcurementAttemptTenantRow) pbGraphObject { return pbObj("tenants", v.ID) })
	w.keys = pbPrimary(w, r.Keys, "keys", func(v PeriodicBudgetKeyRow) string { return v.ID }, func(v PeriodicBudgetKeyRow) pbGraphObject { return pbObj("keys", v.ID) })
	w.users = pbPrimary(w, r.Users, "users", func(v ProcurementApprovalUserRow) string { return v.ID }, func(v ProcurementApprovalUserRow) pbGraphObject { return pbObj("users", v.ID) })
	w.requests = pbPrimary(w, r.Requests, "requests", func(v PeriodicBudgetRequestRow) string { return v.ID }, func(v PeriodicBudgetRequestRow) pbGraphObject { return pbObj("requests", v.ID) })
	w.reservations = pbPrimary(w, r.Reservations, "reservations", func(v CustomerKeyReservationRow) string { return v.ID }, func(v CustomerKeyReservationRow) pbGraphObject { return pbObj("reservations", v.ID) })
	w.settlements = pbPrimary(w, r.Settlements, "settlements", func(v PeriodicBudgetSettlementRow) string { return v.ID }, func(v PeriodicBudgetSettlementRow) pbGraphObject { return pbObj("settlements", v.ID) })
	w.attempts = pbPrimary(w, r.Attempts, "attempts", func(v PeriodicBudgetAttemptRow) string { return v.ID }, func(v PeriodicBudgetAttemptRow) pbGraphObject { return pbObj("attempts", v.ID) })
	w.accounts = pbPrimary(w, r.Accounts, "accounts", func(v ProcurementBudgetAccountRow) string { return v.ID }, func(v ProcurementBudgetAccountRow) pbGraphObject { return pbObj("accounts", v.ID) })
	w.bindings = pbPrimary(w, r.Bindings, "bindings", func(v PeriodicBudgetBindingRow) string { return v.ID }, func(v PeriodicBudgetBindingRow) pbGraphObject { return pbObj("bindings", v.ID) })
	w.procurement = pbPrimary(w, r.Procurement, "procurement", func(v PeriodicBudgetSupplierRow) string { return v.AttemptID }, func(v PeriodicBudgetSupplierRow) pbGraphObject { return pbObj("procurement", v.AttemptID) })
	w.pe = pbPrimary(w, r.ProcurementEvents, "procurement_events", func(v PeriodicBudgetProcurementEventRow) string { return v.ID }, func(v PeriodicBudgetProcurementEventRow) pbGraphObject { return pbObj("procurement_events", v.ID) })
	w.audits = pbPrimary(w, r.Audits, "audits", func(v PeriodicBudgetAuditRow) string { return v.ID }, func(v PeriodicBudgetAuditRow) pbGraphObject { return pbObj("audits", v.ID) })
	// Natural indexes iterate original order, never Go map iteration order.
	seen := map[pbGraphObject]bool{}
	first := func(o pbGraphObject) bool {
		if seen[o] {
			return false
		}
		seen[o] = true
		return w.tick()
	}
	m := &w.out.Mismatches
	for _, v := range r.Controls {
		o := pbObj("controls", v.ID)
		if !first(o) {
			continue
		}
		w.uuid(v.ID, o)
		w.uuid(v.ConfigurationOperationID, o)
		w.enum(v.Scope, o, "tenant", "key", "procurement")
		w.enum(v.PeriodKind, o, "utc_day", "utc_month")
		w.amount(v.LimitAmount, true, false, o)
		w.revision(v.Revision, true, o)
		w.revision(v.EventSeq, false, o)
		w.amount(v.HeldAmount, false, false, o)
		w.amount(v.SpentAmount, false, false, o)
		w.xid(v.CreatedXID, o)
		w.timestamp(v.TrackingStartedAt, true, o)
		w.timestamp(v.EffectiveFrom, true, o)
		w.timestamp(v.MaxObservedWindowStart, true, o)
		if v.LastEventID != nil {
			w.uuid(*v.LastEventID, o)
		}
		if target, ok := pbTarget(v); ok {
			if v.Scope != "tenant" {
				w.uuid(target, o)
			}
			pbNatural(w, w.scopes, pbGraphPair{v.Scope, target}, v.ID, &m.DuplicateControlScopeRows, o)
		}
	}
	for _, v := range r.Windows {
		o := pbObj("windows", v.ID)
		if !first(o) {
			continue
		}
		w.uuid(v.ID, o)
		w.uuid(v.ControlID, o)
		w.timestamp(v.StartAt, true, o)
		w.timestamp(v.EndAt, true, o)
		w.amount(v.HeldAmount, false, false, o)
		w.amount(v.SpentAmount, false, false, o)
		w.revision(v.Revision, false, o)
		if v.LastEventID != nil {
			w.uuid(*v.LastEventID, o)
		}
		if pbFinite(v.StartAt) {
			pbNatural(w, w.windowStarts, pbGraphWindowKey{v.ControlID, v.StartAt.Value.UTC()}, v.ID, &m.DuplicateWindowStartRows, o)
		}
	}
	for _, v := range r.Allocations {
		o := pbObj("allocations", v.ID)
		if !first(o) {
			continue
		}
		for _, id := range []string{v.ID, v.ControlID, v.SubjectID, v.OriginSourceID} {
			w.uuid(id, o)
		}
		if v.WindowID != nil {
			w.uuid(*v.WindowID, o)
		}
		if v.LastEventID != nil {
			w.uuid(*v.LastEventID, o)
		}
		w.enum(v.SubjectType, o, "customer_request", "supplier_attempt")
		w.enum(v.Origin, o, "new", "legacy_import", "legacy_catchup")
		w.enum(v.State, o, "held", "disputed", "settled", "released")
		w.amount(v.UpperAmount, true, false, o)
		for _, s := range []string{v.Baseline, v.SourceHeld, v.SourceSpent, v.HeldAmount, v.SpentAmount} {
			w.amount(s, false, false, o)
		}
		w.revision(v.Revision, false, o)
		w.xid(v.CreatedXID, o)
		w.timestamp(v.CreatedAt, true, o)
		w.timestamp(v.AdmittedAt, false, o)
		pbNatural(w, w.subjects, pbGraphSubject{v.ControlID, v.SubjectType, v.SubjectID}, v.ID, &m.DuplicateAllocationSubjectRows, o)
	}
	seqs := map[pbGraphPair]string{}
	for _, v := range r.Events {
		o := pbObj("events", v.ID)
		if !first(o) {
			continue
		}
		for _, id := range []string{v.ID, v.ControlID, v.AllocationID, v.SourceID} {
			w.uuid(id, o)
		}
		w.enum(v.SourceKind, o, "reservation", "settlement", "procurement_event", "legacy_import")
		w.enum(v.AfterState, o, "held", "disputed", "settled", "released")
		n := w.revision(v.Seq, true, o)
		w.revision(v.AllocationBeforeRevision, false, o)
		if v.WindowBeforeRevision != nil {
			w.revision(*v.WindowBeforeRevision, false, o)
		}
		for _, s := range []string{v.BeforeHeld, v.BeforeSpent, v.AfterHeld, v.AfterSpent, v.AfterSourceSpent} {
			w.amount(s, false, false, o)
		}
		w.amount(v.HeldDelta, false, true, o)
		w.amount(v.SpentDelta, false, true, o)
		w.xid(v.CreatedXID, o)
		w.timestamp(v.CreatedAt, true, o)
		if n != nil {
			pbNatural(w, seqs, pbGraphPair{v.ControlID, n.String()}, v.ID, &m.DuplicateEventSequenceRows, o)
		}
		pbNatural(w, w.effects, pbGraphSubject{v.ControlID, v.SourceKind, v.SourceID}, v.ID, &m.DuplicateEffectRows, o)
	}
	for _, v := range r.Operations {
		o := pbObj("operations", v.OperationID)
		if !first(o) {
			continue
		}
		w.uuid(v.OperationID, o)
		w.uuid(v.ResourceID, o)
		w.enum(v.Kind, o, "tenant_periodic_budget.configure", "key_periodic_budget.configure", "procurement_periodic_budget.configure")
		if v.Scope != "platform" && v.Kind != "key_periodic_budget.configure" {
			w.uuid(v.Scope, o)
		}
		w.revision(v.Revision, true, o)
		w.xid(v.CreatedXID, o)
		w.timestamp(v.CreatedAt, true, o)
	}
	for _, v := range r.Versions {
		o := pbGraphObject{"versions", v.ControlID, v.Revision}
		if !first(o) {
			continue
		}
		w.uuid(v.ControlID, o)
		w.uuid(v.OperationID, o)
		w.revision(v.Revision, true, o)
		w.enum(v.PeriodKind, o, "utc_day", "utc_month")
		w.amount(v.LimitAmount, true, false, o)
		w.xid(v.CreatedXID, o)
		w.timestamp(v.CreatedAt, true, o)
		if _, ok := w.versionOps[v.OperationID]; ok {
			m.DuplicateVersionOperationRows++
			w.excluded[o] = true
		} else {
			w.versionOps[v.OperationID] = pbGraphPair{v.ControlID, v.Revision}
		}
	}
	for _, v := range r.Requests {
		o := pbObj("requests", v.ID)
		if !first(o) {
			continue
		}
		for _, s := range []*string{v.CreatedXID} {
			if s != nil {
				w.xid(*s, o)
			}
		}
		for _, s := range []*string{v.TenantControl, v.KeyControl} {
			if s != nil {
				w.uuid(*s, o)
			}
		}
		w.timestamp(v.AdmittedAt, false, o)
	}
	for _, v := range r.Reservations {
		o := pbObj("reservations", v.ID)
		if !first(o) {
			continue
		}
		w.amount(v.Amount, true, false, o)
		w.enum(v.Status, o, "reserved", "pending_reconciliation", "settled", "released")
		pbNatural(w, w.requestReservations, v.RequestID, v.ID, &m.DuplicateReservationRequestRows, o)
	}
	for _, v := range r.Settlements {
		o := pbObj("settlements", v.ID)
		if !first(o) {
			continue
		}
		w.amount(v.ChargedAmount, true, false, o)
		w.enum(v.Status, o, "settled", "released")
		pbNatural(w, w.requestSettlements, v.RequestID, v.ID, &m.DuplicateSettlementRequestRows, o)
	}
	for _, v := range r.Attempts {
		o := pbObj("attempts", v.ID)
		if !first(o) {
			continue
		}
		if v.CreatedXID != nil {
			w.xid(*v.CreatedXID, o)
		}
		w.timestamp(v.DispatchCheckedAt, false, o)
		w.enum(v.DeliveryState, o, "prepared", "sending", "completed", "failed", "unknown")
	}
	for _, v := range r.Accounts {
		o := pbObj("accounts", v.ID)
		if !first(o) {
			continue
		}
		w.amount(v.HeldAmount, false, false, o)
		w.amount(v.SpentAmount, false, false, o)
	}
	creds := map[string]string{}
	for _, v := range r.Bindings {
		o := pbObj("bindings", v.ID)
		if !first(o) {
			continue
		}
		pbNatural(w, creds, v.CredentialID, v.ID, &m.DuplicateBindingCredentialRows, o)
	}
	for _, v := range r.Procurement {
		o := pbObj("procurement", v.AttemptID)
		if !first(o) {
			continue
		}
		w.amount(v.UpperAmount, true, false, o)
		w.amount(v.HeldAmount, false, false, o)
		w.amount(v.SpentAmount, false, false, o)
		w.enum(v.State, o, "prepared", "sending", "unknown", "disputed", "settled", "released")
		if v.CreatedXID != nil {
			w.xid(*v.CreatedXID, o)
		}
		if v.ControlID != nil {
			w.uuid(*v.ControlID, o)
		}
		w.timestamp(v.AdmittedAt, false, o)
	}
	peKeys := map[string]string{}
	for _, v := range r.ProcurementEvents {
		o := pbObj("procurement_events", v.ID)
		if !first(o) {
			continue
		}
		w.amount(v.HeldDelta, false, true, o)
		w.amount(v.SpentDelta, false, true, o)
		w.enum(v.Type, o, "reserved", "consumption_unknown", "recovered_unknown", "released_no_consumption", "expired_without_permit", "cost_confirmed", "cost_corrected", "evidence_disputed")
		if v.CreatedXID != nil {
			w.xid(*v.CreatedXID, o)
		}
		pbNatural(w, peKeys, v.EventKey, v.ID, &m.DuplicateProcurementEventKeyRows, o)
	}
	for _, v := range r.Audits {
		o := pbObj("audits", v.ID)
		if !first(o) {
			continue
		}
		w.timestamp(v.CreatedAt, true, o)
		if v.Action == "periodic_budget.configured" {
			pbNatural(w, w.auditTargets, v.Target, v.ID, &m.DuplicateAuditTargetRows, o)
		}
	}
	for id, e := range w.events {
		if !w.tick() {
			return
		}
		if !w.active(pbObj("events", id)) {
			continue
		}
		w.controlEvents[e.ControlID] = append(w.controlEvents[e.ControlID], e)
		w.allocationEvents[e.AllocationID] = append(w.allocationEvents[e.AllocationID], e)
	}
	for id, e := range w.pe {
		if !w.tick() {
			return
		}
		if w.active(pbObj("procurement_events", id)) {
			w.sourcePE[e.AttemptID] = append(w.sourcePE[e.AttemptID], e)
		}
	}
}

func pbContains(w PeriodicBudgetWindowRow, at PeriodicBudgetGraphTime) bool {
	return pbFinite(w.StartAt) && pbFinite(w.EndAt) && pbFinite(at) && !at.Value.Before(w.StartAt.Value) && at.Value.Before(w.EndAt.Value)
}
func pbSameMoment(a PeriodicBudgetGraphTime, t time.Time) bool {
	return pbFinite(a) && a.Value.Equal(t)
}
