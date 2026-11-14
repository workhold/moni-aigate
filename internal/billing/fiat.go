package billing

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// FiatMoney is an amount in a fiat currency's micro-unit. It is deliberately
// not assignable to Money, which is denominated in micro-COIN.
type FiatMoney int64

func (m FiatMoney) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatInt(int64(m), 10))
}
func (m *FiatMoney) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("money must be a decimal string")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid money")
	}
	*m = FiatMoney(n)
	return nil
}
