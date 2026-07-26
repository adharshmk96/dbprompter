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

const promptRules = `Rules:
- Use only tables and columns present in the schema.
- Prefer explicit JOINs following the listed foreign keys.
- Table descriptions and tags explain what the data means — trust them.
- Unless the question asks otherwise, limit large results to 100 rows.`

// jsonPromptFmt is used for in-app generation, where the reply is parsed.
const jsonPromptFmt = `You are an expert %s SQL engineer. You are given a database schema and a question.
Write a single %s query that answers the question.

Respond with ONLY a JSON object, no markdown fences, in this exact shape:
{"sql": "<the query>", "explanation": "<one or two sentences on how it works>"}

` + promptRules

// sqlPromptFmt is used for the copy-to-clipboard prompt, where a human pastes
// the reply straight into the editor — so plain SQL is what we want back.
const sqlPromptFmt = `You are an expert %s SQL engineer. You are given a database schema and a question.
Write a single %s query that answers the question.

Respond with ONLY the SQL query. No explanation, no commentary, no markdown fences.

` + promptRules

// BuildPrompt renders the system and user messages for copy-paste into any AI
// chat. It needs no provider, so the UI can offer it even with no AI
// configured, and it asks for bare SQL that can be pasted into the editor.
func (s *Service) BuildPrompt(connID int64, question string) (system, user string, err error) {
	return s.buildPrompt(connID, question, sqlPromptFmt)
}

func (s *Service) buildPrompt(connID int64, question, systemFmt string) (system, user string, err error) {
	conn, err := s.st.GetConnection(connID)
	if err != nil {
		return "", "", fmt.Errorf("load connection: %w", err)
	}
	schemaCtx, err := s.buildContext(connID, question)
	if err != nil {
		return "", "", fmt.Errorf("build schema context: %w", err)
	}
	dialect := dialectName(conn.Type)
	system = fmt.Sprintf(systemFmt, dialect, dialect)
	user = fmt.Sprintf("Database schema:\n\n%s\n\nQuestion: %s", schemaCtx, question)
	return system, user, nil
}

func (s *Service) GenerateSQL(ctx context.Context, connID, providerID int64, question string) (GenerateResult, error) {
	prov, err := s.st.GetProvider(providerID)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("load AI provider: %w", err)
	}

	system, user, err := s.buildPrompt(connID, question, jsonPromptFmt)
	if err != nil {
		return GenerateResult{}, err
	}

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
