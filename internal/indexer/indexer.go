// Package indexer runs schema indexing in the background, one goroutine per
// connection, reporting progress through the store's jobs table.
package indexer

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/adharshmk96/dbprompter/internal/dbconn"
	"github.com/adharshmk96/dbprompter/internal/introspect"
	"github.com/adharshmk96/dbprompter/internal/store"
)

type Indexer struct {
	st      *store.Store
	mu      sync.Mutex
	running map[int64]bool
}

func New(st *store.Store) *Indexer {
	return &Indexer{st: st, running: map[int64]bool{}}
}

// Start kicks off indexing for a connection unless one is already running.
func (ix *Indexer) Start(connID int64) {
	ix.mu.Lock()
	if ix.running[connID] {
		ix.mu.Unlock()
		return
	}
	ix.running[connID] = true
	ix.mu.Unlock()

	go func() {
		defer func() {
			ix.mu.Lock()
			delete(ix.running, connID)
			ix.mu.Unlock()
		}()
		ix.run(connID)
	}()
}

func (ix *Indexer) run(connID int64) {
	fail := func(err error) {
		log.Printf("index connection %d: %v", connID, err)
		_ = ix.st.SetJob(connID, "error", 0, 0, err.Error())
	}

	conn, err := ix.st.GetConnection(connID)
	if err != nil {
		fail(err)
		return
	}
	if err := ix.st.SetJob(connID, "running", 0, 0, ""); err != nil {
		fail(err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	db, err := dbconn.Open(conn.Type, conn.DSN)
	if err != nil {
		fail(err)
		return
	}
	defer db.Close()

	intro, err := introspect.For(conn.Type)
	if err != nil {
		fail(err)
		return
	}

	schema, fks, err := intro.Introspect(ctx, db)
	if err != nil {
		fail(err)
		return
	}

	total := len(schema.Tables)
	_ = ix.st.SetJob(connID, "running", total, total/2, "")

	if err := ix.st.ReplaceMetadata(connID, schema, fks); err != nil {
		fail(err)
		return
	}
	_ = ix.st.SetJob(connID, "done", total, total, "")
	log.Printf("indexed connection %d (%s): %d tables, %d foreign keys", connID, conn.Name, total, len(fks))
}
