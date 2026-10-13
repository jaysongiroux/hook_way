package routingstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	types "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/jaysongiroux/hook_way/internal/routing"
)

type fakeAPI struct {
	mu     sync.Mutex
	items  map[routing.Key]map[string]types.AttributeValue
	getErr error
	gets   int
}

func (f *fakeAPI) GetItem(ctx context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if !aws.ToBool(in.ConsistentRead) {
		panic("not consistent")
	}
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded read")
	}
	k := routing.Key{ID: in.Key["id"].(*types.AttributeValueMemberS).Value, IDType: in.Key["id_type"].(*types.AttributeValueMemberS).Value}
	return &dynamodb.GetItemOutput{Item: f.items[k]}, f.getErr
}
func (f *fakeAPI) PutItem(ctx context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := routing.Key{ID: in.Item["id"].(*types.AttributeValueMemberS).Value, IDType: in.Item["id_type"].(*types.AttributeValueMemberS).Value}

	f.items[k] = in.Item
	return &dynamodb.PutItemOutput{}, nil
}
func fakeStore() (*Store, *fakeAPI) {
	f := &fakeAPI{items: map[routing.Key]map[string]types.AttributeValue{}}
	return &Store{client: f, opts: (routing.StoreConfig{Table: "routing"}).Defaults(), timeout: time.Second}, f
}
func TestExactTypedReadsImmediateWritesNoCache(t *testing.T) {
	s, f := fakeStore()
	ctx := context.Background()
	a := routing.Key{ID: "same:id", IDType: "record_a"}
	b := routing.Key{ID: "same:id", IDType: "record_b"}
	if _, found, err := s.Get(ctx, a); err != nil || found {
		t.Fatal(found, err)
	}
	for _, r := range []routing.Record{{Key: a, Environment: "preview", CreatedAt: 1}, {Key: b, Environment: "production", CreatedAt: 2}} {
		if err := s.Put(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	r, found, err := s.Get(ctx, a)
	if err != nil || !found || r.Environment != "preview" {
		t.Fatal(r, found, err)
	}
	if err := s.Put(ctx, routing.Record{Key: a, Environment: "new-env", CreatedAt: 3}); err != nil {
		t.Fatal(err)
	}
	r, _, _ = s.Get(ctx, a)
	if r.Environment != "new-env" || r.CreatedAt != 3 || f.gets != 3 {
		t.Fatal(r, f.gets)
	}
	f.getErr = errors.New("outage")
	if _, found, err := s.Get(ctx, a); found || err == nil {
		t.Fatal("outage became miss")
	}
}
func TestConcurrentLastWriteWinsTypedRegistration(t *testing.T) {
	s, _ := fakeStore()
	var w sync.WaitGroup
	for i := 0; i < 30; i++ {
		w.Add(1)
		go func() {
			defer w.Done()
			if err := s.Put(context.Background(), routing.Record{Key: routing.Key{ID: "a", IDType: "record_a"}, Environment: "preview"}); err != nil {
				t.Error(err)
			}
		}()
	}
	w.Wait()
	r, found, err := s.Get(context.Background(), routing.Key{ID: "a", IDType: "record_a"})
	if err != nil || !found || r.Environment != "preview" {
		t.Fatal(r, found, err)
	}
}
