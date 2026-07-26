package introspect

import (
	"context"
	"database/sql"
	"fmt"
)

type sqliteIntrospector struct{}

func (sqliteIntrospector) Introspect(ctx context.Context, db *sql.DB) (*Schema, []FK, error) {
	tblRows, err := db.QueryContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name`)
	if err != nil {
		return nil, nil, err
	}
	var names []string
	for tblRows.Next() {
		var n string
		if err := tblRows.Scan(&n); err != nil {
			tblRows.Close()
			return nil, nil, err
		}
		names = append(names, n)
	}
	tblRows.Close()
	if err := tblRows.Err(); err != nil {
		return nil, nil, err
	}

	var tables []Table
	var fks []FK
	for _, name := range names {
		t := Table{Name: name, RowCount: -1}

		cols, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%q)`, name))
		if err != nil {
			return nil, nil, err
		}
		for cols.Next() {
			var cid, notnull, pk int
			var colName, colType string
			var dflt sql.NullString
			if err := cols.Scan(&cid, &colName, &colType, &notnull, &dflt, &pk); err != nil {
				cols.Close()
				return nil, nil, err
			}
			t.Columns = append(t.Columns, Column{
				Name: colName, DataType: colType,
				Nullable: notnull == 0, PK: pk > 0,
			})
		}
		cols.Close()
		if err := cols.Err(); err != nil {
			return nil, nil, err
		}

		fkRows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA foreign_key_list(%q)`, name))
		if err != nil {
			return nil, nil, err
		}
		for fkRows.Next() {
			var id, seq int
			var toTable, from string
			var to sql.NullString
			var onUpdate, onDelete, match string
			if err := fkRows.Scan(&id, &seq, &toTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				fkRows.Close()
				return nil, nil, err
			}
			fks = append(fks, FK{
				Name:      fmt.Sprintf("%s_fk_%d", name, id),
				FromTable: name, FromColumn: from,
				ToTable: toTable, ToColumn: to.String,
			})
		}
		fkRows.Close()
		if err := fkRows.Err(); err != nil {
			return nil, nil, err
		}

		// row count is cheap enough locally
		var n int64
		if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %q`, name)).Scan(&n); err == nil {
			t.RowCount = n
		}
		tables = append(tables, t)
	}
	return &Schema{Tables: tables}, fks, nil
}
