// Package marketingfacts contains no production code. It exists for the tests
// below, which pin the numbers and claims that the public marketing site makes
// to the code that actually produces them.
//
// web/src/marketing/productionCopy.ts quotes these numbers. A TypeScript test
// can check that the strings are internally consistent, but only Go can tell
// whether they are still true. If one of these fails, the site has started
// making a false claim: update the copy and the expectation here together.
//
// Every test here reads the copy file, and that is not incidental. The
// previous version of this file named web/src/marketing/stats.ts in its
// failure messages but never opened it. stats.ts was deleted in bf371ae and
// every test kept passing, because they only ever compared a Go count to a Go
// constant and mentioned the path in a string. A guardrail that never reads
// the thing it guards is not a guardrail -- it is a comment that costs CI
// time. TestProductionCopyFileExists fails loudly if the path moves again.
package marketingfacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/auth"
)

// The site files this package guards. These are read, not just named.
const (
	productionCopyPath = "web/src/marketing/productionCopy.ts"
	compareCopyPath    = "web/src/marketing/compareCopy.ts"
	featuresCopyPath   = "web/src/marketing/Features.tsx"
	destSchemaPath     = "web/src/graph/destinationSchema.json"
)

// claimCopyPaths are every file that makes a prose delivery claim. The
// Kafka dead-letter error was duplicated across two of these, so checking
// only the tile copy would have left the Features bento still lying.
var claimCopyPaths = []string{productionCopyPath, featuresCopyPath}

// readRepoFile reads a path relative to the repository root.
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(filepath.Join("..", "..", rel)))
	if err != nil {
		t.Fatalf("read %s: %v\n"+
			"This package exists to pin that file's claims to the code. If it moved, "+
			"re-point the path constant here; do not delete the test, or the guardrail "+
			"goes back to passing vacuously.", rel, err)
	}
	return string(data)
}

func uniqueMatches(t *testing.T, re *regexp.Regexp, src string) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		found[m[1]] = true
	}
	return found
}

// singleInt pulls exactly one integer capture out of src, failing with a
// message that says which symbol went missing. Matching on the symbol name
// rather than a line number survives the file being reordered, and fails
// loudly (rather than silently passing) if the symbol is renamed.
func singleInt(t *testing.T, src, path, pattern, symbol string) int {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%s: could not find %s. The marketing site quotes this value; "+
			"if the symbol was renamed, update the pattern here so the site stays pinned to it.",
			path, symbol)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("%s: %s is not an integer: %v", path, symbol, err)
	}
	return n
}

// requireTileValue asserts that some production tile headlines the given
// figure. The Go-side count being right is only half the claim; the other half
// is that the site is actually quoting it.
func requireTileValue(t *testing.T, copySrc, value, what string) {
	t.Helper()
	if !regexp.MustCompile(`value:\s*"` + regexp.QuoteMeta(value) + `"`).MatchString(copySrc) {
		t.Fatalf("the code says %s is %s, but no tile in %s headlines value: %q. "+
			"Either the tile was dropped or its number drifted.", what, value, productionCopyPath, value)
	}
}

func requireCopyContains(t *testing.T, copySrc, want, what string) {
	t.Helper()
	if !strings.Contains(copySrc, want) {
		t.Fatalf("the code says %s is %q, but %s does not mention it anywhere.",
			what, want, productionCopyPath)
	}
}

var stringLiteral = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

// copyClauses returns the sentence-level clauses of every string literal in
// the copy file. Claims have to be checked one clause at a time, not against
// the whole file: "Webhook, Kafka and RabbitMQ retry with backoff; only
// webhook honours a 429" is two separate claims with different subjects, and
// a whole-file substring check would see Kafka and 429 together and either
// pass wrongly or fail wrongly.
func copyClauses(src string) []string {
	var out []string
	for _, m := range stringLiteral.FindAllStringSubmatch(src, -1) {
		for _, clause := range regexp.MustCompile(`[.;](?:\s|$)`).Split(m[1], -1) {
			if clause = strings.TrimSpace(clause); clause != "" {
				out = append(out, clause)
			}
		}
	}
	return out
}

// clausesMentioning returns the clauses matching re, and fails if there are
// none -- so that softening the copy into silence cannot quietly disarm the
// assertion that follows.
func clausesMentioning(t *testing.T, re *regexp.Regexp, what string) map[string][]string {
	t.Helper()
	hits := map[string][]string{}
	total := 0
	for _, path := range claimCopyPaths {
		for _, c := range copyClauses(readRepoFile(t, path)) {
			if re.MatchString(c) {
				hits[path] = append(hits[path], c)
				total++
			}
		}
	}
	if total == 0 {
		t.Fatalf("no marketing copy in %v says anything about %s. If the claim was removed on "+
			"purpose, remove this test with it; leaving it here makes it pass vacuously.",
			claimCopyPaths, what)
	}
	return hits
}

// TestProductionCopyFileExists is the backstop for the failure mode described
// in the package comment: it asserts the guarded file is really there and
// really is the copy file, so the rest of this package cannot go back to
// grading its own homework.
func TestProductionCopyFileExists(t *testing.T) {
	src := readRepoFile(t, productionCopyPath)
	if !strings.Contains(src, "PRODUCTION_CARDS") {
		t.Fatalf("%s exists but does not export PRODUCTION_CARDS; the tests in this package "+
			"are pinning the wrong file.", productionCopyPath)
	}
	if len(copyClauses(src)) < 10 {
		t.Fatalf("%s parsed to %d clauses, which is too few to be the real copy file.",
			productionCopyPath, len(copyClauses(src)))
	}
}

func TestQuotedInflightCeilingMatchesConfigDefault(t *testing.T) {
	const quoted = "256"
	src := readRepoFile(t, "internal/config/config.go")
	got := singleInt(t, src, "internal/config/config.go",
		`defaultMaxInflightRequests\s*=\s*(\d+)`, "defaultMaxInflightRequests")
	if strconv.Itoa(got) != quoted {
		t.Fatalf("internal/config defaults max_inflight_requests to %d but %s quotes %s",
			got, productionCopyPath, quoted)
	}
	requireTileValue(t, readRepoFile(t, productionCopyPath), quoted, "the default in-flight ceiling")
}

func TestQuotedWebhookRetryDefaultsMatchPublisher(t *testing.T) {
	const (
		quotedAttempts = "3"
		quotedInitial  = "500ms"
		quotedMax      = "30s"
	)
	src := readRepoFile(t, "internal/queue/webhook.go")
	copySrc := readRepoFile(t, productionCopyPath)

	attempts := singleInt(t, src, "internal/queue/webhook.go",
		`retryMaxAttempts\s*=\s*(\d+)`, "the retryMaxAttempts default")
	if strconv.Itoa(attempts) != quotedAttempts {
		t.Fatalf("internal/queue/webhook.go retries %d times but %s quotes %s",
			attempts, productionCopyPath, quotedAttempts)
	}
	requireTileValue(t, copySrc, quotedAttempts, "the webhook retry attempt count")

	initialMS := singleInt(t, src, "internal/queue/webhook.go",
		`retryInitialBackoff\s*=\s*(\d+)\s*\*\s*time\.Millisecond`, "the retryInitialBackoff default")
	if got := strconv.Itoa(initialMS) + "ms"; got != quotedInitial {
		t.Fatalf("internal/queue/webhook.go starts backoff at %s but %s quotes %s",
			got, productionCopyPath, quotedInitial)
	}
	requireCopyContains(t, copySrc, quotedInitial, "the initial webhook backoff")

	maxSec := singleInt(t, src, "internal/queue/webhook.go",
		`retryMaxBackoff\s*=\s*(\d+)\s*\*\s*time\.Second`, "the retryMaxBackoff default")
	if got := strconv.Itoa(maxSec) + "s"; got != quotedMax {
		t.Fatalf("internal/queue/webhook.go caps backoff at %s but %s quotes %s",
			got, productionCopyPath, quotedMax)
	}
	requireCopyContains(t, copySrc, quotedMax, "the maximum webhook backoff")
}

func TestQuotedMetricCountMatchesDeclarations(t *testing.T) {
	const quoted = "45"
	src := readRepoFile(t, "internal/metrics/metrics.go")
	names := uniqueMatches(t, regexp.MustCompile(`"(hookway_[a-z0-9_]+)"`), src)
	if strconv.Itoa(len(names)) != quoted {
		t.Fatalf("internal/metrics/metrics.go declares %d distinct metrics but %s quotes %s",
			len(names), productionCopyPath, quoted)
	}
	requireTileValue(t, readRepoFile(t, productionCopyPath), quoted, "the exported metric count")
}

func TestQuotedSchemeCountMatchesRegistry(t *testing.T) {
	const quoted = "11"
	if got := len(auth.Schemes()); strconv.Itoa(got) != quoted {
		t.Fatalf("auth registry has %d schemes but %s quotes %s", got, productionCopyPath, quoted)
	}
	// Deliberately "verification schemes", not "signature schemes": of the 11,
	// two are header-exact comparisons and one is no_auth, which verifies
	// nothing. /compare says "wire-format schemes" for the same reason.
	copySrc := readRepoFile(t, productionCopyPath)
	requireTileValue(t, copySrc, quoted, "the registered scheme count")
	if strings.Contains(copySrc, "signature schemes") {
		t.Fatalf("%s calls the %s registry entries \"signature schemes\". "+
			"no_auth verifies nothing and two schemes are plain header comparisons, "+
			"so the honest label is \"verification schemes\".", productionCopyPath, quoted)
	}
}

// TestQuotedDestinationCountMatchesSchema closes a real drift hole. The
// homepage marquee renders DEST_TYPES.length, so it self-updates; compareCopy
// hardcodes the same number in prose, so it does not. Adding a destination
// would have silently left /compare a count behind.
func TestQuotedDestinationCountMatchesSchema(t *testing.T) {
	var schema map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readRepoFile(t, destSchemaPath)), &schema); err != nil {
		t.Fatalf("parse %s: %v", destSchemaPath, err)
	}
	compareSrc := readRepoFile(t, compareCopyPath)
	m := regexp.MustCompile(`(\d+)\s+catalog types`).FindStringSubmatch(compareSrc)
	if m == nil {
		t.Fatalf("%s no longer states an \"N catalog types\" count; %s has %d destinations "+
			"and the marquee quotes that number, so /compare must agree or say nothing.",
			compareCopyPath, destSchemaPath, len(schema))
	}
	if quoted, _ := strconv.Atoi(m[1]); quoted != len(schema) {
		t.Fatalf("%s has %d destination types but %s quotes %d catalog types",
			destSchemaPath, len(schema), compareCopyPath, quoted)
	}
}

// TestDeadLetterClaimDoesNotNameKafka guards the claim the site got wrong:
// "Webhook, Kafka and RabbitMQ back off, honour a 429, then dead-letter."
// A dead_letter config block exists only for webhook and rabbitmq. Kafka has
// no dead-letter path whatsoever, and RabbitMQ's is a broker-side DLX that
// dead-letters what a consumer rejects -- not a publish Hookway failed to make.
func TestDeadLetterClaimDoesNotNameKafka(t *testing.T) {
	for _, path := range []string{"internal/queue/kafka.go", "internal/queue/kafka_client.go"} {
		if strings.Contains(strings.ToLower(readRepoFile(t, path)), "dead") {
			t.Fatalf("%s now mentions dead-lettering. If Kafka gained a dead-letter path, "+
				"this test's premise is stale and the site may finally say so.", path)
		}
	}
	for path, clauses := range clausesMentioning(t, regexp.MustCompile(`(?i)dead[- ]letter`), "dead-lettering") {
		for _, clause := range clauses {
			if regexp.MustCompile(`(?i)kafka`).MatchString(clause) {
				t.Fatalf("%s names Kafka in a dead-letter claim: %q\n"+
					"Kafka has no dead-letter path; dead_letter is configurable for webhook and rabbitmq only.",
					path, clause)
			}
		}
	}
}

// TestRetryAfterClaimNamesOnlyHTTPDestinations guards the other half of the
// same false sentence. Only internal/queue/webhook_engine.go and
// internal/flowconfig/node_http_request.go call ParseRetryAfter /
// RetryableStatus. Kafka and RabbitMQ speak binary protocols and cannot
// return an HTTP 429 at all.
func TestRetryAfterClaimNamesOnlyHTTPDestinations(t *testing.T) {
	for _, path := range []string{"internal/queue/kafka.go", "internal/queue/rabbitmq.go"} {
		src := readRepoFile(t, path)
		if strings.Contains(src, "429") || strings.Contains(src, "Retry-After") {
			t.Fatalf("%s now handles an HTTP 429 / Retry-After. This test's premise is stale.", path)
		}
	}
	for path, clauses := range clausesMentioning(t, regexp.MustCompile(`429`), "429 handling") {
		for _, clause := range clauses {
			for _, banned := range []string{"Kafka", "RabbitMQ"} {
				if regexp.MustCompile(`(?i)` + banned).MatchString(clause) {
					t.Fatalf("%s names %s in a 429 claim: %q\n"+
						"%s speaks a binary protocol and cannot return an HTTP 429; only the webhook "+
						"destination and the http_request node parse Retry-After.",
						path, banned, clause, banned)
				}
			}
		}
	}
}

// The replay tile headlines how long a missed delivery is kept by default.
func TestQuotedReplayRetentionMatchesDefault(t *testing.T) {
	const quoted = "7"
	const path = "internal/replay/config/config.go"
	hours := singleInt(t, readRepoFile(t, path), path,
		`defaultTTL\s*=\s*(\d+)\s*\*\s*time\.Hour`, "the defaultTTL retention")
	if hours%24 != 0 || strconv.Itoa(hours/24) != quoted {
		t.Fatalf("%s keeps records for %dh by default but %s quotes %s days",
			path, hours, productionCopyPath, quoted)
	}
	requireTileValue(t, readRepoFile(t, productionCopyPath), quoted, "the default replay retention in days")
}
