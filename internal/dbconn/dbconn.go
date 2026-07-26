// Package dbconn opens connections to the user's target databases.
package dbconn

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "modernc.org/sqlite"
)

// driverFor maps our connection type to the registered sql driver name.
func driverFor(dbType string) (string, error) {
	switch dbType {
	case "postgres":
		return "pgx", nil
	case "mysql":
		return "mysql", nil
	case "mssql":
		return "sqlserver", nil
	case "sqlite":
		return "sqlite", nil
	default:
		return "", fmt.Errorf("unsupported database type %q", dbType)
	}
}

func Open(dbType, dsn string) (*sql.DB, error) {
	driver, err := driverFor(dbType)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxIdleTime(time.Minute)
	return db, nil
}

// Test opens and pings a target database with a short timeout.
func Test(dbType, dsn string) error {
	db, err := Open(dbType, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}
