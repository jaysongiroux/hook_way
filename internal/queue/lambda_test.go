package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

type fakeLambda struct {
	functions map[string]bool
	invokes   []*lambda.InvokeInput
	invokeErr error
	getErr    map[string]error
	eventFail bool // when true, RequestResponse returns FunctionError
}

func newFakeLambda() *fakeLambda {
	return &fakeLambda{
		functions: map[string]bool{},
		getErr:    map[string]error{},
	}
}

func (f *fakeLambda) seed(name string) { f.functions[name] = true }

func (f *fakeLambda) Invoke(ctx context.Context, in *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	f.invokes = append(f.invokes, in)
	if f.invokeErr != nil {
		return nil, f.invokeErr
	}
	name := aws.ToString(in.FunctionName)
	if !f.functions[name] {
		return nil, fmt.Errorf("ResourceNotFoundException: function %q", name)
	}
	out := &lambda.InvokeOutput{StatusCode: 200, Payload: []byte(`{"ok":true}`)}
	if in.InvocationType == lambdatypes.InvocationTypeEvent {
		out.StatusCode = 202
		out.Payload = nil
	}
	if f.eventFail && in.InvocationType == lambdatypes.InvocationTypeRequestResponse {
		out.FunctionError = aws.String("Unhandled")
		out.Payload = []byte(`{"errorMessage":"boom"}`)
	}
	return out, nil
}

func (f *fakeLambda) GetFunction(ctx context.Context, in *lambda.GetFunctionInput, _ ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error) {
	name := aws.ToString(in.FunctionName)
	if err, ok := f.getErr[name]; ok {
		return nil, err
	}
	if !f.functions[name] {
		return nil, fmt.Errorf("ResourceNotFoundException: function %q", name)
	}
	return &lambda.GetFunctionOutput{}, nil
}

func TestLambdaPublisherEventInvoke(t *testing.T) {
	fake := newFakeLambda()
	fake.seed("orders-worker")
	pub := NewLambdaPublisher(fake, LambdaConfig{
		Region: "us-east-1", DestinationID: "workers",
		Targets: []LambdaTarget{{Name: "orders-worker"}},
	})
	if err := pub.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	msg := Message{ID: "evt_1", Body: map[string]any{"hello": "world"}}
	if err := pub.Publish(context.Background(), "workers", "orders-worker", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(fake.invokes) != 1 {
		t.Fatalf("invokes=%d", len(fake.invokes))
	}
	in := fake.invokes[0]
	if aws.ToString(in.FunctionName) != "orders-worker" {
		t.Fatalf("FunctionName=%q", aws.ToString(in.FunctionName))
	}
	if in.InvocationType != lambdatypes.InvocationTypeEvent {
		t.Fatalf("InvocationType=%q", in.InvocationType)
	}
	var got Message
	if err := json.Unmarshal(in.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "evt_1" {
		t.Fatalf("payload id=%q", got.ID)
	}
}

func TestLambdaPublisherRequestResponseFunctionError(t *testing.T) {
	fake := newFakeLambda()
	fake.seed("orders-worker")
	fake.eventFail = true
	pub := NewLambdaPublisher(fake, LambdaConfig{
		Region: "us-east-1", DestinationID: "workers",
		InvocationType: "RequestResponse",
		Targets:        []LambdaTarget{{Name: "orders-worker"}},
	})
	err := pub.Publish(context.Background(), "workers", "orders-worker", Message{ID: "evt_1"})
	if err == nil || !strings.Contains(err.Error(), "function error") {
		t.Fatalf("err=%v, want function error", err)
	}
}

func TestLambdaPublisherRejectsOversizeEventPayload(t *testing.T) {
	fake := newFakeLambda()
	fake.seed("orders-worker")
	pub := NewLambdaPublisher(fake, LambdaConfig{
		Region: "us-east-1", DestinationID: "workers",
		Targets: []LambdaTarget{{Name: "orders-worker"}},
	})
	// Build a body that pushes the marshaled Message over 256 KiB.
	big := strings.Repeat("x", lambdaEventPayloadMaxBytes)
	err := pub.Publish(context.Background(), "workers", "orders-worker", Message{ID: "evt_1", Body: big})
	if err == nil || !strings.Contains(err.Error(), "invoke limit") {
		t.Fatalf("err=%v, want size-limit error", err)
	}
	if len(fake.invokes) != 0 {
		t.Fatal("oversize payload must fail closed")
	}
}

func TestLambdaPublisherReadyProbesStaticOnly(t *testing.T) {
	fake := newFakeLambda()
	fake.seed("static-fn")
	pub := NewLambdaPublisher(fake, LambdaConfig{
		Region: "us-east-1", DestinationID: "workers",
		Targets: []LambdaTarget{
			{Name: "static-fn"},
			{Name: "{env}-fn", Template: mustMatcher("{env}-fn", map[string]string{"env": "^[a-z]+$"})},
		},
	})
	if err := pub.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	// Missing static target fails Ready.
	fake2 := newFakeLambda()
	pub2 := NewLambdaPublisher(fake2, LambdaConfig{
		Region: "us-east-1", DestinationID: "workers",
		Targets: []LambdaTarget{{Name: "missing"}},
	})
	if err := pub2.Ready(context.Background()); err == nil {
		t.Fatal("expected Ready error for missing function")
	}
}

func TestLambdaPublisherDynamicTarget(t *testing.T) {
	fake := newFakeLambda()
	fake.seed("prod-fn")
	pub := NewLambdaPublisher(fake, LambdaConfig{
		Region: "us-east-1", DestinationID: "workers",
		Targets: []LambdaTarget{{
			Name:     "{env}-fn",
			Template: mustMatcher("{env}-fn", map[string]string{"env": "^[a-z]+$"}),
		}},
	})
	if err := pub.Publish(context.Background(), "workers", "prod-fn", Message{ID: "evt_1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if aws.ToString(fake.invokes[0].FunctionName) != "prod-fn" {
		t.Fatalf("FunctionName=%q", aws.ToString(fake.invokes[0].FunctionName))
	}
	// Does not match {env}-fn (digits not allowed in env).
	err := pub.Publish(context.Background(), "workers", "prod-99-fn", Message{ID: "evt_2"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err=%v, want not configured", err)
	}
}

func TestLambdaPublisherQualifierAndARN(t *testing.T) {
	fake := newFakeLambda()
	name := "orders-worker:prod"
	arn := "arn:aws:lambda:us-east-1:123456789012:function:orders-worker"
	fake.seed(name)
	fake.seed(arn)
	pub := NewLambdaPublisher(fake, LambdaConfig{
		Region: "us-east-1", DestinationID: "workers",
		Targets: []LambdaTarget{{Name: name}, {Name: arn}},
	})
	if err := pub.Publish(context.Background(), "workers", name, Message{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "workers", arn, Message{ID: "b"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.invokes) != 2 {
		t.Fatalf("invokes=%d", len(fake.invokes))
	}
}

func TestLambdaPublisherDefaultInvocationTypeIsEvent(t *testing.T) {
	fake := newFakeLambda()
	fake.seed("fn")
	pub := NewLambdaPublisher(fake, LambdaConfig{
		Region: "us-east-1", Targets: []LambdaTarget{{Name: "fn"}},
	})
	_ = pub.Publish(context.Background(), "workers", "fn", Message{ID: "1"})
	if fake.invokes[0].InvocationType != lambdatypes.InvocationTypeEvent {
		t.Fatalf("got %q", fake.invokes[0].InvocationType)
	}
	// RequestResponse path records the type.
	fake2 := newFakeLambda()
	fake2.seed("fn")
	pub2 := NewLambdaPublisher(fake2, LambdaConfig{
		Region: "us-east-1", InvocationType: "RequestResponse",
		Targets: []LambdaTarget{{Name: "fn"}},
	})
	_ = pub2.Publish(context.Background(), "workers", "fn", Message{ID: "1"})
	if fake2.invokes[0].InvocationType != lambdatypes.InvocationTypeRequestResponse {
		t.Fatalf("got %q", fake2.invokes[0].InvocationType)
	}
}
