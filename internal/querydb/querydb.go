// Package querydb provides database for queries.
package querydb

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/koron/duckpop/internal/conndb"
)

type Database struct {
	mu      sync.RWMutex
	queries map[ID]*Query
}

type ID uint32

func (id ID) String() string {
	return fmt.Sprintf("Q_%08x", uint32(id))
}

func ParseID(s string) (ID, error) {
	if !strings.HasPrefix(s, "Q_") {
		return 0, errors.New("query ID should start with \"Q_\"")
	}
	n, err := strconv.ParseUint(s[2:], 16, 32)
	if err != nil {
		return 0, err
	}
	return ID(n), nil
}

type Query struct {
	ID     ID
	ConnID conndb.ID
	Query  string
	Start  time.Time

	ctx    context.Context
	cancel context.CancelFunc
	db     *Database
}

// QueryStats contains query statistics.
type QueryStats struct {
	ID       string `json:"ID"`
	ConnID   string `json:"ConnID"`
	Query    string `json:"Query"`
	Start    string `json:"Start"`
	Duration string `json:"Duration"`
}

func (db *Database) newID() ID {
	for {
		id := ID(rand.Uint32())
		if _, ok := db.queries[id]; !ok {
			return id
		}
	}
}

func (db *Database) Add(ctx context.Context, connID conndb.ID, query string) *Query {
	qctx, cancel := context.WithCancel(ctx)
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.queries == nil {
		db.queries = map[ID]*Query{}
	}
	q := &Query{
		ID:     db.newID(),
		ConnID: connID,
		Query:  query,
		Start:  time.Now(),
		ctx:    qctx,
		cancel: cancel,
		db:     db,
	}
	db.queries[q.ID] = q
	return q
}

func (db *Database) remove(id ID) bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	if _, ok := db.queries[id]; !ok {
		return false
	}
	delete(db.queries, id)
	return true
}

func (db *Database) Queries() []*Query {
	db.mu.RLock()
	defer db.mu.RUnlock()
	queries := make([]*Query, 0, len(db.queries))
	for _, q := range db.queries {
		queries = append(queries, q)
	}
	return queries
}

func (db *Database) Query(id ID) (*Query, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	q, ok := db.queries[id]
	return q, ok
}

func (q *Query) Context() context.Context {
	return q.ctx
}

func (q *Query) Close() {
	if !q.db.remove(q.ID) {
		return
	}
	q.cancel()
}

func (q *Query) Stats(now time.Time) QueryStats {
	return QueryStats{
		ID:       q.ID.String(),
		ConnID:   q.ConnID.String(),
		Query:    q.Query,
		Start:    q.Start.Format(time.RFC3339),
		Duration: now.Sub(q.Start).String(),
	}
}
