package maintenancetrust

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// This fake records fixed reader calls only. It cannot satisfy an actual
// snapshot binding, InstallationProof or authority issuer verification.
type maintenanceFactsTx struct {
	pgx.Tx
	exec     func(context.Context, string, ...any) (pgconn.CommandTag, error)
	queryRow func(context.Context, string, ...any) pgx.Row
}

func (t *maintenanceFactsTx) Exec(ctx context.Context, q string, args ...any) (pgconn.CommandTag, error) {
	return t.exec(ctx, q, args...)
}

func (t *maintenanceFactsTx) QueryRow(ctx context.Context, q string, args ...any) pgx.Row {
	return t.queryRow(ctx, q, args...)
}

type maintenanceFactsRow func(...any) error

func (r maintenanceFactsRow) Scan(dest ...any) error { return r(dest...) }

func TestMaintenanceSnapshotReadersRejectInvalidInputWithoutSQL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		st   *snapshotState
		want error
	}{
		{"nil context", nil, nil, ErrInput},
		{"cancelled", ctx, nil, ErrTimeout},
		{"nil snapshot", context.Background(), nil, ErrTransaction},
		{"missing gate", context.Background(), &snapshotState{}, ErrTransaction},
		{"missing transaction", context.Background(), &snapshotState{gate: &gateState{pins: purePins()}}, ErrTransaction},
		{"invalid pins", context.Background(), &snapshotState{rawTx: &maintenanceFactsTx{}, gate: &gateState{}}, ErrIdentity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if id, err := readMaintenanceIdentitySnapshot(tc.ctx, tc.st); id != nil || err != tc.want {
				t.Fatal("identity reader accepted invalid input")
			}
			if err := verifyMaintenanceStoppedSnapshot(tc.ctx, tc.st); err != tc.want {
				t.Fatal("stopped reader accepted invalid input")
			}
			if raw, err := readMaintenanceManifestSnapshot(tc.ctx, tc.st); raw != nil || err != tc.want {
				t.Fatal("manifest reader accepted invalid input")
			}
			facts, err := readPredecessorSnapshot(tc.ctx, tc.st)
			if !reflect.DeepEqual(facts, predecessorSnapshotFacts{}) || err != tc.want {
				t.Fatal("predecessor reader returned partial facts")
			}
		})
	}
}

func TestMaintenanceStoppedSnapshotClearsBeforeCompleteRuntimeSample(t *testing.T) {
	for _, sandbox := range []bool{false, true} {
		pins := purePins()
		wantRoles := []uint32{12, 13}
		if sandbox {
			pins = pureSandboxPins()
			wantRoles = []uint32{12, 13, 14, 18, 20}
		}
		calls := 0
		tx := &maintenanceFactsTx{
			exec: func(_ context.Context, q string, args ...any) (pgconn.CommandTag, error) {
				calls++
				if calls != 1 || q != clearMaintenanceActivitySQL || len(args) != 1 || args[0] != pgx.QueryExecModeSimpleProtocol {
					t.Fatal("activity cache was not separately cleared first")
				}
				return pgconn.CommandTag{}, nil
			},
			queryRow: func(_ context.Context, q string, args ...any) pgx.Row {
				calls++
				if calls != 2 || q != stoppedMaintenanceActivitySQL || len(args) != 3 || args[0] != pgx.QueryExecModeSimpleProtocol || args[1] != pins.Database.OID || !reflect.DeepEqual(args[2], wantRoles) {
					t.Fatal("runtime sample did not follow clear with the pinned role set")
				}
				return maintenanceFactsRow(func(dest ...any) error {
					if len(dest) != 1 {
						t.Fatal("unexpected activity projection")
					}
					*dest[0].(*int64) = 0
					return nil
				})
			},
		}
		if err := verifyMaintenanceStoppedSnapshot(context.Background(), &snapshotState{rawTx: tx, gate: &gateState{pins: pins}}); err != nil || calls != 2 {
			t.Fatal("complete stopped sample failed")
		}
	}
}

func TestMaintenanceStoppedSnapshotRejectsActiveOrFailedSamples(t *testing.T) {
	for _, tc := range []struct {
		name       string
		active     int64
		clearError bool
		scanError  bool
		cancel     bool
		want       error
	}{
		{"active runtime", 1, false, false, false, ErrIdentity},
		{"clear failure", 0, true, false, false, ErrIdentity},
		{"sample failure", 0, false, true, false, ErrIdentity},
		{"cancel during clear", 0, false, false, true, ErrTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			samples := 0
			tx := &maintenanceFactsTx{
				exec: func(context.Context, string, ...any) (pgconn.CommandTag, error) {
					if tc.cancel {
						cancel()
					}
					if tc.clearError {
						return pgconn.CommandTag{}, ErrExecution
					}
					return pgconn.CommandTag{}, nil
				},
				queryRow: func(context.Context, string, ...any) pgx.Row {
					samples++
					return maintenanceFactsRow(func(dest ...any) error {
						if tc.scanError {
							return ErrExecution
						}
						*dest[0].(*int64) = tc.active
						return nil
					})
				},
			}
			if err := verifyMaintenanceStoppedSnapshot(ctx, &snapshotState{rawTx: tx, gate: &gateState{pins: purePins()}}); err != tc.want {
				t.Fatal("unsafe stopped sample accepted")
			}
			if tc.clearError && samples != 0 {
				t.Fatal("sampled after cache clear failed")
			}
		})
	}
}

func TestMaintenanceFactsCannotIssueOriginalAuthority(t *testing.T) {
	st := &snapshotState{rawTx: &maintenanceFactsTx{}, gate: &gateState{pins: purePins()}}
	a := &authorityStage{snapshot: st}
	if id, err := a.readMaintenanceIdentity(context.Background()); id != nil || err != ErrTransaction {
		t.Fatal("identity wrapper skipped issuer verification")
	}
	if raw, err := a.attestManifest(context.Background()); raw != nil || err != ErrTransaction {
		t.Fatal("manifest wrapper skipped issuer verification")
	}
	if err := a.verifyStopped(context.Background()); err != ErrTransaction {
		t.Fatal("stopped wrapper skipped issuer verification")
	}
	if e, err := readPredecessorAuthority(a); e != nil || err != ErrTransaction {
		t.Fatal("predecessor wrapper accepted unissued authority")
	}
	facts := predecessorSnapshotFacts{canonical: []byte("private-original"), v1SHA256: "private-digest"}
	for _, v := range []any{facts, &facts} {
		if _, ok := v.(interface{ Verify(context.Context) error }); ok {
			t.Fatal("plain predecessor facts acquired an authorization method")
		}
		raw, err := json.Marshal(v)
		if err != nil || containsAuthorityText(string(raw), "private-") || containsAuthorityText(fmt.Sprintf("%#v", v), "private-") {
			t.Fatal("snapshot facts leaked their private values")
		}
	}
}
