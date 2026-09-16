import { useState } from "react";
import { Button, Group, Menu, Modal, Stack, Text, TextInput } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { useGraphStore } from "./store";

export function FlowSwitcher() {
  const [opened, { open, close }] = useDisclosure(false);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const otherFlows = useGraphStore((s) => s.otherFlows);
  const setActiveFlow = useGraphStore((s) => s.setActiveFlow);
  const addFlow = useGraphStore((s) => s.addFlow);
  const flowNames = [activeFlow, ...Object.keys(otherFlows)].sort();

  const submit = () => {
    const trimmed = name.trim();
    if (!trimmed) {
      setError("Enter a name.");
      return;
    }
    if (!addFlow(trimmed)) {
      setError("That flow already exists.");
      return;
    }
    setName("");
    setError("");
    close();
  };

  const openCreate = () => {
    setName("");
    setError("");
    open();
  };

  return (
    <>
      <Menu>
        <Menu.Target>
          <Button size="xs" variant="light" aria-label="Active flow">
            {activeFlow}
          </Button>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Label>Flows in this config</Menu.Label>
          {flowNames.map((f) => (
            <Menu.Item
              key={f}
              onClick={() => setActiveFlow(f)}
              rightSection={f === activeFlow ? "✓" : undefined}
              style={{ fontFamily: "ui-monospace, Menlo, monospace" }}
            >
              {f}
            </Menu.Item>
          ))}
          <Menu.Divider />
          <Menu.Item onClick={openCreate} aria-label="New flow">
            New flow…
          </Menu.Item>
        </Menu.Dropdown>
      </Menu>
      <Modal opened={opened} onClose={close} title="New flow" size="sm" transitionProps={{ duration: 0 }}>
        <Stack gap="sm">
          <Text size="sm" c="dimmed">
            A flow is one HTTP pipeline. Each needs its own http_trigger path. Destinations are shared across flows.
          </Text>
          <TextInput
            label="Name"
            aria-label="New flow name"
            description="YAML key under flows:. Use letters, digits, and underscores."
            placeholder="billing_webhooks"
            value={name}
            data-autofocus
            error={error || undefined}
            onChange={(e) => {
              setName(e.currentTarget.value);
              setError("");
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") submit();
            }}
          />
          <Group justify="flex-end">
            <Button variant="subtle" onClick={close}>
              Cancel
            </Button>
            <Button onClick={submit}>Create</Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}
