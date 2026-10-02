package capture

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

// zeroFields lists every exported field reachable from v that holds its zero
// value. A round-trip test only proves the fields its fixture sets, so a
// field left at zero — or one added to graph.Envelope, queue.Message, or
// webhook.Envelope later — would go unchecked.
func zeroFields(v reflect.Value, path string, out *[]string) {
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			*out = append(*out, path)
			return
		}
		zeroFields(v.Elem(), path, out)
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			if v.IsZero() {
				*out = append(*out, path)
			}
			return
		}
		for i := range v.NumField() {
			f := v.Type().Field(i)
			// Only this field is transient serialization metadata; its omission
			// is checked explicitly by TestSnapshotDropsRequestLocalRepresentation.
			if v.Type() == reflect.TypeOf(graph.Verified{}) && f.Name == "OriginalJSON" {
				continue
			}
			if f.IsExported() {
				zeroFields(v.Field(i), path+"."+f.Name, out)
			}
		}
	case reflect.Map:
		if v.Len() == 0 {
			*out = append(*out, path)
			return
		}
		iter := v.MapRange()
		for iter.Next() {
			zeroFields(iter.Value(), fmt.Sprintf("%s[%v]", path, iter.Key()), out)
		}
	case reflect.Slice:
		if v.Len() == 0 {
			*out = append(*out, path)
			return
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		for i := range v.Len() {
			zeroFields(v.Index(i), fmt.Sprintf("%s[%d]", path, i), out)
		}
	default:
		if v.IsZero() {
			*out = append(*out, path)
		}
	}
}

func TestRoundTripFixturesSetEveryField(t *testing.T) {
	for name, fixture := range map[string]any{"richEnvelope": richEnvelope(), "richMessage": richMessage()} {
		var zero []string
		zeroFields(reflect.ValueOf(fixture), name, &zero)
		if len(zero) > 0 {
			t.Errorf("%s leaves fields at their zero value, so its round trip does not check them: %v", name, zero)
		}
	}
}
