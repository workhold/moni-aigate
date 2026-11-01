package paymentsandboxschema

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFrozenSQLAndStaticInventory(t *testing.T) {
	for _, c := range []Component{Mock, Receiver} {
		t.Run(string(c), func(t *testing.T) {
			source, e := os.ReadFile(filepath.Join("..", "..", "specs", "022-self-service-payments", "b-"+string(c)+"-schema-prototype.sql"))
			if e != nil {
				t.Fatal(e)
			}
			embedded, e := SQL(c)
			if e != nil || embedded != string(source) {
				t.Fatal("embedded SQL differs from frozen source")
			}
			inventory, e := Inventory(c)
			if e != nil || len(inventory.Tables) == 0 || len(inventory.Functions) == 0 || len(inventory.Constraints) == 0 {
				t.Fatal("missing pinned inventory")
			}
			inventory.Tables[0].Columns[0].Name = "tampered"
			independent, e := Inventory(c)
			if e != nil || independent.Tables[0].Columns[0].Name == "tampered" {
				t.Fatal("inventory aliases caller memory")
			}
			version, e := Version(c)
			if e != nil || version != 1 {
				t.Fatal("wrong schema version")
			}
		})
	}
	if _, e := SQL("app"); e == nil {
		t.Fatal("application schema must not be exposed")
	}
	if _, e := Inventory(""); e == nil {
		t.Fatal("zero component accepted")
	}
}
