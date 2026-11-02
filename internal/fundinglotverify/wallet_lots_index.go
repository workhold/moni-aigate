package fundinglotverify

import "reflect"

func wlTextField(row reflect.Value, name string) (string, bool) {
	v := row.FieldByName(name)
	if !v.IsValid() {
		return "", false
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.String {
		return "", false
	}
	return v.String(), true
}
func (w *wlWork) by(table, field, value string) []wlRef {
	key := wlField{table, field}
	index, exists := w.fields[key]
	if !exists {
		t := w.tables[table]
		if t == nil {
			w.err = ErrWalletLotsRowsInput
			return nil
		}
		index = map[string][]wlRef{}
		for i := 0; i < t.rows.Len(); i++ {
			if !w.tick() {
				return nil
			}
			if text, ok := wlTextField(t.rows.Index(i), field); ok {
				index[text] = append(index[text], wlRef{table, i})
			}
		}
		w.fields[key] = index
	}
	return index[value]
}
func (w *wlWork) exists(table, id string) bool {
	t := w.tables[table]
	return t != nil && len(t.primary[wlKey{id: id}]) > 0
}
func (w *wlWork) buildIndexes() {
	v := reflect.ValueOf(w.in)
	counts := reflect.ValueOf(&w.out.Scanned).Elem()
	for i := 0; i < v.NumField(); i++ {
		table := v.Type().Field(i).Name
		rows := v.Field(i)
		counts.Field(i).SetUint(uint64(rows.Len()))
		t := &wlTable{rows: rows, primary: map[wlKey][]wlRef{}}
		w.tables[table] = t
		idField := "ID"
		switch table {
		case "Attestations":
			idField = "EpochID"
		case "LegacyReceipts", "LegacySandboxReceipts":
			idField = "ReceiptID"
		case "LegacySettlements":
			idField = "SettlementID"
		case "LegacyApprovedRefunds":
			idField = "RefundOrderID"
		case "Heads":
			idField = "TenantID"
		case "Balances":
			idField = "LotID"
		case "LegacySources":
			idField = "SourceID"
		}
		for j := 0; j < rows.Len(); j++ {
			if !w.tick() {
				return
			}
			ref := wlRef{table, j}
			row := rows.Index(j)
			w.qualify(row, "", ref)
			id, _ := wlTextField(row, idField)
			key := wlKey{id: id}
			if table == "LegacySources" {
				key.kind, _ = wlTextField(row, "Kind")
			}
			t.primary[key] = append(t.primary[key], ref)
			w.qualifyUUIDs(table, row, ref)
			w.qualifyShape(row.Interface(), ref)
		}
		for _, refs := range t.primary {
			if !w.tick() {
				return
			}
			if len(refs) > 1 {
				w.out.Mismatches.DuplicateFacts++
			}
		}
	}
	// Each natural-key group is counted once; NULL is different from empty TEXT.
	for _, u := range []struct{ table, a, b string }{
		{"Accounts", "TenantID", "Kind"}, {"Journals", "OperationID", ""}, {"Lines", "JournalID", "AccountID"},
		{"Receipts", "SourceKind", "SourceID"}, {"Receipts", "JournalID", ""},
		{"Reservations", "RequestID", ""}, {"Settlements", "RequestID", ""}, {"Settlements", "JournalID", ""},
		{"LegacyReceipts", "JournalID", ""}, {"LegacySandboxReceipts", "CaptureID", ""}, {"LegacySandboxReceipts", "JournalID", ""},
		{"LegacySettlements", "ReservationID", ""}, {"LegacySettlements", "RequestID", ""}, {"LegacySettlements", "JournalID", ""},
		{"LegacyApprovedRefunds", "JournalID", ""}, {"LegacyOpenings", "ReservationID", ""},
		{"LegacySources", "JournalID", ""}, {"LegacySources", "JournalOperationID", ""},
		{"Operations", "TenantID", "Seq"}, {"Operations", "PreviousID", ""}, {"Operations", "JournalID", ""},
		{"Lots", "FundingReceiptID", ""}, {"Lots", "LegacyOpeningID", ""}, {"Lots", "LegacyRefundOrderID", ""},
		{"Events", "PreviousEventID", ""}, {"Events", "LotID", "AfterRevision"}, {"Events", "WalletOperationID", "LotID"},
		{"ReservationLots", "ReservationID", "LotID"}, {"ReservationLots", "ReservationID", "AllocationIndex"},
		{"SettlementLots", "ReservationLotID", ""},
		{"RefundLots", "RefundOrderID", "LotID"}, {"RefundLots", "RefundOrderID", "AllocationIndex"},
	} {
		w.unique(u.table, u.a, u.b)
	}
	// The partial UNIQUE available opening and normalized new refund namespace.
	seenOpen := map[string]int{}
	seenSource := map[wlKey]int{}
	for _, r := range w.in.LegacyOpenings {
		if !w.tick() {
			return
		}
		if r.Kind == "available" {
			seenOpen[r.TenantID]++
		}
	}
	for _, n := range seenOpen {
		if !w.tick() {
			return
		}
		if n > 1 {
			w.out.Mismatches.DuplicateFacts++
		}
	}
	for _, r := range w.in.Operations {
		if !w.tick() {
			return
		}
		seenSource[wlKey{wsSourceKind(r.Kind), r.SourceID}]++
	}
	for _, n := range seenSource {
		if !w.tick() {
			return
		}
		if n > 1 {
			w.out.Mismatches.DuplicateFacts++
		}
	}
}
func (w *wlWork) qualifyUUIDs(table string, row reflect.Value, ref wlRef) {
	var fields []string
	switch table {
	case "Epochs":
		fields = []string{"ID"}
	case "Attestations", "LegacyReceipts", "LegacySandboxReceipts", "LegacySettlements", "LegacyApprovedRefunds", "LegacySources":
		fields = []string{"EpochID"}
	case "LegacyOpenings":
		fields = []string{"ID", "EpochID"}
	case "Operations":
		fields = []string{"ID", "EpochID", "PreviousID"}
	case "Heads":
		fields = []string{"EpochID", "TailOperationID"}
	case "Lots":
		fields = []string{"ID", "LegacyOpeningID"}
	case "Events":
		fields = []string{"ID", "WalletOperationID", "LotID", "PreviousEventID"}
	case "Balances":
		fields = []string{"LotID", "LastEventID"}
	case "ReservationLots":
		fields = []string{"ID", "LotID"}
	case "SettlementLots":
		fields = []string{"ID", "ReservationLotID"}
	case "RefundLots":
		fields = []string{"ID", "LotID", "SettlementLotID"}
	}
	for _, field := range fields {
		if value, ok := wlTextField(row, field); ok && !wsUUID(value) {
			w.mark(&w.out.Mismatches.InvalidValueRows, ref)
		}
	}
}

type wlUniqueKey struct {
	a, b string
	null bool
}

func (w *wlWork) unique(table, a, b string) {
	t := w.tables[table]
	seen := map[wlUniqueKey]int{}
	for i := 0; i < t.rows.Len(); i++ {
		if !w.tick() {
			return
		}
		row := t.rows.Index(i)
		av, aok := wlTextField(row, a)
		// Accounts has a non-nullable natural key with NULL platform ownership.
		if !aok && table != "Accounts" {
			continue
		}
		bv := ""
		if b != "" {
			var ok bool
			bv, ok = wlTextField(row, b)
			if !ok {
				continue
			}
		}
		seen[wlUniqueKey{av, bv, !aok}]++
	}
	for _, n := range seen {
		if !w.tick() {
			return
		}
		if n > 1 {
			w.out.Mismatches.DuplicateFacts++
		}
	}
}
