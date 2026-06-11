// Package csv provides CSV formatter for Duckpop.
package csv

import (
	"database/sql"
	"encoding/csv"
	"io"

	"github.com/koron/duckpop/internal/formatter"
)

const (
	nullStrDefault = "NULL"
)

func init() {
	formatter.Register(&Factory{}, "csv")
}

type Factory struct {
}

var _ formatter.Factory = (*Factory)(nil)

func (f *Factory) ContentType() string {
	return "text/csv"
}

func (f *Factory) Create(w io.Writer, params map[string]string) (formatter.Writer, error) {
	ww := csv.NewWriter(w)
	// Apply params
	nullStr, ok := params["null"]
	if !ok {
		nullStr = nullStrDefault
	}
	return &Writer{
		w:       ww,
		nullStr: nullStr,
	}, nil
}

type Writer struct {
	w       *csv.Writer
	nullStr string

	records    []string
	converters []func(any) string
}

var _ formatter.Writer = (*Writer)(nil)

func (w *Writer) WriteHeader(columnTypes []*sql.ColumnType) error {
	w.records = make([]string, len(columnTypes))
	w.converters = make([]func(any) string, len(columnTypes))
	for i, typ := range columnTypes {
		w.records[i] = typ.Name()
		switch typ.DatabaseTypeName() {
		case "DATE":
			w.converters[i] = formatter.DateToStr
		case "INTERVAL":
			w.converters[i] = formatter.IntervalToStr
		case "TIME":
			w.converters[i] = formatter.TimeToStr
		case "TIMESTAMP":
			w.converters[i] = formatter.TimestampToStr
		case "BLOB":
			w.converters[i] = formatter.BlobToStr
		default:
			w.converters[i] = formatter.AnyToStr
		}
	}
	return w.w.Write(w.records)
}

func (w *Writer) WriteBody(values []any) error {
	if w.records == nil {
		return formatter.ErrNoHeaderWritten
	}
	if len(w.records) != len(values) {
		return formatter.ErrCountMismatch
	}
	for i, v := range values {
		if v == nil {
			w.records[i] = w.nullStr
			continue
		}
		w.records[i] = w.converters[i](v)
	}
	return w.w.Write(w.records)
}

func (w *Writer) Flush() error {
	w.w.Flush()
	return w.w.Error()
}
