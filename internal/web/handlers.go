package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/adharshmk96/dbprompter/internal/dbconn"
	"github.com/adharshmk96/dbprompter/internal/query"
	"github.com/adharshmk96/dbprompter/internal/store"
)

// ---------- connections ----------

type connVM struct {
	store.Connection
	Job        store.Job
	TableCount int
}

func (s *Server) connectionVMs() ([]connVM, error) {
	conns, err := s.st.ListConnections()
	if err != nil {
		return nil, err
	}
	out := make([]connVM, 0, len(conns))
	for _, c := range conns {
		vm := connVM{Connection: c}
		vm.Job, _ = s.st.GetJob(c.ID)
		vm.TableCount, _ = s.st.CountTables(c.ID)
		out = append(out, vm)
	}
	return out, nil
}

func (s *Server) connectionsPage(w http.ResponseWriter, r *http.Request) {
	vms, err := s.connectionVMs()
	if err != nil {
		http.Error(w, errText(err), http.StatusInternalServerError)
		return
	}
	s.renderPage(w, "connections.html", map[string]any{
		"Active":      "connections",
		"Connections": vms,
	})
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	dbType := r.FormValue("type")
	dsn := strings.TrimSpace(r.FormValue("dsn"))
	if name == "" || dsn == "" {
		http.Error(w, "name and DSN are required", http.StatusBadRequest)
		return
	}
	conn, err := s.st.CreateConnection(name, dbType, dsn)
	if err != nil {
		http.Error(w, errText(err), http.StatusInternalServerError)
		return
	}
	s.idx.Start(conn.ID)
	http.Redirect(w, r, "/connections", http.StatusSeeOther)
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	dbType := r.FormValue("type")
	dsn := strings.TrimSpace(r.FormValue("dsn"))
	if dsn == "" {
		s.renderAlert(w, "error", "Enter a DSN first.")
		return
	}
	if err := dbconn.Test(dbType, dsn); err != nil {
		s.renderAlert(w, "error", "Connection failed: "+errText(err))
		return
	}
	s.renderAlert(w, "ok", "Connection successful.")
}

func (s *Server) reindexConnection(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	_ = s.st.SetJob(id, "running", 0, 0, "")
	s.idx.Start(id)
	s.connectionStatus(w, r)
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := s.st.DeleteConnection(id); err != nil {
		http.Error(w, errText(err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/connections", http.StatusSeeOther)
}

func (s *Server) connectionStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	job, err := s.st.GetJob(id)
	if err != nil {
		job = store.Job{ConnectionID: id, Status: "pending"}
	}
	count, _ := s.st.CountTables(id)
	s.renderPart(w, "conn_status", map[string]any{"Job": job, "TableCount": count, "ID": id})
}

// ---------- explorer ----------

func (s *Server) explorerPage(w http.ResponseWriter, r *http.Request) {
	conns, err := s.st.ListConnections()
	if err != nil {
		http.Error(w, errText(err), http.StatusInternalServerError)
		return
	}
	connID, _ := strconv.ParseInt(r.URL.Query().Get("conn"), 10, 64)
	if connID == 0 && len(conns) > 0 {
		connID = conns[0].ID
	}
	var tables []store.TableRow
	if connID != 0 {
		tables, _ = s.st.ListTables(connID, "")
	}
	s.renderPage(w, "explorer.html", map[string]any{
		"Active":      "explorer",
		"Connections": conns,
		"CurrentConn": connID,
		"Tables":      tables,
	})
}

func (s *Server) explorerTables(w http.ResponseWriter, r *http.Request) {
	connID, _ := strconv.ParseInt(r.URL.Query().Get("conn"), 10, 64)
	q := r.URL.Query().Get("q")
	tables, err := s.st.ListTables(connID, q)
	if err != nil {
		s.renderAlert(w, "error", errText(err))
		return
	}
	s.renderPart(w, "table_list", map[string]any{"Tables": tables})
}

func (s *Server) explorerTableDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	detail, err := s.st.GetTable(id)
	if err != nil {
		s.renderAlert(w, "error", errText(err))
		return
	}
	s.renderPart(w, "table_detail", detail)
}

func (s *Server) saveTableTags(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	tags := strings.TrimSpace(r.FormValue("tags"))
	note := strings.TrimSpace(r.FormValue("note"))
	if err := s.st.UpdateTableTags(id, tags, note); err != nil {
		s.renderAlert(w, "error", errText(err))
		return
	}
	detail, err := s.st.GetTable(id)
	if err != nil {
		s.renderAlert(w, "error", errText(err))
		return
	}
	detail.Tags, detail.Note = tags, note
	s.renderPart(w, "table_detail", detail)
}

// ---------- AI ----------

func (s *Server) aiPage(w http.ResponseWriter, r *http.Request) {
	conns, _ := s.st.ListConnections()
	providers, _ := s.st.ListProviders()
	s.renderPage(w, "ai.html", map[string]any{
		"Active":      "ai",
		"Connections": conns,
		"Providers":   providers,
	})
}

func (s *Server) aiGenerate(w http.ResponseWriter, r *http.Request) {
	connID, _ := strconv.ParseInt(r.FormValue("conn"), 10, 64)
	providerID, _ := strconv.ParseInt(r.FormValue("provider"), 10, 64)
	question := strings.TrimSpace(r.FormValue("question"))
	if connID == 0 || providerID == 0 || question == "" {
		s.renderAlert(w, "error", "Pick a connection, a provider, and type a question.")
		return
	}
	res, err := s.ai.GenerateSQL(r.Context(), connID, providerID, question)
	if err != nil {
		s.renderAlert(w, "error", errText(err))
		return
	}
	s.renderPart(w, "ai_result", map[string]any{
		"SQL":         res.SQL,
		"Explanation": res.Explanation,
		"Model":       res.Model,
	})
}

// aiPrompt renders the exact prompt that would be sent to a model, so the user
// can copy it into any AI chat. Works with no provider configured.
func (s *Server) aiPrompt(w http.ResponseWriter, r *http.Request) {
	connID, _ := strconv.ParseInt(r.FormValue("conn"), 10, 64)
	question := strings.TrimSpace(r.FormValue("question"))
	if connID == 0 || question == "" {
		s.renderAlert(w, "error", "Pick a connection and type a question.")
		return
	}
	system, user, err := s.ai.BuildPrompt(connID, question)
	if err != nil {
		s.renderAlert(w, "error", errText(err))
		return
	}
	s.renderPart(w, "ai_prompt", map[string]any{
		"Prompt": system + "\n\n" + user,
	})
}

func (s *Server) runQuery(w http.ResponseWriter, r *http.Request) {
	connID, _ := strconv.ParseInt(r.FormValue("conn"), 10, 64)
	sqlText := r.FormValue("sql")
	allowWrites := r.FormValue("allow_writes") == "on" || r.FormValue("allow_writes") == "true"
	conn, err := s.st.GetConnection(connID)
	if err != nil {
		s.renderAlert(w, "error", "Pick a connection first.")
		return
	}
	offset, _ := strconv.Atoi(r.FormValue("offset"))
	res, err := query.Run(conn.Type, conn.DSN, sqlText, allowWrites, offset)
	if err != nil {
		s.renderAlert(w, "error", errText(err))
		return
	}
	s.renderPart(w, "results", res)
}

// ---------- settings ----------

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	providers, _ := s.st.ListProviders()
	s.renderPage(w, "settings.html", map[string]any{
		"Active":    "settings",
		"Providers": providers,
	})
}

func (s *Server) createProvider(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	kind := r.FormValue("kind")
	baseURL := strings.TrimSpace(r.FormValue("base_url"))
	apiKey := strings.TrimSpace(r.FormValue("api_key"))
	model := strings.TrimSpace(r.FormValue("model"))
	if name == "" || model == "" {
		http.Error(w, "name and model are required", http.StatusBadRequest)
		return
	}
	if kind != "anthropic" && kind != "openai" {
		http.Error(w, "unknown provider kind", http.StatusBadRequest)
		return
	}
	if _, err := s.st.CreateProvider(name, kind, baseURL, apiKey, model); err != nil {
		http.Error(w, errText(err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := s.st.DeleteProvider(id); err != nil {
		http.Error(w, errText(err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
