package flowconfig

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/routing"
)

type registrationStore struct {
	calls   int
	fail    int
	records []routing.Record
}

func (s *registrationStore) Get(context.Context, routing.Key) (routing.Record, bool, error) {
	return routing.Record{}, false, nil
}
func (s *registrationStore) Put(_ context.Context, r routing.Record) error {
	s.calls++
	if s.calls <= s.fail {
		return errors.New("synthetic outage")
	}
	s.records = append(s.records, r)
	return nil
}
func TestRegistrationPreservesKnownSuccessAndInput(t *testing.T) {
	registry, e := routing.NewRegistry(routing.Config{Profiles: map[string]routing.Profile{"sandbox": {AccountID: "000000000000", CredentialMode: "sandbox"}}, Environments: map[string]routing.Environment{"preview": {Accounts: []string{"000000000000"}, Profiles: []string{"sandbox"}, DestinationPrefix: "preview"}}})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name           string
		status, fail   int
		id             string
		selectionError string
		want           string
		calls          int
	}{{"registered", 201, 0, "42", "", "registered", 1}, {"retry", 200, 1, "42", "", "registered", 2}, {"pending", 201, 9, "42", "", "pending", 3}, {"missing", 201, 0, "", "missing_field", "unregistered", 0}, {"invalid", 200, 0, "bad\n", "", "unregistered", 0}, {"failure", 400, 0, "42", "", "", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			store := &registrationStore{fail: tc.fail}
			o := RegisterRouteOptions{Mode: "after_create", Store: "primary", Profile: "sandbox", EnvironmentVariable: "route", IDSource: "upstream", IDPath: "created_id", IDType: "record_a", MaxAttempts: 3, Backoff: "1ms"}
			n, e := newRegisterRouteNode("register", o, RegistryOptions{RoutingRegistry: registry, RoutingStores: map[string]routing.Reader{"primary": store}})
			if e != nil {
				t.Fatal(e)
			}
			in := graph.Envelope{Vars: map[string]string{"route": "preview"}, Upstream: &graph.UpstreamResponse{Status: tc.status, Header: http.Header{"X-Sample": []string{"original"}}, Body: []byte(`{"id":"42"}`), Values: map[string]string{"created_id": tc.id}, SelectionError: tc.selectionError}}
			original := graph.Envelope{Vars: map[string]string{"route": "preview"}, Upstream: &graph.UpstreamResponse{Status: tc.status, Header: in.Upstream.Header.Clone(), Body: append([]byte(nil), in.Upstream.Body...), Values: map[string]string{"created_id": tc.id}, SelectionError: tc.selectionError}}
			out, e := n.Run(context.Background(), in)
			if e != nil {
				t.Fatal(e)
			}
			result := out.(graph.Envelope)
			if result.Upstream.Status != tc.status || string(result.Upstream.Body) != string(in.Upstream.Body) || result.Upstream.Header.Get("X-Routing-Registration") != tc.want || store.calls != tc.calls {
				t.Fatal(result, store)
			}
			if !reflect.DeepEqual(in, original) {
				t.Fatal("registration mutated input")
			}
		})
	}
}
