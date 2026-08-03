export type FieldKind = "text" | "list" | "select" | "bool" | "number";

export interface FieldSpec {
  key: string;
  label: string;
  kind?: FieldKind;
  options?: string[];
  placeholder?: string;
  default?: string | boolean | number;
}

export interface DestTypeSpec {
  block: string;
  fields: FieldSpec[];
}

const EXCHANGE_TYPES = ["direct", "fanout", "topic", "headers"];
const REDIS_MODES = ["single", "cluster", "sentinel"];
const KAFKA_ACKS = ["all", "one", "none"];
const KAFKA_COMPRESSION = ["none", "gzip", "snappy", "lz4", "zstd"];

const redisFields: FieldSpec[] = [
  { key: "mode", label: "Mode", kind: "select", options: REDIS_MODES, default: "single" },
  { key: "url", label: "URL", placeholder: "redis://localhost:6379" },
  { key: "addrs", label: "Addresses (comma-separated; cluster/sentinel)", kind: "list", placeholder: "host1:6379,host2:6379" },
  { key: "master_name", label: "Sentinel master name" },
  { key: "username", label: "Username" },
  { key: "password", label: "Password" },
  { key: "db", label: "DB", kind: "number" },
  { key: "key_prefix", label: "Key prefix" },
];

export const DEST_TYPE_SPECS: Record<string, DestTypeSpec> = {
  rabbitmq: {
    block: "rabbitmq",
    fields: [
      { key: "url", label: "AMQP URL", placeholder: "${RABBITMQ_URL}" },
      { key: "exchange", label: "Exchange", placeholder: "hookway.webhooks" },
      { key: "exchange_type", label: "Exchange type", kind: "select", options: EXCHANGE_TYPES, default: "direct" },
      { key: "durable", label: "Durable", kind: "bool", default: true },
      { key: "declare_queues", label: "Declare queues", kind: "bool", default: true },
      { key: "publish_timeout", label: "Publish timeout", placeholder: "5s" },
    ],
  },
  sqs: {
    block: "sqs",
    fields: [
      { key: "region", label: "Region", placeholder: "us-east-1" },
      { key: "endpoint", label: "Endpoint (optional)" },
      { key: "access_key_id", label: "Access key ID" },
      { key: "secret_access_key", label: "Secret access key" },
    ],
  },
  sns: {
    block: "sns",
    fields: [
      { key: "region", label: "Region", placeholder: "us-east-1" },
      { key: "endpoint", label: "Endpoint (optional)" },
      { key: "access_key_id", label: "Access key ID" },
      { key: "secret_access_key", label: "Secret access key" },
    ],
  },
  s3: {
    block: "s3",
    fields: [
      { key: "region", label: "Region", placeholder: "us-east-1" },
      { key: "bucket", label: "Bucket", placeholder: "my-bucket" },
      { key: "endpoint", label: "Endpoint (optional)" },
      { key: "create", label: "Create bucket", kind: "bool", default: false },
      { key: "sse", label: "Server-side encryption" },
    ],
  },
  postgres: {
    block: "postgres",
    fields: [
      { key: "url", label: "URL", placeholder: "postgres://user:pass@host:5432/db" },
      { key: "schema", label: "Schema", placeholder: "public" },
      { key: "table", label: "Table", placeholder: "webhook_events" },
      { key: "max_conns", label: "Max connections", kind: "number" },
    ],
  },
  mongodb: {
    block: "mongodb",
    fields: [
      { key: "uri", label: "URI", placeholder: "mongodb://host:27017" },
      { key: "database", label: "Database", placeholder: "webhooks" },
      { key: "max_pool_size", label: "Max pool size", kind: "number" },
    ],
  },
  kafka: {
    block: "kafka",
    fields: [
      { key: "brokers", label: "Brokers (comma-separated)", kind: "list", placeholder: "broker-1:9092,broker-2:9092" },
      { key: "acks", label: "Acks", kind: "select", options: KAFKA_ACKS, default: "all" },
      { key: "compression", label: "Compression", kind: "select", options: KAFKA_COMPRESSION, default: "none" },
      { key: "publish_timeout", label: "Publish timeout", placeholder: "5s" },
    ],
  },
  webhook: {
    block: "webhook",
    fields: [
      { key: "url", label: "URL", placeholder: "https://api.example.com/hooks" },
      { key: "timeout", label: "Timeout", placeholder: "10s" },
      { key: "max_queue", label: "Max queue", kind: "number" },
    ],
  },
  redis_stream: { block: "redis", fields: redisFields },
  redis_list: { block: "redis", fields: redisFields },
  redis_pubsub: { block: "redis", fields: redisFields },
  redis_kv: { block: "redis", fields: redisFields },
};

export const DEST_TYPES: string[] = [
  "rabbitmq", "sqs", "sns", "s3", "postgres", "mongodb", "kafka", "webhook",
  "redis_stream", "redis_list", "redis_pubsub", "redis_kv",
];

export function defaultSettings(type: string): Record<string, unknown> {
  const spec = DEST_TYPE_SPECS[type];
  const out: Record<string, unknown> = {};
  if (!spec) return out;
  for (const f of spec.fields) {
    out[f.key] = f.default ?? (f.kind === "bool" ? false : "");
  }
  return out;
}

// Shared inspector enums (hoisted so Milestone 7 validation can reuse them).
export const AUTH_TYPES = ["hmac_sha256", "header_exact", "jwt", "none"];
export const FILTER_SOURCES = ["body", "header", "query"];
export const OPERATORS = ["eq", "ne", "gt", "gte", "lt", "lte", "contains", "exists"];

export const SCHEMES_BY_AUTH: Record<string, string[]> = {
  hmac_sha256: ["hmac_sha256_hex", "hmac_sha256_base64", "hmac_sha1_hex", "hmac_sha256_ts_colon", "hmac_sha256_ts_dot"],
  jwt: ["jwt_hs256", "jwt_rs256", "jwt_es256"],
};

export function defaultScheme(authType: string): string {
  return SCHEMES_BY_AUTH[authType]?.[0] ?? "";
}
