package secretref

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

type fakeFetcher struct {
	mu  sync.Mutex
	v   []byte
	err error
	n   atomic.Int32
}

func (f *fakeFetcher) Fetch(context.Context) ([]byte, error) {
	f.n.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return append([]byte(nil), f.v...), nil
}

func (f *fakeFetcher) set(v []byte, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.v = append([]byte(nil), v...)
	f.err = err
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func mustCached(t *testing.T, fetch fetcher, refresh, overlap time.Duration, now func() time.Time) *cached {
	t.Helper()
	c, err := newCached(Ref{Provider: "env", Name: "TEST", Refresh: refresh, Overlap: overlap}, fetch, slog.New(slog.DiscardHandler), nil, now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestEnvResolverMatchesGetenv(t *testing.T) {
	t.Setenv("HOOKWAY_SECRETREF_TEST", "exact-v1-value")
	r, err := New(Ref{Provider: "env", Name: "HOOKWAY_SECRETREF_TEST", Refresh: time.Hour}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	got, err := r.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !bytes.Equal(got[0], []byte("exact-v1-value")) {
		t.Fatalf("Secrets() = %q, want the env value (V1 ${VAR} equivalent)", got)
	}
}

func TestFileResolverReadsContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := New(Ref{Provider: "file", Name: path, Refresh: time.Hour}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	got, err := r.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !bytes.Equal(got[0], []byte("from-file")) {
		t.Fatalf("Secrets() = %q, want trimmed file contents", got)
	}
}

func TestNewRejectsMissingEnv(t *testing.T) {
	_, err := New(Ref{Provider: "env", Name: "HOOKWAY_SECRETREF_MISSING_" + t.Name(), Refresh: time.Hour}, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing env")
	}
}

// TestRotationHasNoFlagDay proves the overlap window works: a sender still
// signing with the previous secret must be accepted throughout the overlap,
// so rotation never drops traffic. V1 cannot do this -- one secret per
// endpoint means rotation is a flag day (W3).
func TestRotationHasNoFlagDay(t *testing.T) {
	clk := &clock{t: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)}
	fetch := &fakeFetcher{v: []byte("old")}
	c := mustCached(t, fetch, time.Hour, 10*time.Minute, clk.Now)

	assertContains := func(want string, ok bool) {
		t.Helper()
		got, err := c.Secrets(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, s := range got {
			if bytes.Equal(s, []byte(want)) {
				found = true
			}
		}
		if found != ok {
			t.Fatalf("secret %q present=%v, want %v (secrets=%q)", want, found, ok, got)
		}
	}

	assertContains("old", true)
	assertContains("new", false)

	fetch.set([]byte("new"), nil)
	c.refreshOnce(context.Background())
	assertContains("old", true)
	assertContains("new", true)

	clk.advance(10*time.Minute - time.Second)
	assertContains("old", true)
	assertContains("new", true)

	clk.advance(2 * time.Second)
	assertContains("old", false)
	assertContains("new", true)
}

func TestRefreshFailureKeepsLastGoodValue(t *testing.T) {
	var fails atomic.Int32
	fetch := &fakeFetcher{v: []byte("good")}
	c, err := newCached(Ref{Provider: "env", Name: "X", Refresh: time.Hour, Overlap: time.Minute}, fetch, slog.New(slog.DiscardHandler), func() { fails.Add(1) }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	fetch.set(nil, errors.New("secrets manager blip"))
	c.refreshOnce(context.Background())
	got, err := c.Secrets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !bytes.Equal(got[0], []byte("good")) {
		t.Fatalf("after failed refresh Secrets() = %q, want last good value", got)
	}
	if fails.Load() != 1 {
		t.Fatalf("onFail calls = %d, want 1", fails.Load())
	}
}

func TestSecretsNeverCallsFetcher(t *testing.T) {
	fetch := &fakeFetcher{v: []byte("v")}
	c := mustCached(t, fetch, time.Hour, 0, time.Now)
	before := fetch.n.Load()
	for i := 0; i < 50; i++ {
		if _, err := c.Secrets(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if fetch.n.Load() != before {
		t.Fatalf("Secrets() fetched from the backing store (%d calls); must be cache-only", fetch.n.Load()-before)
	}
}

func TestConcurrentSecretsAndRefresh(t *testing.T) {
	fetch := &fakeFetcher{v: []byte("a")}
	c := mustCached(t, fetch, time.Hour, time.Minute, time.Now)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, err := c.Secrets(context.Background()); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			fetch.set([]byte{byte('a' + i%2)}, nil)
			c.refreshOnce(context.Background())
		}
	}()
	wg.Wait()
}

func TestAWSFetcherTakesNewestListedSecret(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	fake := &fakeSecretsAPI{
		list: []listedSecret{
			{id: "old", changed: old},
			{id: "new", changed: newer},
		},
		values: map[string][]byte{"old": []byte("previous"), "new": []byte("current")},
	}
	f := &awsFetcher{client: fake, name: "prod-lambdas"}
	got, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("current")) {
		t.Fatalf("Fetch() = %q, want newest listed secret", got)
	}
	if fake.listCalls != 1 {
		t.Fatalf("ListSecrets calls = %d, want 1", fake.listCalls)
	}
}

type fakeSecretsAPI struct {
	list      []listedSecret
	values    map[string][]byte
	listCalls int
	pages     [][]listedSecret // optional pagination
}

func (f *fakeSecretsAPI) ListSecrets(context.Context, *secretsmanager.ListSecretsInput, ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	f.listCalls++
	out := &secretsmanager.ListSecretsOutput{}
	for _, s := range f.list {
		changed := s.changed
		out.SecretList = append(out.SecretList, smtypes.SecretListEntry{
			Name:            aws.String(s.id),
			LastChangedDate: &changed,
		})
	}
	return out, nil
}

func (f *fakeSecretsAPI) GetSecretValue(_ context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	v, ok := f.values[aws.ToString(in.SecretId)]
	if !ok {
		return nil, errors.New("not found")
	}
	s := string(v)
	return &secretsmanager.GetSecretValueOutput{SecretString: &s}, nil
}
