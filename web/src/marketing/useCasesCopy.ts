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
  lead: "Hookway sits in front of the systems you already run. These are the flows operators actually configure: verify once, then branch, transform, and publish without a SaaS control plane in the path.",
} as const;

export const USE_CASES: UseCase[] = [
  {
    id: "multipart-s3",
    title: "Store upload attachments in S3, metadata elsewhere",
    summary:
      "A multipart webhook can fan out so decoded file bytes land in S3 while form fields publish to a queue.",
    detail:
      "Parse multipart/form-data into fields and files, reshape each branch with a code node, then publish.payload: object to S3 and an envelope to RabbitMQ (or any other destination). The object key keeps the vendor filename and Content-Type.",
    examplePath: "examples/multipart-split.yaml",
    exampleHref: repoBlob("examples/multipart-split.yaml"),
    exampleLabel: "multipart-split.yaml",
  },
  {
    id: "fanout",
    title: "Fan out one verified event to several backends",
    summary: "One signature check, then concurrent publishes to the destinations your services already consume.",
    detail:
      "Declare destinations once and reference them from publish nodes. Delivery failure policy chooses stop-on-failure or skip-failed fan-out. Consumers still dedupe on the envelope id.",
    examplePath: "examples/destinations/rabbitmq.yaml",
    exampleHref: repoBlob("examples/destinations/rabbitmq.yaml"),
    exampleLabel: "rabbitmq.yaml",
  },
  {
    id: "xml-proxy",
    title: "Prune XML and forward the transformed document",
    summary: "Verify the raw bytes, work on an element tree, then HTTP-forward application/xml upstream.",
    detail:
      "body_format: xml keeps attributes and repeated siblings addressable. xml_prune applies fixed rules; http_request can send body: transformed with the pruned document.",
    examplePath: "examples/xml.yaml",
    exampleHref: repoBlob("examples/xml.yaml"),
    exampleLabel: "xml.yaml",
  },
  {
    id: "branching",
    title: "Branch on payload shape before delivery",
    summary: "Drop noise, reject bad payloads, or route the first matching labelled path.",
    detail:
      "filter stops a branch, if/else splits two outcomes, and switch picks the first matching port. Conditions can read headers or dotted body paths after JSON or XML parse.",
    examplePath: "examples/filters.yaml",
    exampleHref: repoBlob("examples/filters.yaml"),
    exampleLabel: "filters.yaml",
  },
  {
    id: "temporal",
    title: "Start a Temporal workflow from a verified webhook",
    summary: "Publish the envelope (or body) onto a Temporal task queue without embedding a vendor SDK in the edge.",
    detail:
      "The publish target is the task queue. Hookway remains the verification and routing layer; Temporal owns durable execution.",
    examplePath: "examples/destinations/temporal.yaml",
    exampleHref: repoBlob("examples/destinations/temporal.yaml"),
    exampleLabel: "temporal.yaml",
  },
  {
    id: "sync-proxy",
    title: "Reverse-proxy a request synchronously",
    summary: "When you need the upstream status and body back to the sender, use http_request plus respond passthrough.",
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
