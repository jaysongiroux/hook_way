package flowconfig

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestReadBodyRejectsOversizeBody(t *testing.T) {
	reg, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("read_body")
	node, err := spec.New(nodeConfigFromYAML(t, "read_body", "max_bytes: 10\n"))
	if err != nil {
		t.Fatal(err)
	}

	ctx := WithBodyReader(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
	_, err = node.Run(ctx, graph.RequestHead{})
	if err == nil {
		t.Fatal("Run() = nil for a 100-byte body with max_bytes: 10")
	}
	if !strings.Contains(err.Error(), "10") {
		t.Errorf("error %q should name the limit so the operator can raise it", err)
	}
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != 413 {
		t.Errorf("status = %+v, want 413 NodeError", err)
	}
}

func TestReadBodyAcceptsBodyAtLimit(t *testing.T) {
	reg, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("read_body")
	node, err := spec.New(nodeConfigFromYAML(t, "read_body", "max_bytes: 4\n"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithBodyReader(context.Background(), strings.NewReader("abcd"))
	out, err := node.Run(ctx, graph.RequestHead{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	raw := out.(graph.RawRequest)
	if string(raw.Body) != "abcd" {
		t.Errorf("body = %q", raw.Body)
	}
}

func TestReadBodyRejectsEmptyAndMissingReader(t *testing.T) {
	reg, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("read_body")
	node, err := spec.New(nodeConfigFromYAML(t, "read_body", "max_bytes: 10\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.Run(WithBodyReader(context.Background(), strings.NewReader("")), graph.RequestHead{}); err == nil {
		t.Fatal("empty body accepted")
	}
	if _, err := node.Run(context.Background(), graph.RequestHead{}); err == nil {
		t.Fatal("missing reader accepted")
	}
}

func TestReadBodyAllowEmptyIsExplicitAndStillBounded(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		for _, size := range []int{0, 3, 4, 5} {
			node := readBodyNode{id: "read", maxBytes: 4, allowEmpty: true}
			ctx := WithBodyReader(context.Background(), strings.NewReader(strings.Repeat("x", size)))
			out, err := node.Run(ctx, graph.RequestHead{Method: method})
			if (err != nil) != (size > 4) {
				t.Fatalf("%s size %d: %v", method, size, err)
			}
			if err == nil && len(out.(graph.RawRequest).Body) != size {
				t.Fatal("body changed")
			}
		}
	}
}
