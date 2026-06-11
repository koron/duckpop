// Package accesslog provides access log for duckpop
package accesslog

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/koron/duckpop/internal/authn"
	"github.com/koron/duckpop/internal/conndb"
)

type QueryReporter interface {
	QueryReport(query string, duration time.Duration)
}

type wrapWriter struct {
	base   http.ResponseWriter
	status int
	bsize  int

	queryReport *queryReport
}

type queryReport struct {
	query    string
	duration time.Duration
}

func (w *wrapWriter) Header() http.Header {
	return w.base.Header()
}

func (w *wrapWriter) Write(data []byte) (int, error) {
	n, err := w.base.Write(data)
	w.bsize += n
	return n, err
}

func (w *wrapWriter) WriteHeader(statusCode int) {
	w.base.WriteHeader(statusCode)
	w.status = statusCode
}

func (w *wrapWriter) QueryReport(query string, duration time.Duration) {
	w.queryReport = &queryReport{
		query:    query,
		duration: duration,
	}
}

func writeLog(logger *slog.Logger, ww *wrapWriter, r *http.Request) {
	attrs := make([]slog.Attr, 0, 12)

	// Basic information: remote, authn
	attrs = append(attrs, slog.String("remote_addr", r.RemoteAddr))
	authnID, ok := authn.AuthnID(r.Context())
	if ok {
		attrs = append(attrs, slog.String("authn_id", authnID.String()))
	}

	// Request information: method, path, protocol version, referer,
	// user-agent, status, response size
	attrs = append(attrs,
		slog.String("method", r.Method),
		slog.String("path", r.URL.RequestURI()),
		slog.String("proto", r.Proto),
	)
	if referer := r.Referer(); referer != "" {
		attrs = append(attrs, slog.String("referer", referer))

	}
	if userAgent := r.UserAgent(); userAgent != "" {
		attrs = append(attrs, slog.String("user_agent", userAgent))
	}
	attrs = append(attrs,
		slog.Int("status", ww.status),
		slog.Int("size", ww.bsize),
	)

	// Connection ID
	if cid, ok := conndb.GetID(r.Context()); ok {
		attrs = append(attrs, slog.String("conn_id", cid.String()))
	}

	// Query information
	if ww.queryReport != nil {
		attrs = append(attrs,
			slog.String("query", ww.queryReport.query),
			slog.Duration("duration", ww.queryReport.duration),
		)
	}

	logger.LogAttrs(context.Background(), slog.LevelInfo, "access", attrs...)
}

func WrapHandler(logger *slog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := &wrapWriter{base: w}
		h.ServeHTTP(ww, r)
		writeLog(logger, ww, r)
	})
}
