import { Badge } from "@mantine/core";
import { DARK } from "../shared/ui/tokens";
import { statusLabel, type Failure } from "./session";

export function FailureState({ record }: { record: Failure }) {
  return (
    <Badge
      className="delivery-state"
      c={DARK.text}
      variant="light"
      color={
        record.status === "unknown"
          ? "yellow"
          : record.status === "resolved"
            ? "green"
            : record.status === "dismissed"
              ? "gray"
              : "brand"
      }
    >
      {statusLabel(record)}
    </Badge>
  );
}
