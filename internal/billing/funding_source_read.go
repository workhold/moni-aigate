package billing

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

var (
	fundingSourceReadInput     = errors.New("funding_source_read_input")
	fundingSourceReadResource  = errors.New("funding_source_read_resource")
	fundingSourceReadCanceled  = errors.New("funding_source_read_canceled")
	fundingSourceReadTimeout   = errors.New("funding_source_read_timeout")
	fundingSourceReadExecution = errors.New("funding_source_read_execution")
)

const (
	fundingSourceReadRowLimit  = 100000
	fundingSourceReadByteLimit = 16 << 20
	fundingSourcePageSize      = 128
)

type fundingSourceReadBudget struct {
	rows  int
	bytes int
}

func (b *fundingSourceReadBudget) add(fields ...string) error {
	if b.rows >= fundingSourceReadRowLimit {
		return fundingSourceReadResource
	}
	b.rows++
	for _, field := range fields {
		if !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
			return fundingSourceReadExecution
		}
		if len(field) > fundingSourceReadByteLimit-b.bytes {
			return fundingSourceReadResource
		}
		b.bytes += len(field)
	}
	return nil
}

func fundingSourceReadFailure(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fundingSourceReadTimeout
		}
		return fundingSourceReadCanceled
	}
	return fundingSourceReadExecution
}

func fundingSourcePageSQL(schema, table string, columns ...string) string {
	identifier := pgx.Identifier{schema, table}.Sanitize()
	return "SELECT " + strings.Join(columns, ",") + " FROM " + identifier +
		" WHERE NOT $1::pg_catalog.bool OR id COLLATE \"C\" > $2::pg_catalog.text COLLATE \"C\"" +
		" ORDER BY id COLLATE \"C\" LIMIT 128"
}

type fundingSourceCreditRawRow struct {
	id, tenantID, amount, state, requester string
	approver, journalID                    *string
}

func readFundingSourcePages(ctx context.Context, query fundingSourceQuery, sql string, scan func(fundingSourceRows) (string, []string, func(), error), budget *fundingSourceReadBudget) error {
	hasCursor, cursor := false, ""
	for {
		if ctx.Err() != nil {
			return fundingSourceReadFailure(ctx)
		}
		rows, err := query.Query(sql, hasCursor, cursor)
		if err != nil {
			return fundingSourceReadFailure(ctx)
		}
		count, last := 0, cursor
		for {
			if ctx.Err() != nil {
				_ = rows.Close()
				return fundingSourceReadFailure(ctx)
			}
			more, nextErr := rows.Next()
			if nextErr != nil {
				_ = rows.Close()
				return fundingSourceReadFailure(ctx)
			}
			if !more {
				break
			}
			key, fields, retain, scanErr := scan(rows)
			if scanErr != nil {
				_ = rows.Close()
				return fundingSourceReadFailure(ctx)
			}
			if count >= fundingSourcePageSize || (hasCursor || count > 0) && key <= last {
				_ = rows.Close()
				return fundingSourceReadExecution
			}
			if err := budget.add(fields...); err != nil {
				_ = rows.Close()
				return err
			}
			if retain == nil {
				_ = rows.Close()
				return fundingSourceReadExecution
			}
			retain()
			last = key
			count++
		}
		rowsErr := rows.Err()
		closeErr := rows.Close()
		if ctx.Err() != nil {
			return fundingSourceReadFailure(ctx)
		}
		if rowsErr != nil {
			return fundingSourceReadFailure(ctx)
		}
		if closeErr != nil {
			return fundingSourceReadFailure(ctx)
		}
		if count < fundingSourcePageSize {
			if ctx.Err() != nil {
				return fundingSourceReadFailure(ctx)
			}
			return nil
		}
		hasCursor, cursor = true, last
	}
}

func readFundingSourceFacts(ctx context.Context, query fundingSourceQuery, schema string) (fundinglotverify.FundingSourceRows, fundinglotverify.FundingSourceLinksCheck, error) {
	if ctx == nil {
		return fundinglotverify.FundingSourceRows{}, fundinglotverify.FundingSourceLinksCheck{}, fundingSourceReadInput
	}
	if ctx.Err() != nil {
		return fundinglotverify.FundingSourceRows{}, fundinglotverify.FundingSourceLinksCheck{}, fundingSourceReadFailure(ctx)
	}
	if query == nil || schema == "" || schema == "pg_catalog" || schema == "information_schema" || strings.HasPrefix(schema, "pg_") || strings.ContainsRune(schema, 0) {
		return fundinglotverify.FundingSourceRows{}, fundinglotverify.FundingSourceLinksCheck{}, fundingSourceReadInput
	}
	var rows fundinglotverify.FundingSourceRows
	var budget fundingSourceReadBudget
	if err := readFundingSourcePages(ctx, query, fundingSourcePageSQL(schema, "funding_receipts",
		"id", "tenant_id", "source_kind", "source_id", "amount::pg_catalog.text", "currency", "journal_id"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingSourceReceiptRow
		if err := source.Scan(&row.ID, &row.TenantID, &row.SourceKind, &row.SourceID, &row.Amount, &row.Currency, &row.JournalID); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID, row.TenantID, row.SourceKind, row.SourceID, row.Amount, row.Currency, row.JournalID}, func() { rows.Receipts = append(rows.Receipts, row) }, nil
	}, &budget); err != nil {
		return fundinglotverify.FundingSourceRows{}, fundinglotverify.FundingSourceLinksCheck{}, err
	}
	if err := readFundingSourcePages(ctx, query, fundingSourcePageSQL(schema, "credit_orders",
		"id", "tenant_id", "amount::pg_catalog.text", "state", "requester", "approver", "journal_id"), func(source fundingSourceRows) (string, []string, func(), error) {
		var raw fundingSourceCreditRawRow
		if err := source.Scan(&raw.id, &raw.tenantID, &raw.amount, &raw.state, &raw.requester, &raw.approver, &raw.journalID); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		row := fundinglotverify.FundingSourceCreditRow{
			ID: raw.id, TenantID: raw.tenantID, Amount: raw.amount, State: raw.state, Requester: raw.requester,
			Approver: raw.approver, JournalID: raw.journalID,
		}
		fields := []string{raw.id, raw.tenantID, raw.amount, raw.state, raw.requester}
		if raw.approver != nil {
			fields = append(fields, *raw.approver)
		}
		if raw.journalID != nil {
			fields = append(fields, *raw.journalID)
		}
		return raw.id, fields, func() { rows.Credits = append(rows.Credits, row) }, nil
	}, &budget); err != nil {
		return fundinglotverify.FundingSourceRows{}, fundinglotverify.FundingSourceLinksCheck{}, err
	}
	if err := readFundingSourcePages(ctx, query, fundingSourcePageSQL(schema, "voucher_redemptions",
		"id", "tenant_id", "voucher_id", "funding_receipt_id", "amount::pg_catalog.text", "journal_id"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.FundingSourceRedemptionRow
		if err := source.Scan(&row.ID, &row.TenantID, &row.VoucherID, &row.FundingReceiptID, &row.Amount, &row.JournalID); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID, row.TenantID, row.VoucherID, row.FundingReceiptID, row.Amount, row.JournalID}, func() { rows.Redemptions = append(rows.Redemptions, row) }, nil
	}, &budget); err != nil {
		return fundinglotverify.FundingSourceRows{}, fundinglotverify.FundingSourceLinksCheck{}, err
	}
	check, err := fundinglotverify.CheckFundingSourceLinks(ctx, rows)
	if err != nil {
		switch err {
		case fundinglotverify.ErrFundingSourceRowsResource:
			return fundinglotverify.FundingSourceRows{}, fundinglotverify.FundingSourceLinksCheck{}, fundingSourceReadResource
		case fundinglotverify.ErrFundingSourceRowsCanceled:
			return fundinglotverify.FundingSourceRows{}, fundinglotverify.FundingSourceLinksCheck{}, fundingSourceReadCanceled
		case fundinglotverify.ErrFundingSourceRowsTimeout:
			return fundinglotverify.FundingSourceRows{}, fundinglotverify.FundingSourceLinksCheck{}, fundingSourceReadTimeout
		default:
			return fundinglotverify.FundingSourceRows{}, fundinglotverify.FundingSourceLinksCheck{}, fundingSourceReadExecution
		}
	}
	return rows, check, nil
}
