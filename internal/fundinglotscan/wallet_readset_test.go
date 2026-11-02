package fundinglotscan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type walletReadsetTable struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

type walletReadsetContract struct {
	FormatVersion int                  `json:"format_version"`
	Tables        []walletReadsetTable `json:"tables"`
	History       walletReadsetTable   `json:"history"`
	Counts        struct {
		Tables             int `json:"tables"`
		Columns            int `json:"columns"`
		ColumnsWithHistory int `json:"columns_with_history"`
	} `json:"counts"`
	SourceSHA256 map[string]string `json:"source_sha256"`
}

func loadWalletReadsetContract(t *testing.T) walletReadsetContract {
	t.Helper()
	raw, err := os.ReadFile("../../specs/022-self-service-payments/c0-wallet-readset.json")
	if err != nil {
		t.Fatal(err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var contract walletReadsetContract
	if err := d.Decode(&contract); err != nil {
		t.Fatal(err)
	}
	if d.Decode(new(any)) != io.EOF {
		t.Fatal("readset contract has trailing JSON")
	}
	if contract.FormatVersion != 1 || contract.Counts.Tables != 84 || contract.Counts.Columns != 776 || contract.Counts.ColumnsWithHistory != 778 {
		t.Fatal("readset contract differs from frozen R1 84/776/778 boundary")
	}
	if contract.History.Name != "schema_migrations" || !slices.Equal(contract.History.Columns, []string{"name", "checksum"}) {
		t.Fatal("migration history must remain a separate exact two-column read")
	}
	return contract
}

// This is an ACL review boundary, not an alternate implementation of the
// scanner. A reviewed static list is compared in both directions with its real
// registry so adding a read cannot silently inherit an unchecked permission.
func TestWalletReadsetMatchesReviewedACLContract(t *testing.T) {
	contract := loadWalletReadsetContract(t)
	if len(contract.Tables) != 84 {
		t.Fatal("reviewed readset must contain exactly 84 tables")
	}
	reviewed := make(map[string]map[string]bool, len(contract.Tables))
	columns, previous := 0, ""
	for _, table := range contract.Tables {
		if table.Name == "" || table.Name <= previous || table.Name == contract.History.Name || len(table.Columns) == 0 {
			t.Fatal("reviewed tables must be nonempty, unique, sorted, and exclude migration history")
		}
		previous = table.Name
		set, prevColumn := map[string]bool{}, ""
		for _, column := range table.Columns {
			if column == "" || column <= prevColumn {
				t.Fatalf("reviewed columns must be nonempty, unique and sorted: %s", table.Name)
			}
			set[column], prevColumn = true, column
			columns++
		}
		reviewed[table.Name] = set
	}
	if columns != 776 {
		t.Fatalf("reviewed readset has %d columns, want 776", columns)
	}

	actual, _, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings(), walletBindings())
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]map[string]bool, len(actual))
	actualColumns := 0
	for _, table := range actual {
		name := table.spec.name
		seen[name] = map[string]bool{}
		if _, ok := reviewed[name]; !ok {
			t.Errorf("scanner reads unreviewed table %s; review the R1 ACL contract", name)
		}
		for _, column := range table.spec.columns {
			actualColumns++
			seen[name][column.name] = true
			if !reviewed[name][column.name] {
				t.Errorf("scanner reads unreviewed column %s.%s; review the R1 ACL contract", name, column.name)
			}
		}
	}
	for _, table := range contract.Tables {
		if _, ok := seen[table.Name]; !ok {
			t.Errorf("reviewed table %s is no longer scanned; review the retained ACL", table.Name)
		}
		for _, column := range table.Columns {
			if !seen[table.Name][column] {
				t.Errorf("reviewed column %s.%s is no longer scanned; review the retained ACL", table.Name, column)
			}
		}
	}
	if len(actual) != 84 || actualColumns != 776 {
		t.Errorf("scanner reads %d tables/%d columns, want the reviewed 84/776", len(actual), actualColumns)
	}
}

func TestWalletReadsetSourcePinsAndHistory(t *testing.T) {
	contract := loadWalletReadsetContract(t)
	// Freeze the pin set as well as each digest: dropping a source pin must not
	// turn a changed SQL builder, registry, DTO or history reader into an omission.
	sources := []string{
		"internal/fundinglotscan/binding.go",
		"internal/fundinglotscan/checks.go",
		"internal/fundinglotscan/epoch_proof_projection.go",
		"internal/fundinglotscan/projection_funding.go",
		"internal/fundinglotscan/projection_periodic.go",
		"internal/fundinglotscan/projection_procurement.go",
		"internal/fundinglotscan/projection_sandbox.go",
		"internal/fundinglotscan/projection_wallet.go",
		"internal/fundinglotscan/reader.go",
		"internal/fundinglotscan/seed_projection.go",
		"internal/fundinglotscan/types.go",
		"internal/fundinglotscan/wallet.go",
		"internal/fundinglotverify/customer_key_graph.go",
		"internal/fundinglotverify/funding_graph_rows.go",
		"internal/fundinglotverify/funding_source_graph.go",
		"internal/fundinglotverify/funding_sources.go",
		"internal/fundinglotverify/ledger.go",
		"internal/fundinglotverify/periodic_budget_graph.go",
		"internal/fundinglotverify/procurement_approval.go",
		"internal/fundinglotverify/procurement_attempt.go",
		"internal/fundinglotverify/procurement_budget.go",
		"internal/fundinglotverify/procurement_command.go",
		"internal/fundinglotverify/procurement_cost_journal.go",
		"internal/fundinglotverify/procurement_evidence_graph.go",
		"internal/fundinglotverify/sandbox_source_graph.go",
		"internal/fundinglotverify/wallet_lots.go",
		"internal/fundinglotverify/wallet_sequence.go",
		"migrations/embed.go",
		"migrations/verify_readonly.go",
	}
	if len(contract.SourceSHA256) != len(sources) {
		t.Fatal("readset source pin set changed without R1 review")
	}
	for _, source := range sources {
		want, ok := contract.SourceSHA256[source]
		decoded, err := hex.DecodeString(want)
		if !ok || err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != want {
			t.Fatalf("missing or noncanonical readset source SHA: %s", source)
		}
		raw, err := os.ReadFile(filepath.Join("../..", source))
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(raw)
		if hex.EncodeToString(got[:]) != want {
			t.Errorf("readset source changed: %s; review scanner SQL/ACL/history before updating its pin", source)
		}
	}
	// A newly added scanner production file must also enter the reviewed input
	// set. Tests are deliberately excluded; this test never hashes itself.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		if _, ok := contract.SourceSHA256["internal/fundinglotscan/"+file]; !ok {
			t.Errorf("unreviewed scanner production source %s", file)
		}
	}
}
