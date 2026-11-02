package fundinglotinit

import "reflect"

// Merge sorting checks cancellation at every comparison and copy. Keys are raw
// strings, so comparisons are the specified UTF-8 byte tuple ordering.
func (w *work) sortPlan(p *Plan) error {
	v := reflect.ValueOf(p).Elem()
	for i := 1; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		key := "ID"
		switch name {
		case "LegacyReceipts", "LegacySandboxReceipts":
			key = "ReceiptID"
		case "LegacySettlements":
			key = "SettlementID"
		case "LegacyApprovedRefunds":
			key = "RefundOrderID"
		case "LegacySources":
			key = "SourceID"
		case "Heads":
			key = "TenantID"
		case "Balances":
			key = "LotID"
		}
		rows := v.Field(i)
		if rows.Len() < 2 {
			if err := w.check(); err != nil {
				return err
			}
			continue
		}
		scratch := reflect.MakeSlice(rows.Type(), rows.Len(), rows.Len())
		less := func(a, b reflect.Value) bool {
			if name == "LegacySources" {
				x, y := a.FieldByName("Kind").String(), b.FieldByName("Kind").String()
				if x != y {
					return x < y
				}
			}
			return a.FieldByName(key).String() < b.FieldByName(key).String()
		}
		for width := 1; width < rows.Len(); width *= 2 {
			for start := 0; start < rows.Len(); start += 2 * width {
				if err := w.check(); err != nil {
					return err
				}
				mid, end := min(start+width, rows.Len()), min(start+2*width, rows.Len())
				left, right := start, mid
				for dst := start; dst < end; dst++ {
					if err := w.check(); err != nil {
						return err
					}
					if left < mid && (right >= end || !less(rows.Index(right), rows.Index(left))) {
						scratch.Index(dst).Set(rows.Index(left))
						left++
					} else {
						scratch.Index(dst).Set(rows.Index(right))
						right++
					}
				}
			}
			for j := 0; j < rows.Len(); j++ {
				if err := w.check(); err != nil {
					return err
				}
				rows.Index(j).Set(scratch.Index(j))
			}
		}
	}
	return w.check()
}
