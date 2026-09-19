import type { IconType } from "react-icons";
import { FaAws } from "react-icons/fa6";
import {
  SiApachekafka,
  SiMongodb,
  SiPostgresql,
  SiRabbitmq,
  SiRedis,
  SiTemporal,
} from "react-icons/si";
import { TbRoute, TbWebhook } from "react-icons/tb";
import { Box, Text } from "@mantine/core";
import { humanLabel } from "../graph/nodeSchema";
import { DESTINATION_ITEM_CLASS } from "./marketingStyles";

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
  s3: { label: "Amazon S3", icon: FaAws },
  sns: { label: "Amazon SNS", icon: FaAws },
  sqs: { label: "Amazon SQS", icon: FaAws },
  temporal: { label: "Temporal", icon: SiTemporal },
  webhook: { label: "Webhook", icon: TbWebhook },
};

export function destinationPresentation(type: string): DestinationPresentation {
  return DESTINATION_PRESENTATIONS[type] ?? {
    label: humanLabel(type).replace(/\b\w/g, (letter) => letter.toUpperCase()),
    icon: TbRoute,
  };
}

export function DestinationIdentity({ type, role }: { type: string; role?: string }) {
  const { label, icon: Icon } = destinationPresentation(type);
  return (
    <Box component="span" className={DESTINATION_ITEM_CLASS} role={role} data-destination={type}>
      <Icon aria-hidden="true" focusable="false" size={18} />
      <Text component="span" size="sm" fw={500} c="inherit">
        {label}
      </Text>
    </Box>
  );
}
