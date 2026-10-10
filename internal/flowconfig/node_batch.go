package flowconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/routing"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

type BatchChildOptions struct {
	Entry string           `yaml:"entry" json:"entry" jsonschema:"required"`
	Nodes []map[string]any `yaml:"nodes" json:"nodes" jsonschema:"required,minItems=1,maxItems=64"`
	Edges []string         `yaml:"edges" json:"edges"`
}
type BatchOptions struct {
	Mode                 string            `yaml:"mode" json:"mode" jsonschema:"required,enum=shared,enum=dedicated"`
	Profile              string            `yaml:"profile" json:"profile" jsonschema:"required"`
	EnvironmentPath      string            `yaml:"environment_path" json:"environment_path" jsonschema:"required"`
	ReceivingEnvironment string            `yaml:"receiving_environment,omitempty" json:"receiving_environment,omitempty"`
	EventIDPath          string            `yaml:"event_id_path" json:"event_id_path" jsonschema:"required"`
	GroupIDPath          string            `yaml:"group_id_path,omitempty" json:"group_id_path,omitempty"`
	MaxChildren          int               `yaml:"max_children,omitempty" json:"max_children,omitempty" jsonschema:"minimum=1,maximum=10000"`
	Concurrency          int               `yaml:"concurrency,omitempty" json:"concurrency,omitempty" jsonschema:"minimum=1,maximum=64"`
	MaxInflight          int               `yaml:"max_inflight,omitempty" json:"max_inflight,omitempty" jsonschema:"minimum=1,maximum=1024"`
	Timeout              string            `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Child                BatchChildOptions `yaml:"child" json:"child" jsonschema:"required"`
}

func (o BatchOptions) normalized() BatchOptions {
	if o.MaxChildren == 0 {
		o.MaxChildren = 1000
	}
	if o.Concurrency == 0 {
		o.Concurrency = 8
	}
	if o.Mode == "shared" {
		o.Concurrency = 1
	}
	if o.MaxInflight == 0 {
		o.MaxInflight = 64
	}
	if o.Timeout == "" {
		o.Timeout = "30s"
	}
	return o
}
func batchChildConfig(o BatchOptions) (graph.FlowConfig, error) {
	data, err := yaml.Marshal(o.Child.Nodes)
	if err != nil {
		return graph.FlowConfig{}, err
	}
	var nodes []graph.NodeConfig
	if err = yaml.Unmarshal(data, &nodes); err != nil {
		return graph.FlowConfig{}, err
	}
	return graph.FlowConfig{Nodes: nodes, Edges: o.Child.Edges}, nil
}
func validateBatch(o BatchOptions) error {
	if o.Mode != "shared" && o.Mode != "dedicated" {
		return errors.New("batch mode must be shared or dedicated")
	}
	if o.Profile == "" || !validSelectionPath(o.EnvironmentPath) || !validSelectionPath(o.EventIDPath) || (o.GroupIDPath != "" && !validSelectionPath(o.GroupIDPath)) {
		return errors.New("batch requires profile and bounded body paths")
	}
	if o.Mode == "shared" && (o.ReceivingEnvironment != "" || o.Concurrency > 1) {
		return errors.New("shared batches are sequential and cannot select a receiving_environment")
	}
	if o.Mode == "dedicated" && (o.ReceivingEnvironment == "" || o.GroupIDPath != "") {
		return errors.New("dedicated batches require receiving_environment and group by event ID")
	}
	if o.MaxChildren < 0 || o.MaxChildren > 10000 || o.Concurrency < 0 || o.Concurrency > 64 || o.MaxInflight < 0 || o.MaxInflight > 1024 {
		return errors.New("batch limits are out of range")
	}
	o = o.normalized()
	d, err := time.ParseDuration(o.Timeout)
	if err != nil || d <= 0 || d > 2*time.Minute {
		return errors.New("batch timeout must be positive and at most 2m")
	}
	if len(o.Child.Nodes) == 0 || len(o.Child.Nodes) > 64 || !graph.ValidID(o.Child.Entry) {
		return errors.New("batch requires a bounded child flow and entry")
	}
	fc, err := batchChildConfig(o)
	if err != nil {
		return err
	}
	publishes := 0
	for _, node := range fc.Nodes {
		if validationCatalog[node.Type].batchChildOrder <= 0 {
			return errors.New("batch children support only envelope transforms, filters, selectors and one publish; nested batches, triggers and responses are forbidden")
		}
		if node.Type == "publish" {
			publishes++
		}
	}
	if publishes != 1 {
		return errors.New("batch child must contain exactly one publish")
	}
	return nil
}

type batchNode struct {
	id       string
	opts     BatchOptions
	child    *graph.Flow
	registry *routing.Registry
	slots    chan struct{}
	fleet    chan struct{}
	observe  func(string, string)
	logger   func(string, int, string)
}

func newBatchNode(id string, o BatchOptions, opts RegistryOptions) (graph.Node, error) {
	o = o.normalized()
	if opts.BatchSlots == nil {
		opts.BatchSlots = make(chan struct{}, 1024)
	}
	n := &batchNode{id: id, opts: o, registry: opts.RoutingRegistry, slots: make(chan struct{}, o.MaxInflight), observe: opts.BatchObservation, fleet: opts.BatchSlots}
	if !opts.ValidateOnly {
		if n.registry == nil || !n.registry.HasProfile(o.Profile) {
			return nil, fmt.Errorf("%w: batch profile is not bound", ErrRoutingConfiguration)
		}
		if o.Mode == "dedicated" {
			b, err := n.registry.Admit(o.ReceivingEnvironment, o.Profile)
			if err != nil {
				return nil, fmt.Errorf("%w: batch receiving environment is not admitted", ErrRoutingConfiguration)
			}
			n.opts.ReceivingEnvironment = b.Name
		}
	}
	if opts.Logger != nil {
		n.logger = func(parent string, index int, result string) {
			opts.Logger.Info("batch child", "node_id", id, "request_id", parent, "child_index", index, "result", result)
		}
	}
	fc, err := batchChildConfig(o)
	if err != nil {
		return nil, err
	}
	for _, nc := range fc.Nodes {
		if len(nc.ID) <= len(id)+1 || nc.ID[:len(id)+1] != id+"." {
			return nil, errors.New("batch child IDs must start with the parent node ID plus a dot")
		}
	}
	reg, err := NewRegistryWithOptions(opts)
	if err != nil {
		return nil, err
	}
	n.child, err = graph.CompileChild(id+".child", fc, o.Child.Entry, reg)
	return n, err
}
func (n *batchNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	return n.RunChildren(ctx, in, graph.RunOptions{PublishTimeout: mustDuration(n.opts.Timeout)})
}

func (n *batchNode) outcome(parent string, index int, result string) {
	if n.observe != nil {
		func() { defer func() { _ = recover() }(); n.observe(n.id, result) }()
	}
	if n.logger != nil {
		n.logger(parent, index, result)
	}
}

type batchChildResult struct {
	reason     string
	deliveries []graph.Delivery
}

func (n *batchNode) RunChildren(ctx context.Context, in graph.Payload, run graph.RunOptions) (graph.Payload, error) {
	parent, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{NodeID: n.id, Status: 500, Err: errors.New("batch expects Envelope")}
	}
	var children []any
	switch b := parent.Body.(type) {
	case []any:
		children = b
	case map[string]any:
		if n.opts.Mode == "shared" {
			children = []any{b}
		}
	}
	if children == nil {
		return nil, graph.NodeError{NodeID: n.id, Status: 400, Err: errors.New("batch body must be an array, or an object in shared mode")}
	}
	if len(children) > n.opts.MaxChildren {
		return nil, graph.NodeError{NodeID: n.id, Status: 413, Err: errors.New("batch child limit exceeded")}
	}
	ctx, cancel := context.WithTimeout(ctx, mustDuration(n.opts.Timeout))
	defer cancel()
	results := make([]batchChildResult, len(children))
	for i := range results {
		results[i].reason = "unattempted"
	}
	// Fixed workers take indexes synchronously; no goroutine is allocated per child.
	next := 0
	var scheduling sync.Mutex
	var workers sync.WaitGroup
	for w := 0; w < n.opts.Concurrency && w < len(children); w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				scheduling.Lock()
				if next >= len(children) || ctx.Err() != nil {
					scheduling.Unlock()
					return
				}
				i := next
				next++
				scheduling.Unlock()
				select {
				case n.slots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				if ctx.Err() != nil {
					<-n.slots
					return
				}
				select {
				case n.fleet <- struct{}{}:
				case <-ctx.Done():
					<-n.slots
					return
				}
				results[i] = n.executeChild(ctx, parent, children[i], i, run)
				<-n.fleet
				<-n.slots
			}
		}()
	}
	workers.Wait()
	term := graph.Terminal{}
	primary, dropped, skipped, unattempted := 0, 0, 0, 0
	for i, r := range results {
		n.outcome(parent.V.Raw.Head.RequestID, i, r.reason)
		term.Deliveries = append(term.Deliveries, r.deliveries...)
		switch r.reason {
		case "published":
			primary++
		case "environment_skipped", "filtered":
			skipped++
		case "unattempted":
			unattempted++
		default:
			dropped++
		}
	}
	status := 200
	if n.opts.Mode == "dedicated" && (dropped > 0 || unattempted > 0) {
		status = 502
	}
	response := map[string]any{"message": "Processed", "primary": primary, "dropped": dropped + skipped + unattempted}
	if n.opts.Mode == "dedicated" {
		response["dropped"] = dropped
		response["skipped"] = skipped
		response["unattempted"] = unattempted
	}
	body, _ := json.Marshal(response)
	term.Response = &graph.Response{Status: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}
	return term, nil
}
func (n *batchNode) executeChild(ctx context.Context, parent graph.Envelope, body any, index int, run graph.RunOptions) batchChildResult {
	if _, ok := body.(map[string]any); !ok {
		return batchChildResult{reason: "child_invalid"}
	}
	rawEnv, found := transform.GetPath(body, n.opts.EnvironmentPath)
	if !found || rawEnv == nil {
		if n.opts.Mode == "dedicated" {
			return batchChildResult{reason: "environment_skipped"}
		}
		return batchChildResult{reason: "environment_missing"}
	}
	envName, ok := rawEnv.(string)
	if !ok {
		if n.opts.Mode == "dedicated" {
			return batchChildResult{reason: "environment_skipped"}
		}
		return batchChildResult{reason: "environment_denied"}
	}
	if envName == "" {
		if n.opts.Mode == "dedicated" {
			return batchChildResult{reason: "environment_skipped"}
		}
		return batchChildResult{reason: "environment_missing"}
	}
	if n.opts.Mode == "dedicated" {
		canonical, known := n.registry.CanonicalEnvironment(envName)
		if !known || canonical != n.opts.ReceivingEnvironment {
			return batchChildResult{reason: "environment_skipped"}
		}
	}
	if n.registry == nil {
		return batchChildResult{reason: "environment_denied"}
	}
	binding, err := n.registry.Admit(envName, n.opts.Profile)
	if err != nil {
		return batchChildResult{reason: "environment_denied"}
	}
	if n.opts.Mode == "dedicated" && binding.Name != n.opts.ReceivingEnvironment {
		return batchChildResult{reason: "environment_skipped"}
	}
	eventID, ok := batchID(body, n.opts.EventIDPath)
	if !ok {
		return batchChildResult{reason: "id_invalid"}
	}
	group := eventID
	if n.opts.GroupIDPath != "" {
		if v, found := transform.GetPath(body, n.opts.GroupIDPath); found && v != nil && !batchEmptyString(v) {
			var valid bool
			group, valid = batchID(body, n.opts.GroupIDPath)
			if !valid {
				return batchChildResult{reason: "id_invalid"}
			}
		}
	}
	wire, err := json.Marshal(body)
	if err != nil {
		return batchChildResult{reason: "child_invalid"}
	}
	// Authentication has already verified the original parent. Child serialization
	// is the sole broker/raw surface; the original bytes remain only in the parent.
	child := parent
	child.Body = body
	child.BodyFormat = "json"
	child.V.Raw.Body = wire
	child.V.OriginalJSON = nil // Child bytes have a different source from the signed parent.
	child.Publish = nil
	child.Upstream = nil
	child.Extra = nil
	child.Branch = ""
	child.MessageAttributes = nil
	child.Vars = maps.Clone(parent.Vars)
	if child.Vars == nil {
		child.Vars = map[string]string{}
	}
	child.Vars["route"] = binding.Name
	child.Vars["route_prefix"] = binding.DestinationPrefix
	child.Vars["route_backend_host"] = binding.BackendHost
	child.Vars["route_account_id"] = binding.AccountID
	child.Vars["route_credential_mode"] = binding.CredentialMode
	child.V.Raw.Head.EventID = eventID
	child.Vars["batch_index"] = strconv.Itoa(index)
	child.Vars["batch_group_id"] = group
	child.Vars["batch_event_id"] = eventID
	if captured, ok := parent.Extra.(CapturedEvent); ok {
		event := captured.Event
		event.ID = eventID
		event.Body = body
		event.RawBody = wire
		event.RawBodyBase64 = nil
		event.Variables = child.Vars
		child.Extra = CapturedEvent{Event: event}
	}
	term, err := n.child.RunChild(ctx, child, run)
	result := batchChildResult{reason: "filtered", deliveries: term.Deliveries}
	if err != nil {
		result.reason = "child_failed"
	}
	// A successful arrival cannot erase another arrival's failure. Counts stay
	// per child event even when its shared publish node executes several times.
	published, unattempted, failed := false, false, false
	for _, d := range term.Deliveries {
		switch {
		case d.Unattempted:
			unattempted = true
		case d.Err != nil:
			failed = true
		default:
			published = true
		}
	}
	switch {
	case failed:
		result.reason = "publish_failed"
	case err != nil || unattempted:
		result.reason = "child_failed"
	case published:
		result.reason = "published"
	}
	return result
}
func batchID(body any, path string) (string, bool) {
	v, ok := transform.GetPath(body, path)
	if !ok {
		return "", false
	}
	var s string
	switch id := v.(type) {
	case string:
		s = id
	case json.Number:
		s = id.String()
	case float64:
		s = strconv.FormatFloat(id, 'f', -1, 64)
	default:
		return "", false
	}
	if len(s) == 0 || len(s) > 128 {
		return "", false
	}
	for _, r := range s {
		if r < 33 || r > 126 {
			return "", false
		}
	}
	return s, true
}
func mustDuration(s string) time.Duration { d, _ := time.ParseDuration(s); return d }

// ExpandedNodes exposes a bounded child definition to destination validation,
// serving metadata and catalogs. It never follows recursive references.
func ExpandedNodes(fc graph.FlowConfig) []graph.NodeConfig {
	out := append([]graph.NodeConfig(nil), fc.Nodes...)
	for _, nc := range fc.Nodes {
		if nc.Type == "batch" {
			o, err := decodeOptions[BatchOptions](nc)
			if err == nil {
				child, err := batchChildConfig(o)
				if err == nil {
					out = append(out, child.Nodes...)
				}
			}
		}
	}
	return out
}

func batchEmptyString(v any) bool { s, ok := v.(string); return ok && s == "" }
