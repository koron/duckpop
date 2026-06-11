// Package {{.packageName}} provides {{.name}} formatter for Duckpop.
package {{.packageName}}

import (
	"database/sql"
	"io"

	"github.com/koron/duckpop/internal/formatter"
)

func init() {
	formatter.Register(&Factory{}, "{{.registerName}}")
}

type Factory struct {
}

var _ formatter.Factory = (*Factory)(nil)

func (f *Factory) ContentType() string {
	return "{{.contentType}}"
}

func (f *Factory) Create(w io.Writer, params map[string]string) (formatter.Writer, error) {
	return &Writer{
		// TODO:
	}, nil
}

type Writer struct {
	// TODO:
}

var _ formatter.Writer = (*Writer)(nil)

func (w *Writer) WriteHeader(columnTypes []*sql.ColumnType) error {
	// TODO:
	return nil
}

func (w *Writer) WriteBody(values []any) error {
	// TODO:
	return nil
}

func (w *Writer) Flush() error {
	// TODO:
	return nil
}
