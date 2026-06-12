// Package duckdbinit provides an initializer for DuckDB instance.
package duckdbinit

import (
	"context"
	"database/sql"
	"net/url"
)

func Open(ctx context.Context, s Settings, initQueries ...string) (*sql.DB, *sql.Conn, error) {
	p := url.Values{}
	p.Add("home_directory", s.HomeDir)
	db, err := sql.Open("duckdb", "?"+p.Encode())
	if err != nil {
		return nil, nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	if err := s.init(ctx, conn, initQueries); err != nil {
		conn.Close()
		db.Close()
		return nil, nil, err
	}
	return db, conn, nil
}

type Settings struct {
	HomeDir string

	Threads        int
	MemoryLimit    string
	ExtensionDir   string
	SecretDir      string
	TempDir        string
	MaxTempDirSize string

	AllowedDirectories []string

	EnableExternalAccess bool
	LockConfig           bool
}

func (s Settings) init(ctx context.Context, conn *sql.Conn, initQueries []string) error {
	if err := s.apply(ctx, conn); err != nil {
		return err
	}
	for _, initQuery := range initQueries {
		if initQuery != "" {
			if _, err := conn.ExecContext(ctx, initQuery); err != nil {
				return err
			}
		}
	}
	// Finally, configure enable_external_access and lock_configuration.
	ex := &execContext{ctx: ctx, conn: conn}
	if !s.EnableExternalAccess {
		setNoCheck(ex, "enable_external_access", false)
	}
	if s.LockConfig {
		setNoCheck(ex, "lock_configuration", true)
	}
	return nil
}

// apply applies limits for the resources used by a DuckDB instance.
func (s Settings) apply(ctx context.Context, conn *sql.Conn) error {
	// NOTE: home_directory should be specified in DSN
	ex := &execContext{ctx: ctx, conn: conn}
	set(ex, "threads", s.Threads)
	set(ex, "memory_limit", s.MemoryLimit)
	set(ex, "extension_directory", s.ExtensionDir)
	set(ex, "secret_directory", s.SecretDir)
	set(ex, "temp_directory", s.TempDir)
	set(ex, "max_temp_directory_size", s.MaxTempDirSize)
	if len(s.AllowedDirectories) > 0 {
		setNoCheck(ex, "allowed_directories", s.AllowedDirectories)
	}
	return ex.err
}

type execContext struct {
	ctx  context.Context
	conn *sql.Conn
	err  error
}

func set[T comparable](ex *execContext, name string, v T) {
	if ex.err != nil {
		return
	}
	var zero T
	if v == zero {
		return
	}
	_, err := ex.conn.ExecContext(ex.ctx, "SET GLOBAL "+name+" = ?", v)
	ex.err = err
}

func setNoCheck(ex *execContext, name string, v any) {
	if ex.err != nil {
		return
	}
	_, err := ex.conn.ExecContext(ex.ctx, "SET GLOBAL "+name+" = ?", v)
	ex.err = err
}
