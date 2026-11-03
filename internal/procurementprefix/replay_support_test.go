package procurementprefix

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReplayTupleKeysPreserveExactComponents(t *testing.T) {
	fixtures := [][]string{nil, {""}, {"", ""}, {"a:b", "c"}, {"a", "b:c"}, {"1:a"}, {"a", ""}, {"é", "网关"}, {"\x00", "x"}, {"x", "\x00"}, {"3:abc", ""}}
	seen := make(map[string]int)
	for i, parts := range fixtures {
		k := tupleKey(parts...)
		if prev, ok := seen[k]; ok {
			t.Fatalf("ambiguous keys %d %d", prev, i)
		}
		seen[k] = i
	}
	if tupleKey("é", "网关") != "2:é6:网关" {
		t.Fatal("tuple length must count UTF8 bytes")
	}
	s := make(keySet)
	s.add("a", "bc")
	s.add("a", "bc")
	s.add("ab", "c")
	if len(s) != 2 {
		t.Fatal("set did not use exact tuples")
	}
}

// Deterministic cancellation at a later work check, without sleep or timing
// dependence, exercises parsing/sorting/index loops after preflight completes.
type replayCancelContext struct {
	context.Context
	remaining int
}

func (c *replayCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestReplayWorkBudgetsAndRequiredZeroResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		rows Rows
		want error
	}{
		{"nil", nil, Rows{}, ErrInput},
		{"field", context.Background(), Rows{OriginalRows: OriginalRows{Channels: []ChannelRow{{ID: strings.Repeat("x", int(fieldLimit)+1)}}}}, ErrResource},
		{"all_arrays", context.Background(), Rows{OriginalRows: OriginalRows{Channels: make([]ChannelRow, 50001)}, PrefixRows: PrefixRows{RootSteps: make([]RootStepRow, 50000)}}, ErrResource},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, s, err := newReplayWork(tc.ctx, tc.rows)
			if !errors.Is(err, tc.want) || w != nil || !reflect.DeepEqual(s, Scan{}) {
				t.Fatalf("bad rejection: %v %v %+v", w, err, s)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, expired := range []context.Context{ctx, func() context.Context {
		c, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer stop()
		return c
	}()} {
		_, _, err := newReplayWork(expired, Rows{})
		if err != ErrCanceled && err != ErrTimeout {
			t.Fatalf("expiry returned %v", err)
		}
	}
	w, _, err := newReplayWork(context.Background(), Rows{})
	if err != nil {
		t.Fatal(err)
	}
	// Use the preserved absolute deadline even if ctx.Err has not fired.
	w.b.deadline = time.Now().Add(-time.Second)
	if keys, err := enumerateRequiredBase(w, requiredAllFixture()); err != ErrTimeout || !reflect.DeepEqual(keys, propositionKeys{}) {
		t.Fatal("expired Required returned partial keys")
	}
	for _, phase := range []string{"index", "parse", "sort"} {
		t.Run(phase, func(t *testing.T) {
			c := &replayCancelContext{Context: context.Background(), remaining: 4}
			rw := &requiredWork{replay: &replayWork{b: budget{ctx: c}}, keys: newPropositionKeys()}
			switch phase {
			case "index":
				r := requiredAllFixture()
				for i := 0; i < 3000; i++ {
					r.Conflicts = append(r.Conflicts, r.Conflicts[0])
					r.Conflicts[len(r.Conflicts)-1].ID = fmt.Sprint(i)
				}
				keys, err := enumerateRequiredBase(rw.replay, r)
				if err != ErrCanceled || !reflect.DeepEqual(keys, propositionKeys{}) {
					t.Fatal("cancelled enumeration returned partial keys")
				}
			case "parse":
				if rw.jsonUnicode(`"`+strings.Repeat("x", 60000)+`"`) || rw.err != ErrCanceled {
					t.Fatal("Unicode scan did not stop on cancellation")
				}
			case "sort":
				values := make([]int64, 3000)
				for i := range values {
					values[i] = int64(3000 - i)
				}
				if rw.sortRevisions(values) != ErrCanceled {
					t.Fatal("sort did not stop on cancellation")
				}
			}
		})
	}
}
func TestRequiredParserGrammarAndMergeSort(t *testing.T) {
	w := &requiredWork{replay: &replayWork{b: budget{ctx: context.Background()}}}
	for _, raw := range []string{`"\ud800"`, `"\udc00"`, `"\ud800\u0041"`, `"\ud800\udc00` + `"` + `x"\ud800"`} {
		if w.jsonUnicode(raw) {
			t.Fatalf("malformed UTF16 accepted: %s", raw)
		}
	}
	for _, raw := range []string{`"\ud83d\ude00"`, `"literal 😀"`, `"\\ud800"`} {
		if !w.jsonUnicode(raw) {
			t.Fatalf("valid UTF16 rejected: %s", raw)
		}
	}
	for _, raw := range []string{
		strings.Replace(requiredFixtureUsage, `"status":"confirmed_final"`, `"status":"confirmed_final","sta\u0074us":"confirmed_final"`, 1),
		strings.Replace(requiredFixtureUsage, `"input_tokens":10`, `"input_tokens":1e1`, 1),
		strings.Replace(requiredFixtureUsage, `"total_tokens":12`, `"total_tokens":13`, 1),
	} {
		if _, ok := w.usageParse(raw); ok {
			t.Fatal("invalid usage accepted")
		}
	}
	values := []int64{9, 1, 7, 7, 0, 3, 2}
	if err := w.sortRevisions(values); err != nil || !reflect.DeepEqual(values, []int64{0, 1, 2, 3, 7, 7, 9}) {
		t.Fatal("bounded sort changed values")
	}
}
