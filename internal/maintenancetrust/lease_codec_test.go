package maintenancetrust

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type leaseCodecCallback struct{ calls *int }

func (v leaseCodecCallback) RewriteQuery(context.Context, *pgx.Conn, string, []any) (string, []any, error) {
	*v.calls++
	return "", nil, nil
}
func (v leaseCodecCallback) ScanRow(pgx.Rows) error { *v.calls++; return nil }
func (v leaseCodecCallback) Scan(any) error         { *v.calls++; return nil }

func TestReadLeaseCodecArgumentsAreClosedAndOwned(t *testing.T) {
	now := time.Now().UTC()
	data := []byte{1, 2}
	names := []string{"a", "b"}
	i16 := []int16{1}
	i32 := []int32{2}
	i64 := []int64{3}
	u32 := []uint32{4}
	in := []any{nil, true, "s", int(1), int8(1), int16(1), int32(1), int64(1), uint32(1), now, data, names, i16, i32, i64, u32}
	out, err := leaseArguments(in)
	if err != nil || !reflect.DeepEqual(out, in) {
		t.Fatal("exact parameter profile")
	}
	data[0] = 9
	names[0] = "changed"
	i16[0] = 9
	i32[0] = 9
	i64[0] = 9
	u32[0] = 9
	if out[10].([]byte)[0] != 1 || out[11].([]string)[0] != "a" || out[12].([]int16)[0] != 1 || out[13].([]int32)[0] != 2 || out[14].([]int64)[0] != 3 || out[15].([]uint32)[0] != 4 {
		t.Fatal("parameter slice aliases caller")
	}
	calls := 0
	type alias string
	var pointer *string
	for _, v := range []any{leaseCodecCallback{&calls}, pgx.NamedArgs{"a": 1}, pgx.QueryExecModeSimpleProtocol, pgx.QueryResultFormats{0}, alias("x"), pointer, 1.5, uint64(1), map[string]any{}, []any{1}} {
		if _, err := leaseArguments([]any{v}); err != ErrInput {
			t.Fatal("unreviewed argument accepted")
		}
	}
	if calls != 0 {
		t.Fatal("argument callback invoked")
	}
	if _, err := leaseArguments(make([]any, 1025)); err != ErrInput {
		t.Fatal("argument count not bounded")
	}
	if _, err := leaseArguments([]any{make([]byte, leaseArgumentBytes+1)}); err != ErrInput {
		t.Fatal("scalar bytes not bounded")
	}
	if _, err := leaseArguments([]any{make([]byte, leaseArgumentBytes), true}); err != ErrInput {
		t.Fatal("cumulative bytes not bounded")
	}
	if _, err := leaseArguments([]any{make([]string, leaseArgumentBytes/8+1)}); err != ErrInput {
		t.Fatal("empty array metadata not bounded")
	}
}

func TestReadLeaseCodecDestinationsAreClosedAndOwned(t *testing.T) {
	var b bool
	var s string
	var data []byte
	var n int
	var i16 int16
	var i32 int32
	var i64 int64
	var u32 uint32
	var stamp time.Time
	var ts pgtype.Timestamptz
	var text pgtype.Text
	var a16 []int16
	var a32 []int32
	var a64 []int64
	var au []uint32
	var names []string
	var ps *string
	var pb *bool
	var pn *int64
	var pt *time.Time
	dest := []any{&b, &s, &data, &n, &i16, &i32, &i64, &u32, &stamp, &ts, &text, &a16, &a32, &a64, &au, &names, &ps, &pb, &pn, &pt}
	targets, decode, err := leaseDestinations(dest)
	if err != nil || len(decode) != len(dest) {
		t.Fatal("exact destination profile")
	}
	raw := []byte{1, 2}
	*decode[2].(*[]byte) = raw
	arr := []int16{3}
	*decode[11].(*[]int16) = arr
	str := "kept"
	*decode[16].(**string) = &str
	yes := true
	*decode[17].(**bool) = &yes
	number := int64(7)
	*decode[18].(**int64) = &number
	now := time.Now()
	*decode[19].(**time.Time) = &now
	leaseOwnTargets(targets)
	leaseAssign(targets)
	raw[0] = 9
	arr[0] = 9
	str = "changed"
	yes = false
	number = 9
	now = time.Time{}
	if data[0] != 1 || a16[0] != 3 || ps == nil || *ps != "kept" || pb == nil || !*pb || pn == nil || *pn != 7 || pt == nil || pt.IsZero() {
		t.Fatal("destination borrows decoder data")
	}
	calls := 0
	var anyDest any
	var borrowed pgtype.DriverBytes
	type alias string
	var custom alias
	var nilString *string
	for _, d := range []any{leaseCodecCallback{&calls}, &anyDest, &borrowed, &custom, nilString, new(int8), new(uint64), nil} {
		if _, _, err := leaseDestinations([]any{d}); err != ErrInput {
			t.Fatal("unsafe scan destination accepted")
		}
	}
	if calls != 0 {
		t.Fatal("scan callback invoked")
	}
	if _, _, err := leaseDestinations(make([]any, 1025)); err != ErrInput {
		t.Fatal("destination count not bounded")
	}
}
