import { Badge } from "@mantine/core";
import { DARK } from "../shared/ui/tokens";
import { statusLabel, type ReplayRecord } from "./session";

export function RecordState({ record }: { record: ReplayRecord }) {
  return (
    <Badge
      className="delivery-state"
      c={DARK.text}
      variant="light"
      color={
        record.dismissed_at
          ? "gray"
          : record.delivery_status === "unknown"
          ? "yellow"
          : record.delivery_status === "resolved"
            ? "green"
            : "brand"
      }
    >
      {record.kind === "inspection" ? (!!record.dismissed_at ? "Dismissed · View only" : "View only") : statusLabel(record)}
    </Badge>
  );
}
