package flowconfig

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

// catalogDestinationTypes is the destination catalog Factory.Create must stay
// in lockstep with. A new destination type that is not listed here has no
// publish coverage.
var catalogDestinationTypes = []string{
	"rabbitmq", "sqs", "sns", "s3", "lambda", "redis_stream", "redis_list",
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
	for _, dt := range catalogDestinationTypes {
		t.Run(dt, func(t *testing.T) {
			if !strings.Contains(src, `"`+dt+`"`) {
				t.Fatalf("internal/queue/factory.go does not mention %q; publish cannot deliver it", dt)
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
