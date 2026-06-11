// Package duckserver proivdes HTTP server of Duckpop.
package duckserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	"github.com/koron-go/ctxsrv"
	"github.com/koron-go/daemonic/hupfile"
	"github.com/koron-go/daemonic/pidfile"
	"github.com/koron/duckpop/internal/accesslog"
	"github.com/koron/duckpop/internal/authn"
	"github.com/koron/duckpop/internal/conndb"
	"github.com/koron/duckpop/internal/duckdbinit"
	"github.com/koron/duckpop/internal/fileserver"
	"github.com/koron/duckpop/internal/formatter"
	"github.com/koron/duckpop/internal/httperror"
	"github.com/koron/duckpop/internal/querydb"
)

const (
	AuthnIDHeader      = "Duckpop-Authnid"
	ConnectionIDHeader = "Duckpop-Connectionid"
	QueryIDHeader      = "Duckpop-Queryid"
	DurationHeader     = "Duckpop-Duration"

	defaultFormat = "csv"
)

var (
	ErrNoQuery = errors.New("no queries")
)

type Config struct {
	EnableDebugLog bool
	EnablePprof    bool

	Address     string
	MaxBodySize int64
	MaxDB       int

	PIDFile         string
	AccessLogFile   string
	AccessLogFormat string

	AuthnFile string
	NoAuthz   bool

	DBHomeDir        string
	DBThreads        int
	DBMemoryLimit    string
	DBMaxTempDirSize string
	DBExternalAccess bool
	DBLockConfig     bool
	DBInitQuery      string

	UIResourceFS fs.FS
}

func getwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func DefaultConfig() Config {
	return Config{
		Address:          "localhost:9281",
		MaxDB:            20,
		MaxBodySize:      1 << 20, // 1 MiB
		AccessLogFormat:  "text",
		DBHomeDir:        filepath.Join(getwd(), ".duckpop"),
		DBThreads:        1,
		DBMemoryLimit:    "1GiB",
		DBMaxTempDirSize: "10GiB",
		DBExternalAccess: true,
		DBLockConfig:     true,
	}
}

type Server struct {
	config *Config

	logger       *slog.Logger
	accessLogger *slog.Logger

	address         string
	pidFile         string
	accessLogFile   string
	accessLogFormat logFormat

	authenticator *authn.Authenticator
	withoutAuthz  bool

	dbSharedDir   string
	dbPrivateRoot string
	dbSettings    duckdbinit.Settings
	dbInitQuery   string

	connManager   *conndb.Manager
	queryDatabase querydb.Database

	uiFS fs.FS

	startedMu   sync.Mutex
	startedCond *sync.Cond

	URL string
}

func New(c Config) (*Server, error) {
	homedir, err := filepath.Abs(c.DBHomeDir)
	if err != nil {
		return nil, fmt.Errorf("failed to detemine DBHomeDir: %w", err)
	}

	srv := Server{
		config:        &c,
		address:       c.Address,
		pidFile:       c.PIDFile,
		accessLogFile: c.AccessLogFile,
		withoutAuthz:  c.NoAuthz,
		dbSharedDir:   filepath.Join(homedir, "shared"),
		dbPrivateRoot: filepath.Join(homedir, "private"),
		dbSettings: duckdbinit.Settings{
			HomeDir:              homedir,
			Threads:              c.DBThreads,
			MemoryLimit:          c.DBMemoryLimit,
			ExtensionDir:         filepath.Join(homedir, "extensions"),
			SecretDir:            filepath.Join(homedir, "stored_secrets"),
			TempDir:              filepath.Join(homedir, "tmp"),
			MaxTempDirSize:       c.DBMaxTempDirSize,
			EnableExternalAccess: c.DBExternalAccess,
			LockConfig:           c.DBLockConfig,
		},
		dbInitQuery: c.DBInitQuery,
		uiFS:        c.UIResourceFS,
	}

	srv.logger = slog.Default()
	if c.EnableDebugLog {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	lf, err := parseLogFormat(c.AccessLogFormat)
	if err != nil {
		return nil, err
	}
	srv.accessLogFormat = lf

	if c.AuthnFile != "" {
		a, err := authn.LoadFile(c.AuthnFile)
		if err != nil {
			return nil, err
		}
		srv.authenticator = a
	}

	// Setup DB connection manager
	srv.connManager = &conndb.Manager{
		MaxDB:  c.MaxDB,
		Opener: conndb.OpenerFunc(srv.connectDuckDB),
		Closer: conndb.CloserFunc(srv.closeDuckDB),
	}

	srv.startedCond = sync.NewCond(&srv.startedMu)

	return &srv, nil
}

type logFormat int

const (
	textLog logFormat = iota + 1
	jsonLog
)

func parseLogFormat(s string) (logFormat, error) {
	switch strings.ToLower(s) {
	case "text":
		return textLog, nil
	case "json":
		return jsonLog, nil
	default:
		return 0, fmt.Errorf("unsupported log format: %q", s)
	}
}

// Setup access logger
func (srv *Server) setupAccessLogger() (io.Closer, error) {
	// Special setting to discard access logs during testing
	if srv.accessLogFile == "test.discard" {
		return nil, nil
	}

	var (
		logw   io.Writer = os.Stdout
		closer io.Closer
	)
	if srv.accessLogFile != "" {
		w, err := hupfile.New(srv.accessLogFile)
		if err != nil {
			return nil, fmt.Errorf("failed to open access log file: %w", err)
		}
		logw = w
		closer = w
	}

	switch srv.accessLogFormat {
	case textLog:
		srv.accessLogger = slog.New(slog.NewTextHandler(logw, nil))
	case jsonLog:
		srv.accessLogger = slog.New(slog.NewJSONHandler(logw, nil))
	default:
		return nil, errors.New("invalid access log format")
	}
	return closer, nil
}

func (srv *Server) Serve(ctx context.Context) error {
	// Preparement: check database configuration.
	err := srv.checkDB(ctx)
	if err != nil {
		return err
	}

	// Set PID file
	if srv.pidFile != "" {
		err := pidfile.Write(srv.pidFile)
		if err != nil {
			return fmt.Errorf("failed to create PID file: %w", err)
		}
		defer pidfile.Close()
	}

	c, err := srv.setupAccessLogger()
	if err != nil {
		return err
	}
	if c != nil {
		defer c.Close()
	}

	srvctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	httpsrv := &http.Server{
		Addr:        srv.address,
		Handler:     srv.newDuckpopHandler(),
		ConnContext: srv.connManager.ConnContext,
		ConnState:   srv.connManager.ConnState,
		BaseContext: func(ln net.Listener) context.Context {
			srv.startedCond.L.Lock()
			addr := ln.Addr()
			srv.logger.Info("listening on", "addr", addr, "pprof", srv.config.EnablePprof)
			srv.URL = "http://" + addr.String()
			srv.startedCond.Broadcast()
			srv.startedCond.L.Unlock()
			return context.Background()
		},
	}

	// Start server
	return ctxsrv.HTTP(httpsrv).WithShutdownTimeout(time.Minute).ServeWithContext(srvctx)
}

func (srv *Server) WaitServe() {
	srv.startedCond.L.Lock()
	for srv.URL == "" {
		srv.startedCond.Wait()
	}
	srv.startedCond.L.Unlock()
}

func (srv *Server) SharedDir() string {
	return srv.dbSharedDir
}

func (srv *Server) PrivateRoot() string {
	return srv.dbPrivateRoot
}

func (srv *Server) checkDB(ctx context.Context) error {
	db, conn, err := srv.connectDuckDB(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer db.Close()
	return conn.PingContext(ctx)
}

func (srv *Server) connectDuckDB(ctx context.Context) (*sql.DB, *sql.Conn, error) {
	// Compose duckdbinit.Settings
	settings := srv.dbSettings
	if srv.dbSharedDir != "" {
		if err := os.MkdirAll(srv.dbSharedDir, 0750); err != nil {
			return nil, nil, err
		}
		settings.AllowedDirectories = append(settings.AllowedDirectories, srv.dbSharedDir)
	}
	privateDir, err := srv.getPrivateDir(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	if privateDir != "" {
		settings.AllowedDirectories = append(settings.AllowedDirectories, privateDir)
	}
	// Prepare initQueries
	initQueries := make([]string, 0, 4)
	if srv.dbSharedDir != "" {
		initQueries = append(initQueries, fmt.Sprintf("CREATE MACRO public_dir(name) AS concat('%s', '/', name)", srv.dbSharedDir))
	}
	if privateDir != "" {
		initQueries = append(initQueries, fmt.Sprintf("CREATE MACRO private_dir(name) AS concat('%s', '/', name)", privateDir))
	}
	if srv.dbInitQuery != "" {
		initQueries = append(initQueries, srv.dbInitQuery)
	}
	if entry, ok := authn.AuthnEntry(ctx); ok && entry.InitQuery != "" {
		initQueries = append(initQueries, entry.InitQuery)
	}
	// Open and connect to a database.
	db, conn, err := duckdbinit.Open(ctx, settings, initQueries...)
	if err != nil {
		return nil, nil, err
	}
	return db, conn, nil
}

func (srv *Server) closeDuckDB(ctx context.Context, db *sql.DB) error {
	privateDir, _ := srv.getPrivateDir(ctx, false)
	if privateDir != "" {
		if err := os.RemoveAll(privateDir); err != nil {
			srv.logger.Warn("failed to remove private directory", "dir", privateDir, "error", err)
		}
	}
	return db.Close()
}

func (srv *Server) getPrivateDir(ctx context.Context, makeDir bool) (string, error) {
	if srv.dbPrivateRoot == "" {
		return "", nil
	}
	connID, ok := conndb.GetID(ctx)
	if !ok {
		slog.Debug("connection ID cannot be determined")
		return "", nil
	}
	privateDir := filepath.Join(srv.dbPrivateRoot, connID.String())
	if makeDir {
		if err := os.MkdirAll(privateDir, 0700); err != nil {
			return "", err
		}
	}
	return privateDir, nil
}

func (srv *Server) newDuckpopHandler() http.Handler {
	// Define handlers
	mux := http.NewServeMux()
	mux.Handle("/{$}", errorAwareHandler(srv.handleQuery))
	mux.Handle("GET /ping/{$}", errorAwareHandler(srv.handlePing))
	mux.Handle("GET /config/", errorAwareHandler(srv.handleConfig))
	mux.Handle("GET /status/connections/{$}", errorAwareHandler(srv.handleStatusConnections))
	mux.Handle("GET /status/queries/{$}", errorAwareHandler(srv.handleStatusQueries))
	mux.Handle("DELETE /status/queries/{queryID}", errorAwareHandler(srv.handleInterruptQuery))
	if srv.dbSharedDir != "" {
		h := srv.authzChangeOperationHanlder(fileserver.New(srv.dbSharedDir))
		mux.Handle("/shared/", http.StripPrefix("/shared/", h))
	}
	if srv.uiFS != nil {
		mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServerFS(srv.uiFS)))
	}
	if srv.config.EnablePprof {
		mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	}

	// Install middlewares.
	var h http.Handler = mux
	if srv.accessLogger != nil {
		h = accesslog.WrapHandler(srv.accessLogger, h)
	}
	h = srv.authenticator.AuthenticateHandler(h)
	return h
}

func errorAwareHandler(handle func(http.ResponseWriter, *http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := handle(w, r)
		if err != nil {
			httperror.Write(w, err)
		}
	})
}

func (srv *Server) checkAuthz(w http.ResponseWriter, r *http.Request) error {
	if srv.authenticator == nil {
		return nil
	}
	if id, ok := authn.AuthnID(r.Context()); ok {
		// Insert AuthnID to response header.
		w.Header().Set(AuthnIDHeader, id.String())
		return nil
	}
	if srv.withoutAuthz {
		return nil
	}
	return httperror.New(401)
}

func (srv *Server) authzChangeOperationHanlder(handle http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET", "HEAD", "OPTIONS", "PROPFIND":
			// no authz
		default:
			// Under authz control
			err := srv.checkAuthz(w, r)
			if err != nil {
				httperror.Write(w, err)
				return
			}
		}
		handle.ServeHTTP(w, r)
	})
}

func (srv *Server) handlePing(w http.ResponseWriter, r *http.Request) error {
	w.WriteHeader(200)
	w.Write([]byte("OK\r\n"))
	return nil
}

func (srv *Server) handleConfig(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(srv.config)
}

func (srv *Server) shouldRedirectToUI(r *http.Request) bool {
	if srv.uiFS == nil || r.Method != "GET" {
		return false
	}
	_, err := readQparamQuery(r)
	return errors.Is(err, ErrNoQuery)
}

func (srv *Server) handleQuery(w http.ResponseWriter, r *http.Request) error {
	if srv.shouldRedirectToUI(r) {
		http.Redirect(w, r, "/ui/", http.StatusTemporaryRedirect)
		return nil
	}
	if err := srv.checkAuthz(w, r); err != nil {
		return err
	}
	if r.Method != "GET" && r.Method != "POST" {
		return httperror.New(404)
	}
	r.Body = http.MaxBytesReader(w, r.Body, srv.config.MaxBodySize)
	query, err := readQuery(r)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil // http.MaxBytesReader already wrote 413 response
		}
		return httperror.Newf(400, "No queries: %s", err)
	}

	// determine format from the request
	format := getFormat(r)
	factory, formatWriter, err := formatter.FindAndCreate(format, w)
	if err != nil {
		return httperror.Newf(400, "Unsupported format: %s", err)
	}

	// Determine a database connection which associated with the requenst.
	client, err := srv.connManager.Client(r.Context())
	if err != nil {
		return httperror.Newf(500, "No associated DB: %s", err)
	}
	w.Header().Set(ConnectionIDHeader, client.ID.String())
	conn, err := client.Conn()
	if err != nil {
		if errors.Is(err, conndb.ErrMaxDB) {
			return httperror.Newf(429, err.Error())
		}
		return httperror.Newf(500, "Failed to connect DB: %s", err)
	}

	// Register an executing query, and defer unregister it.
	q := srv.queryDatabase.Add(r.Context(), client.ID, query)
	w.Header().Set(QueryIDHeader, q.ID.String())
	defer q.Close()

	if r.Header.Get("Expect") == "100-continue" {
		w.WriteHeader(http.StatusContinue)
	}

	// Execute a query
	rows, err := conn.QueryContext(q.Context(), query)
	dur := time.Since(q.Start)
	if r, ok := w.(accesslog.QueryReporter); ok {
		r.QueryReport(query, dur)
	}
	w.Header().Set(DurationHeader, dur.String())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return httperror.Newf(504, err.Error())
		}
		if _, ok := err.(*duckdb.Error); !ok {
			return httperror.Newf(500, "DB error: %s", err)
		}
		return httperror.Newf(400, "Query error: %s", err)
	}
	defer rows.Close()

	// Write the response body
	w.Header().Set("Content-Type", factory.ContentType())
	w.WriteHeader(200)
	err = writeRows(q.Context(), formatWriter, rows)
	if err != nil {
		return httperror.Newf(500, "Serialization error: %s", err)
	}
	return nil
}

func readQuery(r *http.Request) (string, error) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	if len(b) > 0 {
		return string(b), nil
	}
	return readQparamQuery(r)
}

func readQparamQuery(r *http.Request) (string, error) {
	q := r.URL.Query()
	if s := q.Get("q"); s != "" {
		return s, nil
	}
	if s := q.Get("query"); s != "" {
		return s, nil
	}
	return "", ErrNoQuery
}

func getFormat(r *http.Request) string {
	q := r.URL.Query()
	format := q.Get("format")
	if format == "" {
		format = q.Get("f")
	}
	if format == "" {
		format = defaultFormat
	}
	return format
}

func writeRows(ctx context.Context, fw formatter.Writer, rows *sql.Rows) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Write the header
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	err = fw.WriteHeader(columnTypes)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Prepare for scan
	receivers := make([]any, len(columnTypes))
	values := make([]any, len(columnTypes))
	for i := range receivers {
		receivers[i] = new(any)
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := rows.Scan(receivers...)
		if err != nil {
			return err
		}
		for i, pv := range receivers {
			values[i] = *pv.(*any)
		}
		err = fw.WriteBody(values)
		if err != nil {
			return err
		}
	}
	return fw.Flush()
}

type ConnectionStatus struct {
	ID      string      `json:"ID"`
	DBStats sql.DBStats `json:"DBStats"`
}

func (srv *Server) handleStatusConnections(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "application/jsonlines")
	w.WriteHeader(200)
	enc := json.NewEncoder(w)
	for id, db := range srv.connManager.Databases() {
		s := ConnectionStatus{
			ID:      id.String(),
			DBStats: db.Stats(),
		}
		if err := enc.Encode(s); err != nil {
			return err
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return nil
}

func (srv *Server) handleStatusQueries(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "application/jsonlines")
	w.WriteHeader(200)
	now := time.Now()
	enc := json.NewEncoder(w)
	for _, q := range srv.queryDatabase.Queries() {
		if err := enc.Encode(q.Stats(now)); err != nil {
			return err
		}
		if _, err := w.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return nil
}

func (srv *Server) handleInterruptQuery(w http.ResponseWriter, r *http.Request) error {
	if err := srv.checkAuthz(w, r); err != nil {
		return err
	}
	id, err := querydb.ParseID(r.PathValue("queryID"))
	if err != nil {
		return httperror.Newf(400, "ID syntax error: %s", err)
	}
	q, ok := srv.queryDatabase.Query(id)
	if !ok {
		return httperror.New(404)
	}
	q.Close()
	w.WriteHeader(204)
	return nil
}
