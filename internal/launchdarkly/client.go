// Package launchdarkly owns native SDK clients. It is never imported by WASM.
package launchdarkly

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/jaysongiroux/hook_way/internal/featureflags"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldlog"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	ld "github.com/launchdarkly/go-server-sdk/v7"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
	"github.com/launchdarkly/go-server-sdk/v7/ldcomponents"
)

type Client struct {
	sdk     *ld.LDClient
	done    chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func New(c flowconfig.LaunchDarklyConnection, status func(string)) (*Client, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	key := os.Getenv(c.SDKKeyEnv)
	if key == "" {
		return nil, errors.New("LaunchDarkly SDK key environment variable is missing or empty")
	}
	timeout := 5 * time.Second
	if c.InitializationTimeout != "" {
		timeout, _ = time.ParseDuration(c.InitializationTimeout)
	}
	config := clientConfig(c)
	return newClient(key, config, timeout, status)
}

func newClient(key string, config ld.Config, timeout time.Duration, status func(string)) (*Client, error) {
	sdk, err := ld.MakeCustomClient(key, config, timeout)
	if sdk == nil {
		return nil, errors.New("LaunchDarkly client configuration failed")
	}
	// Timeout leaves the background data source alive; fallback evaluations are safe.
	if err != nil && !errors.Is(err, ld.ErrInitializationTimeout) {
		_ = sdk.Close()
		return nil, errors.New("LaunchDarkly client initialization failed")
	}
	return wrap(sdk, status), nil
}
func wrap(sdk *ld.LDClient, status func(string)) *Client {
	c := &Client{sdk: sdk, done: make(chan struct{}), stopped: make(chan struct{})}
	provider := sdk.GetDataSourceStatusProvider()
	listener := provider.AddStatusListener()
	report := func(s interfaces.DataSourceStatus) {
		if status != nil {
			status(string(s.State))
		}
	}
	report(provider.GetStatus())
	go func() {
		defer close(c.stopped)
		defer provider.RemoveStatusListener(listener)
		for {
			select {
			case <-c.done:
				return
			case s, ok := <-listener:
				if !ok {
					return
				}
				report(s)
			}
		}
	}()
	return c
}
func (c *Client) Close() error {
	var err error
	c.once.Do(func() { close(c.done); <-c.stopped; err = c.sdk.Close() })
	return err
}
func (c *Client) Evaluate(ctx featureflags.Context, key, kind string, fallback any) featureflags.Result {
	builder := ldcontext.NewBuilder(ctx.Key).Kind(ldcontext.Kind(ctx.Kind))
	for name, value := range ctx.Attributes {
		builder.SetValue(name, ldvalue.FromJSONMarshal(value))
	}
	value, detail, err := c.sdk.JSONVariationDetail(key, builder.Build(), ldvalue.FromJSONMarshal(fallback))
	reason := string(detail.Reason.GetKind())
	if err != nil || reason == "ERROR" {
		code := string(detail.Reason.GetErrorKind())
		switch code {
		case "CLIENT_NOT_READY", "FLAG_NOT_FOUND", "WRONG_TYPE", "USER_NOT_SPECIFIED", "MALFORMED_FLAG", "EXCEPTION":
		default:
			code = "EVALUATION_ERROR"
		}
		return featureflags.Result{Value: fallback, UsedFallback: true, Reason: "ERROR", ErrorKind: code}
	}
	normalized, e := featureflags.Normalize(value.AsArbitraryValue())
	if e != nil || !featureflags.Matches(kind, normalized) {
		return featureflags.Result{Value: fallback, UsedFallback: true, Reason: "ERROR", ErrorKind: "WRONG_TYPE"}
	}
	return featureflags.Result{Value: normalized, Reason: reason}
}

func clientConfig(c flowconfig.LaunchDarklyConnection) ld.Config {
	config := ld.Config{Logging: ldcomponents.Logging().Loggers(ldlog.NewDisabledLoggers())}
	if c.SendEvents != nil && !*c.SendEvents {
		config.Events = ldcomponents.NoEvents()
	} else {
		config.Events = ldcomponents.SendEvents().AllAttributesPrivate(true)
	}
	return config
}
