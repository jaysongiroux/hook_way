import { AppShell, Group, Text, Title, Box, Button, ScrollArea } from "@mantine/core";
import { useDisclosure, useDocumentTitle } from "@mantine/hooks";
import { Palette } from "./graph/Palette";
import { Canvas } from "./graph/Canvas";
import { Inspector } from "./graph/Inspector";
import { ExportModal } from "./graph/ExportModal";
import { EnvPanel } from "./graph/EnvPanel";
import { ServerPanel } from "./graph/ServerPanel";
import { ImportModal } from "./graph/ImportModal";
import { usePersistence } from "./graph/usePersistence";
import { useGraphStore } from "./graph/store";
import { clearGraph } from "./graph/persistence";
import { BrandIcon } from "./BrandIcon";
import { BUILDER_CSS, BUILDER_ROOT_CLASS, BUILDER_TOOLBAR_CLASS } from "./builderStyles";

const HEADER_HEIGHT = 60;

// App is the enterprise three-pane shell: header (toolbar), left navbar
// (palette), right aside (inspector placeholder — Milestone 5), and the main
// canvas. The canvas gets an explicit viewport-based height because React Flow
// needs a sized container.
export default function App() {
  useDocumentTitle("Hookway Config");
  usePersistence();
  const [importOpen, { open: openImport, close: closeImport }] = useDisclosure(false);
  const [exportOpen, { open: openExport, close: closeExport }] = useDisclosure(false);
  const [envOpen, { open: openEnv, close: closeEnv }] = useDisclosure(false);
  const [serverOpen, { open: openServer, close: closeServer }] = useDisclosure(false);

  return (
    <>
      <style>{BUILDER_CSS}</style>
      <AppShell
        className={BUILDER_ROOT_CLASS}
        header={{ height: HEADER_HEIGHT }}
        navbar={{ width: 232, breakpoint: "sm" }}
        aside={{ width: 320, breakpoint: "md" }}
        padding={0}
      >
        <AppShell.Header>
          <Group h="100%" px={18} justify="space-between">
            <Group gap={8}>
              <BrandIcon size={30} />
              <Title order={4} c="white" style={{ letterSpacing: -0.3 }}>
                Hookway Config
              </Title>
            </Group>
            <Group gap={2} className={BUILDER_TOOLBAR_CLASS}>
              <Button size="xs" variant="subtle" onClick={openImport}>
                Import
              </Button>
              <Button size="xs" variant="subtle" onClick={openExport}>
                Export
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
                className="hookway-clear-action"
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
          <Box mb="md">
            <Text fw={700} size="xs" tt="uppercase" c="white" style={{ letterSpacing: 1.2 }}>
              Palette
            </Text>
            <Text size="xs" c="dimmed" mt={3}>
              Drag a block onto the canvas
            </Text>
          </Box>
          <Palette />
        </AppShell.Navbar>

        <AppShell.Aside p={0}>
          <ScrollArea h="100%" type="auto" px="md" py="md" scrollbarSize={8}>
            <Text fw={700} size="xs" tt="uppercase" c="white" mb="md" style={{ letterSpacing: 1.2 }}>
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
      </AppShell>
    </>
  );
}
