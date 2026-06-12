// Package conndb provides a per-connection database instance.
package conndb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"sync"

	"github.com/koron/duckpop/internal/syncmap"
)

type Manager struct {
	MaxDB  int
	Opener Opener
	Closer Closer

	connToID syncmap.Map[net.Conn, ID]
	clients  syncmap.Map[ID, *Client]

	dbCount int
	dbMutex sync.Mutex
}

type Opener interface {
	Open(ctx context.Context) (*sql.DB, *sql.Conn, error)
}

type OpenerFunc func(ctx context.Context) (*sql.DB, *sql.Conn, error)

func (fn OpenerFunc) Open(ctx context.Context) (*sql.DB, *sql.Conn, error) {
	return fn(ctx)
}

type Closer interface {
	Close(ctx context.Context, db *sql.DB) error
}

type CloserFunc func(ctx context.Context, db *sql.DB) error

func (fn CloserFunc) Close(ctx context.Context, db *sql.DB) error {
	return fn(ctx, db)
}

type ID uint32

func (id ID) String() string {
	return fmt.Sprintf("C_%08x", uint32(id))
}

func (m *Manager) withNewClient(ctx context.Context, c net.Conn) *Client {
	client := &Client{m: m}
	for {
		id := ID(rand.Uint32())
		_, ok := m.clients.LoadOrStore(id, client)
		if !ok {
			m.connToID.Store(c, id)
			client.ID = id
			client.ctx = context.WithValue(ctx, connIDKey{}, client.ID)
			return client
		}
	}
}

type connIDKey = struct{}

func (m *Manager) ConnContext(ctx context.Context, c net.Conn) context.Context {
	client := m.withNewClient(ctx, c)
	return client.Context()
}

func (m *Manager) ConnState(c net.Conn, s http.ConnState) {
	if s == http.StateClosed {
		err := m.closeConn(c)
		if err != nil {
			slog.Warn("failed to close DB", "error", err)
		}
	}
}

func (m *Manager) closeConn(c net.Conn) error {
	id, ok := m.connToID.LoadAndDelete(c)
	if !ok {
		return fmt.Errorf("no ID for net.Conn=%p", c)
	}

	client, ok := m.clients.LoadAndDelete(id)
	if !ok {
		return nil
	}

	go func(client *Client) {
		client.mu.Lock()
		err := client.close()
		client.mu.Unlock()
		if err != nil {
			slog.Warn("failed to close DB", "connID", client.ID, "error", err)
		}
	}(client)

	return nil
}

var (
	ErrNoID         = errors.New("no ID assigned for the context")
	ErrNoConnection = errors.New("no connections assigned for the context")
	ErrMaxDB        = errors.New("reached maximum number of DB")
	ErrNoOpener     = errors.New("no Opener specified")
)

// GetID extracts associated conndb.ID from context.Context
func GetID(ctx context.Context) (ID, bool) {
	id, ok := ctx.Value(connIDKey{}).(ID)
	return id, ok
}

func dbToStr(db *sql.DB) string {
	return fmt.Sprintf("%p", db)
}

func (m *Manager) openDB(ctx context.Context, id ID) (*sql.DB, *sql.Conn, error) {
	m.dbMutex.Lock()
	defer m.dbMutex.Unlock()
	if m.dbCount >= m.MaxDB {
		return nil, nil, ErrMaxDB
	}
	if m.Opener == nil {
		return nil, nil, ErrNoOpener
	}
	db, conn, err := m.Opener.Open(context.WithValue(ctx, connIDKey{}, id))
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxIdleConns(0)
	m.dbCount++
	slog.Debug("DB opened", "connID", id, "DB", dbToStr(db), "count", m.dbCount)
	return db, conn, nil
}

func (m *Manager) closeDB(db *sql.DB, id ID) error {
	m.dbMutex.Lock()
	if m.dbCount > 0 {
		m.dbCount--
	}
	count := m.dbCount
	m.dbMutex.Unlock()
	ctx := context.WithValue(context.Background(), connIDKey{}, id)
	if m.Closer == nil {
		return db.Close()
	}
	slog.Debug("DB closed", "connID", id, "DB", dbToStr(db), "count", count)
	return m.Closer.Close(ctx, db)
}

func (m *Manager) Databases() iter.Seq2[ID, *sql.DB] {
	return func(yield func(ID, *sql.DB) bool) {
		m.clients.Range(func(id ID, c *Client) bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.db == nil {
				return true
			}
			return yield(id, c.db)
		})
	}
}

func (m *Manager) Client(ctx context.Context) (*Client, error) {
	id, ok := ctx.Value(connIDKey{}).(ID)
	if !ok {
		return nil, ErrNoID
	}
	client, ok := m.clients.Load(id)
	if !ok {
		return nil, ErrNoConnection
	}
	return client, nil
}

type Client struct {
	m   *Manager
	ctx context.Context

	ID ID

	mu   sync.Mutex
	db   *sql.DB
	conn *sql.Conn
}

func (client *Client) Context() context.Context {
	return client.ctx
}

func (client *Client) Conn() (*sql.Conn, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.conn != nil {
		return client.conn, nil
	}
	if client.db == nil {
		db, conn, err := client.m.openDB(client.ctx, client.ID)
		if err != nil {
			return nil, err
		}
		client.db = db
		client.conn = conn
	}
	return client.conn, nil
}

func (client *Client) close() error {
	var err1, err2 error
	if client.conn != nil {
		err1 = client.conn.Close()
		client.conn = nil
	}
	if client.db != nil {
		err2 = client.m.closeDB(client.db, client.ID)
		client.db = nil
	}
	if err1 != nil {
		return err1
	}
	return err2
}
