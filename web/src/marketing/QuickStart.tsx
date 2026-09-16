import { Box, Button, Group, Paper, Text } from "@mantine/core";
import { useState } from "react";
import { QUICK_START_COMMAND } from "./quickStartCommand";
import { DARK } from "../tokens";

// One command, one click to copy it. The button reports back so the click is
// not silent.
export function QuickStart() {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(QUICK_START_COMMAND);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard access can be denied; the command stays selectable on screen.
      setCopied(false);
    }
  };
  return (
    <Paper radius="sm" p="sm" style={{ background: DARK.recessed, border: `1px solid ${DARK.border}` }}>
      <Group justify="space-between" wrap="nowrap" gap="sm">
        <Box style={{ minWidth: 0, overflowX: "auto" }}>
          <Text
            component="code"
            c={DARK.text}
            style={{
              fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
              fontSize: 13,
              whiteSpace: "nowrap",
            }}
          >
            <Text component="span" c={DARK.muted} style={{ fontFamily: "inherit" }}>
              ${" "}
            </Text>
            {QUICK_START_COMMAND}
          </Text>
        </Box>
        <Button
          size="xs"
          variant="default"
          onClick={copy}
          style={{ flexShrink: 0 }}
          styles={{ root: { background: DARK.surface, borderColor: DARK.border, color: DARK.text } }}
        >
          {copied ? "Copied" : "Copy"}
        </Button>
      </Group>
    </Paper>
  );
}
