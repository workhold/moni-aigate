package periodicbudget_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func pbBillRecoveryWaitForActualDeadline(t *testing.T, d *pbBillDispatchFixture, request string) {
	t.Helper()
	for {
		var expired bool
		var remainingMicros int64
		pbTxMust(t, "observe actual DB request deadline without changing source facts", d.f.app.QueryRow(d.f.ctx, `WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS at)
 SELECT request_deadline<instant.at,greatest(1,ceil(extract(epoch FROM (request_deadline-instant.at))*1000000))::bigint
 FROM request_records,instant WHERE id=$1`, request).Scan(&expired, &remainingMicros))
		if expired {
			return
		}
		wait := time.Duration(remainingMicros) * time.Microsecond
		if wait > 2*time.Second {
			wait = 2 * time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-d.f.ctx.Done():
			timer.Stop()
			t.Fatal("actual request deadline wait exceeded fixture context")
		}
	}
}

func TestPBBillingRecoverProcurementExpiresActualPreparedAndSendingSources(t *testing.T) {
	for _, sending := range []bool{false, true} {
		name := "prepared_released"
		if sending {
			name = "sending_unknown"
		}
		t.Run(name, func(t *testing.T) {
			d := pbBillDispatchNew(t)
			f := d.f
			var deadline time.Time
			pbTxMust(t, "choose legal short real request deadline before Reserve", f.app.QueryRow(f.ctx, `SELECT clock_timestamp()+interval '2 seconds'`).Scan(&deadline))
			r, err := d.store.Reserve(f.ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key, ClientIP: "127.0.0.1"},
				RequestID: uuid.NewString(), ModelID: f.model, RouteRevision: 2, RequestHMAC: uuid.NewString(), IdempotencyKey: uuid.NewString(), ProfileVersion: f.profileVersion, PolicyVersion: "r3",
				InputUpperBound: 100, OutputLimit: 20, RequestDeadline: deadline})
			pbTxMust(t, "Reserve actual customer source with original short deadline", err)
			t.Cleanup(func() { d.store.EndRequest(r.RequestID) })
			a, err := d.store.RecordAttempt(f.ctx, billing.AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: d.channel, ChannelVersionID: d.version,
				CredentialID: d.credential, UpstreamModel: "upstream", Sequence: 1, CostQuote: &d.quote})
			pbTxMust(t, "RecordAttempt creates real 240 supplier reserve and periodic source effect", err)
			d.state(t, r, a, "prepared", "accepted", false)
			if sending {
				pbTxMust(t, "commit actual sending permission before the short deadline", d.store.PrepareDispatch(f.ctx, billing.DispatchInput{TenantID: f.tenant, AttemptID: a.ID}))
				d.state(t, r, a, "sending", "dispatching", true)
			}
			// Recovery is favorable cleanup. Current zero caps must neither
			// prevent a provably unsent release nor erase uncertain consumption.
			for _, scope := range []string{"tenant", "key", "procurement"} {
				d.cap(t, scope, "0")
			}
			pbBillRecoveryWaitForActualDeadline(t, d, r.RequestID)
			n, err := d.store.RecoverProcurement(f.ctx, 100)
			pbTxMust(t, "actual RecoverProcurement after observed real deadline", err)
			if n != 1 {
				t.Fatal("recovery did not process exactly the original prepared/sending source")
			}
			state, delivery, request, afterHeld, allocationState, eventType, delta, eventKey := "released", "failed", "accepted", "0", "released", "expired_without_permit", "-240", "release:"+a.ID
			supplierRevision := 2
			if sending {
				state, delivery, request, afterHeld, allocationState, eventType, delta, eventKey = "unknown", "unknown", "dispatching", "240", "held", "recovered_unknown", "0", "unknown:"+a.ID
				supplierRevision = 3 // prepared->sending advances 019 state revision without a money event.
			}
			var exact bool
			oldStart := time.Date(d.sourceAt.Year(), d.sourceAt.Month(), d.sourceAt.Day(), 0, 0, 0, 0, time.UTC)
			pbTxMust(t, "verify exact 019 recovery event and B mirror with untouched customer reserve", f.app.QueryRow(f.ctx, `SELECT
 (SELECT state=$4 AND held_amount=$5::numeric AND spent_amount=0 AND revision=$17 FROM procurement_reservations WHERE attempt_id=$1)
 AND (SELECT held_amount=$5::numeric AND spent_amount=0 FROM procurement_accounts WHERE id=$2)
 AND (SELECT delivery_state=$6 AND (pb_dispatch_checked_at IS NOT NULL)=$7 FROM upstream_attempts WHERE id=$1)
 AND (SELECT status=$8 AND billing_status='reserved' FROM request_records WHERE id=$3)
 AND (SELECT status='reserved' AND amount=120 AND deadline>clock_timestamp() FROM reservations WHERE request_id=$3)
 AND (SELECT spent=0 AND held=120 FROM api_keys WHERE id=$9)
 AND (SELECT balance=9880 FROM ledger_accounts WHERE tenant_id=$10 AND kind='AVAILABLE')
 AND (SELECT balance=120 FROM ledger_accounts WHERE tenant_id=$10 AND kind='HELD')
 AND (SELECT count(*)=0 FROM settlements WHERE request_id=$3)
 AND (SELECT count(*)=1 FROM journals WHERE reference=$3 AND kind='reserve')
 AND (SELECT count(*)=2 AND bool_and(a.held_amount=120 AND a.spent_amount=0 AND a.state='held' AND a.revision=1 AND w.start_at=$11)
 FROM periodic_budget_allocations a JOIN periodic_budget_controls c ON c.id=a.control_id JOIN periodic_budget_windows w ON w.id=a.window_id WHERE c.scope IN ('tenant','key'))
 AND (SELECT a.held_amount=$5::numeric AND a.spent_amount=0 AND a.state=$12 AND a.revision=2 AND w.start_at=$11
 FROM periodic_budget_allocations a JOIN periodic_budget_windows w ON w.id=a.window_id WHERE a.control_id=$13 AND a.subject_id=$1)
 AND (SELECT held_amount=$5::numeric AND spent_amount=0 AND event_seq=2 FROM periodic_budget_controls WHERE id=$13)
 AND (SELECT count(*)=1 FROM procurement_budget_events WHERE attempt_id=$1 AND event_key=$14 AND type=$15 AND held_delta=$16::numeric AND spent_delta=0)
 AND (SELECT count(*)=1 FROM periodic_budget_events be JOIN procurement_budget_events pe ON pe.id=be.source_id
 JOIN periodic_budget_allocations a ON a.id=be.allocation_id
 WHERE be.control_id=$13 AND be.source_kind='procurement_event' AND pe.attempt_id=$1 AND pe.event_key=$14
 AND a.subject_id=$1 AND be.allocation_before_revision=1 AND be.before_held=240 AND be.before_spent=0
 AND be.after_held=$5::numeric AND be.after_spent=0 AND be.after_source_spent=0 AND be.after_state=$12
 AND be.held_delta=pe.held_delta AND be.spent_delta=pe.spent_delta)
 AND (SELECT count(*)=2 FROM procurement_budget_events)
 AND (SELECT count(*)=4 FROM periodic_budget_events)
 AND (SELECT count(*)=1 FROM upstream_attempts)
 AND (SELECT count(*)=0 FROM finalization_records)
 AND (SELECT count(*)=0 FROM metering_events)`, a.ID, f.account, r.RequestID, state, afterHeld, delivery, sending, request, f.key, f.tenant,
				oldStart, allocationState, d.controls["procurement"], eventKey, eventType, delta, supplierRevision).Scan(&exact))
			if !exact {
				t.Fatal("recovery failed exact source/mirror conservation or altered customer R/attempt count")
			}
			before := d.snapshot(t)
			n, err = d.store.RecoverProcurement(f.ctx, 100)
			pbTxMust(t, "repeat actual procurement recovery with original source identity", err)
			if n != 0 || before != d.snapshot(t) {
				t.Fatal("repeated recovery appended an event, changed money or recreated a sending attempt")
			}
			f.verifyFinance(t)
		})
	}
}
