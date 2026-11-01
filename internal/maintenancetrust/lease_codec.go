package maintenancetrust

import (
	"bytes"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

const leaseArgumentLimit = 1024
const leaseArgumentBytes = 16 << 20

// Exact concrete types prevent pgx control arguments, QueryRewriter, Valuer
// and user codecs from obtaining a driver capability through an argument.
func leaseArguments(args []any) ([]any, error) {
	if len(args) > leaseArgumentLimit {
		return nil, ErrInput
	}
	out := make([]any, len(args))
	remaining := leaseArgumentBytes
	consume := func(n int) bool {
		if n < 0 || n > remaining {
			return false
		}
		remaining -= n
		return true
	}
	for i, value := range args {
		switch v := value.(type) {
		case nil:
			out[i] = nil
		case bool:
			if !consume(1) {
				return nil, ErrInput
			}
			out[i] = v
		case string:
			if !consume(len(v)) {
				return nil, ErrInput
			}
			out[i] = v
		case int:
			if !consume(8) {
				return nil, ErrInput
			}
			out[i] = v
		case int8:
			if !consume(1) {
				return nil, ErrInput
			}
			out[i] = v
		case int16:
			if !consume(2) {
				return nil, ErrInput
			}
			out[i] = v
		case int32:
			if !consume(4) {
				return nil, ErrInput
			}
			out[i] = v
		case int64:
			if !consume(8) {
				return nil, ErrInput
			}
			out[i] = v
		case uint32:
			if !consume(4) {
				return nil, ErrInput
			}
			out[i] = v
		case time.Time:
			if !consume(8) {
				return nil, ErrInput
			}
			out[i] = v
		case []byte:
			if !consume(len(v)) {
				return nil, ErrInput
			}
			out[i] = bytes.Clone(v)
		case []string:
			if len(v) > remaining/8 || !consume(len(v)*8) {
				return nil, ErrInput
			}
			for _, s := range v {
				if !consume(len(s)) {
					return nil, ErrInput
				}
			}
			out[i] = cloneLeaseSlice(v)
		case []int16:
			if len(v) > remaining/2 || !consume(len(v)*2) {
				return nil, ErrInput
			}
			out[i] = cloneLeaseSlice(v)
		case []int32:
			if len(v) > remaining/4 || !consume(len(v)*4) {
				return nil, ErrInput
			}
			out[i] = cloneLeaseSlice(v)
		case []int64:
			if len(v) > remaining/8 || !consume(len(v)*8) {
				return nil, ErrInput
			}
			out[i] = cloneLeaseSlice(v)
		case []uint32:
			if len(v) > remaining/4 || !consume(len(v)*4) {
				return nil, ErrInput
			}
			out[i] = cloneLeaseSlice(v)
		default:
			return nil, ErrInput
		}
	}
	return out, nil
}

func cloneLeaseSlice[T any](v []T) []T {
	if v == nil {
		return nil
	}
	out := make([]T, len(v))
	copy(out, v)
	return out
}

type leaseScanTarget struct{ destination, temporary any }

// Scan never forwards caller destinations to pgx. In particular RowScanner
// would otherwise receive the original pgx.Rows and its Conn method.
func leaseDestinations(dest []any) ([]leaseScanTarget, []any, error) {
	if len(dest) > leaseArgumentLimit {
		return nil, nil, ErrInput
	}
	targets := make([]leaseScanTarget, len(dest))
	decode := make([]any, len(dest))
	for i, d := range dest {
		var local any
		switch v := d.(type) {
		case *bool:
			if v != nil {
				local = new(bool)
			}
		case *string:
			if v != nil {
				local = new(string)
			}
		case *[]byte:
			if v != nil {
				local = new([]byte)
			}
		case *int:
			if v != nil {
				local = new(int)
			}
		case *int16:
			if v != nil {
				local = new(int16)
			}
		case *int32:
			if v != nil {
				local = new(int32)
			}
		case *int64:
			if v != nil {
				local = new(int64)
			}
		case *uint32:
			if v != nil {
				local = new(uint32)
			}
		case *time.Time:
			if v != nil {
				local = new(time.Time)
			}
		case *pgtype.Timestamptz:
			if v != nil {
				local = new(pgtype.Timestamptz)
			}
		case *pgtype.Text:
			if v != nil {
				local = new(pgtype.Text)
			}
		case *[]int16:
			if v != nil {
				local = new([]int16)
			}
		case *[]int32:
			if v != nil {
				local = new([]int32)
			}
		case *[]int64:
			if v != nil {
				local = new([]int64)
			}
		case *[]uint32:
			if v != nil {
				local = new([]uint32)
			}
		case *[]string:
			if v != nil {
				local = new([]string)
			}
		case **string:
			if v != nil {
				local = new(*string)
			}
		case **bool:
			if v != nil {
				local = new(*bool)
			}
		case **int64:
			if v != nil {
				local = new(*int64)
			}
		case **time.Time:
			if v != nil {
				local = new(*time.Time)
			}
		}
		if local == nil {
			return nil, nil, ErrInput
		}
		targets[i], decode[i] = leaseScanTarget{d, local}, local
	}
	return targets, decode, nil
}

// Own potentially large decoder buffers outside the lifecycle mutex. The
// final publication below only assigns already owned pointers and scalars.
func leaseOwnTargets(targets []leaseScanTarget) {
	for i := range targets {
		target := &targets[i]
		switch v := target.temporary.(type) {
		case *[]byte:
			owned := bytes.Clone(*v)
			target.temporary = &owned
		case *[]int16:
			owned := cloneLeaseSlice(*v)
			target.temporary = &owned
		case *[]int32:
			owned := cloneLeaseSlice(*v)
			target.temporary = &owned
		case *[]int64:
			owned := cloneLeaseSlice(*v)
			target.temporary = &owned
		case *[]uint32:
			owned := cloneLeaseSlice(*v)
			target.temporary = &owned
		case *[]string:
			owned := cloneLeaseSlice(*v)
			target.temporary = &owned
		case **string:
			owned := cloneLeasePointer(*v)
			target.temporary = &owned
		case **bool:
			owned := cloneLeasePointer(*v)
			target.temporary = &owned
		case **int64:
			owned := cloneLeasePointer(*v)
			target.temporary = &owned
		case **time.Time:
			owned := cloneLeasePointer(*v)
			target.temporary = &owned
		}
	}
}

func leaseAssign(targets []leaseScanTarget) {
	for _, target := range targets {
		switch d := target.destination.(type) {
		case *bool:
			*d = *target.temporary.(*bool)
		case *string:
			*d = *target.temporary.(*string)
		case *[]byte:
			*d = *target.temporary.(*[]byte)
		case *int:
			*d = *target.temporary.(*int)
		case *int16:
			*d = *target.temporary.(*int16)
		case *int32:
			*d = *target.temporary.(*int32)
		case *int64:
			*d = *target.temporary.(*int64)
		case *uint32:
			*d = *target.temporary.(*uint32)
		case *time.Time:
			*d = *target.temporary.(*time.Time)
		case *pgtype.Timestamptz:
			*d = *target.temporary.(*pgtype.Timestamptz)
		case *pgtype.Text:
			*d = *target.temporary.(*pgtype.Text)
		case *[]int16:
			*d = *target.temporary.(*[]int16)
		case *[]int32:
			*d = *target.temporary.(*[]int32)
		case *[]int64:
			*d = *target.temporary.(*[]int64)
		case *[]uint32:
			*d = *target.temporary.(*[]uint32)
		case *[]string:
			*d = *target.temporary.(*[]string)
		case **string:
			*d = *target.temporary.(**string)
		case **bool:
			*d = *target.temporary.(**bool)
		case **int64:
			*d = *target.temporary.(**int64)
		case **time.Time:
			*d = *target.temporary.(**time.Time)
		}
	}
}
func cloneLeasePointer[T any](v *T) *T {
	if v == nil {
		return nil
	}
	result := *v
	return &result
}
