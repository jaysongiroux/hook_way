import { Alert, Button, Stack, Text } from "@mantine/core";
import type { ReactNode } from "react";
import { DARK } from "./tokens";

export function LoadingState({ children }: { children: ReactNode }) {
  return (
    <Text size="sm" role="status">
      {children}
    </Text>
  );
}
export function EmptyState({ children }: { children: ReactNode }) {
  return (
    <Text size="sm" c={DARK.muted}>
      {children}
    </Text>
  );
}
export function ErrorState({
  title,
  children,
  onRetry,
}: {
  title: string;
  children: ReactNode;
  onRetry?: () => void;
}) {
  return (
    <Alert color="red" title={title}>
      <Stack gap="xs">
        {children}
        {onRetry && (
          <Button size="xs" variant="light" onClick={onRetry}>
            Try again
          </Button>
        )}
      </Stack>
    </Alert>
  );
}
