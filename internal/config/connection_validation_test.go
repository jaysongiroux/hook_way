package config

import (
	"strings"
	"testing"
)

func requireIssue(t *testing.T, issues map[string]ValidationIssue, field, wantSubstring string) ValidationIssue {
	t.Helper()
	issue, ok := issues[field]
	if !ok {
		t.Fatalf("no issue on %q; got fields %v", field, sortedStringKeys(issues))
	}
	if !strings.Contains(issue.Message, wantSubstring) {
		t.Errorf("issue on %q = %q, want it to mention %q", field, issue.Message, wantSubstring)
	}
	return issue
}

func requireNoIssue(t *testing.T, issues map[string]ValidationIssue, field string) {
	t.Helper()
	if issue, ok := issues[field]; ok {
		t.Errorf("unexpected issue on %q: %s", field, issue.Message)
	}
}

func TestUnknownConnectionNameIsRejected(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
destinations:
  events: {type: s3, connection: nope, s3: {bucket: b, region: us-east-1}}
`)
	issue := requireIssue(t, issues, "destinations.events.connection", `unknown connection "nope"`)
	if issue.Suggestion == "" {
		t.Error("suggestion should tell the operator what to do")
	}
}

func TestConnectionTypeMustMatchDestinationType(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
connections:
  pg: {type: postgres, url: "postgres://db/x"}
destinations:
  events: {type: s3, connection: pg, s3: {bucket: b, region: us-east-1}}
`)
	requireIssue(t, issues, "destinations.events.connection", `cannot serve a "s3" destination`)
}

func TestConnectionTypeIsRequiredAndEnumerated(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
connections:
  a: {url: "postgres://db/x"}
  b: {type: sqlite, url: "file:x"}
`)
	requireIssue(t, issues, "connections.a.type", "connection type is required")
	requireIssue(t, issues, "connections.b.type", `"sqlite" is not supported`)
}

func TestConnectionRejectsFieldsOfAnotherType(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
connections:
  aws_prod:
    type: aws
    region: us-east-1
    brokers: [broker:9092]
destinations:
  events: {type: s3, connection: aws_prod, s3: {bucket: b}}
`)
	requireIssue(t, issues, "connections.aws_prod.brokers", `not valid for a connection of type "aws"`)
}

// tls.enabled exists on redis/kafka/temporal but not on postgres, whose TLS is
// driven by the DSN's sslmode. The decode target is the union of every type's
// fields, so only the per-type ownership check can catch this.
func TestConnectionRejectsTLSEnabledOnPostgres(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
connections:
  pg:
    type: postgres
    url: "postgres://db/x"
    tls: {enabled: true}
destinations:
  rows: {type: postgres, connection: pg, postgres: {table: t}}
`)
	requireIssue(t, issues, "connections.pg.tls.enabled", `not valid for a connection of type "postgres"`)
	requireNoIssue(t, issues, "connections.pg.tls.ca_cert")
}

func TestDestinationMustNotSetAConnectionOwnedField(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
connections:
  aws_prod: {type: aws, region: us-east-1, access_key_id: k, secret_access_key: s}
destinations:
  events:
    type: s3
    connection: aws_prod
    s3: {bucket: b, region: eu-west-2}
`)
	issue := requireIssue(t, issues, "destinations.events.s3.region", `provided by connection "aws_prod"`)
	if !strings.Contains(issue.Suggestion, "remove region") {
		t.Errorf("suggestion = %q, want it to say how to fix", issue.Suggestion)
	}
	// bucket is a sink field and stays the destination's business.
	requireNoIssue(t, issues, "destinations.events.s3.bucket")
}

// The zero-value cases. A struct-based conflict check cannot see these,
// because Go cannot distinguish "db was written as 0" from "db was omitted" --
// which is the whole reason the check runs on the YAML node tree.
func TestDestinationConflictDetectionSeesZeroValues(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
connections:
  r:
    type: redis
    mode: single
    url: redis://redis:6379
    db: 5
destinations:
  stream:
    type: redis_stream
    connection: r
    redis:
      db: 0
      key_prefix: "evt:"
      tls: {insecure_skip_verify: false}
`)
	requireIssue(t, issues, "destinations.stream.redis.db", `provided by connection "r"`)
	requireIssue(t, issues, "destinations.stream.redis.tls.insecure_skip_verify", `provided by connection "r"`)
	requireNoIssue(t, issues, "destinations.stream.redis.key_prefix")
}

func TestUnreferencedConnectionIsAWarningNotAnError(t *testing.T) {
	doc := serverBlock + `
connections:
  unused: {type: aws, region: us-east-1}
destinations:
  events: {type: s3, s3: {bucket: b, region: us-east-1}}
`
	issue := requireIssue(t, decodeIssues(t, doc), "connections.unused", "not referenced")
	if got := issue.EffectiveSeverity(); got != SeverityWarning {
		t.Errorf("severity = %q, want %q -- an unused connection must not block serving", got, SeverityWarning)
	}

	// And it must survive the severity normalization the loader applies.
	_, issues := DecodeSharedYAML([]byte(doc), "test.yaml")
	for _, i := range issues {
		if i.Field == "connections.unused" && i.Severity != SeverityWarning {
			t.Errorf("normalizeIssueSeverities dropped the warning severity: %+v", i)
		}
	}
}

func TestConnectionUsedOnlyByReplayIsNotReportedUnused(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
connections:
  pg: {type: postgres, url: "postgres://db/x"}
replay:
  enabled: true
  addr: ":9091"
  connection: pg
destinations:
  events: {type: s3, s3: {bucket: b, region: us-east-1}}
`)
	requireNoIssue(t, issues, "connections.pg")
}

func TestReplayRejectsANonPostgresConnection(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
connections:
  r: {type: redis, mode: single, url: redis://redis:6379}
replay:
  enabled: true
  addr: ":9091"
  connection: r
destinations:
  events: {type: s3, s3: {bucket: b, region: us-east-1}}
`)
	requireIssue(t, issues, "replay.connection", "requires a \"postgres\" connection")
}

func TestConnectionNameCharsetIsValidated(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
connections:
  "bad name": {type: aws, region: us-east-1}
destinations:
  events: {type: s3, connection: "bad name", s3: {bucket: b}}
`)
	requireIssue(t, issues, "connections.bad name", "unsupported characters")
}

func TestUnknownKeyInsideAConnectionNamesTheFullPath(t *testing.T) {
	_, issues := DecodeSharedYAML([]byte(serverBlock+`
connections:
  aws_prod: {type: aws, acces_key_id: typo}
`), "test.yaml")
	var found bool
	for _, issue := range issues {
		if issue.Field == "connections.aws_prod.acces_key_id" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected connections.aws_prod.acces_key_id; got %+v", issues)
	}
}

// A required field supplied by a connection must be reported at the path the
// operator wrote. Beyond readability, configapi.ValidateWithEnv resolves this
// path against the YAML to decide whether a missing ${VAR} downgrades the
// error to a warning -- a path that is not in the document breaks that.
func TestIssuesOnConnectionSuppliedFieldsNameTheConnection(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
connections:
  aws_prod: {type: aws, access_key_id: k, secret_access_key: s}
destinations:
  events: {type: s3, connection: aws_prod, s3: {bucket: b}}
`)
	issue := requireIssue(t, issues, "connections.aws_prod.region", "region is required")
	if !strings.Contains(issue.Message, "via destinations.events.connection") {
		t.Errorf("message = %q, want it to name the destination that pulled the connection in", issue.Message)
	}
	requireNoIssue(t, issues, "destinations.events.s3.region")
}

// The mirror of the above: a sink field's error must stay where it was
// written, even on a destination that uses a connection.
func TestIssuesOnSinkFieldsStayOnTheDestination(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
connections:
  pg: {type: postgres, url: "postgres://db/x"}
destinations:
  rows: {type: postgres, connection: pg, postgres: {table: "bad-table"}}
`)
	requireIssue(t, issues, "destinations.rows.postgres.table", "unsupported characters")
	requireNoIssue(t, issues, "connections.pg.table")
}

func TestInlineDestinationsAreUnaffectedByTheRemap(t *testing.T) {
	issues := decodeIssues(t, serverBlock+`
destinations:
  events: {type: s3, s3: {bucket: b}}
`)
	issue := requireIssue(t, issues, "destinations.events.s3.region", "region is required")
	if strings.Contains(issue.Message, "via") {
		t.Errorf("inline destination message was rewritten: %q", issue.Message)
	}
}

// A misspelled key is a typo, not a wrong-type field. unknownYAMLFields
// already reports it; the ownership pass must not pile a second, misleading
// "not valid for type aws" message on top.
func TestTypoInAConnectionIsReportedOnce(t *testing.T) {
	_, issues := DecodeSharedYAML([]byte(serverBlock+`
connections:
  aws_prod: {type: aws, region: us-east-1, acces_key_id: typo}
destinations:
  events: {type: s3, connection: aws_prod, s3: {bucket: b}}
`), "test.yaml")
	var count int
	for _, issue := range issues {
		if issue.Field == "connections.aws_prod.acces_key_id" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("got %d issues on the typo, want exactly 1", count)
	}
}
