package transform

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/jsonbudget"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// ScalarCoercion is explicit: union/anyOf identifiers are not silently changed.
type ScalarCoercion struct {
	Path string `yaml:"path" json:"path" jsonschema:"required"`
	Type string `yaml:"type" json:"type" jsonschema:"required,enum=string,enum=number,enum=integer,enum=boolean,enum=null"`
}
type Validator struct {
	schema    *jsonschema.Schema
	coercions []ScalarCoercion
	work      int
	unique    bool
}
type offlineSchemaLoader struct{}

func (offlineSchemaLoader) Load(string) (any, error) {
	return nil, errors.New("external schema resources are disabled")
}
func NewValidator(schema map[string]any, coercions []ScalarCoercion) (*Validator, error) {
	raw, err := json.Marshal(schema)
	if err != nil || len(raw) > 256<<10 || len(schema) == 0 {
		return nil, errors.New("JSON schema must be nonempty and at most 256 KiB")
	}
	if err := jsonbudget.Check(raw, jsonbudget.Limits{MaxDepth: 64, MaxNodes: 10000}); err != nil {
		return nil, err
	}
	doc, err := DecodeJSON(raw)
	if err != nil {
		return nil, err
	}
	count := 0
	if err = boundJSON(doc, 0, &count, 10000, 64); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft7)
	c.UseLoader(offlineSchemaLoader{})
	c.AssertFormat()
	const location = "https://hookway.invalid/schema.json"
	if err = c.AddResource(location, doc); err != nil {
		return nil, errors.New("invalid JSON schema resource")
	}
	compiled, err := c.Compile(location)
	if err != nil {
		return nil, errors.New("invalid JSON schema: unsupported draft, reference, or schema constraint")
	}
	anchors, err := compileDeclaredSchemaAnchors(c, location, doc)
	if err != nil {
		return nil, errors.New("invalid JSON schema: unsupported anchor")
	}
	work, unique, err := schemaWork(compiled, anchors)
	if err != nil {
		return nil, err
	}
	if len(coercions) > 64 {
		return nil, errors.New("at most 64 explicit scalar coercions are supported")
	}
	for _, rule := range coercions {
		if !JSONFieldPath(rule.Path) {
			return nil, errors.New("invalid coercion path")
		}
		switch rule.Type {
		case "string", "number", "integer", "boolean", "null":
		default:
			return nil, errors.New("invalid coercion type")
		}
		// Only a literal, single type at a properties path can authorize coercion.
		current := doc
		for _, part := range strings.Split(rule.Path, ".") {
			m, ok := current.(map[string]any)
			if !ok {
				return nil, errors.New("coercion requires literal properties")
			}
			properties, _ := m["properties"].(map[string]any)
			current = properties[part]
		}
		node, ok := current.(map[string]any)
		if !ok || node["type"] != rule.Type {
			return nil, errors.New("coercion type must match a single declared property type")
		}
	}
	return &Validator{schema: compiled, coercions: append([]ScalarCoercion(nil), coercions...), work: work, unique: unique}, nil
}
func (v *Validator) Validate(body any) (any, error) {
	count := 0
	if err := boundJSON(body, 0, &count, 1000000, 128); err != nil {
		return nil, err
	}
	width := 1
	if v.unique {
		width = maxArrayWidth(body)
	}
	if v.work > maxValidationWork/max(1, count)/width ||
		v.work > maxValidationTextWork/validationTextWork(body)/width {
		return nil, ErrJSONValidationWork
	}
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > 20<<20 {
		return nil, errors.New("JSON validation body limit")
	}
	out, err := DecodeJSON(raw)
	if err != nil {
		return nil, err
	}
	for _, rule := range v.coercions {
		value, found := GetPath(out, rule.Path)
		if !found {
			continue
		}
		next, ok := coerceScalar(value, rule.Type)
		if !ok {
			continue
		}
		out, err = SetPath(out, rule.Path, next)
		if err != nil {
			return nil, err
		}
	}
	if err = v.schema.Validate(out); err != nil {
		return nil, validationError(err)
	}
	if len(v.coercions) == 0 {
		return body, nil
	}
	return out, nil
}

// decodeJSONCopy is also used by the original-JSON changed-fields adapter.
func decodeJSONCopy(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	err := d.Decode(&value)
	return value, err
}

// DecodeJSON decodes exactly one bounded JSON value without rounding numbers.
func DecodeJSON(raw []byte) (any, error) {
	if len(raw) > 20<<20 {
		return nil, errors.New("JSON validation body limit")
	}
	if err := jsonbudget.Check(raw, jsonbudget.Limits{}); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, errors.New("invalid JSON body")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("invalid JSON body: expected one value")
	}
	count := 0
	if err := boundJSON(value, 0, &count, 1000000, 128); err != nil {
		return nil, err
	}
	return value, nil
}

// ValidationIssue contains no sample values or schema literals. Path is an RFC
// 6901 JSON pointer; the empty path denotes the whole instance.
type ValidationIssue struct {
	Path    string `json:"path"`
	Keyword string `json:"keyword"`
	Message string `json:"message"`
}
type ValidationError struct {
	Issues []ValidationIssue `json:"issues"`
}

func (*ValidationError) Error() string { return "JSON schema validation failed" }
func validationError(err error) error {
	var source *jsonschema.ValidationError
	if !errors.As(err, &source) {
		return errors.New("JSON schema validation failed")
	}
	out := &ValidationError{}
	var visit func(*jsonschema.ValidationError)
	visit = func(e *jsonschema.ValidationError) {
		if len(out.Issues) >= 64 {
			return
		}
		if len(e.Causes) > 0 {
			for _, child := range e.Causes {
				visit(child)
			}
			return
		}
		keyword := "schema"
		if parts := e.ErrorKind.KeywordPath(); len(parts) > 0 {
			keyword = parts[0]
		}
		path := ""
		for _, part := range e.InstanceLocation {
			path += "/" + strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1")
		}
		message := "value does not satisfy the " + keyword + " constraint"
		if keyword == "type" {
			message = "value has an unexpected JSON type"
		}
		if keyword == "required" {
			message = "required properties are missing"
		}
		out.Issues = append(out.Issues, ValidationIssue{Path: path, Keyword: keyword, Message: message})
	}
	visit(source)
	return out
}
func boundJSON(v any, depth int, count *int, maxNodes, maxDepth int) error {
	*count++
	if depth > maxDepth || *count > maxNodes {
		return errors.New("JSON structure limit exceeded")
	}
	switch x := v.(type) {
	case map[string]any:
		for _, child := range x {
			if err := boundJSON(child, depth+1, count, maxNodes, maxDepth); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range x {
			if err := boundJSON(child, depth+1, count, maxNodes, maxDepth); err != nil {
				return err
			}
		}
	case json.Number:
		if _, _, _, ok := decimalParts(string(x)); !ok {
			return errors.New("JSON numeric structure limit exceeded")
		}
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
	default:
		return errors.New("body is not a JSON value")
	}
	return nil
}
func coerceScalar(v any, target string) (any, bool) {
	switch target {
	case "string":
		switch x := v.(type) {
		case string:
			return x, true
		case nil:
			return "", true
		case bool:
			return strconv.FormatBool(x), true
		case json.Number:
			value, ok := exactDecimal(string(x))
			return value, ok
		}
	case "boolean":
		switch x := v.(type) {
		case bool:
			return x, true
		case nil:
			return false, true
		case string:
			if x == "true" {
				return true, true
			}
			if x == "false" {
				return false, true
			}
		case json.Number:
			if x == "1" {
				return true, true
			}
			if x == "0" {
				return false, true
			}
		}
	case "null":
		switch x := v.(type) {
		case nil:
			return nil, true
		case string:
			if x == "" {
				return nil, true
			}
		case bool:
			if !x {
				return nil, true
			}
		case json.Number:
			if digits, _, _, ok := decimalParts(string(x)); ok && digits == "0" {
				return nil, true
			}
		}
	case "number", "integer":
		var text string
		switch x := v.(type) {
		case json.Number:
			text = string(x)
		case nil:
			text = "0"
		case bool:
			if x {
				text = "1"
			} else {
				text = "0"
			}
		case string:
			text = strings.TrimSpace(x)
			if strings.Contains(text, "_") && len(text) <= maxNumericDigits {
				_, err := strconv.ParseFloat(text, 64)
				if err == nil || errors.Is(err, strconv.ErrRange) {
					text = strings.ReplaceAll(text, "_", "")
				}
			}
		default:
			return nil, false
		}
		digits, point, _, ok := decimalParts(text)
		if !ok {
			// Preserve supported hexadecimal syntax without converting via float64.
			text, ok = exactHex(text)
			if !ok {
				return nil, false
			}
			digits, point, _, ok = decimalParts(text)
		}
		if !ok || (target == "integer" && digits != "0" && point < len(digits)) {
			return nil, false
		}
		value, ok := exactDecimal(text)
		return json.Number(value), ok
	}
	return nil, false
}

// Bound decimal work before the schema library constructs arbitrary precision
// rationals. Neither exponents nor mantissas may force unbounded allocation.
const maxNumericDigits = 4096

var decimalPattern = regexp.MustCompile(`^([+-]?)([0-9]*)(?:\.([0-9]*))?(?:[eE]([+-]?[0-9]+))?$`)

func decimalParts(text string) (digits string, point int, negative bool, ok bool) {
	if len(text) == 0 || len(text) > maxNumericDigits {
		return "", 0, false, false
	}
	parts := decimalPattern.FindStringSubmatch(text)
	if parts == nil || len(parts[2])+len(parts[3]) == 0 {
		return "", 0, false, false
	}
	exponent := 0
	if parts[4] != "" {
		var err error
		exponent, err = strconv.Atoi(parts[4])
		if err != nil || exponent < -maxNumericDigits || exponent > maxNumericDigits {
			return "", 0, false, false
		}
	}
	digits = parts[2] + parts[3]
	point = len(parts[2]) + exponent
	leading := len(digits) - len(strings.TrimLeft(digits, "0"))
	digits = strings.TrimLeft(digits, "0")
	point -= leading
	if digits == "" {
		return "0", 1, false, true
	}
	digits = strings.TrimRight(digits, "0")
	return digits, point, parts[1] == "-", true
}

// exactDecimal uses ordinary JavaScript scalar formatting thresholds, while
// retaining every significant digit rather than converting through float64.
func exactDecimal(text string) (string, bool) {
	digits, point, negative, ok := decimalParts(text)
	if !ok {
		return "", false
	}
	if digits == "0" {
		return "0", true
	}
	prefix := ""
	if negative {
		prefix = "-"
	}
	if point > 0 && point <= 21 {
		if point >= len(digits) {
			return prefix + digits + strings.Repeat("0", point-len(digits)), true
		}
		return prefix + digits[:point] + "." + digits[point:], true
	}
	if point <= 0 && point > -6 {
		return prefix + "0." + strings.Repeat("0", -point) + digits, true
	}
	coefficient := digits[:1]
	if len(digits) > 1 {
		coefficient += "." + digits[1:]
	}
	exponent := strconv.Itoa(point - 1)
	if point-1 >= 0 {
		exponent = "+" + exponent
	}
	return prefix + coefficient + "e" + exponent, true
}

var hexPattern = regexp.MustCompile(`^([+-]?)0[xX]([0-9a-fA-F]*)(?:\.([0-9a-fA-F]*))?[pP]([+-]?[0-9]+)$`)

func exactHex(text string) (string, bool) {
	if len(text) > maxNumericDigits {
		return "", false
	}
	parts := hexPattern.FindStringSubmatch(text)
	if parts == nil || len(parts[2])+len(parts[3]) == 0 {
		return "", false
	}
	exponent, err := strconv.Atoi(parts[4])
	if err != nil || exponent < -maxNumericDigits || exponent > maxNumericDigits {
		return "", false
	}
	exponent -= 4 * len(parts[3])
	if exponent < -maxNumericDigits || exponent > maxNumericDigits {
		return "", false
	}
	coefficient, ok := new(big.Int).SetString(parts[2]+parts[3], 16)
	if !ok {
		return "", false
	}
	if parts[1] == "-" {
		coefficient.Neg(coefficient)
	}
	if exponent >= 0 {
		coefficient.Lsh(coefficient, uint(exponent))
		return exactDecimal(coefficient.String())
	}
	// n/2^k = n*5^k/10^k, a terminating decimal with no rounding.
	power := -exponent
	coefficient.Mul(coefficient, new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(power)), nil))
	return exactDecimal(coefficient.String() + "e-" + strconv.Itoa(power))
}

func JSONFieldPath(path string) bool {
	if len(path) == 0 || len(path) > 256 {
		return false
	}
	for _, p := range strings.Split(path, ".") {
		if p == "" {
			return false
		}
	}
	return true
}

// StripFields copies only the modified ancestor maps. Other fields and scalar
// representations remain unchanged; callers never mutate retained subtrees.
func StripFields(body any, paths []string) (any, error) {
	root, ok := body.(map[string]any)
	if !ok {
		return nil, errors.New("strip_fields requires a JSON object")
	}
	out := cloneMap(root)
	for _, path := range paths {
		if !JSONFieldPath(path) {
			return nil, errors.New("invalid strip path")
		}
		parts := strings.Split(path, ".")
		m := out
		for i, part := range parts {
			if i == len(parts)-1 {
				delete(m, part)
				break
			}
			next, ok := m[part].(map[string]any)
			if !ok {
				break
			}
			copy := cloneMap(next)
			m[part] = copy
			m = copy
		}
	}
	return out, nil
}
