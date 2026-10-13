// Package jsoncanon reproduces JavaScript JSON.parse/stringify semantics for
// bounded original JSON inputs. It does not sort keys or verify signatures.
package jsoncanon

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dop251/goja"
)

const MaxBytes = 20 << 20
const maxDepth = 256
const maxValues = 1 << 20

var program = goja.MustCompile("json representation", "JSON.stringify(parsedJSON)", false)

func Stringify(ctx context.Context, raw []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) > MaxBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, fmt.Errorf("json representation: invalid JSON or input limit")
	}
	budget, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if out, unchanged, err := compactUnchanged(budget, raw); unchanged || err != nil {
		return out, err
	}
	return stringifyVM(budget, raw)
}

// stringifyVM is the reference path for inputs needing JavaScript normalization.
func stringifyVM(budget context.Context, raw []byte) ([]byte, error) {
	vm := goja.New()
	vm.SetMaxCallStackSize(512)
	parser := parser{raw: raw, ctx: budget, vm: vm}
	value, err := parser.value(0)
	if err != nil {
		return nil, err
	}
	if err = vm.Set("parsedJSON", value); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-budget.Done():
			vm.Interrupt("JSON representation deadline")
		case <-done:
		}
	}()
	defer func() { close(done); wg.Wait(); vm.ClearInterrupt() }()
	result, err := vm.RunProgram(program)
	if err != nil {
		if cause := budget.Err(); cause != nil {
			return nil, fmt.Errorf("json representation: execution limit: %w", cause)
		}
		return nil, fmt.Errorf("json representation: execution limit")
	}
	if err = budget.Err(); err != nil {
		return nil, err
	}
	out := []byte(result.String())
	if len(out) > MaxBytes {
		return nil, fmt.Errorf("json representation: output limit")
	}
	return out, nil
}

// The runtime's JSON.parse uses encoding/json, which loses lone UTF-16
// surrogates and rejects overflowing numbers. Parse validated JSON directly
// into JS values to preserve those values, property insertion order, numeric
// rounding and duplicate-key behavior. Objects have no prototype setters.
type parser struct {
	raw         []byte
	pos, values int
	ctx         context.Context
	vm          *goja.Runtime
	setter      goja.Callable
}

func (p *parser) space() {
	for p.pos < len(p.raw) {
		switch p.raw[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}
func (p *parser) value(depth int) (goja.Value, error) {
	p.values++
	if depth > maxDepth || p.values > maxValues {
		return nil, fmt.Errorf("json representation: depth or value limit")
	}
	if err := p.ctx.Err(); err != nil {
		return nil, err
	}
	p.space()
	switch p.raw[p.pos] {
	case '"':
		return p.text()
	case '{':
		p.pos++
		obj := p.vm.NewObject()
		if err := obj.SetPrototype(nil); err != nil {
			return nil, err
		}
		p.space()
		if p.raw[p.pos] == '}' {
			p.pos++
			return obj, nil
		}
		for {
			// Literal UTF-8 keys can use the native setter. Escaped keys retain
			// Reflect.set so lone UTF-16 surrogates never pass through Go strings.
			name, literal, err := p.literalText()
			if err != nil {
				return nil, err
			}
			var key goja.Value
			if !literal {
				key, err = p.text()
				if err != nil {
					return nil, err
				}
			}
			p.space()
			p.pos++ // colon
			value, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			if literal {
				err = obj.Set(name, value)
			} else {
				if p.setter == nil {
					p.setter, _ = goja.AssertFunction(p.vm.Get("Reflect").ToObject(p.vm).Get("set"))
				}
				_, err = p.setter(goja.Undefined(), obj, key, value)
			}
			if err != nil {
				return nil, err
			}
			p.space()
			end := p.raw[p.pos]
			p.pos++
			if end == '}' {
				return obj, nil
			}
			p.space()
		}
	case '[':
		p.pos++
		p.space()
		var values []interface{}
		if p.raw[p.pos] == ']' {
			p.pos++
			return p.vm.NewArray(), nil
		}
		for {
			value, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
			p.space()
			end := p.raw[p.pos]
			p.pos++
			if end == ']' {
				return p.vm.NewArray(values...), nil
			}
		}
	case 't':
		p.pos += 4
		return p.vm.ToValue(true), nil
	case 'f':
		p.pos += 5
		return p.vm.ToValue(false), nil
	case 'n':
		p.pos += 4
		return goja.Null(), nil
	default:
		start := p.pos
		for p.pos < len(p.raw) {
			c := p.raw[p.pos]
			if c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E' || c >= '0' && c <= '9' {
				p.pos++
			} else {
				break
			}
		}
		number, _ := strconv.ParseFloat(string(p.raw[start:p.pos]), 64) // ErrRange intentionally yields infinity.
		return p.vm.ToValue(number), nil
	}
}

// The complete input has already passed UTF-8/JSON validation. Literal strings
// therefore convert directly without a temporary, growing UTF-16 buffer.
// A backslash leaves the cursor untouched for the surrogate-preserving parser.
func (p *parser) literalText() (string, bool, error) {
	start := p.pos + 1
	for end := start; end < len(p.raw); end++ {
		if (end-start)&1023 == 0 {
			if err := p.ctx.Err(); err != nil {
				return "", false, err
			}
		}
		switch p.raw[end] {
		case '\\':
			return "", false, nil
		case '"':
			p.pos = end + 1
			return string(p.raw[start:end]), true, nil
		}
	}
	return "", false, fmt.Errorf("json representation: invalid string")
}

func (p *parser) text() (goja.Value, error) {
	if text, literal, err := p.literalText(); err != nil {
		return nil, err
	} else if literal {
		return p.vm.ToValue(text), nil
	}
	p.pos++
	units := make([]uint16, 0, 32)
	for p.raw[p.pos] != '"' {
		if p.pos&1023 == 0 {
			if err := p.ctx.Err(); err != nil {
				return nil, err
			}
		}
		c := p.raw[p.pos]
		p.pos++
		if c == '\\' {
			c = p.raw[p.pos]
			p.pos++
			switch c {
			case 'u':
				n, _ := strconv.ParseUint(string(p.raw[p.pos:p.pos+4]), 16, 16)
				units = append(units, uint16(n))
				p.pos += 4
			case 'b':
				units = append(units, '\b')
			case 'f':
				units = append(units, '\f')
			case 'n':
				units = append(units, '\n')
			case 'r':
				units = append(units, '\r')
			case 't':
				units = append(units, '\t')
			default:
				units = append(units, uint16(c))
			}
		} else if c < 128 {
			units = append(units, uint16(c))
		} else {
			p.pos--
			r, n := utf8.DecodeRune(p.raw[p.pos:])
			p.pos += n
			if r > 0xffff {
				a, b := utf16.EncodeRune(r)
				units = append(units, uint16(a), uint16(b)) // #nosec G115 -- Valid Unicode scalar or UTF-16 surrogate units fit uint16.
			} else {
				units = append(units, uint16(r)) // #nosec G115 -- Valid Unicode scalar or UTF-16 surrogate units fit uint16.
			}
		}
	}
	p.pos++
	return goja.StringFromUTF16(units), nil
}
