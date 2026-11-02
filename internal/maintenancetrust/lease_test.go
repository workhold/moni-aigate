package maintenancetrust

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestReadLeaseZeroValuesAndRedaction(t *testing.T) {
	var l ReadLease
	var rows Rows
	var row Row
	var proof ScanProof
	if _, err := (*Permit)(nil).BeginScan(context.Background(), ScanLedger); err != ErrInput {
		t.Fatal("nil permit")
	}
	if l.Require(ScanLedger) != ErrInput || l.Abort() != ErrInput || l.Err() != ErrInput {
		t.Fatal("zero lease accepted")
	}
	if _, err := l.Finish(); err != ErrInput {
		t.Fatal("zero Finish")
	}
	if _, err := l.Query("SELECT 1"); err != ErrInput || l.QueryRow("SELECT 1").Scan() != ErrInput {
		t.Fatal("zero query")
	}
	if _, err := rows.Next(); err != ErrInput || rows.Scan() != ErrInput || rows.Err() != ErrInput || rows.Close() != ErrInput || row.Scan() != ErrInput {
		t.Fatal("zero rows")
	}
	if proof.Verify(&Permit{}, ScanLedger) != ErrInput {
		t.Fatal("zero proof")
	}
	if _, ok := l.Deadline(); ok {
		t.Fatal("zero deadline")
	}
	select {
	case <-l.Done():
	default:
		t.Fatal("zero Done not closed")
	}
	for _, v := range []any{l, &l, rows, &rows, row, &row, proof, &proof} {
		typ := reflect.TypeOf(v)
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).IsExported() {
				t.Fatal("exported capability field")
			}
		}
		b, err := json.Marshal(v)
		if err != nil || !containsAuthorityText(string(b), "redacted") || !containsAuthorityText(fmt.Sprintf("%#v", v), "redacted") {
			t.Fatal("unredacted capability")
		}
		for _, method := range []string{"Tx", "Conn", "PgConn", "Context", "Value", "Values", "RawValues", "Exec", "Begin", "Commit", "Rollback", "CopyFrom", "SendBatch"} {
			if _, ok := reflect.TypeOf(v).MethodByName(method); ok {
				t.Fatal("forbidden capability method")
			}
		}
	}
	for _, err := range []error{ErrInput, ErrGateBusy, ErrTimeout, ErrTransaction, ErrExecution} {
		if errors.Unwrap(err) != nil {
			t.Fatal("driver error unwrapped")
		}
	}
	for kind := ScanKind(0); kind < 10; kind++ {
		if validScanKind(kind) != (kind >= 1 && kind <= 6) {
			t.Fatal("unfrozen scan kind")
		}
	}
}

func TestReadLeaseRejectedCallbackNeverReachesDriver(t *testing.T) {
	calls := 0
	driver := &pureLeaseRows{count: 1}
	l, q, _ := pureReadLease(t, driver, false)
	q.lease.snapshot.gate.mu.Lock()
	q.lease.query = nil
	q.lease.snapshot.gate.mu.Unlock()
	if _, err := l.Query("SELECT 1", leaseCodecCallback{&calls}); err != ErrInput || calls != 0 || !l.state.revoked {
		t.Fatal("QueryRewriter not refused before driver")
	}
	driver = &pureLeaseRows{count: 1}
	_, q, _ = pureReadLease(t, driver, false)
	r := &Rows{state: q}
	if ok, err := r.Next(); !ok || err != nil {
		t.Fatal("test row")
	}
	if r.Scan(leaseCodecCallback{&calls}) != ErrInput || calls != 0 || driver.scans.Load() != 0 {
		t.Fatal("RowScanner reached original driver")
	}
}
