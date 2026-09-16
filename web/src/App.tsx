import { AppShell, Box, Button, Group, ScrollArea, Select, Text, TextInput, Title } from "@mantine/core";
import { useDisclosure, useDocumentTitle } from "@mantine/hooks";
import { useEffect, useState } from "react";
import { Palette } from "./graph/Palette";
import { Canvas } from "./graph/Canvas";
import { Inspector } from "./graph/Inspector";
import { ExportModal } from "./graph/ExportModal";
import { EnvPanel } from "./graph/EnvPanel";
import { ServerPanel } from "./graph/ServerPanel";
import { DestinationsPanel } from "./graph/DestinationsPanel";
import { ImportModal } from "./graph/ImportModal";
import { usePersistence } from "./graph/usePersistence";
import { useGraphStore } from "./graph/store";
import { clearGraph } from "./graph/persistence";
import { loadValidator } from "./graph/validate";

const HEADER_HEIGHT = 52;

export default function App() {
  useDocumentTitle("Hookway Config");
  usePersistence();
  useEffect(() => {
    void loadValidator();
  }, []);
  const [importOpen, { open: openImport, close: closeImport }] = useDisclosure(false);
  const [exportOpen, { open: openExport, close: closeExport }] = useDisclosure(false);
  const [envOpen, { open: openEnv, close: closeEnv }] = useDisclosure(false);
  const [serverOpen, { open: openServer, close: closeServer }] = useDisclosure(false);
  const [destOpen, { open: openDest, close: closeDest }] = useDisclosure(false);
  const [newFlow, setNewFlow] = useState("");
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const otherFlows = useGraphStore((s) => s.otherFlows);
  const setActiveFlow = useGraphStore((s) => s.setActiveFlow);
  const addFlow = useGraphStore((s) => s.addFlow);
  const flowNames = [activeFlow, ...Object.keys(otherFlows)].sort();

  return (
    <AppShell
      header={{ height: HEADER_HEIGHT }}
      navbar={{ width: 230, breakpoint: "sm" }}
      aside={{ width: 300, breakpoint: "md" }}
      padding={0}
    >
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Group gap="sm">
            <Title order={4} c="brand.6">
              Hookway Config
            </Title>
            <Select
              aria-label="Active flow"
              size="xs"
              data={flowNames}
              value={activeFlow}
              allowDeselect={false}
              comboboxProps={{ keepMounted: false }}
              onChange={(v) => v && setActiveFlow(v)}
              w={160}
            />
            <TextInput
              size="xs"
              placeholder="new flow"
              value={newFlow}
              onChange={(e) => setNewFlow(e.currentTarget.value)}
              w={110}
            />
            <Button
              size="xs"
              variant="subtle"
              onClick={() => {
                addFlow(newFlow);
                setNewFlow("");
              }}
            >
              Add flow
            </Button>
          </Group>
          <Group gap="xs">
            <Button size="xs" variant="subtle" onClick={openImport}>
              Import
            </Button>
            <Button size="xs" variant="subtle" onClick={openExport}>
              Export
            </Button>
            <Button size="xs" variant="subtle" onClick={openDest}>
              Destinations
            </Button>
            <Button size="xs" variant="subtle" onClick={openEnv}>
              Environment
            </Button>
            <Button size="xs" variant="subtle" onClick={openServer}>
              Server
            </Button>
            <Button
              size="xs"
              variant="subtle"
              color="red"
              onClick={() => {
                useGraphStore.getState().clear();
                void clearGraph();
              }}
            >
              Clear
            </Button>
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar p="md">
        <Text fw={600} size="sm" tt="uppercase" c="dimmed" mb="sm">
          Palette
        </Text>
        <ScrollArea h={`calc(100vh - ${HEADER_HEIGHT + 48}px)`} type="auto">
          <Palette />
        </ScrollArea>
      </AppShell.Navbar>

      <AppShell.Aside p={0}>
        <ScrollArea h="100%" type="auto" px="md" py="md">
          <Text fw={600} size="sm" tt="uppercase" c="dimmed" mb="sm">
            Inspector
          </Text>
          <Inspector />
        </ScrollArea>
      </AppShell.Aside>

      <AppShell.Main>
        <Box style={{ height: `calc(100vh - ${HEADER_HEIGHT}px)` }}>
          <Canvas />
        </Box>
      </AppShell.Main>

      <ImportModal opened={importOpen} onClose={closeImport} />
      <ExportModal opened={exportOpen} onClose={closeExport} />
      <EnvPanel opened={envOpen} onClose={closeEnv} />
      <ServerPanel opened={serverOpen} onClose={closeServer} />
      <DestinationsPanel opened={destOpen} onClose={closeDest} />
    </AppShell>
  );
}
