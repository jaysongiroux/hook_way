export function defaultCoreReplayConfig(): Record<string, unknown> {
  return {
    url: "http://replay:8080",
    capture_token: "${REPLAY_CAPTURE_TOKEN}",
    max_record_bytes: 16777216,
    control: {
      addr: ":9092",
      token: "${REPLAY_CONTROL_TOKEN}",
      max_concurrent: 8,
    },
  };
}

export function defaultReplayServiceConfig(): Record<string, unknown> {
  return {
    server: { addr: ":8080" },
    logging: { level: "info" },
    metrics: { enabled: true, path: "/metrics", addr: ":9091" },
    database: { url: "${REPLAY_DATABASE_URL}", max_conns: 10 },
    core: { control_url: "http://hookway:9092", control_token: "${REPLAY_CONTROL_TOKEN}" },
    retention: {
      ttl: "168h",
      max_records: 100000,
      max_bytes: 10737418240,
      max_record_bytes: 16777216,
      gc_interval: "15m",
    },
    workers: { concurrency: 4, execution_timeout: "30s", lease: "60s" },
    keys: [
      { name: "core", token: "${REPLAY_CAPTURE_TOKEN}", scopes: ["capture"] },
      { name: "operator", token: "${REPLAY_OPERATOR_TOKEN}", scopes: ["read", "payload", "retry"] },
      { name: "prometheus", token: "${REPLAY_METRICS_TOKEN}", scopes: ["metrics"] },
    ],
  };
}
