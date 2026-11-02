package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These exact projections are the public read contract, independently pinned
// here so a missing column/table or accidental secret read fails regression.
var financialTestTables = []string{"users", "tenants", "ledger_accounts", "journals", "journal_lines", "credit_orders", "funding_receipts", "voucher_batches", "vouchers", "voucher_revocations", "voucher_revocation_decisions", "voucher_redemptions", "commerce_commands", "commerce_operation_receipts", "audit_events", "projection_outbox", "api_keys", "request_records", "reservations", "settlements", "refund_orders"}
var financialTestProjections = map[string]string{
	"users": "id", "tenants": "id",
	"ledger_accounts":              "id,tenant_id,kind,balance::pg_catalog.text",
	"journals":                     "id,tenant_id,operation_id,kind,reference,actor,currency,created_txid::pg_catalog.text,created_at",
	"journal_lines":                "id,journal_id,account_id,signed_delta::pg_catalog.text",
	"credit_orders":                "id,tenant_id,business_ref,amount::pg_catalog.text,state,requester,approver,proof_ref,journal_id,created_at",
	"funding_receipts":             "id,tenant_id,source_kind,source_id,amount::pg_catalog.text,currency,journal_id,created_at",
	"voucher_batches":              "id,state,face_amount::pg_catalog.text,count::pg_catalog.text,expires_at,purpose_ref,reason,maker,approver,activated_at,terminal_actor,terminal_at,revision::pg_catalog.text,redeemed_count::pg_catalog.text,created_at",
	"vouchers":                     "id,batch_id,created_at",
	"voucher_revocations":          "id,batch_id,operation_id,maker,expected_revision::pg_catalog.text,expected_redeemed_count::pg_catalog.text,reason,created_at",
	"voucher_revocation_decisions": "id,proposal_id,batch_id,operation_id,approver,status,reason,decided_at",
	"voucher_redemptions":          "id,tenant_id,voucher_id,batch_id,operation_id,actor,amount::pg_catalog.text,journal_id,funding_receipt_id,created_at",
	"commerce_commands":            "operation_id,actor,scope,kind,request_digest,fingerprint_key_version,created_at",
	"commerce_operation_receipts":  "operation_id,kind,scope,actor,status,reason,resource_id,amount::pg_catalog.text,funding_receipt_id,journal_id,created_at",
	"audit_events":                 "id,actor,tenant_scope,action,target,sanitized_diff::pg_catalog.text,request_id,created_at",
	"projection_outbox":            "event_id,tenant_id,operation_id,kind,schema_version,sanitized_payload::pg_catalog.text,created_at",
	"api_keys":                     "id,tenant_id,owner_id,held::pg_catalog.text,spent::pg_catalog.text,lifetime_limit::pg_catalog.text",
	"request_records":              "id,tenant_id,key_id,owner_id,billing_status,input_rate::pg_catalog.text,output_rate::pg_catalog.text,input_upper_bound::pg_catalog.text,output_limit::pg_catalog.text",
	"reservations":                 "id,tenant_id,request_id,amount::pg_catalog.text,status",
	"settlements":                  "id,tenant_id,request_id,charged_amount::pg_catalog.text,raw_retail_amount::pg_catalog.text,waived_retail_amount::pg_catalog.text,refunded_amount::pg_catalog.text,status,reason,journal_id",
	"refund_orders":                "id,tenant_id,settlement_id,business_ref,amount::pg_catalog.text,state,requester,approver,journal_id",
}

func financialTestLedger(table string) bool {
	return table == "ledger_accounts" || table == "journals" || table == "journal_lines"
}
func financialTestKey(table string) string {
	if table == "commerce_commands" || table == "commerce_operation_receipts" {
		return "operation_id"
	}
	if table == "projection_outbox" {
		return "event_id"
	}
	return "id"
}

type financialTestRows struct {
	*fundingSourceReaderTestRows
	sawErr bool
}

func (r *financialTestRows) Err() error { r.sawErr = true; return r.fundingSourceReaderTestRows.Err() }

type financialTestQuery struct {
	tables              map[string][][]any
	calls               map[string]int
	opened              []*financialTestRows
	failTable, failMode string
	onClose             func(string)
	onQuery             func(string)
}

func financialTestQueryNew() *financialTestQuery {
	return &financialTestQuery{tables: map[string][][]any{}, calls: map[string]int{}}
}
func (q *financialTestQuery) Query(sql string, args ...any) (fundingSourceRows, error) {
	var table string
	for _, name := range financialTestTables {
		if strings.Contains(sql, ` FROM "private_schema"."`+name+`" `) {
			table = name
			break
		}
	}
	if table == "" || len(args) != 2 {
		return nil, errors.New("private fixture SQL")
	}
	key := financialTestKey(table)
	projection := "SELECT " + financialTestProjections[table] + ` FROM "private_schema"."` + table + `" `
	if !strings.HasPrefix(sql, projection) || !strings.Contains(sql, "ORDER BY "+key+` COLLATE pg_catalog."C" USING OPERATOR(pg_catalog.<) LIMIT 128`) {
		return nil, errors.New("private projection/order changed")
	}
	if len(q.opened) > 0 {
		prior := q.opened[len(q.opened)-1]
		if !prior.closed || !prior.sawErr {
			return nil, errors.New("prior EOF lifecycle incomplete")
		}
	}
	mode, ok := args[0].(bool)
	if !ok {
		return nil, errors.New("private mode")
	}
	cursor, ok := args[1].(string)
	if !ok {
		return nil, errors.New("private cursor")
	}
	hasCursor := mode
	if financialTestLedger(table) {
		hasCursor = !mode
	}
	if q.calls[table] == 0 && (hasCursor || cursor != "") {
		return nil, errors.New("first cursor changed")
	}
	q.calls[table]++
	if q.onQuery != nil {
		q.onQuery(table)
	}
	if q.failTable == table && q.failMode == "query" {
		return nil, errors.New("private SQL password")
	}
	if q.failTable == table && q.failMode == "nil" {
		return nil, nil
	}
	var page [][]any
	for _, row := range q.tables[table] {
		if hasCursor && row[0].(string) <= cursor {
			continue
		}
		page = append(page, row)
		if len(page) == 128 {
			break
		}
	}
	rows := &financialTestRows{fundingSourceReaderTestRows: &fundingSourceReaderTestRows{values: page, index: -1}}
	if q.onClose != nil {
		rows.onClose = func() { q.onClose(table) }
	}
	if q.failTable == table {
		switch q.failMode {
		case "next":
			rows.nextErr = errors.New("private Next password")
		case "scan":
			rows.scanErr = errors.New("private Scan password")
			rows.values = [][]any{financialTestRow(table, "bad")}
		case "err":
			rows.rowsErr = errors.New("private Err password")
		case "close":
			rows.closeErr = errors.New("private Close password")
		case "duplicate":
			rows.values = [][]any{financialTestRow(table, "same"), financialTestRow(table, "same")}
		case "reverse":
			rows.values = [][]any{financialTestRow(table, "z"), financialTestRow(table, "a")}
		case "oversized":
			for i := 0; i < 129; i++ {
				rows.values = append(rows.values, financialTestRow(table, fmt.Sprintf("k%03d", i)))
			}
		}
	}
	q.opened = append(q.opened, rows)
	return rows, nil
}
func financialTestRow(table, id string) []any {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	switch table {
	case "users", "tenants":
		return []any{id}
	case "ledger_accounts":
		return []any{id, nil, "FUNDING_CLEARING", "0"}
	case "journals":
		return []any{id, "tenant", "op", "other", "reference", "actor", "CNY", "1", now}
	case "journal_lines":
		return []any{id, "journal", "account", "0"}
	case "credit_orders":
		return []any{id, "tenant", "ref", "1", "pending", "maker", nil, "proof", nil, now}
	case "funding_receipts":
		return []any{id, "tenant", "sandbox_capture", "source", "1", "CNY", "journal", now}
	case "voucher_batches":
		return []any{id, "proposed", "1", "1", now.Add(2 * time.Hour), "purpose", "reason", "maker", nil, nil, nil, nil, "1", "0", now}
	case "vouchers":
		return []any{id, "batch", now}
	case "voucher_revocations":
		return []any{id, "batch", "op", "maker", "2", "0", "reason", now}
	case "voucher_revocation_decisions":
		return []any{id, "proposal", "batch", "op", "approver", "rejected", "voucher_evidence_stale", now}
	case "voucher_redemptions":
		return []any{id, "tenant", "voucher", "batch", "op", "actor", "1", "journal", "receipt", now}
	case "commerce_commands":
		return []any{id, "actor", "platform", "payment.sku.publish", strings.Repeat("a", 64), nil, now}
	case "commerce_operation_receipts":
		return []any{id, "payment.sku.publish", "platform", "actor", "accepted", "accepted", "resource", nil, nil, nil, now}
	case "audit_events":
		return []any{id, "actor", nil, "unrelated", "target", "{}", "", now}
	case "projection_outbox":
		return []any{id, "tenant", "op", "unrelated", int32(1), "{}", now}
	case "api_keys":
		return []any{id, "tenant", "owner", "0", "0", nil}
	case "request_records":
		return []any{id, "tenant", "key", "owner", "reserved", "0", "0", "0", "0"}
	case "reservations":
		return []any{id, "tenant", "request", "0", "reserved"}
	case "settlements":
		return []any{id, "tenant", "request", "0", "0", "0", "0", "settled", "completed", "journal"}
	case "refund_orders":
		return []any{id, "tenant", "settlement", "ref", "1", "pending", "requester", nil, nil}
	}
	panic("unknown fixture table")
}
func financialTestZero(t *testing.T, f FundingFinancialFacts) {
	t.Helper()
	if !reflect.DeepEqual(f, FundingFinancialFacts{}) {
		t.Fatal("failure retained partial graph")
	}
}
func TestFundingFinancialReadAllTablesAndBoundaries(t *testing.T) {
	for _, table := range financialTestTables {
		for _, count := range []int{0, 1, 127, 128, 129} {
			t.Run(fmt.Sprintf("%s/%d", table, count), func(t *testing.T) {
				q := financialTestQueryNew()
				for i := 0; i < count; i++ {
					q.tables[table] = append(q.tables[table], financialTestRow(table, fmt.Sprintf("k%04d", i)))
				}
				facts, err := ReadFundingFinancialFacts(context.Background(), q, "private_schema")
				if err != nil || facts.snapshot == nil || len(q.calls) != 21 {
					t.Fatal("fixed complete read failed", err)
				}
				if q.calls[table] != count/128+1 {
					t.Fatal("full page failed to continue to real EOF")
				}
				for name, n := range q.calls {
					if name != table && n != 1 {
						t.Fatal("shared physical relation was reread")
					}
				}
				for _, r := range q.opened {
					if !r.closed || !r.sawErr {
						t.Fatal("EOF did not observe Err and Close")
					}
				}
				if facts.LedgerSummary().Scope != "supplied_ledger_rows" || facts.SourceLinksSummary().Scope != "manual_voucher_receipt_links" || facts.SourceGraphSummary().Scope != "manual_voucher_funding_graph_v1" || facts.CustomerKeySummary().Scope != "customer_key_financial_graph_v1" {
					t.Fatal("core was omitted")
				}
			})
		}
	}
}
func TestFundingFinancialReadLifecycleFailureMatrix(t *testing.T) {
	for _, table := range financialTestTables {
		for _, mode := range []string{"query", "nil", "next", "scan", "err", "close", "duplicate", "reverse", "oversized"} {
			t.Run(table+"/"+mode, func(t *testing.T) {
				q := financialTestQueryNew()
				q.failTable, q.failMode = table, mode
				f, err := ReadFundingFinancialFacts(context.Background(), q, "private_schema")
				if err != ErrFundingFinancialFactsExecution || strings.Contains(err.Error(), "private") {
					t.Fatal("error was not safely classified", err)
				}
				financialTestZero(t, f)
				if len(q.opened) > 0 {
					last := q.opened[len(q.opened)-1]
					if !last.closed {
						t.Fatal("error failed to close rows")
					}
					if (mode == "err" || mode == "close") && !last.sawErr {
						t.Fatal("terminal error skipped Err")
					}
				}
			})
		}
	}
}
func TestFundingFinancialReadPreservesEmptyTextAndOwnedNullableGraph(t *testing.T) {
	q := financialTestQueryNew()
	q.tables["users"] = [][]any{{""}, {"u"}}
	q.tables["tenants"] = [][]any{{""}}
	q.tables["api_keys"] = [][]any{{"", "", "", "0", "0", nil}, {"k", "", "", "0", "0", "0"}}
	q.tables["audit_events"] = [][]any{financialTestRow("audit_events", "a"), financialTestRow("audit_events", "b")}
	q.tables["audit_events"][1][2] = ""
	f, err := ReadFundingFinancialFacts(context.Background(), q, "private_schema")
	if err != nil {
		t.Fatal(err)
	}
	if !f.CustomerKeySummary().Balanced || f.snapshot.sources.Audits[0].TenantScope != nil || f.snapshot.sources.Audits[1].TenantScope == nil || *f.snapshot.sources.Audits[1].TenantScope != "" || f.snapshot.customer.Keys[0].LifetimeLimit != nil || *f.snapshot.customer.Keys[1].LifetimeLimit != "0" {
		t.Fatal("empty identity or nullable values changed")
	}
	if &f.snapshot.sources.Users[0] != &f.snapshot.customer.Users[0] {
		t.Fatal("shared users copied into separately assembled graph")
	}
	raw, err := json.Marshal(f)
	if err != nil || !strings.Contains(string(raw), "redacted") || strings.Contains(fmt.Sprintf("%v %#v", f, f), "private_schema") {
		t.Fatal("facts exposed raw input")
	}
}
func TestFundingFinancialReadCanceledTimeoutAtLastEOF(t *testing.T) {
	for _, table := range []string{"users", "ledger_accounts", "journals", "journal_lines", "refund_orders"} {
		t.Run(table, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			q := financialTestQueryNew()
			q.onClose = func(name string) {
				if name == table {
					cancel()
				}
			}
			f, err := ReadFundingFinancialFacts(ctx, q, "private_schema")
			if err != ErrFundingFinancialFactsCanceled {
				t.Fatal("EOF cancellation lost", err)
			}
			financialTestZero(t, f)
		})
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	f, err := ReadFundingFinancialFacts(ctx, financialTestQueryNew(), "private_schema")
	if err != ErrFundingFinancialFactsTimeout {
		t.Fatal("timeout misclassified", err)
	}
	financialTestZero(t, f)
}
func TestFundingFinancialReadSharedLastTableBudgets(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprintf("rows/%d", extra), func(t *testing.T) {
			q := financialTestQueryNew()
			for i := 0; i < 99999; i++ {
				q.tables["users"] = append(q.tables["users"], []any{fmt.Sprintf("u%06d", i)})
			}
			for i := 0; i < 1+extra; i++ {
				q.tables["refund_orders"] = append(q.tables["refund_orders"], financialTestRow("refund_orders", fmt.Sprintf("r%d", i)))
			}
			f, err := ReadFundingFinancialFacts(context.Background(), q, "private_schema")
			if extra == 0 {
				if err != nil || f.CustomerKeySummary().Scanned.Users != 99999 {
					t.Fatal("exact shared row bound rejected", err)
				}
			} else {
				if err != ErrFundingFinancialFactsResource {
					t.Fatal("last relation reset row budget", err)
				}
				financialTestZero(t, f)
			}
		})
	}
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprintf("bytes/%d", extra), func(t *testing.T) {
			q := financialTestQueryNew()
			last := financialTestRow("refund_orders", "r")
			used := 0
			for _, v := range last {
				if text, ok := v.(string); ok {
					used += len(text)
				}
			}
			q.tables["users"] = [][]any{{strings.Repeat("u", (16<<20)-used+extra)}}
			q.tables["refund_orders"] = [][]any{last}
			f, err := ReadFundingFinancialFacts(context.Background(), q, "private_schema")
			if extra == 0 {
				if err != nil {
					t.Fatal("exact shared bytes rejected", err)
				}
			} else {
				if err != ErrFundingFinancialFactsResource {
					t.Fatal("last relation reset byte budget", err)
				}
				financialTestZero(t, f)
			}
		})
	}
}
func TestFundingFinancialReadInputAndLedgerQualification(t *testing.T) {
	var nilQuery *financialTestQuery
	for _, item := range []struct {
		ctx    context.Context
		q      FundingSourceFactQuery
		schema string
	}{{nil, financialTestQueryNew(), "private_schema"}, {context.Background(), nil, "private_schema"}, {context.Background(), nilQuery, "private_schema"}, {context.Background(), financialTestQueryNew(), "pg_catalog"}, {context.Background(), financialTestQueryNew(), "bad\x00schema"}} {
		f, err := ReadFundingFinancialFacts(item.ctx, item.q, item.schema)
		if err != ErrFundingFinancialFactsInput {
			t.Fatal("invalid entry accepted", err)
		}
		financialTestZero(t, f)
	}
	for _, bad := range []any{"01", time.Time{}} {
		q := financialTestQueryNew()
		row := financialTestRow("journals", "j")
		if _, ok := bad.(string); ok {
			row[7] = bad
		} else {
			row[8] = bad
		}
		q.tables["journals"] = [][]any{row}
		f, err := ReadFundingFinancialFacts(context.Background(), q, "private_schema")
		if err != ErrFundingFinancialFactsExecution {
			t.Fatal("legacy ledger qualification changed", err)
		}
		financialTestZero(t, f)
	}
}

func financialTestCreditFixture() *financialTestQuery {
	q := financialTestQueryNew()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q.tables["users"] = [][]any{{"approver"}, {"maker"}}
	q.tables["tenants"] = [][]any{{"tenant"}}
	q.tables["ledger_accounts"] = [][]any{{"a", "tenant", "AVAILABLE", "10"}, {"b", nil, "FUNDING_CLEARING", "-10"}}
	q.tables["journals"] = [][]any{{"j", "tenant", "c", "credit", "c", "approver", "CNY", "1", now}}
	q.tables["journal_lines"] = [][]any{{"l1", "j", "a", "10"}, {"l2", "j", "b", "-10"}}
	q.tables["credit_orders"] = [][]any{{"c", "tenant", "ref", "10", "approved", "maker", "approver", "proof", "j", now}}
	q.tables["funding_receipts"] = [][]any{{"11111111-1111-4111-8111-111111111111", "tenant", "manual_credit", "c", "10", "CNY", "j", now}}
	q.tables["audit_events"] = [][]any{{"ae1", "maker", "tenant", "credit.created", "c", `{"amount":"10"}`, "", now}, {"ae2", "approver", "tenant", "credit.approved", "c", `{"amount":"10"}`, "", now}}
	q.tables["projection_outbox"] = [][]any{{"e", "tenant", "c", "credited", int32(1), `{"amount":"10"}`, now}}
	q.tables["api_keys"] = [][]any{{"k", "tenant", "maker", "0", "0", "100"}}
	return q
}
func TestFundingFinancialReadSharesSourceEffectsWithAllCores(t *testing.T) {
	q := financialTestCreditFixture()
	f, err := ReadFundingFinancialFacts(context.Background(), q, "private_schema")
	if err != nil || !f.LedgerSummary().RowsConsistent || !f.SourceLinksSummary().ScopedLinksConsistent || !f.SourceGraphSummary().ScopedGraphConsistent || !f.CustomerKeySummary().Balanced {
		t.Fatal("same-row manual funding graph was not retained", err, f.LedgerSummary(), f.SourceGraphSummary(), f.CustomerKeySummary())
	}
	g := f.snapshot
	if &g.sources.Accounts[0] != &g.customer.Accounts[0] || &g.sources.Accounts[0] != &g.ledger.rows.Accounts[0] || &g.sources.Lines[0] != &g.customer.Lines[0] || &g.sources.Journals[0] != &g.customer.Journals[0] {
		t.Fatal("shared physical rows were replaced with separate trusted input")
	}
	if g.sources.Journals[0].CreatedTXID != "1" || g.sources.Journals[0].Actor != "approver" || g.sources.Credits[0].ProofRef != "proof" || g.sources.Outbox[0].SchemaVersion != 1 || g.sources.Audits[1].Action != "credit.approved" {
		t.Fatal("fixed typed column mapping changed")
	}
	// An independent mismatch cannot stop the remaining required reader/core.
	q = financialTestCreditFixture()
	q.tables["journal_lines"][1][3] = "-9"
	f, err = ReadFundingFinancialFacts(context.Background(), q, "private_schema")
	if err != nil || f.LedgerSummary().RowsConsistent || f.SourceGraphSummary().ScopedGraphConsistent || f.CustomerKeySummary().Scope != "customer_key_financial_graph_v1" || q.calls["refund_orders"] != 1 {
		t.Fatal("mismatch skipped another domain", err)
	}
}

func TestFundingFinancialReadCoreFailureAfterAllEOFReturnsZero(t *testing.T) {
	for _, table := range []string{"ledger_accounts", "credit_orders"} {
		t.Run(table, func(t *testing.T) {
			q := financialTestQueryNew()
			row := financialTestRow(table, "row")
			row[3] = "01"
			q.tables[table] = [][]any{row}
			facts, err := ReadFundingFinancialFacts(context.Background(), q, "private_schema")
			if err != ErrFundingFinancialFactsExecution || len(q.calls) != 21 {
				t.Fatal("core execution failure did not occur after complete EOF", err, len(q.calls))
			}
			financialTestZero(t, facts)
		})
	}
}
