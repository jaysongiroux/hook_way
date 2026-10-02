package flowconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/graph"
	hooklogging "github.com/jaysongiroux/hook_way/internal/logging"
	"github.com/jaysongiroux/hook_way/internal/routing"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

// requestSelector reads the immutable ingress view. Repeated values are not
// silently collapsed when selecting a routing identifier.
type requestSelector struct{ Source, Path string }

var errBodyProjection = errors.New("remove_body_fields requires a JSON object")

func (s requestSelector) read(env graph.Envelope) (any, bool, error) {
	switch s.Source {
	case "body":
		v, found := dotted(env.Body, s.Path)
		return v, found, nil
	case "variable":
		v, found := env.Vars[s.Path]
		return v, found, nil
	case "query", "header":
		list, found := env.V.Raw.Head.Query[s.Path]
		if s.Source == "header" {
			list = env.V.Raw.Head.Header.Values(s.Path)
			found = len(list) > 0
		}
		if found && len(list) != 1 {
			return nil, false, errors.New("ambiguous request selector")
		}
		if found {
			return list[0], true, nil
		}
		return nil, false, nil
	default:
		return nil, false, errors.New("invalid request selector source")
	}
}

func (n httpRequestNode) target(env graph.Envelope) (string, error) {
	if n.opts.Upstream == nil {
		if err := validateAuthorityVariables(n.authorityTemplate, env.Vars); err != nil {
			return "", err
		}
		return n.template.Render(env.Vars)
	}
	o := n.opts.Upstream
	if n.registry == nil {
		return "", errors.New("upstream registry is not bound")
	}
	b, err := n.registry.Admit(env.Vars[o.EnvironmentVariable], o.Profile)
	if err != nil || b.BackendHost == "" {
		return "", errors.New("upstream environment is not admitted or has no backend host")
	}
	escaped, err := n.template.Render(env.Vars)
	if err != nil {
		return "", err
	}
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return "", errors.New("invalid upstream path escape")
	}
	scheme := o.Scheme
	if scheme == "" {
		scheme = "https"
	}
	host := b.BackendHost
	if o.Port != 0 {
		host = net.JoinHostPort(host, strconv.Itoa(o.Port))
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: decoded, RawPath: escaped}).String(), nil
}

func newHTTPRequestNodeWithRegistry(id string, o HTTPRequestOptions, allow, deny []string, registry *routing.Registry, validateOnly bool) (graph.Node, error) {
	if o.Upstream != nil && !validateOnly && (registry == nil || !registry.HasProfile(o.Upstream.Profile)) {
		return nil, fmt.Errorf("%w: upstream profile is not bound", ErrRoutingConfiguration)
	}
	value, err := newHTTPRequestNode(id, o, allow, deny)
	if err != nil {
		return nil, err
	}
	n := value.(httpRequestNode)
	n.registry = registry
	return n, nil
}

func projectedQuery(src url.Values, mode string, remove []string) url.Values {
	out := make(url.Values, len(src))
	for k, v := range src {
		if slices.Contains(remove, k) {
			continue
		}
		switch mode {
		case "first":
			if len(v) > 0 {
				out[k] = []string{v[0]}
			}
		case "last":
			if len(v) > 0 {
				out[k] = []string{v[len(v)-1]}
			}
		default:
			out[k] = slices.Clone(v)
		}
	}
	return out
}

func (n httpRequestNode) requestBody(env graph.Envelope, method string) ([]byte, string, error) {
	if len(n.opts.BodyMethods) > 0 && !slices.Contains(n.opts.BodyMethods, method) {
		return nil, "", nil
	}
	if len(n.opts.RemoveBodyFields) > 0 {
		value, err := cloneJSONValue(env.Body)
		if err != nil {
			return nil, "", err
		}
		object, ok := value.(map[string]any)
		if !ok {
			return nil, "", errBodyProjection
		}
		for _, path := range n.opts.RemoveBodyFields {
			parts := strings.Split(path, ".")
			current := object
			for _, key := range parts[:len(parts)-1] {
				current, _ = current[key].(map[string]any)
				if current == nil {
					break
				}
			}
			if current != nil {
				delete(current, parts[len(parts)-1])
			}
		}
		env.Body = object
	}
	return outboundBody(env, n.opts.Body)
}

func sensitiveResponseField(path string) bool {
	return hooklogging.SensitiveHeader(path) || strings.Contains(strings.ToLower(path), "password")
}

// A failed selection is metadata, not a replacement for a successful create
// reply. Registration can recover independently without repeating the create.
func (n httpRequestNode) bindResponse(env *graph.Envelope) {
	o := n.opts.ResponseBindings
	if o == nil {
		return
	}
	v := maps.Clone(env.Vars)
	if v == nil {
		v = map[string]string{}
	}
	for key := range o.Fields {
		delete(v, key)
	}
	if o.Status != "" {
		v[o.Status] = strconv.Itoa(env.Upstream.Status)
	}
	env.Vars = v
	if len(o.Fields) == 0 {
		return
	}
	limit := o.MaxJSONBytes
	if limit == 0 {
		limit = 64 << 10
	}
	if int64(len(env.Upstream.Body)) > limit {
		env.Upstream.SelectionError = "response_json_too_large"
		return
	}
	if !json.Valid(env.Upstream.Body) {
		env.Upstream.SelectionError = "invalid_json"
		return
	}
	dec := json.NewDecoder(bytes.NewReader(env.Upstream.Body))
	dec.UseNumber()
	var body any
	if err := dec.Decode(&body); err != nil {
		env.Upstream.SelectionError = "invalid_json"
		return
	}
	selected := make(map[string]string, len(o.Fields))
	for key, path := range o.Fields {
		value, found := dotted(body, path)
		if !found || value == nil {
			env.Upstream.SelectionError = "missing_field"
			return
		}
		var text string
		switch value := value.(type) {
		case string:
			text = value
		case json.Number:
			text = string(value)
		case bool:
			text = strconv.FormatBool(value)
		default:
			env.Upstream.SelectionError = "invalid_field"
			return
		}
		if text == "" || len(text) > 1024 {
			env.Upstream.SelectionError = "invalid_field"
			return
		}
		selected[key] = text
	}
	env.Upstream.Values = selected
	for k, value := range selected {
		env.Vars[k] = value
	}
}

func validProxyMethod(s string) bool {
	return slices.Contains([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}, s)
}

func validateProxyContract(o HTTPRequestOptions) error {
	if o.Method != "" && (!validProxyMethod(o.Method) || o.ForwardMethod) {
		return errors.New("method must be a supported HTTP method and cannot accompany forward_method")
	}
	for _, method := range append(slices.Clone(o.BodyMethods), o.Retry.Methods...) {
		if !validProxyMethod(method) {
			return errors.New("body_methods and retry.methods require supported uppercase HTTP methods")
		}
	}
	if o.QueryMode != "" && o.QueryMode != "all" && o.QueryMode != "first" && o.QueryMode != "last" {
		return errors.New("query_mode must be all, first or last")
	}
	if (o.QueryMode != "" || len(o.RemoveQuery) > 0) && !o.ForwardQuery {
		return errors.New("query projection requires forward_query")
	}
	if o.HeaderMode != "" && o.HeaderMode != "forward" && o.HeaderMode != "replace" {
		return errors.New("header_mode must be forward or replace")
	}
	for _, h := range append(slices.Clone(o.RemoveHeaders), o.ForwardHeaders...) {
		if !validAuthHeader(h) {
			return errors.New("invalid HTTP header name")
		}
	}
	if len(o.RemoveBodyFields) > 0 && o.Body != "transformed" {
		return errors.New("remove_body_fields requires body: transformed")
	}
	if len(o.RemoveBodyFields) > 64 || len(o.RemoveQuery) > 64 || len(o.RemoveHeaders) > 64 {
		return errors.New("request projections support at most 64 removals each")
	}
	for _, path := range o.RemoveBodyFields {
		if !validSelectionPath(path) {
			return errors.New("invalid body removal path")
		}
	}
	for _, key := range o.RemoveQuery {
		if key == "" || len(key) > 256 {
			return errors.New("invalid query removal key")
		}
	}
	if o.MaxResponseBytes < 0 || o.MaxResponseBytes > 64<<20 {
		return errors.New("max_response_bytes must be 1..67108864")
	}
	if o.Redirects != nil && (o.Redirects.MaxHops < 1 || o.Redirects.MaxHops > 10) {
		return errors.New("redirects.max_hops must be 1..10")
	}
	if b := o.ResponseBindings; b != nil {
		if b.Status == "" && len(b.Fields) == 0 {
			return errors.New("response_bindings requires status or fields")
		}
		if b.Status != "" && (!vars.ValidName(b.Status) || sensitiveResponseField(b.Status)) {
			return errors.New("invalid response status variable")
		}
		if len(b.Fields) > 16 || b.MaxJSONBytes < 0 || b.MaxJSONBytes > 1<<20 {
			return errors.New("response_bindings supports at most 16 fields and 1 MiB JSON")
		}
		for key, path := range b.Fields {
			if !vars.ValidName(key) || key == b.Status || !validSelectionPath(path) || sensitiveResponseField(key) || sensitiveResponseField(path) {
				return errors.New("response bindings require non-sensitive scalar paths and distinct variable names")
			}
		}
	}
	return nil
}

func validSelectionPath(path string) bool {
	if len(path) == 0 || len(path) > 256 {
		return false
	}
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			return false
		}
	}
	return true
}
