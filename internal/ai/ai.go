// Package ai turns natural-language questions into SQL using a configured
// provider: Anthropic (official SDK) or any OpenAI-compatible endpoint
// (OpenAI, Ollama, LM Studio, Groq, ...).
package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/adharshmk96/dbprompter/internal/store"
)

// DefaultModels suggests a model per provider kind for the settings UI.
var DefaultModels = map[string]string{
	"anthropic": "claude-opus-5",
	"openai":    "",
}

type Service struct {
	st *store.Store
}

func NewService(st *store.Store) *Service { return &Service{st: st} }

type GenerateResult struct {
	SQL         string
	Explanation string
	Model       string
}

const systemPromptFmt = `You are an expert %s SQL engineer. You are given a database schema and a question.
Write a single %s query that answers the question.

Respond with ONLY a JSON object, no markdown fences, in this exact shape:
{"sql": "<the query>", "explanation": "<one or two sentences on how it works>"}

Rules:
- Use only tables and columns present in the schema.
- Prefer explicit JOINs following the listed foreign keys.
- Table descriptions and tags explain what the data means — trust them.
- Unless the question asks otherwise, limit large results to 100 rows.`

func (s *Service) GenerateSQL(ctx context.Context, connID, providerID int64, question string) (GenerateResult, error) {
	conn, err := s.st.GetConnection(connID)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("load connection: %w", err)
	}
	prov, err := s.st.GetProvider(providerID)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("load AI provider: %w", err)
	}

	schemaCtx, err := s.buildContext(connID, question)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("build schema context: %w", err)
	}

	dialect := dialectName(conn.Type)
	system := fmt.Sprintf(systemPromptFmt, dialect, dialect)
	user := fmt.Sprintf("Database schema:\n\n%s\n\nQuestion: %s", schemaCtx, question)

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var raw string
	switch prov.Kind {
	case "anthropic":
		raw, err = callAnthropic(ctx, prov, system, user)
	case "openai":
		raw, err = callOpenAICompatible(ctx, prov, system, user)
	default:
		err = fmt.Errorf("unknown provider kind %q", prov.Kind)
	}
	if err != nil {
		return GenerateResult{}, err
	}

	sqlText, explanation := parseResponse(raw)
	if sqlText == "" {
		return GenerateResult{}, fmt.Errorf("the model did not return a SQL query; raw response: %.300s", raw)
	}
	return GenerateResult{SQL: sqlText, Explanation: explanation, Model: prov.Model}, nil
}

func dialectName(dbType string) string {
	switch dbType {
	case "postgres":
		return "PostgreSQL"
	case "mysql":
		return "MySQL"
	case "mssql":
		return "SQL Server (T-SQL)"
	case "sqlite":
		return "SQLite"
	}
	return dbType
}
