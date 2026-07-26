// Package introspect reads schema metadata (tables, columns, foreign keys)
// from target databases, one implementation per dialect.
package introspect

import (
	"context"
	"database/sql"
	"fmt"
)

type Column struct {
	Name     string
	DataType string
	Nullable bool
	PK       bool
}

type Table struct {
	Schema   string
	Name     string
	Comment  string
	RowCount int64 // estimate; -1 when unknown
	Columns  []Column
}

type Schema struct {
	Tables []Table
}

type FK struct {
	Name       string
	FromSchema string
	FromTable  string
	FromColumn string
	ToSchema   string
	ToTable    string
	ToColumn   string
}

type Introspector interface {
	Introspect(ctx context.Context, db *sql.DB) (*Schema, []FK, error)
}

func For(dbType string) (Introspector, error) {
	switch dbType {
	case "postgres":
		return postgresIntrospector{}, nil
	case "mysql":
		return mysqlIntrospector{}, nil
	case "mssql":
		return mssqlIntrospector{}, nil
	case "sqlite":
		return sqliteIntrospector{}, nil
	default:
		return nil, fmt.Errorf("no introspector for database type %q", dbType)
	}
}
