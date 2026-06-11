// Package avro provides Apache Avro formatter for Duckpop.
package avro

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"

	"github.com/hamba/avro/v2"
	"github.com/hamba/avro/v2/ocf"
	"github.com/koron/duckpop/internal/formatter"
)

const (
	defaultSchemaNS   = "net.kaoriya.duckpop"
	defaultSchemaName = "Table"
)

func init() {
	formatter.Register(&Factory{}, "avro")
}

type Factory struct {
}

var _ formatter.Factory = (*Factory)(nil)

func (f *Factory) ContentType() string {
	return "application/avro"
}

func (f *Factory) Create(w io.Writer, params map[string]string) (formatter.Writer, error) {
	// parse params
	schemaNS := formatter.Get(params, "ns", defaultSchemaNS)
	schemaName := formatter.Get(params, "name", defaultSchemaName)
	return &Writer{
		baseW:      w,
		schemaNS:   schemaNS,
		schemaName: schemaName,
	}, nil
}

type Writer struct {
	baseW io.Writer

	schemaNS   string
	schemaName string

	names   []string
	convs   []convertFunc
	encoder *ocf.Encoder
}

var _ formatter.Writer = (*Writer)(nil)

type convertFunc func(any) (any, error)

func nonConvert(v any) (any, error) {
	return v, nil
}

func uint64ToDecimalBytes(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	n := v.(uint64)
	buf := make([]byte, 9)                 // 9 bytes for precision 20
	binary.BigEndian.PutUint64(buf[1:], n) // leading 0 for sign
	return buf, nil
}

func hugeIntToDecimalBytes(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	n := v.(*big.Int)
	const numBytes = 17 // ceil(39 * log2(10) / 8)
	buf := make([]byte, numBytes)
	if n.Sign() < 0 {
		twos := new(big.Int).Add(n, new(big.Int).Lsh(big.NewInt(1), uint(numBytes*8)))
		twos.FillBytes(buf)
	} else {
		n.FillBytes(buf)
	}
	return buf, nil
}

func (w *Writer) type2field(typ *sql.ColumnType) (*avro.Field, convertFunc, error) {
	var avroType avro.Schema
	var convFn convertFunc = nonConvert
	dbType := typ.DatabaseTypeName()
	switch dbType {
	case "BOOLEAN":
		avroType = avro.NewPrimitiveSchema(avro.Boolean, nil)
	case "TINYINT":
		avroType = avro.NewPrimitiveSchema(avro.Int, nil)
	case "SMALLINT":
		avroType = avro.NewPrimitiveSchema(avro.Int, nil)
	case "INTEGER":
		avroType = avro.NewPrimitiveSchema(avro.Int, nil)
	case "BIGINT":
		avroType = avro.NewPrimitiveSchema(avro.Long, nil)
	case "UTINYINT":
		avroType = avro.NewPrimitiveSchema(avro.Int, nil)
	case "USMALLINT":
		avroType = avro.NewPrimitiveSchema(avro.Int, nil)
	case "UINTEGER":
		avroType = avro.NewPrimitiveSchema(avro.Long, nil)
	case "UBIGINT":
		avroType = avro.NewPrimitiveSchema(avro.Fixed, avro.NewDecimalLogicalSchema(20, 0))
		convFn = uint64ToDecimalBytes

	case "FLOAT":
		avroType = avro.NewPrimitiveSchema(avro.Float, nil)
	case "DOUBLE":
		avroType = avro.NewPrimitiveSchema(avro.Double, nil)

	case "HUGEINT":
		avroType = avro.NewPrimitiveSchema(avro.Fixed, avro.NewDecimalLogicalSchema(39, 0))
		convFn = hugeIntToDecimalBytes
	case "UHUGEINT":
		avroType = avro.NewPrimitiveSchema(avro.Fixed, avro.NewDecimalLogicalSchema(39, 0))
		convFn = hugeIntToDecimalBytes

	case "DECIMAL":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.Bytes, avro.NewPrimitiveLogicalSchema(avro.Decimal))

	case "VARCHAR":
		avroType = avro.NewPrimitiveSchema(avro.String, nil)
	case "BLOB":
		avroType = avro.NewPrimitiveSchema(avro.Bytes, nil)

	case "BIGNUM":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.String, nil)
	case "UUID":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.String, avro.NewPrimitiveLogicalSchema(avro.UUID))

	case "DATE":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.Int, avro.NewPrimitiveLogicalSchema(avro.Date))
	case "TIME":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.Int, avro.NewPrimitiveLogicalSchema(avro.TimeMillis))
	case "TIMETZ":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.Int, avro.NewPrimitiveLogicalSchema(avro.TimeMillis))
	case "TIMESTAMP":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.Long, avro.NewPrimitiveLogicalSchema(avro.LocalTimestampMicros))
	case "TIMESTAMPTZ":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.Long, avro.NewPrimitiveLogicalSchema(avro.TimestampMicros))
	case "TIMESTAMP_S":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.Long, avro.NewPrimitiveLogicalSchema(avro.LocalTimestampMillis))
	case "TIMESTAMP_MS":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.Long, avro.NewPrimitiveLogicalSchema(avro.LocalTimestampMillis))
	case "TIMESTAMP_NS":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.Long, avro.NewPrimitiveLogicalSchema(avro.LocalTimestampMicros))
	case "INTERVAL":
		// FIXME:
		avroType = avro.NewPrimitiveSchema(avro.Long, avro.NewPrimitiveLogicalSchema(avro.Duration))

	case "ENUM":
		// TODO:
		return nil, nil, fmt.Errorf("unsupported database type: %s", dbType)
	case "LIST":
		// TODO:
		return nil, nil, fmt.Errorf("unsupported database type: %s", dbType)
	case "STRUCT":
		// TODO:
		return nil, nil, fmt.Errorf("unsupported database type: %s", dbType)
	case "MAP":
		// TODO:
		return nil, nil, fmt.Errorf("unsupported database type: %s", dbType)
	case "ARRAY":
		// TODO:
		return nil, nil, fmt.Errorf("unsupported database type: %s", dbType)
	case "UNION":
		// TODO:
		return nil, nil, fmt.Errorf("unsupported database type: %s", dbType)
	case "BIT":
		// TODO:
		return nil, nil, fmt.Errorf("unsupported database type: %s", dbType)
	case "ANY":
		// TODO:
		return nil, nil, fmt.Errorf("unsupported database type: %s", dbType)
	case "SQLNULL":
		// TODO:
		return nil, nil, fmt.Errorf("unsupported database type: %s", dbType)

	default:
		return nil, nil, fmt.Errorf("unknown database type: %s", dbType)
	}
	avroType, err := avro.NewUnionSchema([]avro.Schema{avro.NewNullSchema(), avroType})
	if err != nil {
		return nil, nil, err
	}
	f, err := avro.NewField(typ.Name(), avroType)
	if err != nil {
		return nil, nil, err
	}
	return f, convFn, nil
}

func (w *Writer) types2schema(types []*sql.ColumnType) (*avro.RecordSchema, error) {
	w.names = make([]string, len(types))
	w.convs = make([]convertFunc, len(types))
	// convert types to fields
	fields := make([]*avro.Field, len(types))
	for i, typ := range types {
		f, convFn, err := w.type2field(typ)
		if err != nil {
			return nil, err
		}
		fields[i] = f
		w.names[i] = f.Name()
		w.convs[i] = convFn
	}
	// nothing to do for SchemaOption for now.
	var opts []avro.SchemaOption
	return avro.NewRecordSchema(w.schemaName, w.schemaNS, fields, opts...)
}

func (w *Writer) WriteHeader(columnTypes []*sql.ColumnType) error {
	if w.baseW == nil {
		return formatter.ErrWithoutFactory
	}
	recordSchema, err := w.types2schema(columnTypes)
	if err != nil {
		return err
	}
	encoder, err := ocf.NewEncoderWithSchema(recordSchema, w.baseW)
	if err != nil {
		return err
	}
	w.encoder = encoder
	return nil
}

func (w *Writer) WriteBody(values []any) error {
	if w.encoder == nil || w.names == nil {
		return formatter.ErrNoHeaderWritten
	}
	if len(values) != len(w.names) {
		return formatter.ErrCountMismatch
	}
	// Reordering an array into a map
	record := map[string]any{}
	for i, name := range w.names {
		v, err := w.convs[i](values[i])
		if err != nil {
			return err
		}
		record[name] = v
	}
	return w.encoder.Encode(record)
}

func (w *Writer) Flush() error {
	// nothing to do.
	return w.encoder.Close()
}
