package procurementprefix

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func populatedOriginals(t *testing.T) OriginalRows {
	t.Helper()
	layout, _ := readLayout(t, "b-procurement-original-core-layout-v1.json")
	var rows OriginalRows
	value := reflect.ValueOf(&rows).Elem()
	for i, table := range layout.Tables {
		array := reflect.MakeSlice(value.Field(i).Type(), 1, 1)
		row := array.Index(0)
		for j, col := range table.Columns {
			f := row.Field(j)
			text := table.Name + "." + col.Name
			if col.Type != "TEXT" && col.Type != "JSONB" && col.Type != "INET" {
				text = "13"
			}
			switch {
			case f.Type() == timeType:
				f.Set(reflect.ValueOf(Time{Time: time.Date(2026, 11, 3, 1, 2, 3, 4000, time.FixedZone("fixture", 3600)), Valid: true}))
			case f.Kind() == reflect.String:
				f.SetString(text)
			case f.Kind() == reflect.Bool:
				f.SetBool(true)
			case f.Kind() == reflect.Pointer:
				p := reflect.New(f.Type().Elem())
				if p.Elem().Kind() == reflect.String {
					p.Elem().SetString(text)
				} else {
					p.Elem().SetBool(true)
				}
				f.Set(p)
			default:
				t.Fatalf("fixture missing physical kind %s", f.Type())
			}
		}
		value.Field(i).Set(array)
	}
	return rows
}

// The oracle is the old reader's unchanged binding order, independently of the
// new projection implementation and of the procurement SQL writer.
func TestBaseProjectionMatchesAllOriginalReaderColumns(t *testing.T) {
	original := populatedOriginals(t)
	before, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := projectBase(context.Background(), Rows{OriginalRows: original})
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(original)
	if err != nil || string(before) != string(after) {
		t.Fatal("projection mutated raw inputs")
	}
	source, err := os.ReadFile("../fundinglotscan/projection_procurement.go")
	if err != nil {
		t.Fatal(err)
	}
	bindings := regexp.MustCompile(`bind\[fundinglotverify\.([A-Za-z0-9]+)\]\("([a-z_]+)", "[^"]+", "([a-z_ ]+)"\)`).FindAllSubmatch(source, -1)
	if len(bindings) != 23 {
		t.Fatal("old original reader ABI inventory changed")
	}
	raw := reflect.ValueOf(original)
	projected := reflect.ValueOf(got)
	if projected.NumField() != 23 {
		t.Fatal("old Base arrays missing")
	}
	columns := 0
	for i, binding := range bindings {
		table := string(binding[2])
		var originalRow reflect.Value
		for j := 0; j < raw.NumField(); j++ {
			if raw.Type().Field(j).Tag.Get("json") == table {
				originalRow = raw.Field(j).Index(0)
				break
			}
		}
		if !originalRow.IsValid() {
			t.Fatalf("missing original physical table %s", table)
		}
		array := projected.Field(i)
		if array.Len() != 1 || array.Type().Elem().Name() != string(binding[1]) {
			t.Fatal("old row ABI array order/type changed")
		}
		row := array.Index(0)
		keys := strings.Fields(string(binding[3]))
		if row.NumField() != len(keys) {
			t.Fatal("old DTO column mapping differs")
		}
		for j, key := range keys {
			var physical reflect.Value
			for k := 0; k < originalRow.NumField(); k++ {
				if originalRow.Type().Field(k).Tag.Get("json") == key {
					physical = originalRow.Field(k)
					break
				}
			}
			if !physical.IsValid() {
				t.Fatalf("missing fixed original column %s.%s", table, key)
			}
			actual := row.Field(j)
			var expected any
			switch {
			case actual.Type() == reflect.TypeOf(time.Time{}):
				expected = physical.Interface().(Time).Time
			case actual.Type() == reflect.TypeOf((*time.Time)(nil)):
				stamp := physical.Interface().(Time).Time
				expected = &stamp
			case actual.Kind() == reflect.Int32:
				n, e := strconv.ParseInt(physical.String(), 10, 32)
				if e != nil {
					t.Fatal(e)
				}
				expected = int32(n)
			default:
				expected = physical.Interface()
			}
			if !reflect.DeepEqual(actual.Interface(), expected) {
				t.Fatalf("%s.%s Base projection changed", table, key)
			}
			if actual.Kind() == reflect.Pointer && actual.Type().Elem().Kind() == reflect.String && actual.Pointer() == physical.Pointer() {
				t.Fatalf("%s.%s retained caller mutable pointer", table, key)
			}
			columns++
		}
	}
	if columns != 173 {
		t.Fatal("incomplete old field oracle")
	}

}

func TestBaseProjectionRejectsLossAndPreservesNull(t *testing.T) {
	valid := Time{Time: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), Valid: true}
	for _, sequence := range []string{"-2147483648", "2147483647", "+01"} {
		n, err := projectInt32(sequence)
		want, e := strconv.ParseInt(sequence, 10, 32)
		if err != nil || e != nil || n != int32(want) {
			t.Fatal("representable int32 value changed")
		}
	}
	for _, sequence := range []string{"2147483648", "-2147483649", "not-an-integer"} {
		rows := Rows{OriginalRows: OriginalRows{UpstreamAttempts: []UpstreamAttemptRow{{Sequence: sequence, CreatedAt: valid}}}}
		if got, err := projectBase(context.Background(), rows); !errors.Is(err, ErrInput) || !reflect.DeepEqual(got, fundinglotverify.ProcurementEvidenceGraphRows{}) {
			t.Fatal("lossy int32 projection returned a partial Base")
		}
	}
	for _, stamp := range []Time{{}, {Valid: true, InfinityModifier: pgtype.Infinity}, {Valid: true, Time: time.Date(2026, 11, 3, 0, 0, 0, 1, time.UTC)}} {
		rows := Rows{OriginalRows: OriginalRows{RequestRecords: []RequestRecordRow{{RequestDeadline: stamp}}}}
		if got, err := projectBase(context.Background(), rows); !errors.Is(err, ErrInput) || !reflect.DeepEqual(got, fundinglotverify.ProcurementEvidenceGraphRows{}) {
			t.Fatal("lossy time projection returned a partial Base")
		}
	}
	rows := Rows{OriginalRows: OriginalRows{ProcurementCostProposals: []ProcurementCostProposalRow{{CreatedAt: valid}}}}
	base, err := projectBase(context.Background(), rows)
	if err != nil || base.Proposals[0].ApprovedAt != nil || base.Proposals[0].Approver != nil {
		t.Fatal("nullable original time/text changed")
	}
	empty := ""
	rows.OriginalRows.ProcurementCostProposals[0].Approver = &empty
	base, err = projectBase(context.Background(), rows)
	if err != nil || base.Proposals[0].Approver == nil || *base.Proposals[0].Approver != "" {
		t.Fatal("empty string was collapsed into SQL NULL")
	}
	rows.OriginalRows.ProcurementCostProposals[0].ApprovedAt = Time{Time: valid.Time}
	if _, err := projectBase(context.Background(), rows); !errors.Is(err, ErrInput) {
		t.Fatal("malformed nullable time was erased")
	}
	if got, err := projectBase(nil, Rows{}); !errors.Is(err, ErrInput) || !reflect.DeepEqual(got, fundinglotverify.ProcurementEvidenceGraphRows{}) {
		t.Fatal("nil context accepted")
	}
}

func TestBaseProjectionSharedPreflightAndEmptyShape(t *testing.T) {
	base, err := projectBase(context.Background(), Rows{})
	if err != nil || !reflect.DeepEqual(base, fundinglotverify.ProcurementEvidenceGraphRows{}) {
		t.Fatal("absent original arrays changed shape")
	}
	base, err = projectBase(context.Background(), Rows{OriginalRows: OriginalRows{Channels: []ChannelRow{}}})
	if err != nil || base.Channels == nil || base.Accounts != nil {
		t.Fatal("present empty and absent arrays were conflated")
	}
	rows := Rows{PrefixRows: PrefixRows{OperationRoots: []OperationRootRow{{ID: strings.Repeat("x", 65537)}}}}
	if got, err := projectBase(context.Background(), rows); !errors.Is(err, ErrResource) || !reflect.DeepEqual(got, fundinglotverify.ProcurementEvidenceGraphRows{}) {
		t.Fatal("Base projection bypassed the prefix part of the shared preflight")
	}
}
