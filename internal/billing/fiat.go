package billing

import "github.com/moni-ai/moni-aigate/internal/economics"

// FiatMoney is an amount in a fiat currency's micro-unit. It remains a
// distinct type from Money (micro-COIN), and retains the decimal-string JSON
// ABI through economics.Money.
type FiatMoney = economics.Money
