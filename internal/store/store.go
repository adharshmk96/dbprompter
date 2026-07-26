// Package store is the app's own SQLite database: saved connections, indexed
// metadata, tags, index-job progress, and AI provider configs.
package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/adharshmk96/dbprompter/internal/introspect"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS connections(
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  type       TEXT NOT NULL,
  dsn        TEXT NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS jobs(
  connection_id INTEGER PRIMARY KEY REFERENCES connections(id) ON DELETE CASCADE,
  status     TEXT NOT NULL DEFAULT 'pending',
  total      INTEGER NOT NULL DEFAULT 0,
  done       INTEGER NOT NULL DEFAULT 0,
  error      TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS db_tables(
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  schema_name   TEXT NOT NULL DEFAULT '',
  table_name    TEXT NOT NULL,
  comment       TEXT NOT NULL DEFAULT '',
  row_count     INTEGER NOT NULL DEFAULT -1,
  tags          TEXT NOT NULL DEFAULT '',
  note          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_tables_conn ON db_tables(connection_id);
CREATE TABLE IF NOT EXISTS db_columns(
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  table_id    INTEGER NOT NULL REFERENCES db_tables(id) ON DELETE CASCADE,
  ordinal     INTEGER NOT NULL DEFAULT 0,
  name        TEXT NOT NULL,
  data_type   TEXT NOT NULL DEFAULT '',
  is_nullable INTEGER NOT NULL DEFAULT 1,
  is_pk       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_columns_table ON db_columns(table_id);
CREATE TABLE IF NOT EXISTS db_fks(
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  name          TEXT NOT NULL DEFAULT '',
  from_schema   TEXT NOT NULL DEFAULT '',
  from_table    TEXT NOT NULL,
  from_column   TEXT NOT NULL,
  to_schema     TEXT NOT NULL DEFAULT '',
  to_table      TEXT NOT NULL,
  to_column     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_fks_conn ON db_fks(connection_id);
CREATE TABLE IF NOT EXISTS ai_providers(
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  kind       TEXT NOT NULL,
  base_url   TEXT NOT NULL DEFAULT '',
  api_key    TEXT NOT NULL DEFAULT '',
  model      TEXT NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE VIRTUAL TABLE IF NOT EXISTS meta_fts USING fts5(
  table_id UNINDEXED, connection_id UNINDEXED, tbl, cols, tags, note, comment
);
`

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc/sqlite is happiest with a single writer connection.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---------- connections ----------

type Connection struct {
	ID        int64
	Name      string
	Type      string // postgres | mysql | mssql | sqlite
	DSN       string
	CreatedAt time.Time
}

func (s *Store) CreateConnection(name, dbType, dsn string) (Connection, error) {
	res, err := s.db.Exec(`INSERT INTO connections(name, type, dsn) VALUES(?,?,?)`, name, dbType, dsn)
	if err != nil {
		return Connection{}, err
	}
	id, _ := res.LastInsertId()
	if _, err := s.db.Exec(`INSERT INTO jobs(connection_id, status) VALUES(?, 'pending')`, id); err != nil {
		return Connection{}, err
	}
	return s.GetConnection(id)
}

func (s *Store) GetConnection(id int64) (Connection, error) {
	var c Connection
	err := s.db.QueryRow(`SELECT id, name, type, dsn, created_at FROM connections WHERE id = ?`, id).
		Scan(&c.ID, &c.Name, &c.Type, &c.DSN, &c.CreatedAt)
	return c, err
}

func (s *Store) ListConnections() ([]Connection, error) {
	rows, err := s.db.Query(`SELECT id, name, type, dsn, created_at FROM connections ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Connection
	for rows.Next() {
		var c Connection
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.DSN, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteConnection(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM meta_fts WHERE connection_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM connections WHERE id = ?`, id)
	return err
}

// ---------- jobs ----------

type Job struct {
	ConnectionID int64
	Status       string // pending | running | done | error
	Total        int
	Done         int
	Error        string
	UpdatedAt    time.Time
}

func (s *Store) GetJob(connID int64) (Job, error) {
	var j Job
	err := s.db.QueryRow(`SELECT connection_id, status, total, done, error, updated_at FROM jobs WHERE connection_id = ?`, connID).
		Scan(&j.ConnectionID, &j.Status, &j.Total, &j.Done, &j.Error, &j.UpdatedAt)
	return j, err
}

func (s *Store) SetJob(connID int64, status string, total, done int, jobErr string) error {
	_, err := s.db.Exec(`INSERT INTO jobs(connection_id, status, total, done, error, updated_at)
		VALUES(?,?,?,?,?,CURRENT_TIMESTAMP)
		ON CONFLICT(connection_id) DO UPDATE SET
		  status=excluded.status, total=excluded.total, done=excluded.done,
		  error=excluded.error, updated_at=CURRENT_TIMESTAMP`,
		connID, status, total, done, jobErr)
	return err
}

// ---------- metadata ----------

// ReplaceMetadata swaps the indexed schema for a connection, preserving any
// user-entered tags/notes keyed by schema.table name.
func (s *Store) ReplaceMetadata(connID int64, sc *introspect.Schema, fks []introspect.FK) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// remember tags before wiping
	type tagged struct{ tags, note string }
	keep := map[string]tagged{}
	rows, err := tx.Query(`SELECT schema_name, table_name, tags, note FROM db_tables WHERE connection_id = ? AND (tags != '' OR note != '')`, connID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var sch, tbl, tags, note string
		if err := rows.Scan(&sch, &tbl, &tags, &note); err != nil {
			rows.Close()
			return err
		}
		keep[sch+"."+tbl] = tagged{tags, note}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, q := range []string{
		`DELETE FROM db_columns WHERE table_id IN (SELECT id FROM db_tables WHERE connection_id = ?)`,
		`DELETE FROM db_tables WHERE connection_id = ?`,
		`DELETE FROM db_fks WHERE connection_id = ?`,
		`DELETE FROM meta_fts WHERE connection_id = ?`,
	} {
		if _, err := tx.Exec(q, connID); err != nil {
			return err
		}
	}

	for _, t := range sc.Tables {
		prev := keep[t.Schema+"."+t.Name]
		res, err := tx.Exec(`INSERT INTO db_tables(connection_id, schema_name, table_name, comment, row_count, tags, note)
			VALUES(?,?,?,?,?,?,?)`, connID, t.Schema, t.Name, t.Comment, t.RowCount, prev.tags, prev.note)
		if err != nil {
			return err
		}
		tableID, _ := res.LastInsertId()
		colNames := make([]string, 0, len(t.Columns))
		for i, c := range t.Columns {
			colNames = append(colNames, c.Name)
			if _, err := tx.Exec(`INSERT INTO db_columns(table_id, ordinal, name, data_type, is_nullable, is_pk)
				VALUES(?,?,?,?,?,?)`, tableID, i, c.Name, c.DataType, boolInt(c.Nullable), boolInt(c.PK)); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO meta_fts(table_id, connection_id, tbl, cols, tags, note, comment)
			VALUES(?,?,?,?,?,?,?)`, tableID, connID, t.Name, strings.Join(colNames, " "), prev.tags, prev.note, t.Comment); err != nil {
			return err
		}
	}

	for _, fk := range fks {
		if _, err := tx.Exec(`INSERT INTO db_fks(connection_id, name, from_schema, from_table, from_column, to_schema, to_table, to_column)
			VALUES(?,?,?,?,?,?,?,?)`,
			connID, fk.Name, fk.FromSchema, fk.FromTable, fk.FromColumn, fk.ToSchema, fk.ToTable, fk.ToColumn); err != nil {
			return err
		}
	}

	return tx.Commit()
}

type TableRow struct {
	ID         int64
	Schema     string
	Name       string
	Comment    string
	RowCount   int64
	Tags       string
	Note       string
	ColumnHint string // first few column names, for list display
}

// ListTables returns tables for the explorer list, optionally filtered by an
// FTS query over names, columns, tags, notes, and comments.
func (s *Store) ListTables(connID int64, q string) ([]TableRow, error) {
	var rows *sql.Rows
	var err error
	if match := ftsQuery(q); match != "" {
		rows, err = s.db.Query(`
			SELECT t.id, t.schema_name, t.table_name, t.comment, t.row_count, t.tags, t.note
			FROM meta_fts f JOIN db_tables t ON t.id = f.table_id
			WHERE f.connection_id = ? AND meta_fts MATCH ?
			ORDER BY rank`, connID, match)
	} else {
		rows, err = s.db.Query(`
			SELECT id, schema_name, table_name, comment, row_count, tags, note
			FROM db_tables WHERE connection_id = ? ORDER BY schema_name, table_name`, connID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TableRow
	for rows.Next() {
		var t TableRow
		if err := rows.Scan(&t.ID, &t.Schema, &t.Name, &t.Comment, &t.RowCount, &t.Tags, &t.Note); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type ColumnRow struct {
	Name     string
	DataType string
	Nullable bool
	PK       bool
}

type FKRow struct {
	Name       string
	FromTable  string
	FromColumn string
	ToTable    string
	ToColumn   string
}

type TableDetail struct {
	TableRow
	ConnectionID int64
	Columns      []ColumnRow
	OutgoingFKs  []FKRow // this table references others
	IncomingFKs  []FKRow // other tables reference this one
}

func (s *Store) GetTable(tableID int64) (TableDetail, error) {
	var d TableDetail
	err := s.db.QueryRow(`SELECT id, connection_id, schema_name, table_name, comment, row_count, tags, note
		FROM db_tables WHERE id = ?`, tableID).
		Scan(&d.ID, &d.ConnectionID, &d.Schema, &d.Name, &d.Comment, &d.RowCount, &d.Tags, &d.Note)
	if err != nil {
		return d, err
	}
	rows, err := s.db.Query(`SELECT name, data_type, is_nullable, is_pk FROM db_columns WHERE table_id = ? ORDER BY ordinal`, tableID)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ColumnRow
		var nullable, pk int
		if err := rows.Scan(&c.Name, &c.DataType, &nullable, &pk); err != nil {
			return d, err
		}
		c.Nullable, c.PK = nullable != 0, pk != 0
		d.Columns = append(d.Columns, c)
	}
	if err := rows.Err(); err != nil {
		return d, err
	}

	out, err := s.db.Query(`SELECT name, from_table, from_column, to_table, to_column FROM db_fks
		WHERE connection_id = ? AND from_table = ? AND from_schema = ?`, d.ConnectionID, d.Name, d.Schema)
	if err != nil {
		return d, err
	}
	defer out.Close()
	for out.Next() {
		var f FKRow
		if err := out.Scan(&f.Name, &f.FromTable, &f.FromColumn, &f.ToTable, &f.ToColumn); err != nil {
			return d, err
		}
		d.OutgoingFKs = append(d.OutgoingFKs, f)
	}
	if err := out.Err(); err != nil {
		return d, err
	}

	in, err := s.db.Query(`SELECT name, from_table, from_column, to_table, to_column FROM db_fks
		WHERE connection_id = ? AND to_table = ? AND to_schema = ?`, d.ConnectionID, d.Name, d.Schema)
	if err != nil {
		return d, err
	}
	defer in.Close()
	for in.Next() {
		var f FKRow
		if err := in.Scan(&f.Name, &f.FromTable, &f.FromColumn, &f.ToTable, &f.ToColumn); err != nil {
			return d, err
		}
		d.IncomingFKs = append(d.IncomingFKs, f)
	}
	return d, in.Err()
}

func (s *Store) UpdateTableTags(tableID int64, tags, note string) error {
	if _, err := s.db.Exec(`UPDATE db_tables SET tags = ?, note = ? WHERE id = ?`, tags, note, tableID); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE meta_fts SET tags = ?, note = ? WHERE table_id = ?`, tags, note, tableID)
	return err
}

// SearchTableIDs ranks tables against a free-text question via FTS.
func (s *Store) SearchTableIDs(connID int64, question string, limit int) ([]int64, error) {
	match := ftsQuery(question)
	if match == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT table_id FROM meta_fts WHERE connection_id = ? AND meta_fts MATCH ? ORDER BY rank LIMIT ?`,
		connID, match, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) CountTables(connID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM db_tables WHERE connection_id = ?`, connID).Scan(&n)
	return n, err
}

// AllFKs returns every foreign key indexed for a connection.
func (s *Store) AllFKs(connID int64) ([]FKRow, error) {
	rows, err := s.db.Query(`SELECT name, from_table, from_column, to_table, to_column FROM db_fks WHERE connection_id = ?`, connID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FKRow
	for rows.Next() {
		var f FKRow
		if err := rows.Scan(&f.Name, &f.FromTable, &f.FromColumn, &f.ToTable, &f.ToColumn); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---------- AI providers ----------

type Provider struct {
	ID      int64
	Name    string
	Kind    string // anthropic | openai
	BaseURL string
	APIKey  string
	Model   string
}

func (s *Store) CreateProvider(name, kind, baseURL, apiKey, model string) (Provider, error) {
	res, err := s.db.Exec(`INSERT INTO ai_providers(name, kind, base_url, api_key, model) VALUES(?,?,?,?,?)`,
		name, kind, baseURL, apiKey, model)
	if err != nil {
		return Provider{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetProvider(id)
}

func (s *Store) GetProvider(id int64) (Provider, error) {
	var p Provider
	err := s.db.QueryRow(`SELECT id, name, kind, base_url, api_key, model FROM ai_providers WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &p.Model)
	return p, err
}

func (s *Store) ListProviders() ([]Provider, error) {
	rows, err := s.db.Query(`SELECT id, name, kind, base_url, api_key, model FROM ai_providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &p.Model); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeleteProvider(id int64) error {
	_, err := s.db.Exec(`DELETE FROM ai_providers WHERE id = ?`, id)
	return err
}

// ---------- helpers ----------

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ftsQuery turns free text into a safe FTS5 prefix query: each token becomes
// a quoted prefix term, so user input can never break FTS syntax.
func ftsQuery(q string) string {
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	})
	var parts []string
	for _, f := range fields {
		parts = append(parts, `"`+f+`"*`)
	}
	return strings.Join(parts, " OR ")
}
