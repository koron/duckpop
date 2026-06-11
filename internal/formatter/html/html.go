// Package html provides HTML formatter for Duckpop.
package html

import (
	"io"

	"github.com/koron/duckpop/internal/formatter"
	"github.com/koron/duckpop/internal/formatter/table"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"
)

func init() {
	formatter.Register(&Factory{}, "html")
}

type Factory struct {
}

var _ formatter.Factory = (*Factory)(nil)

func (f *Factory) ContentType() string {
	return "text/html"
}

func (f *Factory) Create(w io.Writer, params map[string]string) (formatter.Writer, error) {
	var opts []tablewriter.Option
	opts = append(opts, tablewriter.WithRenderer(renderer.NewHTML()))
	// FIXME: Apply params
	tw := tablewriter.NewTable(w, opts...)
	return table.NewWriter(tw)
}
