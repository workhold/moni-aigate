package fundinglotruntime

import (
	"bytes"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

// Only the fixed ReadWallet registry receives this private adapter. Neither
// caller SQL nor caller Rows can be injected through the public composer API.
type walletQuery struct {
	tx      pgx.Tx
	window  *verificationWindow
	pending *walletRows
	failed  bool
}

type walletRows struct {
	query                           *walletQuery
	driver                          pgx.Rows
	current, consumed, eof, errSeen bool
	closed, released                bool
}

func (q *walletQuery) check() error {
	if q == nil || q.failed || q.tx == nil || q.window.check() != nil {
		return ErrRuntime
	}
	return nil
}

func (q *walletQuery) fail() error {
	if q != nil {
		q.failed = true
		if q.window != nil && q.window.cancel != nil {
			q.window.cancel()
		}
	}
	return ErrRuntime
}

func (q *walletQuery) Query(sql string, args ...any) (fundinglotscan.Rows, error) {
	if q.check() != nil || q.pending != nil || strings.TrimSpace(sql) == "" || len(args) > 1024 {
		return nil, q.fail()
	}
	owned := make([]any, len(args)+1)
	owned[0] = pgx.QueryExecModeSimpleProtocol
	remaining := 16 << 20
	for i, value := range args {
		switch v := value.(type) {
		case bool:
			remaining--
			owned[i+1] = v
		case int64:
			remaining -= 8
			owned[i+1] = v
		case string:
			if len(v) > remaining {
				return nil, q.fail()
			}
			remaining -= len(v)
			owned[i+1] = strings.Clone(v)
		default:
			return nil, q.fail()
		}
		if remaining < 0 {
			return nil, q.fail()
		}
	}
	if q.check() != nil {
		return nil, q.fail()
	}
	driver, err := q.tx.Query(q.window.ctx, sql, owned...)
	if err != nil || nilDriver(driver) {
		return nil, q.fail()
	}
	r := &walletRows{query: q, driver: driver}
	q.pending = r
	if q.check() != nil {
		return nil, q.fail()
	}
	return r, nil
}

func nilDriver(rows pgx.Rows) bool {
	if rows == nil {
		return true
	}
	v := reflect.ValueOf(rows)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func (r *walletRows) valid() error {
	if r == nil || r.query == nil {
		return ErrRuntime
	}
	if r.query.check() != nil || nilDriver(r.driver) || r.released || r.query.pending != r {
		return r.query.fail()
	}
	return nil
}

func (r *walletRows) Next() (bool, error) {
	if r.valid() != nil {
		return false, ErrRuntime
	}
	if r.current && !r.consumed {
		return false, r.query.fail()
	}
	if r.eof {
		return false, nil
	}
	more := r.driver.Next()
	if r.query.check() != nil {
		return false, r.query.fail()
	}
	if !more {
		err := r.driver.Err()
		if r.query.check() != nil || err != nil {
			return false, r.query.fail()
		}
	}
	r.current, r.consumed, r.eof = more, false, !more
	return more, nil
}

func (r *walletRows) Scan(dest ...any) error {
	if r.valid() != nil {
		return ErrRuntime
	}
	if !r.current || r.consumed || r.eof || len(dest) > 1024 {
		return r.query.fail()
	}
	local := make([]any, len(dest))
	for i, d := range dest {
		switch v := d.(type) {
		case *bool:
			if v != nil {
				local[i] = new(bool)
			}
		case *pgtype.Text:
			if v != nil {
				local[i] = new(pgtype.Text)
			}
		case **bool:
			if v != nil {
				local[i] = new(*bool)
			}
		case *[]byte:
			if v != nil {
				local[i] = new([]byte)
			}
		}
		if local[i] == nil {
			return r.query.fail()
		}
	}
	if r.driver.Scan(local...) != nil || r.query.check() != nil {
		return r.query.fail()
	}
	// Do not forward arbitrary RowScanner/codecs or driver-owned buffers. Publish
	// only after the complete scan and final absolute deadline check succeed.
	for i, d := range local {
		switch v := d.(type) {
		case *pgtype.Text:
			v.String = strings.Clone(v.String)
		case **bool:
			if *v != nil {
				b := **v
				*v = &b
			}
		case *[]byte:
			*v = bytes.Clone(*v)
		}
		local[i] = d
	}
	if r.query.check() != nil {
		return r.query.fail()
	}
	for i, d := range dest {
		switch v := d.(type) {
		case *bool:
			*v = *local[i].(*bool)
		case *pgtype.Text:
			*v = *local[i].(*pgtype.Text)
		case **bool:
			*v = *local[i].(**bool)
		case *[]byte:
			*v = *local[i].(*[]byte)
		}
	}
	if r.query.check() != nil {
		return r.query.fail()
	}
	r.consumed = true
	return nil
}

func (r *walletRows) Err() error {
	if r != nil && r.released {
		return r.query.check()
	}
	if r.valid() != nil {
		return ErrRuntime
	}
	err := r.driver.Err()
	if r.query.check() != nil || err != nil {
		return r.query.fail()
	}
	if r.eof {
		r.errSeen = true
	}
	return nil
}

func (r *walletRows) Close() error {
	if r != nil && r.released {
		return r.query.check()
	}
	if r.valid() != nil {
		return ErrRuntime
	}
	if !r.eof || !r.errSeen {
		// Keep pending poisoned. pgx.Close would consume unread results; cancel
		// this query instead and leave bounded transaction cleanup to the caller.
		return r.query.fail()
	}
	r.driver.Close()
	if r.query.check() != nil {
		return r.query.fail()
	}
	err := r.driver.Err()
	if r.query.check() != nil || err != nil {
		return r.query.fail()
	}
	r.closed, r.released = true, true
	r.query.pending = nil
	return nil
}
