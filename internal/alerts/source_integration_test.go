package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/google/uuid"
)

func TestPostgresAlertSourceSQLPlansAndNoBusinessRootLocks(t *testing.T) {
	f := integration(t)
	otherTenant, otherAccount := uuid.NewString(), uuid.NewString()
	f.exec(`INSERT INTO tenants(id,name) VALUES($1,'other scope')`, otherTenant)
	f.exec(`INSERT INTO procurement_accounts(id,label,provider,state,limit_amount) VALUES($1,'other scope','fixture','paused',0)`, otherAccount)
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `SET LOCAL session_replication_role='replica'`); err != nil {
		t.Fatal(err)
	}
	// These rows deliberately isolate planner selectivity. Bypassed financial
	// fixture constraints provide no evidence for a financial state transition.
	for _, item := range []struct{ sql, id string }{
		{`INSERT INTO reservations(tenant_id,request_id,amount,status,deadline,created_at) SELECT $1,gen_random_uuid()::text,0,'pending_reconciliation',statement_timestamp()+interval '1 day',statement_timestamp()-interval '2 days' FROM generate_series(1,100000)`, otherTenant},
		{`INSERT INTO reservations(tenant_id,request_id,amount,status,deadline,created_at) SELECT $1,gen_random_uuid()::text,0,'pending_reconciliation',statement_timestamp()+interval '1 day',statement_timestamp()-CASE WHEN n<=3 THEN interval '8 days' ELSE interval '1 day' END FROM generate_series(1,1002)n`, f.tenant},
		{`INSERT INTO reservations(tenant_id,request_id,amount,status,deadline,created_at) SELECT $1,gen_random_uuid()::text,0,'released',statement_timestamp()+interval '1 day',statement_timestamp()-interval '2 days' FROM generate_series(1,20000)`, f.tenant},
		{`INSERT INTO reservations(tenant_id,request_id,amount,status,deadline,created_at) SELECT $1,gen_random_uuid()::text,0,'pending_reconciliation',statement_timestamp()+interval '1 day',statement_timestamp() FROM generate_series(1,20000)`, f.tenant},
		{`INSERT INTO procurement_reservations(attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,state,created_at) SELECT gen_random_uuid()::text,$1,gen_random_uuid()::text,1,0,0,0,'unknown',statement_timestamp()-interval '2 days' FROM generate_series(1,100000)`, otherAccount},
		{`INSERT INTO procurement_reservations(attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,state,created_at) SELECT gen_random_uuid()::text,$1,gen_random_uuid()::text,1,0,0,0,CASE WHEN n%2=0 THEN 'unknown' ELSE 'disputed' END,statement_timestamp()-CASE WHEN n<=3 THEN interval '8 days' ELSE interval '1 day' END FROM generate_series(1,1002)n`, f.account},
		{`INSERT INTO procurement_reservations(attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,state,created_at) SELECT gen_random_uuid()::text,$1,gen_random_uuid()::text,1,0,0,0,'released',statement_timestamp()-interval '2 days' FROM generate_series(1,20000)`, f.account},
		{`INSERT INTO procurement_reservations(attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,state,created_at) SELECT gen_random_uuid()::text,$1,gen_random_uuid()::text,1,0,0,0,'unknown',statement_timestamp() FROM generate_series(1,20000)`, f.account},
	} {
		if _, err = tx.Exec(f.ctx, item.sql, item.id); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"reservations", "procurement_reservations", "tenants", "channels", "procurement_accounts"} {
		f.exec(`ANALYZE ` + table)
	}
	for _, item := range []struct{ name, sql, table, index, id, other string }{
		{"customer", customerSourceSQL, "reservations", "alert_customer_aging_source", f.tenant, otherTenant},
		{"procurement", procurementSourceSQL, "procurement_reservations", "alert_procurement_aging_source", f.account, otherAccount},
	} {
		for _, point := range []struct {
			name, id string
			age      int64
			count    string
			rows     float64
		}{{"normal", item.id, 600, "1001", 1001}, {"sparse", item.id, 604800, "3", 3}, {"zero", item.other, 604800, "0", 0}} {
			t.Run(item.name+"/"+point.name, func(t *testing.T) {
				var raw []byte
				if err := f.app.QueryRow(f.ctx, `EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) `+item.sql, point.id, point.age).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var plan []map[string]any
				if err := json.Unmarshal(raw, &plan); err != nil {
					t.Fatal(err)
				}
				scans := 0
				walkAlertPlan(plan[0]["Plan"].(map[string]any), func(n map[string]any) {
					if n["Relation Name"] != item.table {
						return
					}
					scans++
					if n["Index Name"] != item.index || n["Actual Rows"] != point.rows || !strings.Contains(fmt.Sprint(n["Index Cond"]), "created_at") || n["Actual Loops"] != float64(1) {
						t.Errorf("source not index-bounded: %s", raw)
					}
					t.Log("source_rows", n["Actual Rows"], "shared_blocks", n["Shared Hit Blocks"], "execution_ms", plan[0]["Execution Time"])
				})
				if scans != 1 {
					t.Fatal("expected exactly one source scan", scans, string(raw))
				}
				var exists, lower bool
				var at time.Time
				var count string
				var oldest *time.Time
				var id *string
				if err := f.app.QueryRow(f.ctx, item.sql, point.id, point.age).Scan(&exists, &at, &count, &lower, &oldest, &id); err != nil {
					t.Fatal(err)
				}
				if !exists || count != point.count || lower != (count == "1001") || (oldest == nil) != (count == "0") || (id == nil) != (count == "0") {
					t.Fatal(exists, count, lower, oldest, id)
				}
			})
		}
		var exists, lower bool
		var at time.Time
		var count string
		var oldest *time.Time
		var id *string
		if err := f.app.QueryRow(f.ctx, item.sql, uuid.NewString(), int64(600)).Scan(&exists, &at, &count, &lower, &oldest, &id); err != nil || exists || count != "0" || lower || oldest != nil || id != nil {
			t.Fatal("missing source conflated with valid zero", exists, count, err)
		}
	}
	rootTx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rootTx.Rollback(f.ctx)
	for _, item := range []struct{ table, id string }{{"channels", f.channel}, {"tenants", f.tenant}, {"procurement_accounts", f.account}} {
		if _, err = rootTx.Exec(f.ctx, `SELECT id FROM `+item.table+` WHERE id=$1 FOR NO KEY UPDATE`, item.id); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		sql, id string
		age     bool
	}{{channelSourceSQL, f.channel, false}, {budgetSourceSQL, f.account, false}, {customerSourceSQL, f.tenant, true}, {procurementSourceSQL, f.account, true}} {
		ctx, cancel := context.WithTimeout(f.ctx, time.Second)
		args := []any{item.id}
		if item.age {
			args = append(args, int64(600))
		}
		rows, err := f.app.Query(ctx, item.sql, args...)
		if err == nil {
			for rows.Next() {
				_, err = rows.Values()
				if err != nil {
					break
				}
			}
			rows.Close()
			if err == nil {
				err = rows.Err()
			}
		}
		cancel()
		if err != nil {
			t.Fatal("source took a business-root lock", err)
		}
	}
}

func walkAlertPlan(n map[string]any, fn func(map[string]any)) {
	fn(n)
	if ps, ok := n["Plans"].([]any); ok {
		for _, p := range ps {
			walkAlertPlan(p.(map[string]any), fn)
		}
	}
}

func TestPostgresAlertLabelAcceptanceMatchesGo(t *testing.T) {
	f := integration(t)
	for cp := rune(1); cp <= 0x10ffff; cp++ {
		if !unicode.IsControl(cp) && !unicode.IsSpace(cp) {
			continue
		}
		for _, v := range []string{string(cp) + "a", "a" + string(cp), "a" + string(cp) + "b"} {
			var got bool
			if err := f.app.QueryRow(f.ctx, `SELECT alert_label($1)`, v).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != validLabel(v) {
				t.Fatalf("label disagreement U+%04X %q sql=%v go=%v", cp, v, got, validLabel(v))
			}
		}
	}
}
