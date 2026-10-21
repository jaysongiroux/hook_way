package eventdelivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Typed canonical trees preserve arbitrary JSON numbers without floating-point
// rounding. Objects are sorted; numerically equal decimal spellings share IDs.
func canonical(v any) ([]byte, error) {
	n, e := canonicalValue(v)
	if e != nil {
		return nil, e
	}
	return json.Marshal(n)
}
func canonicalValue(v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return []any{"null"}, nil
	case bool:
		return []any{"bool", x}, nil
	case string:
		return []any{"string", x}, nil
	case json.Number:
		s := strings.ToLower(string(x))
		negative := strings.HasPrefix(s, "-")
		s = strings.TrimPrefix(s, "-")
		var exp int64
		if i := strings.IndexByte(s, 'e'); i >= 0 {
			var e error
			exp, e = strconv.ParseInt(s[i+1:], 10, 64)
			if e != nil || exp > 1e9 || exp < -1e9 {
				return nil, errors.New("number exponent exceeds limit")
			}
			s = s[:i]
		}
		if i := strings.IndexByte(s, '.'); i >= 0 {
			exp -= int64(len(s) - i - 1)
			s = s[:i] + s[i+1:]
		}
		s = strings.TrimLeft(s, "0")
		if s == "" {
			return []any{"number", "0"}, nil
		}
		trim := strings.TrimRight(s, "0")
		exp += int64(len(s) - len(trim))
		if negative {
			trim = "-" + trim
		}
		return []any{"number", fmt.Sprintf("%se%d", trim, exp)}, nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []any{"object"}
		for _, k := range keys {
			v, e := canonicalValue(x[k])
			if e != nil {
				return nil, e
			}
			out = append(out, []any{k, v})
		}
		return out, nil
	case []any:
		out := []any{"array"}
		for _, item := range x {
			v, e := canonicalValue(item)
			if e != nil {
				return nil, e
			}
			out = append(out, v)
		}
		return out, nil
	default:
		b, e := json.Marshal(v)
		if e != nil {
			return nil, e
		}
		var decoded any
		d := json.NewDecoder(strings.NewReader(string(b)))
		d.UseNumber()
		if e = d.Decode(&decoded); e != nil {
			return nil, e
		}
		return canonicalValue(decoded)
	}
}
func lookup(data map[string]any, path string) (any, bool) {
	var v any = data
	for _, part := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return v, true
}
func matches(e event, args, data map[string]any) bool {
	for k, want := range args {
		value, ok := lookup(data, e.opts.Filters[k])
		if !ok {
			return false
		}
		a, er := canonical(want)
		b, other := canonical(value)
		if er != nil || other != nil || string(a) != string(b) {
			return false
		}
	}
	return true
}

func validArguments(e event, args map[string]any) bool {
	for key, value := range args {
		if _, ok := e.opts.Filters[key]; !ok {
			return false
		}
		switch value.(type) {
		case string, bool, json.Number:
		default:
			return false
		}
	}
	return e.input.Validate(args) == nil
}
