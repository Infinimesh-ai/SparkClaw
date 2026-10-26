package emaildiagnostics

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
)

// A read-only catalog check prevents an unsupported/missing index from turning
// a nominally bounded query into a whole-owner sort. It does not repair schema.
func sourcePostgresSchema(ctx context.Context, tx pgx.Tx) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT a.attname,t.typname,a.attnotnull,c.relkind::text FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE a.attrelid=to_regclass('email_management_records') AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum LIMIT 65`)
	if err != nil {
		return false, err
	}
	required := map[string]string{"owner_id": "text", "kind": "text", "id": "text", "parent": "text", "related": "text", "state": "text", "sort_key": "text", "payload": "jsonb"}
	count := 0
	valid := true
	for rows.Next() {
		count++
		var name, typ, kind string
		var notNull bool
		if err = rows.Scan(&name, &typ, &notNull, &kind); err != nil {
			rows.Close()
			return false, err
		}
		if kind != "r" {
			valid = false
		}
		if expected, ok := required[name]; ok {
			if typ != expected || !notNull {
				valid = false
			}
			delete(required, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if !valid || len(required) != 0 || count >= 65 {
		return false, nil
	}
	rows, err = tx.Query(ctx, `SELECT i.indisprimary,ARRAY(SELECT a.attname::text FROM unnest(i.indkey) WITH ORDINALITY AS k(n,position) JOIN pg_catalog.pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.n ORDER BY k.position),ARRAY(SELECT COALESCE(c.collname::text,'') FROM unnest(i.indcollation) WITH ORDINALITY AS k(n,position) LEFT JOIN pg_catalog.pg_collation c ON c.oid=k.n ORDER BY k.position),i.indoption::smallint[] FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class idx ON idx.oid=i.indexrelid JOIN pg_catalog.pg_am am ON am.oid=idx.relam WHERE i.indrelid=to_regclass('email_management_records') AND i.indisvalid AND i.indisready AND i.indpred IS NULL AND i.indexprs IS NULL AND am.amname='btree' LIMIT 65`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	primary, parent, order := false, false, false
	count = 0
	for rows.Next() {
		count++
		var pk bool
		var names, collations []string
		var options []int16
		if err = rows.Scan(&pk, &names, &collations, &options); err != nil {
			return false, err
		}
		if pk && slices.Equal(names, []string{"owner_id", "kind", "id"}) {
			primary = true
		}
		if sourceOrderedIndex(names, collations, options, []string{"owner_id", "kind", "parent", "sort_key", "id"}) {
			parent = true
		}
		if sourceOrderedIndex(names, collations, options, []string{"owner_id", "kind", "sort_key", "id"}) {
			order = true
		}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	return primary && parent && order && count < 65, nil
}

func sourceOrderedIndex(names, collations []string, options []int16, expected []string) bool {
	if !slices.Equal(names, expected) || len(collations) != len(names) || len(options) != len(names) {
		return false
	}
	for i := len(names) - 2; i < len(names); i++ {
		if collations[i] != "C" || options[i]&1 != 1 {
			return false
		}
	}
	return true
}
