package routing

import (
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{Profiles: map[string]Profile{"sandbox": {AccountID: "000000000000", CredentialMode: "sandbox"}, "production": {AccountID: "111111111111", CredentialMode: "production"}}, Environments: map[string]Environment{"preview": {Aliases: []string{"staging"}, Profiles: []string{"sandbox"}, Accounts: []string{"000000000000"}, DestinationPrefix: "preview", BackendHost: "preview.example.test"}, "production": {Aliases: []string{"live"}, Profiles: []string{"production"}, Accounts: []string{"111111111111"}, DestinationPrefix: "prod", BackendHost: "api.example.org"}}}
}
func TestAdmissionAndAtomicRevocation(t *testing.T) {
	c := testConfig()
	r, err := NewRegistry(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, profile, want string }{{"staging", "sandbox", "preview"}, {"live", "production", "production"}, {"live", "sandbox", ""}, {"safe-but-unknown", "sandbox", ""}, {"preview", "unknown", ""}} {
		b, err := r.Admit(tc.name, tc.profile)
		if tc.want == "" {
			if err == nil {
				t.Fatalf("admitted %s", tc.name)
			}
		} else if err != nil || b.Name != tc.want {
			t.Fatal(b, err)
		}
	}
	// Input maps and slices do not mutate an installed snapshot.
	e := c.Environments["preview"]
	e.Profiles[0] = "production"
	c.Environments["preview"] = e
	if _, err := r.Admit("preview", "sandbox"); err != nil {
		t.Fatal("registry aliased caller config")
	}
	c = testConfig()
	delete(c.Environments, "preview")
	if err := r.Replace(c); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Admit("staging", "sandbox"); err == nil {
		t.Fatal("revoked alias admitted")
	}
	bad := testConfig()
	e = bad.Environments["preview"]
	e.Aliases = []string{"live"}
	bad.Environments["preview"] = e
	if r.Replace(bad) == nil {
		t.Fatal("alias collision accepted")
	}
	if _, err := r.Admit("production", "production"); err != nil {
		t.Fatal("invalid replacement corrupted registry")
	}
}
func TestConcurrentAdmissionReplace(t *testing.T) {
	r, _ := NewRegistry(testConfig())
	var w sync.WaitGroup
	for i := 0; i < 10; i++ {
		w.Add(1)
		go func() {
			defer w.Done()
			for j := 0; j < 100; j++ {
				_, _ = r.Admit("preview", "sandbox")
				if err := r.Replace(testConfig()); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	w.Wait()
}

func TestRejectsWrongAccountMalformedHostAndKey(t *testing.T) {
	c := testConfig()
	e := c.Environments["preview"]
	e.Accounts = []string{"111111111111"}
	c.Environments["preview"] = e
	if _, err := NewRegistry(c); err == nil {
		t.Fatal("profile outside account boundary")
	}
	c = testConfig()
	e = c.Environments["preview"]
	e.BackendHost = "example.test/escape"
	c.Environments["preview"] = e
	if _, err := NewRegistry(c); err == nil {
		t.Fatal("non-host accepted")
	}
	for _, k := range []Key{{ID: "", IDType: "record_a"}, {ID: "abc", IDType: ""}, {ID: "abc\x00", IDType: "record_a"}} {
		if ValidateKey(k) == nil {
			t.Fatal("bad key accepted")
		}
	}
}
