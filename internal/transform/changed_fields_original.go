package transform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/jsoncanon"
)

// ChangedFieldsOriginal uses the verified original JSON for order-sensitive
// JavaScript array comparisons. Arrays are atomic paths, while objects recurse.
// This explicit mode does not compare a subsequently rewritten envelope body.
func ChangedFieldsOriginal(ctx context.Context, raw []byte, from, to string) ([]string, error) {
	if len(raw) > jsoncanon.MaxBytes {
		return nil, errors.New("changed-fields input limit")
	}
	a, err := selectRawJSON(raw, from)
	if err != nil {
		return nil, err
	}
	b, err := selectRawJSON(raw, to)
	if err != nil {
		return nil, err
	}
	out := []string{}
	if err = diffOriginal(ctx, a, b, "", &out, 0); err != nil {
		return nil, err
	}
	if promotesStatus(out) && !contains(out, "status") {
		out = append([]string{"status"}, out...)
	}
	return out, nil
}
func selectRawJSON(raw []byte, path string) (json.RawMessage, error) {
	cur := json.RawMessage(raw)
	for _, part := range strings.Split(path, ".") {
		var m map[string]json.RawMessage
		if json.Unmarshal(cur, &m) != nil {
			return nil, errors.New("original JSON path is not an object")
		}
		next, ok := m[part]
		if !ok {
			return nil, errors.New("original JSON path missing")
		}
		cur = next
	}
	return cur, nil
}
func diffOriginal(ctx context.Context, a, b json.RawMessage, path string, out *[]string, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > 128 {
		return errors.New("changed-fields depth limit")
	}
	a = bytes.TrimSpace(a)
	b = bytes.TrimSpace(b)
	if len(a) == 0 || len(b) == 0 {
		return errors.New("missing original JSON")
	}
	if a[0] == '[' && b[0] == '[' {
		x, err := jsoncanon.Stringify(ctx, a)
		if err != nil {
			return err
		}
		y, err := jsoncanon.Stringify(ctx, b)
		if err != nil {
			return err
		}
		if !bytes.Equal(x, y) {
			emit(path, out)
		}
		return nil
	}
	if a[0] == '{' && b[0] == '{' {
		var am, bm map[string]json.RawMessage
		if json.Unmarshal(a, &am) != nil || json.Unmarshal(b, &bm) != nil {
			return errors.New("invalid original JSON")
		}
		seen := map[string]bool{}
		for k := range am {
			seen[k] = true
		}
		for k := range bm {
			seen[k] = true
		}
		keys := make([]string, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			x, ax := am[k]
			y, by := bm[k]
			p := joinPath(path, k)
			if ax != by {
				emit(p, out)
				continue
			}
			if err := diffOriginal(ctx, x, y, p, out, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	x, err := decodeJSONCopy(a)
	if err != nil {
		return err
	}
	y, err := decodeJSONCopy(b)
	if err != nil {
		return err
	}
	if !equal(x, y) {
		emit(path, out)
	}
	return nil
}
