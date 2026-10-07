package main

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
)

func TestServeMigrateAndGCRefuseUnsupportedDatabaseWithoutListening(t *testing.T) {
	for _, future := range []bool{false, true} {
		name, want := "legacy_version_one", "legacy"
		if future {
			name, want = "future_version", "ahead"
		}
		t.Run(name, func(t *testing.T) {
			db, addr := replaytest.Database(t), freeAddr(t)
			path := writeConfig(t, testConfig(db, addr, ""))
			if code, stdout, stderr := runCmd("migrate", "--config", path); code != 0 {
				t.Fatalf("initial migrate: %d %s %s", code, stdout, stderr)
			}
			conn, err := pgx.Connect(t.Context(), db)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close(t.Context()) }()
			setup := `ALTER TABLE records RENAME TO failures`
			if future {
				setup = `INSERT INTO goose_db_version(version_id,is_applied) VALUES(999,true)`
			}
			if _, err := conn.Exec(t.Context(), setup); err != nil {
				t.Fatal(err)
			}
			versionRows := func() string {
				var rows string
				if err := conn.QueryRow(t.Context(), `SELECT json_agg(row_to_json(v) ORDER BY id)::text FROM goose_db_version v`).Scan(&rows); err != nil {
					t.Fatal(err)
				}
				return rows
			}
			before := versionRows()
			for _, command := range []string{"migrate", "serve", "gc"} {
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				var stdout, stderr bytes.Buffer
				code := run(ctx, []string{command, "--config", path}, &stdout, &stderr)
				cancel()
				if output := stdout.String() + stderr.String(); code != 1 || !strings.Contains(output, want) || !strings.Contains(output, "preserve") {
					t.Fatalf("%s admitted incompatible schema: %d %q", command, code, output)
				}
				if connection, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(t.Context(), "tcp", addr); err == nil {
					_ = connection.Close()
					t.Fatal("unsupported database started a listener")
				}
				if after := versionRows(); after != before {
					t.Fatalf("%s changed migration metadata", command)
				}
			}
		})
	}
}
