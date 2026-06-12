package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/koron/duckpop/duckserver"
)

func main() {
	if err := run(); err != nil {
		slog.Error("duckpop terminated", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// Compose the configurations
	config := duckserver.DefaultConfig()
	err := flag2config(&config)
	if err != nil {
		return err
	}
	// Start the server
	srv, err := duckserver.New(config)
	if err != nil {
		return err
	}
	return srv.Serve(context.Background())
}

func flag2config(c *duckserver.Config) error {
	var (
		err           error
		uiResourceDir string
	)

	flag.BoolVar(&c.EnableDebugLog, "debug", false, `enable debug log`)
	flag.BoolVar(&c.EnablePprof, "pprof", false, `enable pprof endpoint`)
	flag.StringVar(&c.Address, "addr", "localhost:9281", `address to host HTTP server`)
	flag.Int64Var(&c.MaxBodySize, "maxbodysize", 1<<20, `max request body size in bytes`)
	flag.IntVar(&c.MaxDB, "maxdb", 20, `maximum number of DB instances`)
	flag.StringVar(&c.PIDFile, "pidfile", "", `file to record the process ID`)
	flag.StringVar(&c.AccessLogFile, "accesslog.file", "", `access log file (default: stdout)`)
	flag.StringVar(&c.AccessLogFormat, "accesslog.format", "text", `access log format: "text" or "json"`)
	flag.StringVar(&c.AuthnFile, "authnfile", "", `authentication information file`)
	flag.BoolVar(&c.NoAuthz, "noauthz", false, `execute queries etc. without authz`)
	flag.StringVar(&c.DBHomeDir, "db.homedir", filepath.Join(getwd(), ".duckpop"), `home dir for duckdb`)
	flag.IntVar(&c.DBThreads, "db.threads", 1, `initial value of DB "threads"`)
	flag.StringVar(&c.DBMemoryLimit, "db.memorylimit", "1GiB", `initial value of DB "memory_limit"`)
	flag.StringVar(&c.DBMaxTempDirSize, "db.maxtempdirsize", "10GiB", `max size of temporary dir`)
	flag.BoolVar(&c.DBExternalAccess, "db.externalaccess", true, `enable external access (use -db.externalaccess=false to disable)`)
	flag.BoolVar(&c.DBLockConfig, "db.lockconfig", true, `lock DB settings. to unlock -db.lockconfig=false`)
	flag.StringVar(&c.DBInitQuery, "db.initquery", "", `DB initialization query or file (prefixed with '@')`)
	flag.StringVar(&uiResourceDir, "ui.resourcedir", "", `UI resource directory for development`)
	flag.Parse()

	if c.NoAuthz && c.AuthnFile == "" {
		return errors.New("-noauthz needs to be used with -authnfile")
	}

	// Try to read init query from a file.
	if strings.HasPrefix(c.DBInitQuery, "@") {
		b, err := os.ReadFile(c.DBInitQuery[1:])
		if err != nil {
			return fmt.Errorf("failed to read init query: %s", err)
		}
		c.DBInitQuery = string(b)
	}

	c.UIResourceFS, err = getUIFS(uiResourceDir)
	if err != nil {
		return err
	}

	return nil
}

func getwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

//go:embed _ui
var uiRawFS embed.FS

func getUIFS(override string) (fs.FS, error) {
	if override != "" {
		return os.DirFS(override), nil
	}
	return fs.Sub(uiRawFS, "_ui")
}
