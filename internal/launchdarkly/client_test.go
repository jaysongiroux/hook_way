package launchdarkly

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/featureflags"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	ld "github.com/launchdarkly/go-server-sdk/v7"
	"github.com/launchdarkly/go-server-sdk/v7/interfaces"
	"github.com/launchdarkly/go-server-sdk/v7/ldcomponents"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
)

func TestSDKTypesTargetingAndUpdates(t *testing.T) {
	data := ldtestdata.DataSource()
	for key, value := range map[string]any{"gate": true, "null": nil, "route": "next", "limit": 2.5, "settings": map[string]any{"limit": 4.0, "list": []any{true, nil}}} {
		data.Update(data.Flag(key).ValueForAll(ldvalue.FromJSONMarshal(value)))
	}
	data.Update(data.Flag("target").BooleanFlag().VariationForAll(false).VariationForKey("organization", "acme", true))
	client, err := newClient("test-key", ld.Config{DataSource: data, Events: ldcomponents.NoEvents(), Logging: ldcomponents.NoLogging()}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := featureflags.Context{Key: "acme", Kind: "organization", Attributes: map[string]any{"seats": 3.0}}
	for _, tc := range []struct {
		key, kind string
		expected  any
	}{{"gate", "boolean", true}, {"route", "string", "next"}, {"limit", "number", 2.5}, {"settings", "json", map[string]any{"limit": 4.0, "list": []any{true, nil}}}, {"target", "boolean", true}, {"null", "json", nil}} {
		r := client.Evaluate(ctx, tc.key, tc.kind, nil)
		if r.UsedFallback || !reflect.DeepEqual(r.Value, tc.expected) {
			t.Fatalf("%s: %+v", tc.key, r)
		}
	}
	other := ctx
	other.Key = "other"
	if client.Evaluate(other, "target", "boolean", true).Value != false {
		t.Fatal("tenant targeting lost")
	}
	if r := client.Evaluate(ctx, "missing", "json", nil); !r.UsedFallback || r.Value != nil || r.ErrorKind != "FLAG_NOT_FOUND" {
		t.Fatalf("missing: %+v", r)
	}
	if r := client.Evaluate(ctx, "gate", "number", 0.0); !r.UsedFallback || r.ErrorKind != "WRONG_TYPE" {
		t.Fatalf("mistyped: %+v", r)
	}
	data.Update(data.Flag("gate").BooleanFlag().On(false).OffVariation(false))
	if r := client.Evaluate(ctx, "gate", "boolean", true); r.Value != false || r.UsedFallback || r.Reason != "OFF" {
		t.Fatalf("off: %+v", r)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	} // Closing twice is safe.
}
func TestInitializationTimeoutRecoversWithSameClient(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: put\ndata: {\"path\":\"/\",\"data\":{\"flags\":{\"gate\":{\"key\":\"gate\",\"version\":1,\"on\":true,\"variations\":[true,false],\"fallthrough\":{\"variation\":0},\"offVariation\":1}},\"segments\":{}}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	defer once.Do(func() { close(release) })
	statuses := make(chan string, 20)
	client, err := newClient("test-key", ld.Config{DataSource: ldcomponents.StreamingDataSource(), ServiceEndpoints: interfaces.ServiceEndpoints{Streaming: server.URL}, Events: ldcomponents.NoEvents(), Logging: ldcomponents.NoLogging()}, 20*time.Millisecond, func(s string) { statuses <- s })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := featureflags.Context{Kind: "user", Key: "tenant"}
	if r := client.Evaluate(ctx, "gate", "boolean", false); !r.UsedFallback || r.ErrorKind != "CLIENT_NOT_READY" {
		t.Fatalf("before init: %+v", r)
	}
	once.Do(func() { close(release) })
	if !client.sdk.GetDataSourceStatusProvider().WaitFor(interfaces.DataSourceStateValid, 3*time.Second) {
		t.Fatal("client never recovered")
	}
	if r := client.Evaluate(ctx, "gate", "boolean", false); r.UsedFallback || r.Value != true {
		t.Fatalf("after init: %+v", r)
	}
	select {
	case s := <-statuses:
		if s != "INITIALIZING" {
			t.Fatalf("initial status=%s", s)
		}
	case <-time.After(time.Second):
		t.Fatal("missing status")
	}
}
func TestMissingEnvironmentKeyFailsBeforeNetwork(t *testing.T) {
	t.Setenv("HOOKWAY_TEST_LD_KEY", "")
	if _, err := New(flowconfig.LaunchDarklyConnection{SDKKeyEnv: "HOOKWAY_TEST_LD_KEY"}, nil); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestEventPrivacyAndOptOut(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			events := make(chan string, 20)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var reader io.Reader = r.Body
				if r.Header.Get("Content-Encoding") == "gzip" {
					gz, err := gzip.NewReader(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					defer gz.Close()
					reader = gz
				}
				body, _ := io.ReadAll(reader)
				events <- string(body)
				w.WriteHeader(202)
			}))
			defer server.Close()
			data := ldtestdata.DataSource()
			data.Update(data.Flag("gate").BooleanFlag().VariationForAll(false).IfMatch("plan", ldvalue.String("sensitive-plan")).ThenReturn(true))
			config := clientConfig(flowconfig.LaunchDarklyConnection{SendEvents: &enabled})
			config.DataSource = data
			config.ServiceEndpoints.Events = server.URL
			config.DiagnosticOptOut = true
			client, err := newClient("test-key", config, time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			result := client.Evaluate(featureflags.Context{Kind: "user", Key: "tenant-key", Attributes: map[string]any{"plan": "sensitive-plan"}}, "gate", "boolean", false)
			if result.Value != true || result.UsedFallback {
				t.Fatal("private attribute was not used in local targeting")
			}
			if !client.sdk.FlushAndWait(2 * time.Second) {
				t.Fatal("flush did not finish")
			}
			if enabled {
				select {
				case payload := <-events:
					if strings.Contains(payload, "sensitive-plan") || !strings.Contains(payload, "tenant-key") {
						t.Fatalf("privacy violation or missing context: %s", payload)
					}
				case <-time.After(time.Second):
					t.Fatal("evaluation events missing")
				}
			} else {
				select {
				case <-events:
					t.Fatal("events sent despite opt-out")
				default:
				}
			}
		})
	}
}
