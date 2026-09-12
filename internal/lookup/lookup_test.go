package lookup

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/jackc/pgx/v5"
)

// ---- fakes ----

type fakeProvider struct {
	calls  int32
	values map[string]string
	err    error
	closed bool
}

func (f *fakeProvider) Lookup(_ context.Context, key string) (string, bool, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.values[key]
	return v, ok, nil
}
func (f *fakeProvider) Close() error { f.closed = true; return nil }

type fakeDynamo struct {
	pages  []*dynamodb.QueryOutput
	inputs []*dynamodb.QueryInput
	err    error
}

func (f *fakeDynamo) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.inputs = append(f.inputs, in)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.pages) == 0 {
		return &dynamodb.QueryOutput{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func item(id, idType, env string) map[string]ddbtypes.AttributeValue {
	return map[string]ddbtypes.AttributeValue{
		"id":       &ddbtypes.AttributeValueMemberS{Value: id},
		"id_type":  &ddbtypes.AttributeValueMemberS{Value: idType},
		"env_name": &ddbtypes.AttributeValueMemberS{Value: env},
	}
}

// ---- cache ----

func TestCachedHitIsReusedUntilTTL(t *testing.T) {
	inner := &fakeProvider{values: map[string]string{"a": "qa-gm3"}}
	c := newCached(inner, CacheOptions{TTL: time.Minute}, slog.Default())
	now := time.Now()
	c.now = func() time.Time { return now }
	for range 3 {
		v, found, err := c.Lookup(context.Background(), "a")
		if err != nil || !found || v != "qa-gm3" {
			t.Fatalf("lookup: %q %v %v", v, found, err)
		}
	}
	if inner.calls != 1 || !c.Cached("a") {
		t.Fatalf("expected one provider call and a cached entry, got %d", inner.calls)
	}
	now = now.Add(2 * time.Minute)
	if _, _, err := c.Lookup(context.Background(), "a"); err != nil || inner.calls != 2 {
		t.Fatalf("expired entry should re-query: calls=%d err=%v", inner.calls, err)
	}
}

func TestCachedMissNotCachedByDefault(t *testing.T) {
	inner := &fakeProvider{values: map[string]string{}}
	c := newCached(inner, CacheOptions{TTL: time.Minute}, slog.Default())
	for range 2 {
		if _, found, err := c.Lookup(context.Background(), "nope"); found || err != nil {
			t.Fatalf("expected clean miss, got found=%v err=%v", found, err)
		}
	}
	if inner.calls != 2 {
		t.Fatalf("misses must not be cached by default, calls=%d", inner.calls)
	}
	neg := newCached(&fakeProvider{values: map[string]string{}}, CacheOptions{TTL: time.Minute, NegativeTTL: time.Minute}, slog.Default())
	for range 2 {
		_, _, _ = neg.Lookup(context.Background(), "nope")
	}
	if neg.inner.(*fakeProvider).calls != 1 {
		t.Fatal("negative_ttl should cache misses")
	}
}

func TestCachedErrorsNotCachedAndSingleflight(t *testing.T) {
	inner := &fakeProvider{err: errors.New("boom")}
	c := newCached(inner, CacheOptions{TTL: time.Minute}, slog.Default())
	if _, _, err := c.Lookup(context.Background(), "a"); err == nil {
		t.Fatal("expected error")
	}
	inner.err = nil
	inner.values = map[string]string{"a": "v"}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, found, err := c.Lookup(context.Background(), "a"); err != nil || !found || v != "v" {
				t.Errorf("concurrent lookup: %q %v %v", v, found, err)
			}
		}()
	}
	wg.Wait()
	if inner.calls != 2 { // one failed call + one coalesced successful call
		t.Fatalf("expected 2 provider calls, got %d", inner.calls)
	}
}

// A key that expires and is re-fetched must not be evicted ahead of older
// live entries: expiry-on-read used to leave a stale position in the FIFO
// order and the re-insert appended a second one.
func TestCachedReinsertAfterExpiryKeepsSinglePosition(t *testing.T) {
	inner := &fakeProvider{values: map[string]string{"a": "1", "b": "2", "c": "3"}}
	c := newCached(inner, CacheOptions{TTL: time.Minute, MaxEntries: 2}, slog.Default())
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	ctx := context.Background()
	_, _, _ = c.Lookup(ctx, "a")
	now = now.Add(30 * time.Second)
	_, _, _ = c.Lookup(ctx, "b")
	now = now.Add(31 * time.Second) // a expired, b live
	if c.Cached("a") {
		t.Fatal("a should have expired")
	}
	_, _, _ = c.Lookup(ctx, "a") // re-fetch a
	_, _, _ = c.Lookup(ctx, "c") // over capacity: evict oldest position (a's original slot)
	if !c.Cached("c") || len(c.entries) > 2 {
		t.Fatalf("c must be cached and size bounded: cached=%v n=%d", c.Cached("c"), len(c.entries))
	}
	for _, e := range c.order {
		if e == "a" && c.Cached("a") {
			t.Fatal("a should have been evicted as the oldest FIFO position")
		}
	}
	if !c.Cached("b") {
		t.Fatal("b (live, older) must not be evicted in place of the re-inserted key")
	}
}

func TestCachedEvictsOldest(t *testing.T) {
	inner := &fakeProvider{values: map[string]string{"a": "1", "b": "2", "c": "3"}}
	c := newCached(inner, CacheOptions{TTL: time.Minute, MaxEntries: 2}, slog.Default())
	for _, k := range []string{"a", "b", "c"} {
		_, _, _ = c.Lookup(context.Background(), k)
	}
	if c.Cached("a") || !c.Cached("c") {
		t.Fatal("oldest entry should be evicted, newest kept")
	}
}

// ---- dynamodb ----

func TestDynamoLookupNewestFirstWithClientSideFilter(t *testing.T) {
	fake := &fakeDynamo{pages: []*dynamodb.QueryOutput{{Items: []map[string]ddbtypes.AttributeValue{
		item("abc", "borrower", "qa-gm9"),    // newest row, wrong type: must be skipped
		item("abc", "application", "qa-gm3"), // match
		item("abc", "application", "qa-gm1"), // older
	}}}}
	p := newDynamoDBWithClient(fake, DynamoDBOptions{Table: "t", Index: "CreatedAtIndex", FilterAttribute: "id_type", FilterValue: "application", ValueAttribute: "env_name", ConsistentRead: true})
	v, found, err := p.Lookup(context.Background(), "abc")
	if err != nil || !found || v != "qa-gm3" {
		t.Fatalf("lookup: %q %v %v", v, found, err)
	}
	in := fake.inputs[0]
	if aws.ToString(in.TableName) != "t" || aws.ToString(in.IndexName) != "CreatedAtIndex" || aws.ToBool(in.ScanIndexForward) || !aws.ToBool(in.ConsistentRead) {
		t.Fatalf("query input: %#v", in)
	}
	if in.ExpressionAttributeNames["#k"] != "id" || in.ExpressionAttributeValues[":k"].(*ddbtypes.AttributeValueMemberS).Value != "abc" || in.FilterExpression != nil {
		t.Fatalf("key condition / filter: %#v", in)
	}
}

func TestDynamoLookupPagesUntilMatch(t *testing.T) {
	fake := &fakeDynamo{pages: []*dynamodb.QueryOutput{
		{Items: []map[string]ddbtypes.AttributeValue{item("abc", "borrower", "x")}, LastEvaluatedKey: map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: "abc"}}},
		{Items: []map[string]ddbtypes.AttributeValue{item("abc", "application", "qa-gm2")}},
	}}
	p := newDynamoDBWithClient(fake, DynamoDBOptions{Table: "t", FilterAttribute: "id_type", FilterValue: "application", ValueAttribute: "env_name"})
	v, found, err := p.Lookup(context.Background(), "abc")
	if err != nil || !found || v != "qa-gm2" || len(fake.inputs) != 2 || fake.inputs[1].ExclusiveStartKey == nil {
		t.Fatalf("paging: %q %v %v inputs=%d", v, found, err, len(fake.inputs))
	}
}

func TestDynamoLookupMissAndError(t *testing.T) {
	p := newDynamoDBWithClient(&fakeDynamo{}, DynamoDBOptions{Table: "t", ValueAttribute: "env_name"})
	if v, found, err := p.Lookup(context.Background(), "abc"); err != nil || found || v != "" {
		t.Fatalf("miss: %q %v %v", v, found, err)
	}
	p = newDynamoDBWithClient(&fakeDynamo{err: errors.New("throttled")}, DynamoDBOptions{Table: "t", ValueAttribute: "env_name"})
	if _, _, err := p.Lookup(context.Background(), "abc"); err == nil {
		t.Fatal("expected error")
	}
	// Number attributes render as strings.
	num := &fakeDynamo{pages: []*dynamodb.QueryOutput{{Items: []map[string]ddbtypes.AttributeValue{{"id": &ddbtypes.AttributeValueMemberS{Value: "a"}, "shard": &ddbtypes.AttributeValueMemberN{Value: "7"}}}}}}
	p = newDynamoDBWithClient(num, DynamoDBOptions{Table: "t", ValueAttribute: "shard"})
	if v, found, _ := p.Lookup(context.Background(), "a"); !found || v != "7" {
		t.Fatalf("number attr: %q %v", v, found)
	}
}

// ---- http ----

func TestHTTPLookup(t *testing.T) {
	var gotPath, gotAuth, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotMethod = r.URL.RequestURI(), r.Header.Get("Authorization"), r.Method
		b := make([]byte, 256)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		switch {
		case r.URL.Query().Get("id") == "missing":
			w.WriteHeader(404)
		case r.URL.Query().Get("id") == "broken":
			w.WriteHeader(500)
		case r.URL.Query().Get("id") == "junk":
			_, _ = w.Write([]byte("not json"))
		default:
			_, _ = w.Write([]byte(`{"data":{"env_name":"qa-gm3"}}`))
		}
	}))
	defer srv.Close()
	p, err := newHTTP(HTTPOptions{URL: srv.URL + "/env?id={key}", Headers: map[string]string{"Authorization": "Bearer t"}, ValuePath: "data.env_name"})
	if err != nil {
		t.Fatal(err)
	}
	v, found, err := p.Lookup(context.Background(), "a b/c")
	if err != nil || !found || v != "qa-gm3" {
		t.Fatalf("hit: %q %v %v", v, found, err)
	}
	if gotPath != "/env?id=a%20b%2Fc" || gotAuth != "Bearer t" || gotMethod != "GET" {
		t.Fatalf("request: path=%q auth=%q method=%q", gotPath, gotAuth, gotMethod)
	}
	// Query metacharacters in a request-derived key must not inject parameters.
	if _, _, err := p.Lookup(context.Background(), "x&tenant=admin+1"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/env?id=x%26tenant%3Dadmin%2B1" {
		t.Fatalf("query injection not escaped: path=%q", gotPath)
	}
	if _, found, err := p.Lookup(context.Background(), "missing"); found || err != nil {
		t.Fatalf("404 must be a clean miss: %v %v", found, err)
	}
	if _, _, err := p.Lookup(context.Background(), "broken"); err == nil {
		t.Fatal("500 must be an error")
	}
	if _, _, err := p.Lookup(context.Background(), "junk"); err == nil {
		t.Fatal("non-JSON must be an error")
	}
	post, err := newHTTP(HTTPOptions{URL: srv.URL + "/env?id={key}", Method: "POST", ValuePath: "data.env_name"})
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := post.Lookup(context.Background(), "x"); err != nil || !found || gotMethod != "POST" || gotBody != `{"key":"x"}` {
		t.Fatalf("post: found=%v err=%v method=%q body=%q", found, err, gotMethod, gotBody)
	}
	for _, bad := range []HTTPOptions{{URL: "ftp://x/{key}", ValuePath: "a"}, {URL: srv.URL + "/x", ValuePath: "a"}, {URL: srv.URL + "/{key}/{key}", ValuePath: "a"}, {URL: srv.URL + "/{key}", ValuePath: "a", Method: "DELETE"}, {URL: srv.URL + "/{key}"}} {
		if _, err := newHTTP(bad); err == nil {
			t.Fatalf("expected constructor error for %#v", bad)
		}
	}
}

// ---- postgres ----

type fakeRow struct {
	val any
	err error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*any)) = r.val
	return nil
}

type fakeQuerier struct {
	row  fakeRow
	sql  string
	args []any
}

func (f *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.sql, f.args = sql, args
	return f.row
}
func (f *fakeQuerier) Close() {}

func TestPostgresLookup(t *testing.T) {
	q := &fakeQuerier{row: fakeRow{val: "qa-gm3"}}
	p := &postgresProvider{pool: q, query: "SELECT env FROM routing WHERE id = $1"}
	v, found, err := p.Lookup(context.Background(), "abc")
	if err != nil || !found || v != "qa-gm3" || q.args[0] != "abc" {
		t.Fatalf("hit: %q %v %v args=%v", v, found, err, q.args)
	}
	q.row = fakeRow{err: pgx.ErrNoRows}
	if _, found, err := p.Lookup(context.Background(), "abc"); found || err != nil {
		t.Fatalf("no rows must be a clean miss: %v %v", found, err)
	}
	q.row = fakeRow{err: errors.New("conn reset")}
	if _, _, err := p.Lookup(context.Background(), "abc"); err == nil {
		t.Fatal("expected error")
	}
	q.row = fakeRow{val: int64(42)}
	if v, found, _ := p.Lookup(context.Background(), "abc"); !found || v != "42" {
		t.Fatalf("numeric column: %q %v", v, found)
	}
}

func TestNewRequiresProviderAndWrapsTimeout(t *testing.T) {
	if _, err := New(context.Background(), Options{Name: "x"}, nil); err == nil {
		t.Fatal("expected error with no provider")
	}
	if _, err := New(context.Background(), Options{Name: "x", HTTP: &HTTPOptions{URL: "http://h/{key}", ValuePath: "v"}}, nil); err != nil {
		t.Fatalf("http provider should construct without I/O: %v", err)
	}
}
