package transform

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// ChangedFields recursively diffs prev against curr and returns dot-notation
// paths that changed. A nested change under "status." also promotes "status"
// itself into the result, matching the "changed fields array" convention
// downstream consumers expect.
func ChangedFields(prev, curr any) []string {
	var paths []string
	diff(prev, curr, "", &paths)
	if promotesStatus(paths) && !contains(paths, "status") {
		paths = append([]string{"status"}, paths...)
	}
	return paths
}

// ChangedFieldsJSON is the SNS String.Array wire form: a JSON array of paths.
func ChangedFieldsJSON(prev, curr any) (string, error) {
	b, err := json.Marshal(ChangedFields(prev, curr))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func diff(prev, curr any, prefix string, out *[]string) {
	if equal(prev, curr) {
		return
	}
	prevMap, prevIsMap := asMap(prev)
	currMap, currIsMap := asMap(curr)
	if prevIsMap && currIsMap {
		keys := make([]string, 0, len(prevMap)+len(currMap))
		seen := map[string]bool{}
		for k := range prevMap {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
		for k := range currMap {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			diff(prevMap[k], currMap[k], joinPath(prefix, k), out)
		}
		return
	}
	prevArr, prevIsArr := asSlice(prev)
	currArr, currIsArr := asSlice(curr)
	if prevIsArr && currIsArr {
		n := len(prevArr)
		if len(currArr) > n {
			n = len(currArr)
		}
		if n == 0 {
			emit(prefix, out)
			return
		}
		for i := 0; i < n; i++ {
			var a, b any
			if i < len(prevArr) {
				a = prevArr[i]
			}
			if i < len(currArr) {
				b = currArr[i]
			}
			diff(a, b, joinPath(prefix, strconv.Itoa(i)), out)
		}
		return
	}
	emit(prefix, out)
}

func emit(prefix string, out *[]string) {
	if prefix == "" {
		return
	}
	*out = append(*out, prefix)
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func promotesStatus(paths []string) bool {
	for _, p := range paths {
		if p == "status" || strings.HasPrefix(p, "status.") {
			return true
		}
	}
	return false
}

func contains(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

func asMap(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
		out := map[string]any{}
		iter := rv.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = iter.Value().Interface()
		}
		return out, true
	}
	return nil, false
}

func asSlice(v any) ([]any, bool) {
	if v == nil {
		return nil, false
	}
	if s, ok := v.([]any); ok {
		return s, true
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

func equal(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.DeepEqual(normalize(a), normalize(b))
}

func normalize(v any) any {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return n.String()
		}
		return f
	default:
		return v
	}
}

// GetPath reads a dotted path from a JSON-like value. Missing paths return (nil, false).
func GetPath(root any, path string) (any, bool) {
	if path == "" {
		return root, true
	}
	cur := root
	for _, part := range strings.Split(path, ".") {
		m, ok := asMap(cur)
		if !ok {
			s, isArr := asSlice(cur)
			if !isArr {
				return nil, false
			}
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(s) {
				return nil, false
			}
			cur = s[i]
			continue
		}
		next, ok := m[part]
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// SetPath writes value at a dotted path, creating maps as needed.
func SetPath(root any, path string, value any) (any, error) {
	if path == "" {
		return value, nil
	}
	parts := strings.Split(path, ".")
	if root == nil {
		root = map[string]any{}
	}
	m, ok := asMap(root)
	if !ok {
		return nil, fmt.Errorf("transform: cannot set %q on %T", path, root)
	}
	out := cloneMap(m)
	cur := out
	for i, part := range parts {
		if i == len(parts)-1 {
			cur[part] = value
			return out, nil
		}
		next, exists := cur[part]
		nm, isMap := asMap(next)
		if !exists || !isMap {
			created := map[string]any{}
			cur[part] = created
			cur = created
			continue
		}
		cloned := cloneMap(nm)
		cur[part] = cloned
		cur = cloned
	}
	return out, nil
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
