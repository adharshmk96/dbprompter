# DB Prompter

Connect to any database, browse its schema, tag what the tables mean, and generate SQL with AI — from a single binary.

## Tech stack

Go + SQLite + htmx/Alpine.js. Everything (templates, CSS, htmx, Alpine, CodeMirror) is embedded in the binary, so there is no Node runtime, no CDN, and no CGO:

```bash
CGO_ENABLED=0 go build -o dbprompter .
```

Cross-compiles to linux/windows/macOS from any machine.

## Run

```bash
./dbprompter serve
```

The dashboard opens at `http://127.0.0.1:8080` — no login. Flags: `--port` (default 8080) and `--data` (default `~/.dbprompter/app.db`).

## What it does

**Connections** — add PostgreSQL, MySQL/MariaDB, SQL Server, or SQLite databases. "Test connection" verifies the DSN before you save; saving kicks off a background goroutine that indexes every table, column, and foreign key. Progress polls into the UI, and re-indexing preserves your tags.

| Type | DSN format |
|---|---|
| PostgreSQL | `postgres://user:pass@localhost:5432/dbname` |
| MySQL | `user:pass@tcp(localhost:3306)/dbname` |
| SQL Server | `sqlserver://user:pass@localhost:1433?database=dbname` |
| SQLite | `/path/to/database.db` |

**Explorer** — search tables by name, column, tag, or note (SQLite FTS5). Each table shows its columns with primary-key and nullability flags, plus which tables it references and which reference it. Tag tables in plain words ("sales, orders, checkout") and add a note describing what a row means — this is what makes the AI good at your schema.

**AI Query** — ask a question in English, get SQL in a syntax-highlighted editor, edit it, and run it. Results render as a table.

## AI providers

Configure providers under Settings; keys are stored only in your local app database.

- **Anthropic** — an API key and a model (default `claude-opus-5`).
- **OpenAI-compatible** — a base URL plus optional key. This covers OpenAI, and **local models** need no key at all: point it at Ollama (`http://localhost:11434/v1`) or LM Studio (`http://localhost:1234/v1`).

For schemas under ~40 tables the whole tagged schema goes to the model. Above that, tables are ranked against your question with FTS and expanded along foreign keys, so joins stay possible without blowing up the prompt.

## Query safety

Queries run with a 30-second timeout and results cap at 500 rows. Anything that isn't a read statement is rejected unless you tick **allow writes**.

## Layout

```
cmd/                 cobra CLI (serve)
internal/store       app SQLite: connections, metadata, tags, jobs, providers + FTS5
internal/dbconn      driver registry (pgx, mysql, mssql, sqlite)
internal/introspect  Introspector interface + one implementation per dialect
internal/indexer     background indexing goroutines with progress
internal/ai          provider interface, Anthropic + OpenAI-compatible, context builder
internal/query       safe query execution
internal/web         handlers, embedded templates and static assets
```
