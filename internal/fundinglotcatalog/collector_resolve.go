package fundinglotcatalog

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"strconv"
)

func registerProfileTx(ctx context.Context, tx pgx.Tx, r *resolver, p collectorProfile, b *collectorBudget) ([]uint32, error) {
	if r.register(refKey{classNamespace, p.schemaOID, 0}, object{"class": "namespace", "scope": p.scope, "name": p.scope}) != nil {
		return nil, ErrCatalog
	}
	rows, e := tx.Query(ctx, `SELECT oid,relname,reltoastrelid FROM pg_catalog.pg_class WHERE relnamespace OPERATOR(pg_catalog.=) $1`, p.schemaOID)
	if e != nil {
		return nil, ErrCatalog
	}
	var rels []uint32
	toastOwners := map[uint32]string{}
	for rows.Next() {
		if b.take(1, 0) != nil {
			rows.Close()
			return nil, ErrCatalog
		}
		var oid, toast uint32
		var name string
		if rows.Scan(&oid, &name, &toast) != nil {
			rows.Close()
			return nil, ErrCatalog
		}
		rels = append(rels, oid)
		if r.register(refKey{classRelation, oid, 0}, object{"class": "relation", "scope": p.scope, "name": name}) != nil {
			rows.Close()
			return nil, ErrCatalog
		}
		if toast != 0 {
			if _, exists := toastOwners[toast]; exists {
				rows.Close()
				return nil, ErrCatalog
			}
			toastOwners[toast] = name
		}
	}
	if rows.Err() != nil {
		rows.Close()
		return nil, ErrCatalog
	}
	rows.Close()
	var toastIDs []uint32
	for toast, owner := range toastOwners {
		if r.register(refKey{classRelation, toast, 0}, object{"class": "relation", "scope": "automatic", "name": "toast_heap", "owner": object{"class": "relation", "scope": p.scope, "name": owner}}) != nil {
			return nil, ErrCatalog
		}
		rels = append(rels, toast)
		toastIDs = append(toastIDs, toast)
	}
	if len(toastIDs) > 0 {
		rows, e = tx.Query(ctx, `SELECT indrelid,indexrelid FROM pg_catalog.pg_index WHERE indrelid OPERATOR(pg_catalog.=) ANY($1::pg_catalog.oid[]) LIMIT 100001`, toastIDs)
		if e != nil {
			return nil, ErrCatalog
		}
		counts := map[uint32]int{}
		for rows.Next() {
			var toast, oid uint32
			if rows.Scan(&toast, &oid) != nil || b.take(1, 0) != nil {
				rows.Close()
				return nil, ErrCatalog
			}
			owner, ok := toastOwners[toast]
			if !ok || counts[toast] != 0 {
				rows.Close()
				return nil, ErrCatalog
			}
			counts[toast]++
			rels = append(rels, oid)
			if r.register(refKey{classRelation, oid, 0}, object{"class": "relation", "scope": "automatic", "name": "toast_index", "owner": object{"class": "relation", "scope": p.scope, "name": owner}}) != nil {
				rows.Close()
				return nil, ErrCatalog
			}
		}
		rows.Close()
		if rows.Err() != nil {
			return nil, ErrCatalog
		}
		for _, toast := range toastIDs {
			if counts[toast] != 1 {
				return nil, ErrCatalog
			}
		}
	}

	rows, e = tx.Query(ctx, `SELECT attrelid,attnum,attname FROM pg_catalog.pg_attribute WHERE attrelid OPERATOR(pg_catalog.=) ANY($1::pg_catalog.oid[]) AND attnum OPERATOR(pg_catalog.>) 0 AND NOT attisdropped`, rels)
	if e != nil {
		return nil, ErrCatalog
	}
	for rows.Next() {
		if b.take(1, 0) != nil {
			rows.Close()
			return nil, ErrCatalog
		}
		var rel, num uint32
		var name string
		if rows.Scan(&rel, &num, &name) != nil {
			rows.Close()
			return nil, ErrCatalog
		}
		if r.attributes[rel] == nil {
			r.attributes[rel] = map[uint32]string{}
		}
		r.attributes[rel][num] = name
	}
	if rows.Err() != nil {
		rows.Close()
		return nil, ErrCatalog
	}
	rows.Close()
	rows, e = tx.Query(ctx, `SELECT oid,typname FROM pg_catalog.pg_type WHERE typnamespace OPERATOR(pg_catalog.=) $1`, p.schemaOID)
	if e != nil {
		return nil, ErrCatalog
	}
	for rows.Next() {
		if b.take(1, 0) != nil {
			rows.Close()
			return nil, ErrCatalog
		}
		var oid uint32
		var name string
		if rows.Scan(&oid, &name) != nil {
			rows.Close()
			return nil, ErrCatalog
		}
		ref := object{"class": "type", "scope": p.scope, "name": name}
		if r.register(refKey{classType, oid, 0}, ref) != nil {
			rows.Close()
			return nil, ErrCatalog
		}
		r.types[oid] = ref
	}
	if rows.Err() != nil {
		rows.Close()
		return nil, ErrCatalog
	}
	rows.Close()
	rows, e = tx.Query(ctx, `SELECT oid,proname,proargtypes::pg_catalog.oid[] FROM pg_catalog.pg_proc WHERE pronamespace OPERATOR(pg_catalog.=) $1`, p.schemaOID)
	if e != nil {
		return nil, ErrCatalog
	}
	for rows.Next() {
		if b.take(1, 0) != nil {
			rows.Close()
			return nil, ErrCatalog
		}
		var oid uint32
		var name string
		var args []uint32
		if rows.Scan(&oid, &name, &args) != nil {
			rows.Close()
			return nil, ErrCatalog
		}
		sig := make([]any, len(args))
		for i, a := range args {
			sig[i] = object{"$type": json.Number(strconv.FormatUint(uint64(a), 10))}
		}
		if r.register(refKey{classFunction, oid, 0}, object{"class": "function", "scope": p.scope, "name": name, "identity_arguments": sig}) != nil {
			rows.Close()
			return nil, ErrCatalog
		}
	}
	if rows.Err() != nil {
		rows.Close()
		return nil, ErrCatalog
	}
	rows.Close()
	queries := []struct {
		class uint32
		q     string
	}{
		{2606, `SELECT k.oid,c.relname OPERATOR(pg_catalog.||) '/' OPERATOR(pg_catalog.||) k.conname FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) k.conrelid WHERE k.connamespace OPERATOR(pg_catalog.=) $1`},
		{2604, `SELECT d.oid,c.relname OPERATOR(pg_catalog.||) '/' OPERATOR(pg_catalog.||) d.adnum FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) d.adrelid WHERE c.relnamespace OPERATOR(pg_catalog.=) $1`},
		{2620, `SELECT t.oid,CASE WHEN t.tgisinternal THEN c.relname OPERATOR(pg_catalog.||) '/' OPERATOR(pg_catalog.||) k.conname OPERATOR(pg_catalog.||) '/' OPERATOR(pg_catalog.||) rc.relname OPERATOR(pg_catalog.||) '/' OPERATOR(pg_catalog.||) f.proname OPERATOR(pg_catalog.||) '/' OPERATOR(pg_catalog.||) t.tgtype ELSE c.relname OPERATOR(pg_catalog.||) '/' OPERATOR(pg_catalog.||) t.tgname END FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid OPERATOR(pg_catalog.=) t.tgrelid JOIN pg_catalog.pg_proc f ON f.oid OPERATOR(pg_catalog.=) t.tgfoid LEFT JOIN pg_catalog.pg_constraint k ON k.oid OPERATOR(pg_catalog.=) t.tgconstraint LEFT JOIN pg_catalog.pg_class rc ON rc.oid OPERATOR(pg_catalog.=) k.conrelid WHERE c.relnamespace OPERATOR(pg_catalog.=) $1`},
	}
	for _, q := range queries {
		rows, e = tx.Query(ctx, q.q, p.schemaOID)
		if e != nil {
			return nil, ErrCatalog
		}
		names := map[string]bool{}
		for rows.Next() {
			if b.take(1, 0) != nil {
				rows.Close()
				return nil, ErrCatalog
			}
			var oid uint32
			var name *string
			if rows.Scan(&oid, &name) != nil || name == nil || names[*name] {
				rows.Close()
				return nil, ErrCatalog
			}
			names[*name] = true
			if r.register(refKey{q.class, oid, 0}, object{"class": map[uint32]string{2606: "constraint", 2604: "default", 2620: "trigger"}[q.class], "scope": p.scope, "name": *name}) != nil {
				rows.Close()
				return nil, ErrCatalog
			}
		}
		if rows.Err() != nil {
			rows.Close()
			return nil, ErrCatalog
		}
		rows.Close()
	}

	return rels, nil
}
