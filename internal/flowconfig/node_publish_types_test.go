package flowconfig

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jasongiroux/hookway/internal/graph"
)

// v2DestinationTypes is the catalog Chunk 5 must keep in lockstep with
// internal/queue.Factory.Create. A new destination type that is not listed
// here has no V2 coverage.
var v2DestinationTypes = []string{
	"rabbitmq", "sqs", "sns", "s3", "redis_stream", "redis_list",
	"redis_pubsub", "redis_kv", "postgres", "mongodb", "kafka", "webhook", "temporal",
}

func TestPublishSupportsEveryDestinationType(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	factorySrc, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "queue", "factory.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(factorySrc)
	for _, dt := range v2DestinationTypes {
		t.Run(dt, func(t *testing.T) {
			if !strings.Contains(src, `"`+dt+`"`) {
				t.Fatalf("internal/queue/factory.go does not mention %q; V2 publish cannot deliver it", dt)
			}
			fake := &fakePublisher{}
			node := mustPublishNode(t, "destination: "+dt+"\ntarget: t\n", staticResolver{pub: fake})
			if _, err := node.Run(context.Background(), graph.Envelope{}); err != nil {
				t.Fatalf("publish via resolver: %v", err)
			}
			if fake.lastTarget != "t" {
				t.Fatalf("published target = %q", fake.lastTarget)
			}
		})
	}
	if !bytes.Contains(factorySrc, []byte(`default:`)) || !bytes.Contains(factorySrc, []byte("unsupported destinations.")) {
		t.Fatal("factory.Create must keep a default case so unknown types fail closed")
	}
}
