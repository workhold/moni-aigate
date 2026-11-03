package procurementprefix

import (
	"encoding/json"
	"reflect"
	"strings"
)

// sourceClosureResult remains private and cannot authorize another replay
// domain. In particular, passing the structural leaves below does not qualify
// a source until all branch unions and original/companion relations exist.
type sourceClosureResult struct {
	Stats         SourceClosure
	Qualified     map[string]keySet
	Complete      bool
	Unimplemented []string
	// Only manual-action legacy cases; this is not the complete uncovered-case
	// set, nor an authorization result for another replay domain.
	ReconciliationLegacyCases keySet
}

type sourceClosureState struct {
	w      *replayWork
	stats  SourceClosure
	bad    map[string]keySet
	tables map[string][]sourceRawRow
}

type sourceRawRow struct {
	key   string
	value reflect.Value
}

func (s *sourceClosureState) mark(kind, table, key string) {
	keys := s.bad[kind]
	if keys == nil {
		keys = make(keySet)
		s.bad[kind] = keys
	}
	keys[tupleKey(table, key)] = struct{}{}
}

// checkSourceClosure checks genuine, independently computed structural leaves.
// Complete is deliberately false: local payloads, declared output relations and
// continuity do not replace full historical observations and eligibility work.
// The domain cannot supply Qualified object keys until every obligation exists.
func checkSourceClosure(w *replayWork, rows Rows) (sourceClosureResult, error) {
	if w == nil || w.b.ctx == nil {
		return sourceClosureResult{}, ErrInput
	}
	if _, err := budgetRows(w.b.ctx, rows); err != nil {
		return sourceClosureResult{}, err
	}
	s := sourceClosureState{w: w, bad: make(map[string]keySet), tables: make(map[string][]sourceRawRow)}
	for _, arrays := range []reflect.Value{reflect.ValueOf(rows.OriginalRows), reflect.ValueOf(rows.PrefixRows)} {
		if err := s.physicalRows(arrays); err != nil {
			return sourceClosureResult{}, err
		}
	}
	if err := s.originalUnions(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.originalValueRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.routeRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.usagePayloadRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.payloadRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.pbPayloadRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.pbOperationRelations(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.pbEventRelationRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.pbSnapshotPrefixRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.semanticRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.finishUnionRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.businessUnionRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.generated(rows.PrefixRows); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.canonical(rows.PrefixRows); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.companions(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.originalPointers(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.payloadRelationRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.correctionRelationRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.effectRelations(); err != nil {
		return sourceClosureResult{}, err
	}
	manual, err := s.reconciliationOrder()
	if err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.birthSnapshots(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.chains(rows.PrefixRows); err != nil {
		return sourceClosureResult{}, err
	}
	if err := s.continuityRows(); err != nil {
		return sourceClosureResult{}, err
	}
	if err := w.b.check(); err != nil {
		return sourceClosureResult{}, err
	}
	s.stats.InvalidScalarRows = uint64(len(s.bad["scalar"]))
	s.stats.DuplicateRows = uint64(len(s.bad["duplicate"]))
	s.stats.CompanionRows = uint64(len(s.bad["companion"]))
	s.stats.OriginalPointerRows = uint64(len(s.bad["pointer"]))
	s.stats.GeneratedRows = uint64(len(s.bad["generated"]))
	s.stats.RootTemplateRows = uint64(len(s.bad["root"]))
	s.stats.CanonicalUnionRows = uint64(len(s.bad["canonical"]))
	s.stats.RequestChainRows = uint64(len(s.bad["request_chain"]))
	s.stats.AttemptChainRows = uint64(len(s.bad["attempt_chain"]))
	s.stats.AccountChainRows = uint64(len(s.bad["account_chain"]))
	s.stats.EvidenceChainRows = uint64(len(s.bad["evidence_chain"]))
	return sourceClosureResult{Stats: s.stats, Qualified: make(map[string]keySet), ReconciliationLegacyCases: manual.LegacyCases, Complete: false, Unimplemented: []string{
		"remaining cross-row source/original semantics and actual business cause/decision eligibility; implemented command snapshots, typed audit/outbox slots, correction-original contents, cost cause-mutation-link and declared ledger/PB effects do not qualify their sources",
		"legacy PB precise observation cuts, complete initial membership and historical scope existence/absence bridges; covered command dynamic snapshot amounts/revision/latest-tail prefixes, raw configuration/version/calendar/observation relations and event/current projection replay are implemented",
		"historical latest/sent observation for Reconcile branches without a derived request decision; existing decision-predecessor, attempt continuity and positive PB event bridges do not prove the missing observations",
		"per-object dependency closure and SourceClosure Qualified key construction after all remaining obligations are implemented",
	}}, nil
}

func (s *sourceClosureState) physicalRows(arrays reflect.Value) error {
	for i := 0; i < arrays.NumField(); i++ {
		table := arrays.Type().Field(i).Tag.Get("json")
		layout, exists := sourcePhysicalLayouts[table]
		if !exists {
			return ErrInput
		}
		array := arrays.Field(i)
		seen := make(keySet)
		for j := 0; j < array.Len(); j++ {
			row := array.Index(j)
			if row.NumField() != len(layout.kinds) {
				return ErrInput
			}
			key := sourceRowKey(row, layout.pk)
			if _, duplicate := seen[key]; duplicate {
				s.mark("duplicate", table, key)
			}
			seen[key] = struct{}{}
			s.tables[table] = append(s.tables[table], sourceRawRow{key: key, value: row})
			for k := 0; k < row.NumField(); k++ {
				if err := s.w.tick(); err != nil {
					return err
				}
				if !sourcePhysicalCell(row.Field(k), layout.kinds[k]) {
					s.mark("scalar", table, key)
				}
			}
		}
	}
	return nil
}

func sourcePhysicalCell(v reflect.Value, kind byte) bool {
	nullable := kind >= 'a' && kind <= 'z'
	if nullable {
		kind -= 'a' - 'A'
	}
	if v.Type() == timeType {
		t := v.Interface().(Time)
		return finite(t) || nullable && nullTime(t)
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nullable
		}
		v = v.Elem()
	}
	if kind == 'B' {
		return v.Kind() == reflect.Bool
	}
	if v.Kind() != reflect.String || !validText(v.String()) {
		return false
	}
	value := v.String()
	switch kind {
	case 'T', 'A': // TEXT and the PG16 INET text projection.
		return true
	case 'J':
		return json.Valid([]byte(value))
	case 'H':
		return signedInteger(value, 16)
	case 'I':
		return signedInteger(value, 32)
	case 'L':
		return signedInteger(value, 64)
	case 'X':
		return unsignedInteger(value, 64, false)
	case 'O':
		return unsignedInteger(value, 32, false)
	case 'N':
		_, ok := numeric(value, true)
		return ok
	}
	return false
}

func sourceField(row reflect.Value, name string) reflect.Value {
	if !row.IsValid() || row.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	for i := 0; i < row.NumField(); i++ {
		if row.Type().Field(i).Tag.Get("json") == name {
			return row.Field(i)
		}
	}
	return reflect.Value{}
}

func sourceText(row reflect.Value, name string) string {
	v := sourceField(row, name)
	if v.IsValid() && v.Kind() == reflect.String {
		return v.String()
	}
	return ""
}

func sourceOptional(row reflect.Value, name string) *string {
	v := sourceField(row, name)
	if v.IsValid() && v.Kind() == reflect.Pointer && !v.IsNil() && v.Elem().Kind() == reflect.String {
		return v.Interface().(*string)
	}
	return nil
}

func sourceRowKey(row reflect.Value, pk string) string {
	parts := strings.Fields(pk)
	for i, name := range parts {
		v := sourceField(row, name)
		if v.Type() == timeType {
			t := v.Interface().(Time)
			parts[i] = tupleKey("time", t.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"))
		} else {
			parts[i] = v.String()
		}
	}
	return tupleKey(parts...)
}
