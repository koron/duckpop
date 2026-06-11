// Package table provides plain text table formatter for Duckpop.
package table

import (
	"database/sql"
	"io"

	"github.com/koron/duckpop/internal/formatter"
	"github.com/olekukonko/tablewriter"
)

func init() {
	formatter.Register(&Factory{}, "table")
}

type Factory struct {
}

var _ formatter.Factory = (*Factory)(nil)

func (f *Factory) ContentType() string {
	return "text/plain"
}

func (f *Factory) Create(w io.Writer, params map[string]string) (formatter.Writer, error) {
	var opts []tablewriter.Option
	// FIXME: Apply params
	tw := tablewriter.NewTable(w, opts...)
	return NewWriter(tw)
}

type Writer struct {
	tw *tablewriter.Table
}

func NewWriter(tw *tablewriter.Table) (*Writer, error) {
	return &Writer{tw: tw}, nil
}

var _ formatter.Writer = (*Writer)(nil)

func (w *Writer) WriteHeader(columnTypes []*sql.ColumnType) error {
	elements := make([]string, len(columnTypes))
	for i, typ := range columnTypes {
		elements[i] = typ.Name()
	}
	w.tw.Header(elements)
	return nil
}

func (w *Writer) WriteBody(values []any) error {
	return w.tw.Append(values)
}

func (w *Writer) Flush() error {
	return w.tw.Render()
}
