//go:build historical

package maintenancescan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// These local fixed-reader fixtures provide no lease or authority.
type completeSourcesEmptyQuery struct{ calls int }
type completeSourcesEmptyRows struct {
	policy bool
	index  int
}

func (q *completeSourcesEmptyQuery) Query(sql string, _ ...any) (fundinglotscan.Rows, error) {
	q.calls++
	return &completeSourcesEmptyRows{policy: strings.Contains(sql, `"private_schema"."payment_policy"`), index: -1}, nil
}
func (r *completeSourcesEmptyRows) Next() (bool, error) {
	r.index++
	return r.policy && r.index == 0, nil
}
func (r *completeSourcesEmptyRows) Scan(dest ...any) error {
	if !r.policy || r.index != 0 || len(dest) != 4 {
		return errors.New("private fixture cannot scan this row")
	}
	*dest[0].(*bool) = true
	single, enabled := true, false
	*dest[1].(**bool), *dest[2].(**bool) = &single, &enabled
	*dest[3].(*pgtype.Text) = pgtype.Text{String: "1", Valid: true}
	return nil
}
func (*completeSourcesEmptyRows) Err() error   { return nil }
func (*completeSourcesEmptyRows) Close() error { return nil }

func TestCompleteSourcesMaintenanceRejectsFakeAuthorityAndLeaksNoRawValues(t *testing.T) {
	for _, lease := range []*maintenancetrust.ReadLease{nil, {}} {
		if got, err := VerifyCompleteSourcesMaintenance(lease); got != nil || err != fundinglotscan.ErrInput {
			t.Fatal("nil or zero complete-source lease accepted")
		}
	}
	q := &completeSourcesEmptyQuery{}
	facts, err := fundinglotscan.Read(context.Background(), q, "private_schema", fundinglotscan.ModeOrdinary)
	if err != nil || q.calls != 68 || facts.Summary().Scope != "complete_business_source_graphs_v1" || !facts.Summary().PhysicalEOFComplete {
		t.Fatal("fixed local source reader fixture failed", err)
	}
	for _, result := range []*CompleteSourcesResult{
		nil, {}, {facts: facts, mode: fundinglotscan.ModeOrdinary},
		{proof: &maintenancetrust.ScanProof{}},
		{facts: facts, mode: fundinglotscan.ModeOrdinary, proof: &maintenancetrust.ScanProof{}},
		{facts: facts, mode: fundinglotscan.ModeSandbox, proof: &maintenancetrust.ScanProof{}},
	} {
		permit := &maintenancetrust.Permit{}
		if result.Verify(permit) == nil {
			t.Fatal("local source facts fabricated complete-source authority")
		}
		if got, err := result.Summary(permit); err == nil || got != (fundinglotscan.Report{}) {
			t.Fatal("unverified complete-source summary leaked")
		}
		if result != nil {
			raw, err := json.Marshal(result)
			if err != nil || string(raw) != `{"complete_sources_result":"redacted"}` || !strings.Contains(fmt.Sprintf("%v %#v", result, result), "redacted") {
				t.Fatal("complete-source result exposed private state")
			}
		}
	}
	var decoded CompleteSourcesResult
	if json.Unmarshal([]byte(`{"facts":{"scope":"complete_business_source_graphs_v1"},"mode":1,"proof":{}}`), &decoded) != nil || decoded.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("JSON fabricated complete-source authority")
	}
	typ := reflect.TypeFor[CompleteSourcesResult]()
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).IsExported() {
			t.Fatal("complete-source result exports raw facts, mode or proof")
		}
	}
}

func TestCompleteSourcesMaintenanceClosesFailureCategories(t *testing.T) {
	for _, tc := range []struct{ input, want error }{
		{nil, fundinglotscan.ErrExecution},
		{errors.New("private driver DSN or SQL"), fundinglotscan.ErrExecution},
		{maintenancetrust.ErrTransaction, fundinglotscan.ErrExecution},
		{maintenancetrust.ErrIdentity, fundinglotscan.ErrExecution},
		{maintenancetrust.ErrExecution, fundinglotscan.ErrExecution},
		{maintenancetrust.ErrInput, fundinglotscan.ErrInput},
		{maintenancetrust.ErrTimeout, fundinglotscan.ErrTimeout},
		{context.Canceled, fundinglotscan.ErrCanceled},
		{context.DeadlineExceeded, fundinglotscan.ErrTimeout},
		{fmt.Errorf("private: %w", fundinglotscan.ErrInput), fundinglotscan.ErrInput},
		{fmt.Errorf("private: %w", fundinglotscan.ErrResource), fundinglotscan.ErrResource},
		{fmt.Errorf("private: %w", fundinglotscan.ErrCanceled), fundinglotscan.ErrCanceled},
		{fmt.Errorf("private: %w", fundinglotscan.ErrTimeout), fundinglotscan.ErrTimeout},
		{fmt.Errorf("private: %w", fundinglotscan.ErrExecution), fundinglotscan.ErrExecution},
	} {
		got := completeSourcesMaintenanceFailure(nil, tc.input)
		if got != tc.want || errors.Unwrap(got) != nil || strings.Contains(got.Error(), "private") {
			t.Fatal("complete-source error escaped its safe fixed category", got)
		}
	}
}

func TestCompleteSourcesPrivateAdaptersKeepFirstFailureAndNoContextValue(t *testing.T) {
	q := &completeSourcesLeaseQuery{lease: &maintenancetrust.ReadLease{}}
	if rows, err := q.Query("SELECT 1"); rows != nil || err != maintenancetrust.ErrInput || q.failure != maintenancetrust.ErrInput {
		t.Fatal("query adapter lost its first safe lease failure")
	}
	if q.observe(maintenancetrust.ErrExecution) != maintenancetrust.ErrExecution || q.failure != maintenancetrust.ErrInput {
		t.Fatal("cleanup replaced the originating lease failure")
	}
	c := completeSourcesLeaseContext{}
	if c.Err() != context.Canceled || c.Value("private-key") != nil {
		t.Fatal("lease context forwarded raw values or accepted zero state")
	}
	if _, ok := c.Deadline(); ok {
		t.Fatal("zero lease context invented a deadline")
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("zero lease context did not observe cancellation")
	}
}
