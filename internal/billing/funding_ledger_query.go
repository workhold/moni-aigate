package billing

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// SQL uses the adapter-owned context. The CPU context cannot replace it.
// This private seam is shared only by the two fixed ledger entry points.
type fundingLedgerQuery interface {
	Query(string, ...any) (fundingLedgerRows, error)
}
type fundingLedgerRows interface {
	Next() (bool, error)
	Scan(...any) error
	Err() error
	Close() error
}

type fundingLedgerRawQuery struct {
	ctx context.Context
	tx  pgx.Tx
}
type fundingLedgerRawRows struct {
	ctx  context.Context
	rows pgx.Rows
}

func (q fundingLedgerRawQuery) Query(sql string, args ...any) (fundingLedgerRows, error) {
	rows, err := q.tx.Query(q.ctx, sql, args...)
	if err != nil {
		return nil, fundingLedgerFailure(q.ctx)
	}
	return &fundingLedgerRawRows{ctx: q.ctx, rows: rows}, nil
}
func (r *fundingLedgerRawRows) Next() (bool, error) { return r.rows.Next(), nil }
func (r *fundingLedgerRawRows) Scan(dest ...any) error {
	if r.rows.Scan(dest...) != nil {
		return fundingLedgerFailure(r.ctx)
	}
	return nil
}
func (r *fundingLedgerRawRows) Err() error {
	if r.rows.Err() != nil {
		return fundingLedgerFailure(r.ctx)
	}
	return nil
}
func (r *fundingLedgerRawRows) Close() error { r.rows.Close(); return nil }

func fundingLedgerReadPages(query fundingLedgerQuery, sql string, scan func(fundingLedgerRows) (string, error)) error {
	hasCursor, cursor := false, ""
	for {
		rows, err := query.Query(sql, !hasCursor, cursor)
		if err != nil {
			return err
		}
		count, last := 0, cursor
		for {
			more, err := rows.Next()
			if err != nil {
				_ = rows.Close()
				return err
			}
			if !more {
				break
			}
			key, err := scan(rows)
			if err != nil {
				_ = rows.Close()
				return err
			}
			if count >= 128 || (hasCursor || count > 0) && key <= last {
				_ = rows.Close()
				return fundingLedgerReadExecution
			}
			last = key
			count++
		}
		// Both observations are required by ReadLease, including error paths.
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if count < 128 {
			return nil
		}
		hasCursor, cursor = true, last
	}
}

func readFundingLedgerCore(ctx context.Context, query fundingLedgerQuery, schema string) (fundingLedgerSnapshot, error) {
	if ctx == nil {
		return fundingLedgerSnapshot{}, fundingLedgerReadInput
	}
	if ctx.Err() != nil {
		return fundingLedgerSnapshot{}, fundingLedgerReadCanceled
	}
	var err error
	var out fundingLedgerSnapshot
	var budget fundingLedgerBudget
	err = fundingLedgerReadPages(query, fundingLedgerPageSQL(schema, "ledger_accounts"), func(rows fundingLedgerRows) (string, error) {
		var row fundinglotverify.LedgerAccountRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.Kind, &row.Balance); err != nil {
			return "", err
		}
		fields := []string{row.ID, row.Kind, row.Balance}
		if row.TenantID != nil {
			fields = append(fields, *row.TenantID)
		}
		if err := budget.add(fields...); err != nil {
			return "", err
		}
		out.rows.Accounts = append(out.rows.Accounts, row)
		return row.ID, nil
	})
	if err != nil {
		return fundingLedgerSnapshot{}, err
	}
	err = fundingLedgerReadPages(query, fundingLedgerPageSQL(schema, "journals"), func(rows fundingLedgerRows) (string, error) {
		var row fundingLedgerJournalFacts
		if err := rows.Scan(&row.ID, &row.TenantID, &row.OperationID, &row.Kind, &row.Reference, &row.Actor, &row.Currency, &row.CreatedTXID, &row.CreatedAt); err != nil {
			return "", err
		}
		if err := budget.add(row.ID, row.TenantID, row.OperationID, row.Kind, row.Reference, row.Actor, row.Currency, row.CreatedTXID); err != nil {
			return "", err
		}
		xid, err := strconv.ParseInt(row.CreatedTXID, 10, 64)
		row.CreatedAt = row.CreatedAt.UTC()
		if err != nil || xid <= 0 || strconv.FormatInt(xid, 10) != row.CreatedTXID || row.CreatedAt.IsZero() ||
			row.CreatedAt.Year() < 1 || row.CreatedAt.Year() > 9999 || row.CreatedAt.Nanosecond()%1000 != 0 {
			return "", fundingLedgerReadExecution
		}
		out.journals = append(out.journals, row)
		out.rows.Journals = append(out.rows.Journals, fundinglotverify.LedgerJournalRow{ID: row.ID, TenantID: row.TenantID, OperationID: row.OperationID, Currency: row.Currency})
		return row.ID, nil
	})
	if err != nil {
		return fundingLedgerSnapshot{}, err
	}
	err = fundingLedgerReadPages(query, fundingLedgerPageSQL(schema, "journal_lines"), func(rows fundingLedgerRows) (string, error) {
		var row fundinglotverify.LedgerLineRow
		if err := rows.Scan(&row.ID, &row.JournalID, &row.AccountID, &row.SignedDelta); err != nil {
			return "", err
		}
		if err := budget.add(row.ID, row.JournalID, row.AccountID, row.SignedDelta); err != nil {
			return "", err
		}
		out.rows.Lines = append(out.rows.Lines, row)
		return row.ID, nil
	})
	if err != nil {
		return fundingLedgerSnapshot{}, err
	}
	out.check, err = fundinglotverify.CheckLedgerRows(ctx, out.rows)
	if err != nil {
		switch err {
		case fundinglotverify.ErrLedgerRowsResource:
			return fundingLedgerSnapshot{}, fundingLedgerReadResource
		case fundinglotverify.ErrLedgerRowsCanceled:
			return fundingLedgerSnapshot{}, fundingLedgerReadCanceled
		default:
			return fundingLedgerSnapshot{}, fundingLedgerReadExecution
		}
	}
	return out, nil
}
