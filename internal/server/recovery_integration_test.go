package server

import (
	"context"
	"encoding/json"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"strings"
	"testing"
)

func TestHTTPRecoveryLatchCannotBeClearedByBusinessWrites(t *testing.T) {
	s, p := newAuthFixture(t)
	maker, _ := authUser(t, s, p, "finance_operator")
	checker, _ := authUser(t, s, p, "finance_approver")
	admin, _ := authUser(t, s, p, "admin")
	authExec(t, p, "UPDATE users SET mfa_enabled=true WHERE id=ANY($1)", []string{maker, checker, admin})
	authExec(t, p, "UPDATE recovery_gate SET required=true,epoch=epoch+1,latched_at=clock_timestamp() WHERE singleton")
	required, err := operations.RecoveryRequired(context.Background(), p)
	if err != nil || !required {
		t.Fatal(required, err)
	}
	s.Config.RecoveryMode = required // same persisted OR-env startup contract as main
	raw, csrf := authSession(t, s, p, maker, "", true, true)
	cr, cc := authSession(t, s, p, checker, "", true, true)
	ar, ac := authSession(t, s, p, admin, "", true, true)
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/platform/credits", raw, csrf, s.Config.Origin, map[string]any{}), 503)
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/recovery/propose", ar, ac, s.Config.Origin, map[string]any{"epoch": 1, "evidence_ref": "statement:reconciled", "reason": "external receipts checked"}), 403)
	proposal := authHTTP(t, s, "POST", "/api/recovery/propose", raw, csrf, s.Config.Origin, map[string]any{"epoch": 1, "evidence_ref": "statement:reconciled", "reason": "external receipts checked"})
	requireAuthStatus(t, proposal, 200)
	var gate operations.RecoveryGate
	if err = json.Unmarshal(authData(t, proposal), &gate); err != nil {
		t.Fatal(err)
	}
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/recovery/approve", raw, csrf, s.Config.Origin, map[string]any{"epoch": 1, "proposal_id": gate.ProposalID}), 403)
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/recovery/approve", cr, cc, s.Config.Origin, map[string]any{"epoch": 1, "proposal_id": gate.ProposalID}), 200)
	status := authHTTP(t, s, "GET", "/api/recovery/status", ar, ac, "", nil)
	requireAuthStatus(t, status, 200)
	if !strings.Contains(status.Body.String(), `"restart_required":true`) {
		t.Fatal(status.Body.String())
	}
	// Approval cannot reopen the running process; an explicit restart is required.
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/platform/credits", raw, csrf, s.Config.Origin, map[string]any{}), 503)
}
