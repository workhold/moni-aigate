// Package paymentsandboxschema contains the frozen, independently owned local
// PSP schemas. It never connects to PostgreSQL or applies DDL at service startup.
package paymentsandboxschema

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
)

type Component string

const (
	Mock     Component = "mock"
	Receiver Component = "receiver"
)

var ErrComponent = errors.New("mock_configuration_invalid")

//go:embed mock_v1.sql receiver_v1.sql mock_inventory.json receiver_inventory.json
var files embed.FS

func filename(c Component, suffix string) (string, error) {
	if c != Mock && c != Receiver {
		return "", ErrComponent
	}
	return string(c) + suffix, nil
}
func Version(c Component) (int, error) {
	if _, e := filename(c, ""); e != nil {
		return 0, e
	}
	return 1, nil
}
func SQL(c Component) (string, error) {
	name, e := filename(c, "_v1.sql")
	if e != nil {
		return "", e
	}
	b, e := files.ReadFile(name)
	if e != nil {
		return "", ErrComponent
	}
	return string(b), nil
}
func Digest(c Component) (string, error) {
	s, e := SQL(c)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:]), nil
}

type Column struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	NotNull bool   `json:"not_null"`
	Default string `json:"default"`
}
type Table struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
}
type Function struct {
	Name         string `json:"name"`
	Arguments    string `json:"arguments"`
	Returns      string `json:"returns"`
	Language     string `json:"language"`
	Volatility   string `json:"volatility"`
	Strict       bool   `json:"strict"`
	SourceSHA256 string `json:"source_sha256"`
}
type Trigger struct {
	Table      string `json:"table"`
	Name       string `json:"name"`
	Function   string `json:"function"`
	Type       int16  `json:"type"`
	Deferrable bool   `json:"deferrable"`
	Deferred   bool   `json:"deferred"`
}
type Index struct {
	Name       string `json:"name"`
	Table      string `json:"table"`
	Definition string `json:"definition"`
}
type Constraint struct {
	Table      string `json:"table"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Definition string `json:"definition"`
	Deferrable bool   `json:"deferrable"`
	Deferred   bool   `json:"deferred"`
}

// Inventory is a deep copy of the reviewed static expectation. __SCHEMA__ is
// substituted only with an already validated exact schema identity.
type CatalogInventory struct {
	SQLSHA256   string       `json:"sql_sha256"`
	Tables      []Table      `json:"tables"`
	Functions   []Function   `json:"functions"`
	Triggers    []Trigger    `json:"triggers"`
	Indexes     []Index      `json:"indexes"`
	Constraints []Constraint `json:"constraints"`
}

func Inventory(c Component) (CatalogInventory, error) {
	name, e := filename(c, "_inventory.json")
	if e != nil {
		return CatalogInventory{}, e
	}
	b, e := files.ReadFile(name)
	if e != nil {
		return CatalogInventory{}, ErrComponent
	}
	var v CatalogInventory
	if json.Unmarshal(b, &v) != nil {
		return CatalogInventory{}, ErrComponent
	}
	digest, e := Digest(c)
	if e != nil || v.SQLSHA256 != digest {
		return CatalogInventory{}, ErrComponent
	}
	return v, nil
}

// Permissions returns the exact frozen runtime INSERT/column-UPDATE matrix.
// SELECT is required on every owned table. No other table privilege is allowed.
func Permissions(c Component, table string) (insert bool, updateColumns []string) {
	if c == Receiver {
		return table == "receiver_inbox" || table == "receiver_receipts" || table == "receiver_conflicts", nil
	}
	if c != Mock {
		return false, nil
	}
	switch table {
	case "mock_orders":
		return true, []string{"state", "revision", "closed_at"}
	case "mock_command_processing":
		return true, []string{"state", "completed_at", "released_by_control_id"}
	case "mock_notification_delivery":
		return true, []string{"state", "generation", "attempt_count", "next_attempt_at", "lease_until", "last_error_code", "delivered_at"}
	case "mock_fault_plans":
		return true, []string{"remaining", "consumed_at"}
	case "mock_commands", "mock_command_results", "mock_order_transitions", "mock_captures", "mock_queries", "mock_notification_events", "mock_delivery_attempts", "mock_delivery_observations", "mock_controls":
		return true, nil
	default:
		return false, nil
	}
}
