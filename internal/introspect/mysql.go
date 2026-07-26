package introspect

import (
	"context"
	"database/sql"
)

type mysqlIntrospector struct{}

func (mysqlIntrospector) Introspect(ctx context.Context, db *sql.DB) (*Schema, []FK, error) {
	tblRows, err := db.QueryContext(ctx, `
		SELECT table_name, COALESCE(table_comment, ''), COALESCE(table_rows, 0)
		FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'
		ORDER BY table_name`)
	if err != nil {
		return nil, nil, err
	}
	defer tblRows.Close()

	var tables []Table
	idx := map[string]int{}
	for tblRows.Next() {
		var t Table
		if err := tblRows.Scan(&t.Name, &t.Comment, &t.RowCount); err != nil {
			return nil, nil, err
		}
		idx[t.Name] = len(tables)
		tables = append(tables, t)
	}
	if err := tblRows.Err(); err != nil {
		return nil, nil, err
	}

	colRows, err := db.QueryContext(ctx, `
		SELECT table_name, column_name, data_type,
		       is_nullable = 'YES', column_key = 'PRI'
		FROM information_schema.columns
		WHERE table_schema = DATABASE()
		ORDER BY table_name, ordinal_position`)
	if err != nil {
		return nil, nil, err
	}
	defer colRows.Close()
	for colRows.Next() {
		var tbl string
		var col Column
		if err := colRows.Scan(&tbl, &col.Name, &col.DataType, &col.Nullable, &col.PK); err != nil {
			return nil, nil, err
		}
		if i, ok := idx[tbl]; ok {
			tables[i].Columns = append(tables[i].Columns, col)
		}
	}
	if err := colRows.Err(); err != nil {
		return nil, nil, err
	}

	fkRows, err := db.QueryContext(ctx, `
		SELECT constraint_name, table_name, column_name,
		       referenced_table_name, referenced_column_name
		FROM information_schema.key_column_usage
		WHERE table_schema = DATABASE() AND referenced_table_name IS NOT NULL`)
	if err != nil {
		return nil, nil, err
	}
	defer fkRows.Close()
	var fks []FK
	for fkRows.Next() {
		var f FK
		if err := fkRows.Scan(&f.Name, &f.FromTable, &f.FromColumn, &f.ToTable, &f.ToColumn); err != nil {
			return nil, nil, err
		}
		fks = append(fks, f)
	}
	return &Schema{Tables: tables}, fks, fkRows.Err()
}
