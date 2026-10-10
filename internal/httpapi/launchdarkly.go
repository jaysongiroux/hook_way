package httpapi

import (
	"fmt"
	"io"
	"slices"

	"github.com/jaysongiroux/hook_way/internal/featureflags"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/launchdarkly"
	"github.com/jaysongiroux/hook_way/internal/metrics"
)

func bindLaunchDarkly(configs map[string]flowconfig.LaunchDarklyConnection, m *metrics.Metrics) (func(string) featureflags.Evaluator, []io.Closer, error) {
	return bindLaunchDarklyWithFactory(configs, m, func(c flowconfig.LaunchDarklyConnection, status func(string)) (flagClient, error) {
		return launchdarkly.New(c, status)
	})
}

type flagClient interface {
	io.Closer
	Evaluate(featureflags.Context, string, string, any) featureflags.Result
}
type flagClientFactory func(flowconfig.LaunchDarklyConnection, func(string)) (flagClient, error)

func bindLaunchDarklyWithFactory(configs map[string]flowconfig.LaunchDarklyConnection, m *metrics.Metrics, newClient flagClientFactory) (func(string) featureflags.Evaluator, []io.Closer, error) {
	clients := map[string]flagClient{}
	var closers []io.Closer
	for _, name := range sortedLaunchDarklyNames(configs) {
		client, err := newClient(configs[name], func(state string) { m.FlagConnection(name, state) })
		if err != nil {
			return nil, closers, fmt.Errorf("launchdarkly connection %q: %w", name, err)
		}
		clients[name] = client
		closers = append(closers, client)
	}
	return func(name string) featureflags.Evaluator {
		if c := clients[name]; c != nil {
			return c.Evaluate
		}
		return nil
	}, closers, nil
}

func sortedLaunchDarklyNames(m map[string]flowconfig.LaunchDarklyConnection) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}
