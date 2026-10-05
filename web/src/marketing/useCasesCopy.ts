// Use-case copy for /use-cases. Every scenario points at a committed example
// so claims stay tied to runnable YAML rather than aspirational marketing.

import { EXAMPLES, repoBlob } from "./links";

export interface UseCase {
  id: string;
  title: string;
  summary: string;
  detail: string;
  examplePath: string;
  exampleHref: string;
  exampleLabel: string;
}

export const USE_CASES_INTRO = {
  title: "Patterns that stay in your infrastructure",
  lead: "Connect webhook senders to the systems your team already runs. These examples show how to verify requests, prepare their data, and send events or files to the right service.",
} as const;

export const USE_CASES: UseCase[] = [
  {
    id: "multipart-s3",
    title: "Store upload attachments in S3, metadata elsewhere",
    summary:
      "Store files from a webhook upload in S3 and send the accompanying form fields to a queue.",
    detail:
      "Parse multipart/form-data into fields and files, reshape each branch with a code node, then publish.payload: object to S3 and an envelope to RabbitMQ (or any other destination). The S3 key includes a sanitized vendor filename, and PutObject uses the vendor Content-Type.",
    examplePath: "examples/multipart-split.yaml",
    exampleHref: repoBlob("examples/multipart-split.yaml"),
    exampleLabel: "multipart-split.yaml",
  },
  {
    id: "fanout",
    title: "Fan out one verified event to several backends",
    summary:
      "Verify an event once, then send it to several destinations, such as an archive and a work queue.",
    detail:
      "Declare destinations once and reference them from publish nodes. stop_on_failure publishes in order; skip_failed runs direct publish branches concurrently. Dedupe sender retries with a stable sender event ID; a new request receives a new envelope ID.",
    examplePath: "examples/destinations/rabbitmq.yaml",
    exampleHref: repoBlob("examples/destinations/rabbitmq.yaml"),
    exampleLabel: "rabbitmq.yaml",
  },
  {
    id: "dynamic-routing",
    title: "Route each environment to its own queue",
    summary:
      "Check that the requested environment is allowed, then send the event to its SQS queue.",
    detail:
      "The route node selects an environment from the parsed body, query, or headers and checks it against the configured routing profile. A dedicated flow pins its environment. The example requires each queue to exist; unknown, unsafe, or unresolved targets fail closed.",
    examplePath: "examples/routing/admission.yaml",
    exampleHref: repoBlob("examples/routing/admission.yaml"),
    exampleLabel: "admission.yaml",
  },
  {
    id: "xml-proxy",
    title: "Prune XML and forward the transformed document",
    summary:
      "Verify the sender’s signature, remove unwanted XML elements, and forward the cleaned document to an API.",
    detail:
      "body_format: xml keeps attributes and repeated siblings addressable. xml_prune applies fixed rules; http_request can send body: transformed with the pruned document.",
    examplePath: "examples/xml.yaml",
    exampleHref: repoBlob("examples/xml.yaml"),
    exampleLabel: "xml.yaml",
  },
  {
    id: "branching",
    title: "Branch on payload shape before delivery",
    summary:
      "Filter out unwanted events or choose a delivery path based on fields such as event type or order status.",
    detail:
      "filter stops a branch, if/else splits two outcomes, and switch picks the first matching port. Conditions can read headers or dotted body paths after JSON or XML parse.",
    examplePath: "examples/filters.yaml",
    exampleHref: repoBlob("examples/filters.yaml"),
    exampleLabel: "filters.yaml",
  },
  {
    id: "redis-stream",
    title: "Hand verified events to Redis Streams",
    summary:
      "Append each envelope to a Redis or Valkey stream for workers to consume with XREADGROUP.",
    detail:
      "Hookway writes with XADD; configure Redis persistence and replication for durability. Your consumers own group creation and retention. Choose a list for a simpler work queue or Pub/Sub only when occasional loss is acceptable.",
    examplePath: "examples/destinations/redis_stream.yaml",
    exampleHref: repoBlob("examples/destinations/redis_stream.yaml"),
    exampleLabel: "redis_stream.yaml",
  },
  {
    id: "sync-proxy",
    title: "Reverse-proxy a request synchronously",
    summary:
      "Call an API and return its HTTP status and response body to the original caller on the same connection.",
    detail:
      "This is distinct from the async webhook destination. The graph returns the upstream response on the same connection, with path-variable safety checks.",
    examplePath: "examples/showcase/proxy.yaml",
    exampleHref: repoBlob("examples/showcase/proxy.yaml"),
    exampleLabel: "proxy.yaml",
  },
];

export const USE_CASES_FOOTER = {
  examplesHref: EXAMPLES.href,
  examplesLabel: EXAMPLES.title,
} as const;
