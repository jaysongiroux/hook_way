import type { IconType } from "react-icons";
import {
  SiApachekafka,
  SiMongodb,
  SiPostgresql,
  SiRabbitmq,
  SiRedis,
  SiTemporal,
} from "react-icons/si";
import { TbBucket, TbLambda, TbRoute, TbSpeakerphone, TbStackPush, TbWebhook } from "react-icons/tb";
import { humanLabel } from "./text";

interface DestinationPresentation {
  label: string;
  icon: IconType;
}

// This table supplies human-facing names and brand silhouettes. The list that
// renders them still comes from DEST_TYPES, so a new backend appears in the UI
// even before it receives a tailored presentation here.
export const DESTINATION_PRESENTATIONS: Record<string, DestinationPresentation> = {
  kafka: { label: "Kafka", icon: SiApachekafka },
  mongodb: { label: "MongoDB", icon: SiMongodb },
  postgres: { label: "PostgreSQL", icon: SiPostgresql },
  rabbitmq: { label: "RabbitMQ", icon: SiRabbitmq },
  redis_kv: { label: "Redis KV", icon: SiRedis },
  redis_list: { label: "Redis List", icon: SiRedis },
  redis_pubsub: { label: "Redis Pub/Sub", icon: SiRedis },
  redis_stream: { label: "Redis Stream", icon: SiRedis },
  // simple-icons carries no Amazon service marks, so each AWS backend gets the
  // Tabler glyph for what it actually does rather than a shared cloud logo.
  s3: { label: "Amazon S3", icon: TbBucket },
  sns: { label: "Amazon SNS", icon: TbSpeakerphone },
  sqs: { label: "Amazon SQS", icon: TbStackPush },
  lambda: { label: "AWS Lambda", icon: TbLambda },
  temporal: { label: "Temporal", icon: SiTemporal },
  mcp_events: { label: "MCP events", icon: TbSpeakerphone },
  webhook: { label: "Webhook", icon: TbWebhook },
};

export function destinationPresentation(type: string): DestinationPresentation {
  return DESTINATION_PRESENTATIONS[type] ?? {
    label: humanLabel(type).replace(/\b\w/g, (letter) => letter.toUpperCase()),
    icon: TbRoute,
  };
}

