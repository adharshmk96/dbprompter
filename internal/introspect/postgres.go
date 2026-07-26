package introspect

import (
	"context"
	"database/sql"
)

type postgresIntrospector struct{}

func (postgresIntrospector) Introspect(ctx context.Context, db *sql.DB) (*Schema, []FK, error) {
	// tables + comments + row estimates from pg_catalog
	tblRows, err := db.QueryContext(ctx, `
		SELECT n.nspname, c.relname,
		       COALESCE(obj_description(c.oid, 'pg_class'), ''),
		       GREATEST(c.reltuples::bigint, 0)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','p')
		  AND n.nspname NOT IN ('pg_catalog','information_schema')
		ORDER BY n.nspname, c.relname`)
	if err != nil {
		return nil, nil, err
	}
	defer tblRows.Close()

	var tables []Table
	idx := map[string]int{} // "schema.table" -> index
	for tblRows.Next() {
		var t Table
		if err := tblRows.Scan(&t.Schema, &t.Name, &t.Comment, &t.RowCount); err != nil {
			return nil, nil, err
		}
		idx[t.Schema+"."+t.Name] = len(tables)
		tables = append(tables, t)
	}
	if err := tblRows.Err(); err != nil {
		return nil, nil, err
	}

	// columns, with PK flag
	colRows, err := db.QueryContext(ctx, `
		SELECT c.table_schema, c.table_name, c.column_name, c.data_type,
		       c.is_nullable = 'YES',
		       EXISTS (
		         SELECT 1 FROM information_schema.table_constraints tc
		         JOIN information_schema.key_column_usage kcu
		           ON kcu.constraint_name = tc.constraint_name
		          AND kcu.table_schema = tc.table_schema
		         WHERE tc.constraint_type = 'PRIMARY KEY'
		           AND tc.table_schema = c.table_schema
		           AND tc.table_name = c.table_name
		           AND kcu.column_name = c.column_name
		       )
		FROM information_schema.columns c
		WHERE c.table_schema NOT IN ('pg_catalog','information_schema')
		ORDER BY c.table_schema, c.table_name, c.ordinal_position`)
	if err != nil {
		return nil, nil, err
	}
	defer colRows.Close()
	for colRows.Next() {
		var sch, tbl string
		var col Column
		if err := colRows.Scan(&sch, &tbl, &col.Name, &col.DataType, &col.Nullable, &col.PK); err != nil {
			return nil, nil, err
		}
		if i, ok := idx[sch+"."+tbl]; ok {
			tables[i].Columns = append(tables[i].Columns, col)
		}
	}
	if err := colRows.Err(); err != nil {
		return nil, nil, err
	}

	// foreign keys
	fkRows, err := db.QueryContext(ctx, `
		SELECT tc.constraint_name,
		       tc.table_schema, tc.table_name, kcu.column_name,
		       ccu.table_schema, ccu.table_name, ccu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema
		JOIN information_schema.constraint_column_usage ccu
		  ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema
		WHERE tc.constraint_type = 'FOREIGN KEY'`)
	if err != nil {
		return nil, nil, err
	}
	defer fkRows.Close()
	var fks []FK
	for fkRows.Next() {
		var f FK
		if err := fkRows.Scan(&f.Name, &f.FromSchema, &f.FromTable, &f.FromColumn, &f.ToSchema, &f.ToTable, &f.ToColumn); err != nil {
			return nil, nil, err
		}
		fks = append(fks, f)
	}
	return &Schema{Tables: tables}, fks, fkRows.Err()
}
