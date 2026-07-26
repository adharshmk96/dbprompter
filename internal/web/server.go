// Package web serves the dashboard: an embedded htmx/Alpine UI over the
// store, indexer, query runner, and AI service.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"

	"github.com/adharshmk96/dbprompter/internal/ai"
	"github.com/adharshmk96/dbprompter/internal/indexer"
	"github.com/adharshmk96/dbprompter/internal/store"
)

//go:embed templates static
var assets embed.FS

type Server struct {
	st    *store.Store
	idx   *indexer.Indexer
	ai    *ai.Service
	pages map[string]*template.Template
	parts *template.Template
	mux   *http.ServeMux
}

func New(st *store.Store, idx *indexer.Indexer, aiSvc *ai.Service) *Server {
	s := &Server{st: st, idx: idx, ai: aiSvc, mux: http.NewServeMux()}
	s.parseTemplates()
	s.routes()
	return s
}

var tmplFuncs = template.FuncMap{
	// dict builds a map inside templates: (dict "Key" val "Key2" val2)
	"dict": func(pairs ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(pairs); i += 2 {
			if k, ok := pairs[i].(string); ok {
				m[k] = pairs[i+1]
			}
		}
		return m
	},
}

func (s *Server) parseTemplates() {
	s.pages = map[string]*template.Template{}
	for _, page := range []string{"connections.html", "explorer.html", "ai.html", "settings.html"} {
		s.pages[page] = template.Must(template.New("layout.html").Funcs(tmplFuncs).ParseFS(assets,
			"templates/layout.html",
			"templates/pages/"+page,
			"templates/partials/*.html",
		))
	}
	s.parts = template.Must(template.New("partials").Funcs(tmplFuncs).ParseFS(assets, "templates/partials/*.html"))
}

func (s *Server) routes() {
	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/connections", http.StatusSeeOther)
	})

	s.mux.HandleFunc("GET /connections", s.connectionsPage)
	s.mux.HandleFunc("POST /connections", s.createConnection)
	s.mux.HandleFunc("POST /connections/test", s.testConnection)
	s.mux.HandleFunc("POST /connections/{id}/reindex", s.reindexConnection)
	s.mux.HandleFunc("POST /connections/{id}/delete", s.deleteConnection)
	s.mux.HandleFunc("GET /connections/{id}/status", s.connectionStatus)

	s.mux.HandleFunc("GET /explorer", s.explorerPage)
	s.mux.HandleFunc("GET /explorer/tables", s.explorerTables)
	s.mux.HandleFunc("GET /explorer/table/{id}", s.explorerTableDetail)
	s.mux.HandleFunc("POST /explorer/table/{id}/tags", s.saveTableTags)

	s.mux.HandleFunc("GET /ai", s.aiPage)
	s.mux.HandleFunc("POST /ai/generate", s.aiGenerate)
	s.mux.HandleFunc("POST /query/run", s.runQuery)

	s.mux.HandleFunc("GET /settings", s.settingsPage)
	s.mux.HandleFunc("POST /settings/providers", s.createProvider)
	s.mux.HandleFunc("POST /settings/providers/{id}/delete", s.deleteProvider)
}

func (s *Server) Listen(addr string) error {
	return http.ListenAndServe(addr, s.mux)
}

// renderPage executes a full page through the layout.
func (s *Server) renderPage(w http.ResponseWriter, page string, data map[string]any) {
	tmpl, ok := s.pages[page]
	if !ok {
		http.Error(w, "unknown page "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		log.Printf("render %s: %v", page, err)
	}
}

// renderPart executes a named partial (htmx fragment).
func (s *Server) renderPart(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.parts.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render partial %s: %v", name, err)
	}
}

// renderAlert writes a small inline alert fragment.
func (s *Server) renderAlert(w http.ResponseWriter, kind, msg string) {
	s.renderPart(w, "alert", map[string]any{"Kind": kind, "Message": msg})
}

func errText(err error) string {
	return fmt.Sprintf("%v", err)
}
