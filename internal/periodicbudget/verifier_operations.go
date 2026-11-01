package periodicbudget

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// JSON remains bytes until strict pure decoding. No JSON text is cast to an SQL
// numeric/bool/timestamp, so malformed receipts become findings, not SQL errors.
const verifierOperationsSQL = `WITH nonzero_held AS MATERIALIZED (SELECT DISTINCT control_id FROM @events@ WHERE held_delta<>0)
 SELECT
 o.operation_id,o.kind,o.actor,o.scope,o.target,o.resource_id,o.revision::text,o.input_fingerprint,o.safe_input,o.result_snapshot,
 CASE WHEN isfinite(o.created_at) THEN to_char(o.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') ELSE 'invalid' END,
 c.id,c.scope,coalesce(c.account_id,c.key_id,c.tenant_id),c.tenant_id,c.period_kind,
 CASE WHEN isfinite(c.tracking_started_at) THEN to_char(c.tracking_started_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') ELSE 'invalid' END,
 CASE WHEN isfinite(c.effective_from) THEN to_char(c.effective_from AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') ELSE 'invalid' END,
 v.revision::text,v.period_kind,v.enabled,v.limit_amount::text,
 changed.operation_id IS NOT NULL,u.id IS NOT NULL,
 w.id,
 CASE WHEN isfinite(w.start_at) THEN to_char(w.start_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') ELSE 'invalid' END,
 CASE WHEN isfinite(w.end_at) THEN to_char(w.end_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') ELSE 'invalid' END,
 nh.control_id IS NULL
 FROM @operations@ o LEFT JOIN @controls@ c ON c.id=o.resource_id
 LEFT JOIN @versions@ v ON v.control_id=o.resource_id AND v.revision=o.revision
 LEFT JOIN @versions@ changed ON changed.operation_id=o.operation_id
 LEFT JOIN @users@ u ON u.id=o.actor
 LEFT JOIN nonzero_held nh ON nh.control_id=c.id
 LEFT JOIN @windows@ w ON w.control_id=c.id AND w.start_at=CASE c.period_kind
 WHEN 'utc_day' THEN date_trunc('day',o.created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
 WHEN 'utc_month' THEN date_trunc('month',o.created_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' END
 ORDER BY o.operation_id`

type verifierOperation struct {
	id, kind, actor, scope, target, resource, revision, fingerprint, at string
	input, snapshot                                                     []byte
	control, controlScope, scopeID, tenant, period, tracking, effective *string
	version, versionPeriod, versionLimit                                *string
	versionEnabled                                                      *bool
	changed, actorExists                                                bool
	window, windowStart, windowEnd                                      *string
	zeroHeld                                                            bool
}

func verifierOperations(ctx context.Context, tx pgx.Tx, render verifierRender, out *Verification) error {
	rows, err := tx.Query(ctx, render(verifierOperationsSQL))
	if err != nil {
		return err
	}
	defer rows.Close()
	var lastID string
	var operationBad, snapshotBad, prefixGap bool
	flush := func() {
		if lastID == "" {
			return
		}
		if operationBad {
			out.Mismatches.OperationMismatches++
		}
		if snapshotBad {
			out.Mismatches.SnapshotMismatches++
		} else if prefixGap {
			out.Unproven.OperationSnapshotPrefixUnproven++
		}
	}
	for rows.Next() {
		var o verifierOperation
		if err = rows.Scan(&o.id, &o.kind, &o.actor, &o.scope, &o.target, &o.resource, &o.revision, &o.fingerprint, &o.input, &o.snapshot, &o.at,
			&o.control, &o.controlScope, &o.scopeID, &o.tenant, &o.period, &o.tracking, &o.effective,
			&o.version, &o.versionPeriod, &o.versionEnabled, &o.versionLimit, &o.changed, &o.actorExists,
			&o.window, &o.windowStart, &o.windowEnd, &o.zeroHeld); err != nil {
			return err
		}
		badOperation, badSnapshot, gap := o.check()
		if o.id != lastID {
			flush()
			lastID = o.id
			operationBad, snapshotBad, prefixGap = false, false, false
		}
		operationBad = operationBad || badOperation
		snapshotBad = snapshotBad || badSnapshot
		prefixGap = prefixGap || gap
	}
	if err = rows.Err(); err != nil {
		return err
	}
	flush()
	return nil
}

func (o verifierOperation) check() (badOperation, badSnapshot, gap bool) {
	in, inputErr := commanderDecodeInput(o.input, o.id)
	if inputErr != nil || !ValidPositiveRevision(o.revision) || !ValidUUID(o.actor) || !o.actorExists || !ValidUTC6(o.at) || o.control == nil || o.controlScope == nil || o.scopeID == nil || o.version == nil || o.versionPeriod == nil || o.versionEnabled == nil || o.versionLimit == nil {
		badOperation = true
	} else {
		kind, scope, target := "", "platform", ""
		switch *o.controlScope {
		case "tenant":
			kind = "tenant_periodic_budget.configure"
			target = "/api/platform/tenants/" + *o.scopeID + "/periodic-budget"
		case "key":
			kind = "key_periodic_budget.configure"
			target = "/api/keys/" + *o.scopeID + "/periodic-budget"
			if o.tenant != nil {
				scope = *o.tenant
			} else {
				badOperation = true
			}
		case "procurement":
			kind = "procurement_periodic_budget.configure"
			target = "/api/platform/procurement-accounts/" + *o.scopeID + "/periodic-budget"
		default:
			badOperation = true
		}
		if o.kind != kind || o.scope != scope || o.target != target || o.resource != *o.control || o.revision != *o.version || in.PeriodKind != *o.versionPeriod || in.Enabled != *o.versionEnabled || in.LimitAmount != *o.versionLimit {
			badOperation = true
		}
		rev, _ := strconv.ParseInt(o.revision, 10, 64)
		expected := rev
		if o.changed {
			expected--
		}
		if in.ExpectedRevision != strconv.FormatInt(expected, 10) {
			badOperation = true
		}
		fp, e := Fingerprint(o.kind, o.actor, o.scope, o.target, PutPeriodicBudget{o.id, in.ExpectedRevision, in.PeriodKind, in.Enabled, in.LimitAmount})
		if e != nil || fp != o.fingerprint {
			badOperation = true
		}
	}
	v, err := readDecodeView(o.snapshot)
	if err != nil {
		return badOperation, true, false
	}
	if o.controlScope == nil || o.scopeID == nil || o.version == nil || o.versionPeriod == nil || o.versionEnabled == nil || o.versionLimit == nil || o.tracking == nil || o.effective == nil || o.period == nil {
		return badOperation, true, false
	}
	if v.Scope != *o.controlScope || v.ScopeID != *o.scopeID || v.Revision != o.revision || v.AsOf != o.at || v.PeriodKind == nil || *v.PeriodKind != *o.versionPeriod || v.Enabled != *o.versionEnabled || v.LimitAmount == nil || *v.LimitAmount != *o.versionLimit || v.TrackingStartedAt == nil || *v.TrackingStartedAt != *o.tracking || v.EffectiveFrom == nil || *v.EffectiveFrom != *o.effective {
		badSnapshot = true
	}
	at, e1 := time.Parse(utc6Layout, o.at)
	tracking, e2 := time.Parse(utc6Layout, *o.tracking)
	effective, e3 := time.Parse(utc6Layout, *o.effective)
	if e1 != nil || e2 != nil || e3 != nil || !validPeriod(*o.period) {
		return badOperation, true, false
	}
	state := "active"
	if !*o.versionEnabled {
		state = "disabled"
	} else if at.Before(effective) {
		state = "scheduled"
	}
	if v.State != state || verifierPeriodStart(*o.period, at).Before(verifierPeriodStart(*o.period, tracking)) {
		badSnapshot = true
	}
	if v.CurrentWindow != nil {
		if o.window == nil || o.windowStart == nil || o.windowEnd == nil || v.CurrentWindow.ID != *o.window || v.CurrentWindow.StartAt != *o.windowStart || v.CurrentWindow.EndAt != *o.windowEnd {
			badSnapshot = true
		}
	}
	if badSnapshot {
		return badOperation, true, false
	}
	if o.window == nil && o.zeroHeld {
		if v.CurrentWindow != nil || v.CarryHeld != "0" {
			return badOperation, true, false
		}
		return badOperation, false, false
	}
	return badOperation, false, true
}

func verifierPeriodStart(period string, at time.Time) time.Time {
	at = at.UTC()
	day := at.Day()
	if period == "utc_month" {
		day = 1
	}
	return time.Date(at.Year(), at.Month(), day, 0, 0, 0, 0, time.UTC)
}
