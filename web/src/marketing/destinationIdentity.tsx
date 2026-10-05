import { Box, Text } from "@mantine/core";
import { destinationPresentation } from "../shared/ui/destinationIdentity";
import { DESTINATION_ITEM_CLASS } from "./marketingStyles";

export { destinationPresentation, DESTINATION_PRESENTATIONS } from "../shared/ui/destinationIdentity";

export function DestinationIdentity({
  type,
  role,
}: {
  type: string;
  role?: string;
}) {
  const { label, icon: Icon } = destinationPresentation(type);
  return (
    <Box
      component="span"
      className={DESTINATION_ITEM_CLASS}
      role={role}
      data-destination={type}
    >
      <Icon aria-hidden="true" focusable="false" size={18} />
      <Text component="span" size="sm" fw={500} c="inherit">
        {label}
      </Text>
    </Box>
  );
}
