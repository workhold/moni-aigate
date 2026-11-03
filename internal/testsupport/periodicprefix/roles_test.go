package periodicprefix

import (
	"context"
	"strings"
	"testing"
)

func TestPrivilegeSubsetPreservesRequiredAndReachableBoundaries(t *testing.T) {
	for _, test := range []struct {
		name               string
		actual, allowed    []bool
		required, expected bool
	}{
		{"required_exact", []bool{true, false, true}, []bool{true, false, true}, true, true},
		{"required_missing", []bool{false, false, true}, []bool{true, false, true}, true, false},
		{"reachable_subset", []bool{false, false, true}, []bool{true, false, true}, false, true},
		{"reachable_escalation", []bool{false, true, true}, []bool{true, false, true}, false, false},
		{"required_escalation", []bool{true, true, true}, []bool{true, false, true}, true, false},
		{"shape", []bool{true}, []bool{true, false}, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if privilegeSubset(test.actual, test.allowed, test.required) != test.expected {
				t.Fatal("effective privilege boundary mismatch")
			}
		})
	}
}

func TestRolePinsAndUnavailableTransaction(t *testing.T) {
	valid := Pins{Schema: "pb_candidate_fixture", AppRole: "candidate_app", ProjectionRole: "candidate_projection"}
	if !validRolePins(valid) || VerifyRolesTx(context.Background(), nil, valid) != ErrRolePrivileges || VerifyRolesTx(nil, nil, valid) != ErrRolePrivileges {
		t.Fatal("nil transaction or valid pin shape handled incorrectly")
	}
	for _, pins := range []Pins{
		{Schema: "public", AppRole: "a", ProjectionRole: "p"},
		{Schema: "pg_temp_123", AppRole: "a", ProjectionRole: "p"},
		{Schema: "information_schema", AppRole: "a", ProjectionRole: "p"},
		{Schema: "fixture", AppRole: "same", ProjectionRole: "same"},
		{Schema: "fixture", AppRole: "a\x00b", ProjectionRole: "p"},
		{Schema: "fixture", AppRole: strings.Repeat("a", 64), ProjectionRole: "p"},
		{Schema: "", AppRole: "a", ProjectionRole: "p"},
	} {
		if validRolePins(pins) {
			t.Fatal("invalid role pins accepted")
		}
	}
}
