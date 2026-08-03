import { AppShell, Group, Text, Title, Box, Button, ScrollArea } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import { Palette } from "./graph/Palette";
import { Canvas } from "./graph/Canvas";
import { Inspector } from "./graph/Inspector";
import { ExportModal } from "./graph/ExportModal";
import { EnvPanel } from "./graph/EnvPanel";
import { ImportModal } from "./graph/ImportModal";
import { usePersistence } from "./graph/usePersistence";
import { useGraphStore } from "./graph/store";
import { clearGraph } from "./graph/persistence";

const HEADER_HEIGHT = 52;

// App is the enterprise three-pane shell: header (toolbar), left navbar
// (palette), right aside (inspector placeholder — Milestone 5), and the main
// canvas. The canvas gets an explicit viewport-based height because React Flow
// needs a sized container.
export default function App() {
  usePersistence();
  const [importOpen, { open: openImport, close: closeImport }] = useDisclosure(false);
  const [exportOpen, { open: openExport, close: closeExport }] = useDisclosure(false);
  const [envOpen, { open: openEnv, close: closeEnv }] = useDisclosure(false);

  return (
    <AppShell
      header={{ height: HEADER_HEIGHT }}
      navbar={{ width: 220, breakpoint: "sm" }}
      aside={{ width: 300, breakpoint: "md" }}
      padding={0}
    >
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          <Title order={4} c="brand.6">
            Hookway Config
          </Title>
          <Group gap="xs">
            <Button size="xs" variant="subtle" onClick={openImport}>Import</Button>
            <Button size="xs" variant="subtle" onClick={openExport}>Export</Button>
            <Button size="xs" variant="subtle" onClick={openEnv}>Environment</Button>
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
        <Palette />
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
    </AppShell>
  );
}
