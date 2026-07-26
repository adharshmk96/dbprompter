// Package query executes user SQL against target databases with guardrails:
// a timeout, a read-only gate, and a row cap.
package query

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adharshmk96/dbprompter/internal/dbconn"
)

const (
	MaxRows = 500
	Timeout = 30 * time.Second
)

type Result struct {
	Columns      []string
	Rows         [][]string
	RowsAffected int64
	Truncated    bool
	Duration     time.Duration
	IsSelect     bool
}

var readOnlyPrefixes = []string{"select", "with", "show", "explain", "describe", "desc", "pragma", "values"}

func isReadOnly(sqlText string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(sqlText))
	// strip leading line comments
	for strings.HasPrefix(trimmed, "--") {
		if i := strings.IndexByte(trimmed, '\n'); i >= 0 {
			trimmed = strings.TrimSpace(trimmed[i+1:])
		} else {
			return false
		}
	}
	for _, p := range readOnlyPrefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

// Run executes sqlText against the target database. When allowWrites is
// false, only statements that start with a read-only keyword are accepted.
func Run(dbType, dsn, sqlText string, allowWrites bool) (*Result, error) {
	if strings.TrimSpace(sqlText) == "" {
		return nil, fmt.Errorf("empty query")
	}
	readOnly := isReadOnly(sqlText)
	if !readOnly && !allowWrites {
		return nil, fmt.Errorf("this looks like a write statement — enable \"allow writes\" to run it")
	}

	db, err := dbconn.Open(dbType, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	start := time.Now()

	if !readOnly {
		res, err := db.ExecContext(ctx, sqlText)
		if err != nil {
			return nil, err
		}
		affected, _ := res.RowsAffected()
		return &Result{RowsAffected: affected, Duration: time.Since(start)}, nil
	}

	rows, err := db.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := &Result{Columns: cols, IsSelect: true}
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	for rows.Next() {
		if len(out.Rows) >= MaxRows {
			out.Truncated = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]string, len(cols))
		for i, v := range raw {
			row[i] = render(v)
		}
		out.Rows = append(out.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Duration = time.Since(start)
	return out, nil
}

func render(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", x)
	}
}
