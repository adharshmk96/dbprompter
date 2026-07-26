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
	PageSize = 100
	Timeout  = 30 * time.Second
)

type Result struct {
	Columns      []string
	Rows         [][]string
	RowsAffected int64
	Duration     time.Duration
	IsSelect     bool

	// Paging. Offset is the index of the first row in Rows; HasMore reports
	// that the driver had at least one row past this page.
	Offset  int
	HasMore bool
}

// Page returns the 1-based page number of this result.
func (r *Result) Page() int { return r.Offset/PageSize + 1 }

// NextOffset and PrevOffset are the offsets the pager buttons should request.
func (r *Result) NextOffset() int { return r.Offset + PageSize }
func (r *Result) PrevOffset() int {
	if r.Offset < PageSize {
		return 0
	}
	return r.Offset - PageSize
}

// FirstRow and LastRow are the 1-based row numbers this page covers.
func (r *Result) FirstRow() int { return r.Offset + 1 }
func (r *Result) LastRow() int  { return r.Offset + len(r.Rows) }

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

// Run executes sqlText against the target database and returns one page of
// PageSize rows starting at offset. When allowWrites is false, only statements
// that start with a read-only keyword are accepted.
//
// Paging re-runs the query and skips offset rows in the driver rather than
// rewriting the user's SQL with LIMIT/OFFSET: the SQL stays exactly what the
// user wrote, and it works the same across every supported dialect.
func Run(dbType, dsn, sqlText string, allowWrites bool, offset int) (*Result, error) {
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
	if offset < 0 {
		offset = 0
	}
	out := &Result{Columns: cols, IsSelect: true, Offset: offset}
	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	skipped := 0
	for rows.Next() {
		if skipped < offset {
			skipped++
			continue
		}
		if len(out.Rows) >= PageSize {
			// one row past the page: there is a next page
			out.HasMore = true
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
