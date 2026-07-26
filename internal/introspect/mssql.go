package introspect

import (
	"context"
	"database/sql"
)

type mssqlIntrospector struct{}

func (mssqlIntrospector) Introspect(ctx context.Context, db *sql.DB) (*Schema, []FK, error) {
	tblRows, err := db.QueryContext(ctx, `
		SELECT s.name, t.name, COALESCE(p.rows, -1)
		FROM sys.tables t
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		LEFT JOIN (
			SELECT object_id, SUM(rows) AS rows FROM sys.partitions
			WHERE index_id IN (0, 1) GROUP BY object_id
		) p ON p.object_id = t.object_id
		ORDER BY s.name, t.name`)
	if err != nil {
		return nil, nil, err
	}
	defer tblRows.Close()

	var tables []Table
	idx := map[string]int{}
	for tblRows.Next() {
		var t Table
		if err := tblRows.Scan(&t.Schema, &t.Name, &t.RowCount); err != nil {
			return nil, nil, err
		}
		idx[t.Schema+"."+t.Name] = len(tables)
		tables = append(tables, t)
	}
	if err := tblRows.Err(); err != nil {
		return nil, nil, err
	}

	colRows, err := db.QueryContext(ctx, `
		SELECT c.table_schema, c.table_name, c.column_name, c.data_type,
		       CASE WHEN c.is_nullable = 'YES' THEN 1 ELSE 0 END,
		       CASE WHEN pk.column_name IS NULL THEN 0 ELSE 1 END
		FROM information_schema.columns c
		LEFT JOIN (
			SELECT kcu.table_schema, kcu.table_name, kcu.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
			  ON kcu.constraint_name = tc.constraint_name
			 AND kcu.table_schema = tc.table_schema
			WHERE tc.constraint_type = 'PRIMARY KEY'
		) pk ON pk.table_schema = c.table_schema
		    AND pk.table_name = c.table_name
		    AND pk.column_name = c.column_name
		ORDER BY c.table_schema, c.table_name, c.ordinal_position`)
	if err != nil {
		return nil, nil, err
	}
	defer colRows.Close()
	for colRows.Next() {
		var sch, tbl string
		var nullable, pk int
		var col Column
		if err := colRows.Scan(&sch, &tbl, &col.Name, &col.DataType, &nullable, &pk); err != nil {
			return nil, nil, err
		}
		col.Nullable, col.PK = nullable != 0, pk != 0
		if i, ok := idx[sch+"."+tbl]; ok {
			tables[i].Columns = append(tables[i].Columns, col)
		}
	}
	if err := colRows.Err(); err != nil {
		return nil, nil, err
	}

	fkRows, err := db.QueryContext(ctx, `
		SELECT fk.name,
		       sf.name, tf.name, cf.name,
		       st.name, tt.name, ct.name
		FROM sys.foreign_key_columns fkc
		JOIN sys.foreign_keys fk ON fk.object_id = fkc.constraint_object_id
		JOIN sys.tables tf ON tf.object_id = fkc.parent_object_id
		JOIN sys.schemas sf ON sf.schema_id = tf.schema_id
		JOIN sys.columns cf ON cf.object_id = fkc.parent_object_id AND cf.column_id = fkc.parent_column_id
		JOIN sys.tables tt ON tt.object_id = fkc.referenced_object_id
		JOIN sys.schemas st ON st.schema_id = tt.schema_id
		JOIN sys.columns ct ON ct.object_id = fkc.referenced_object_id AND ct.column_id = fkc.referenced_column_id`)
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
