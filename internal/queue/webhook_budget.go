package queue

import (
	"reflect"
	"time"
)

// webhookMessageCharge is no-serialization preflight accounting, not a heap
// measurement. It charges all retained Message forms (including json:"-"
// raw/object/frozen publish forms), even aliases, plus wire-encoding headroom:
// strings may expand sixfold in JSON; byte slices may be base64 encoded. The
// charge remains held through retries, DLQ and terminal capture handoff.
// Object/allocator/transport overhead and separately owned capture buffers are
// not an absolute process-memory guarantee; use container limits as that bound.
func webhookMessageCharge(msg Message, limit int64) (int64, bool) {
	remaining := limit
	nodes := 0
	var visit func(reflect.Value, int) bool
	spend := func(n int64) bool {
		if n > remaining {
			return false
		}
		remaining -= n
		return true
	}
	visit = func(v reflect.Value, depth int) bool {
		nodes++
		// Reflection also visits interfaces, map keys and metadata: leave room
		// for the parser's supported 1024 JSON levels / one million JSON nodes.
		if depth > 4096 || nodes > 8_000_000 || !spend(64) {
			return false
		}
		if !v.IsValid() {
			return true
		}
		if v.Type() == reflect.TypeOf(time.Time{}) {
			return spend(64)
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			return v.IsNil() || visit(v.Elem(), depth+1)
		case reflect.String:
			return int64(v.Len()) <= remaining/7 && spend(7*int64(v.Len()))
		case reflect.Slice:
			if v.Type().Elem().Kind() == reflect.Uint8 {
				return int64(v.Len()) <= remaining/3 && spend(3*int64(v.Len()))
			}
			fallthrough
		case reflect.Array:
			for i := 0; i < v.Len(); i++ {
				if !visit(v.Index(i), depth+1) {
					return false
				}
			}
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				if !visit(iter.Key(), depth+1) || !visit(iter.Value(), depth+1) {
					return false
				}
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() && !visit(v.Field(i), depth+1) {
					return false
				}
			}
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
		default:
			return false
		}
		return true
	}
	if !spend(1024) || !visit(reflect.ValueOf(msg), 0) {
		return 0, false
	}
	return limit - remaining, true
}
