// Package formatter provides formatter framework for Duckpop.
package formatter

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/duckdb/duckdb-go/v2"
)

var (
	ErrUnsupportedFormat = errors.New("unsupported format")
)

type Factory interface {
	ContentType() string
	Create(w io.Writer, params map[string]string) (Writer, error)
}

type Writer interface {
	WriteHeader(columnTypes []*sql.ColumnType) error
	WriteBody(values []any) error
	Flush() error
}

var factories = map[string]Factory{}

func Register(factory Factory, names ...string) {
	for _, name := range names {
		name = strings.ToLower(name)
		if _, ok := factories[name]; ok {
			panic(fmt.Sprintf("formatter %q is duplicated", name))
		}
		factories[name] = factory
	}
}

func Find(name string) (Factory, bool) {
	f, ok := factories[strings.ToLower(name)]
	return f, ok
}

var (
	ErrWithoutFactory  = errors.New("made without a factory")
	ErrNoHeaderWritten = errors.New("no headers written")
	ErrCountMismatch   = errors.New("header and body count mismatch")
)

func AnyToStr(v any) string {
	return fmt.Sprint(v)
}

func DateToStr(v any) string {
	t := v.(time.Time)
	return t.Format("2006-01-02")
}

func IntervalToStr(v any) string {
	interval, ok := v.(duckdb.Interval)
	if !ok {
		return fmt.Sprint(v)
	}
	parts := make([]string, 0, 7)
	if interval.Months != 0 {
		y, m := interval.Months/12, interval.Months%12
		if y != 0 {
			parts = append(parts, fmt.Sprintf("%dy", y))
		}
		if m != 0 {
			parts = append(parts, fmt.Sprintf("%dmo", m))
		}
	}
	if interval.Days != 0 {
		parts = append(parts, fmt.Sprintf("%dd", interval.Days))
	}
	if interval.Micros != 0 {
		const (
			hour = 60 * 60 * 1000 * 1000
			min  = 60 * 1000 * 1000
			sec  = 1000 * 1000
			msec = 1000
		)
		us := interval.Micros
		h := us / hour
		if h != 0 {
			us -= h * hour
			parts = append(parts, fmt.Sprintf("%dh", h))
		}
		m := us / min
		if m != 0 {
			us -= m * min
			parts = append(parts, fmt.Sprintf("%dm", m))
		}
		s := us / sec
		if s != 0 {
			us -= s * sec
			parts = append(parts, fmt.Sprintf("%ds", s))
		}
		ms := us / msec
		if ms != 0 {
			us -= ms * msec
			parts = append(parts, fmt.Sprintf("%dms", ms))
		}
		if us != 0 {
			parts = append(parts, fmt.Sprintf("%dμs", us))
		}
	}
	if len(parts) == 0 {
		return "0"
	}
	return strings.Join(parts, " ")
}

func TimeToStr(v any) string {
	t := v.(time.Time)
	return t.Format("15:04:05")
}

func TimestampToStr(v any) string {
	t := v.(time.Time)
	return t.Format("2006-01-02 15:04:05")
}

func BlobToStr(v any) string {
	return string(v.([]uint8))
}

func Get(params map[string]string, name, defaultValue string) string {
	if s, ok := params[name]; ok {
		return s
	}
	return defaultValue
}

func FindAndCreate(s string, w io.Writer) (Factory, Writer, error) {
	parts := strings.Split(s, ",")
	format := parts[0]

	// Parse params
	params := map[string]string{}
	for _, s := range parts[1:] {
		p := strings.SplitN(s, ":", 2)
		if p[0] == "" {
			continue
		}
		if len(p) == 1 {
			params[p[0]] = ""
			continue
		}
		params[p[0]] = p[1]
	}

	factory, ok := Find(format)
	if !ok {
		return nil, nil, ErrUnsupportedFormat
	}
	writer, err := factory.Create(w, params)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid parameters: format=%s params=%+v", format, params)
	}

	return factory, writer, nil
}
