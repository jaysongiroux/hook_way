package jsoncanon

import (
	"context"
	"fmt"
	"strconv"

	"github.com/dop251/goja/ftoa"
)

// compactUnchanged copies validated JSON only after proving that JavaScript
// parse/stringify would change nothing except insignificant whitespace. It
// never handles escaped strings, duplicate keys, or array-index object keys;
// those retain the reference VM path. Numbers must already match the same
// binary64 formatter used by Goja. No objects or runtimes are shared by requests.
func compactUnchanged(ctx context.Context, raw []byte) ([]byte, bool, error) {
	p := unchangedParser{ctx: ctx, raw: raw, out: make([]byte, 0, len(raw))}
	ok, err := p.value(0)
	if !ok || err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return p.out, true, nil
}

type unchangedParser struct {
	ctx         context.Context
	raw, out    []byte
	pos, values int
}

func (p *unchangedParser) space() {
	for p.pos < len(p.raw) {
		switch p.raw[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}
func (p *unchangedParser) text() ([]byte, bool, error) {
	start := p.pos
	p.pos++
	for p.raw[p.pos] != '"' {
		if p.raw[p.pos] == '\\' {
			return nil, false, nil
		}
		if (p.pos-start)%4096 == 0 {
			if err := p.ctx.Err(); err != nil {
				return nil, false, err
			}
		}
		p.pos++
	}
	p.pos++
	p.out = append(p.out, p.raw[start:p.pos]...)
	return p.raw[start+1 : p.pos-1], true, nil
}
func (p *unchangedParser) value(depth int) (bool, error) {
	p.values++
	if depth > maxDepth || p.values > maxValues {
		return false, fmt.Errorf("json representation: depth or value limit")
	}
	if err := p.ctx.Err(); err != nil {
		return false, err
	}
	p.space()
	switch p.raw[p.pos] {
	case '"':
		_, ok, err := p.text()
		return ok, err
	case '{', '[':
		object := p.raw[p.pos] == '{'
		end := byte(']')
		if object {
			end = '}'
		}
		p.out = append(p.out, p.raw[p.pos])
		p.pos++
		p.space()
		if p.raw[p.pos] == end {
			p.out = append(p.out, end)
			p.pos++
			return true, nil
		}
		var keys map[string]struct{}
		if object {
			keys = make(map[string]struct{})
		}
		for {
			if object {
				keyBytes, ok, err := p.text()
				if !ok || err != nil {
					return false, err
				}
				key := string(keyBytes)
				if _, exists := keys[key]; exists {
					return false, nil
				}
				// ECMAScript enumerates array-index properties before other properties.
				// Reject all of them rather than trying to reorder or infer input order.
				if len(key) > 0 && key[0] >= '0' && key[0] <= '9' {
					if index, err := strconv.ParseUint(key, 10, 32); err == nil && index < 1<<32-1 && strconv.FormatUint(index, 10) == key {
						return false, nil
					}
				}
				keys[key] = struct{}{}
				p.space()
				p.pos++
				p.out = append(p.out, ':')
			}
			ok, err := p.value(depth + 1)
			if !ok || err != nil {
				return false, err
			}
			p.space()
			token := p.raw[p.pos]
			p.pos++
			p.out = append(p.out, token)
			if token == end {
				return true, nil
			}
			p.space()
		}
	default:
		start := p.pos
		for p.pos < len(p.raw) {
			switch p.raw[p.pos] {
			case ',', ']', '}', ' ', '\t', '\r', '\n':
				goto tokenEnd
			}
			p.pos++
		}
	tokenEnd:
		token := p.raw[start:p.pos]
		switch string(token) {
		case "true", "false", "null":
		default:
			// Validated integers with at most 15 digits fit exactly in binary64
			// and already have JavaScript's spelling, except negative zero.
			if exactSmallInteger(token) {
				break
			}
			number, err := strconv.ParseFloat(string(token), 64)
			canonical := err == nil && string(ftoa.FToStr(number, ftoa.ModeStandard, 0, nil)) == string(token)
			if !canonical {
				// Range overflow is valid JavaScript JSON input. It needs the
				// reference VM path, rather than becoming a parse failure.
				return false, nil
			}
		}
		p.out = append(p.out, token...)
		return true, nil
	}
}

func exactSmallInteger(token []byte) bool {
	digits := token
	if digits[0] == '-' {
		digits = digits[1:]
		if len(digits) == 1 && digits[0] == '0' {
			return false
		}
	}
	if len(digits) > 15 {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
