package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/lookup"
	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/vars"
)

// endpointVariables is the compiled per-endpoint state for routing variables:
// the path pattern, the variable specs, and the parsed target template of
// every authored route plus the webhook url template of its destination.
// broken is set when any of those failed to compile; config validation
// rejects such configs before serving, but tests may build a Config directly,
// so the handler fails closed (500) rather than trusting partial state.
type endpointVariables struct {
	path      vars.PathPattern
	specs     []vars.Spec
	targets   []vars.Template   // index-aligned with EndpointConfig.Destinations
	urls      []vars.Template   // index-aligned; zero when the destination url is static
	needed    [][]string        // index-aligned; every variable a route's target, url, or workflow_id prefix references
	urlVars   [][]string        // index-aligned; variables referenced by the destination url
	providers []lookup.Provider // lookup providers to close on shutdown
	broken    bool
}

// compileEndpointVariables builds endpointVariables for one endpoint, logging
// (never panicking) on compile errors. Lookup-source variables get their
// provider from factory; a construction failure marks the endpoint broken.
func compileEndpointVariables(ctx context.Context, logger *slog.Logger, name string, endpoint config.EndpointConfig, destinations map[string]config.DestinationConfig, factory LookupFactory, m *metrics.Metrics) endpointVariables {
	ev := endpointVariables{}
	if endpoint.Path != "" && endpoint.Path[0] == '/' {
		pp, err := vars.ParsePath(endpoint.Path)
		if err != nil {
			logger.Error("endpoint path pattern is invalid; endpoint will reject all requests", "endpoint", name, "error", err.Error())
			ev.broken = true
		} else {
			ev.path = pp
		}
	}
	specs, err := endpoint.VariableSpecs()
	if err != nil {
		logger.Error("endpoint variables are invalid; endpoint will reject all requests", "endpoint", name, "error", err.Error())
		ev.broken = true
	}
	for i := range specs {
		if specs[i].Source != vars.SourceLookup {
			continue
		}
		lc := endpoint.Variables[specs[i].Name].Lookup
		if lc == nil || factory == nil {
			logger.Error("lookup variable has no provider; endpoint will reject all requests", "endpoint", name, "variable", specs[i].Name)
			ev.broken = true
			continue
		}
		provider, err := factory(ctx, lookupOptions(specs[i].Name, *lc), logger)
		if err != nil {
			logger.Error("lookup provider failed to initialise; endpoint will reject all requests", "endpoint", name, "variable", specs[i].Name, "error", err.Error())
			ev.broken = true
			continue
		}
		ev.providers = append(ev.providers, provider)
		specs[i].Lookup = &meteredLookup{inner: provider, endpoint: name, variable: specs[i].Name, provider: lc.Provider, metrics: m}
	}
	ev.specs = specs
	ev.targets = make([]vars.Template, len(endpoint.Destinations))
	ev.urls = make([]vars.Template, len(endpoint.Destinations))
	ev.needed = make([][]string, len(endpoint.Destinations))
	ev.urlVars = make([][]string, len(endpoint.Destinations))
	for i, route := range endpoint.Destinations {
		tpl, err := vars.Parse(route.Target)
		if err != nil {
			logger.Error("route target template is invalid; endpoint will reject all requests", "endpoint", name, "target", route.Target, "error", err.Error())
			ev.broken = true
			continue
		}
		ev.targets[i] = tpl
		needed := tpl.Names()
		dest, ok := destinations[route.Destination]
		if ok && dest.Type == "webhook" {
			urlTpl, err := vars.Parse(dest.Webhook.URL)
			if err != nil {
				logger.Error("webhook url template is invalid; endpoint will reject all requests", "endpoint", name, "destination", route.Destination, "error", err.Error())
				ev.broken = true
				continue
			}
			if urlTpl.HasPlaceholders() {
				ev.urls[i] = urlTpl
				ev.urlVars[i] = urlTpl.Names()
				needed = append(needed, ev.urlVars[i]...)
			}
		}
		// A temporal workflow_id prefix renders from the same variables at
		// publish time; an unbound one there can never succeed on retry, so
		// it must count as needed here and take the unresolved path.
		if ok && dest.Type == "temporal" {
			prefixTpl, err := vars.Parse(route.TemporalOptions.WorkflowID.Prefix)
			if err != nil {
				logger.Error("temporal workflow_id prefix template is invalid; endpoint will reject all requests", "endpoint", name, "destination", route.Destination, "error", err.Error())
				ev.broken = true
				continue
			}
			needed = append(needed, prefixTpl.Names()...)
		}
		ev.needed[i] = needed
	}
	return ev
}

// resolvedRoute is an authored route with its target rendered for this
// request. template keeps the authored target for metric labels so
// cardinality stays bounded by the config, not by the request stream.
type resolvedRoute struct {
	config.EndpointDestinationConfig
	template string
}

// resolveRoutes renders the active routes (given by authored index) for one
// request. It returns the rendered routes, the resolved variable values, and
// the sorted names of variables that were needed but unresolved (or whose
// rendered target was unsafe). When unresolved is non-empty the caller must
// not publish to the returned routes.
func (ev endpointVariables) resolveRoutes(ctx context.Context, endpoint config.EndpointConfig, activeIdx []int, in vars.Input) (routes []resolvedRoute, values map[string]string, unresolved []string, errs map[string]error) {
	var res vars.Result
	if len(ev.specs) > 0 {
		res = vars.Resolve(ctx, ev.specs, in)
		values = res.Values
		errs = res.Errors
	}
	needed := map[string]struct{}{}
	for _, i := range activeIdx {
		for _, n := range ev.needed[i] {
			needed[n] = struct{}{}
		}
	}
	bad := map[string]struct{}{}
	for _, n := range res.Unresolved {
		if _, ok := needed[n]; ok {
			bad[n] = struct{}{}
		}
	}
	// A variable rendered into a destination url is bounded to a safe name
	// so a request cannot smuggle '/', '@', '?', '#', or ':' past the
	// validation-time literal domain suffix and pick an arbitrary host.
	for _, i := range activeIdx {
		for _, n := range ev.urlVars[i] {
			if v, ok := values[n]; ok && !config.IsSafeName(v) {
				bad[n] = struct{}{}
			}
		}
	}
	routes = make([]resolvedRoute, 0, len(activeIdx))
	for _, i := range activeIdx {
		route := endpoint.Destinations[i]
		tpl := ev.targets[i]
		if !tpl.HasPlaceholders() {
			routes = append(routes, resolvedRoute{EndpointDestinationConfig: route, template: route.Target})
			continue
		}
		rendered, err := tpl.Render(values)
		if err != nil {
			// Already accounted for in bad via res.Unresolved when the
			// variable is declared; an undeclared placeholder lands here too.
			var unbound vars.ErrUnbound
			if errors.As(err, &unbound) {
				bad[unbound.Name] = struct{}{}
			}
			continue
		}
		// The rendered whole must be a safe name, and a variable may not
		// smuggle a .fifo suffix into a template that does not end in one.
		if !config.IsSafeName(rendered) || (strings.HasSuffix(rendered, ".fifo") && !strings.HasSuffix(tpl.LiteralSuffix(), ".fifo")) {
			for _, n := range tpl.Names() {
				bad[n] = struct{}{}
			}
			continue
		}
		route.Target = rendered
		routes = append(routes, resolvedRoute{EndpointDestinationConfig: route, template: endpoint.Destinations[i].Target})
	}
	if len(bad) > 0 {
		unresolved = make([]string, 0, len(bad))
		for n := range bad {
			unresolved = append(unresolved, n)
		}
		sort.Strings(unresolved)
		return nil, values, unresolved, errs
	}
	return routes, values, nil, errs
}

// staticRoutes wraps authored routes untouched (no variables declared).
func staticRoutes(endpoint config.EndpointConfig, activeIdx []int) []resolvedRoute {
	routes := make([]resolvedRoute, 0, len(activeIdx))
	for _, i := range activeIdx {
		route := endpoint.Destinations[i]
		routes = append(routes, resolvedRoute{EndpointDestinationConfig: route, template: route.Target})
	}
	return routes
}
