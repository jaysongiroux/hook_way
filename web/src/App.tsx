import { Box, Button, Group, Modal, Stack, Text } from "@mantine/core";
import { useDisclosure, useDocumentTitle, useMediaQuery } from "@mantine/hooks";
import { useEffect, useRef, useState } from "react";
import { Palette } from "./graph/Palette";
import { Canvas } from "./graph/Canvas";
import { Inspector } from "./graph/Inspector";
import { ExportModal } from "./graph/ExportModal";
import { EnvPanel } from "./graph/EnvPanel";
import { ServerPanel } from "./graph/ServerPanel";
import { DestinationsPanel } from "./graph/DestinationsPanel";
import { ImportModal } from "./graph/ImportModal";
import { BuilderHeader } from "./graph/BuilderHeader";
import { SidePanel, usePanelWidths } from "./graph/BuilderShell";
import { BUILDER_CSS } from "./graph/builderStyles";
import { usePersistence } from "./graph/usePersistence";
import { useGraphStore } from "./graph/store";
import { clearGraph } from "./graph/persistence";
import { loadValidator } from "./graph/validate";
import { starterDocument } from "./graph/starter";
import { DARK } from "./tokens";

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
  const [clearOpen, { open: openClear, close: closeClear }] = useDisclosure(false);
  const compact = useMediaQuery("(max-width: 64em)") ?? false;
  const compactRef = useRef(compact);
  const [paletteOpen, setPaletteOpen] = useState(true);
  const [inspectorOpen, setInspectorOpen] = useState(true);
  const { bodyRef, paletteWidth, inspectorWidth, setPalette, setInspector } = usePanelWidths();

  useEffect(() => {
    if (compact && !compactRef.current) {
      setPaletteOpen(false);
      setInspectorOpen(false);
    }
    if (!compact && compactRef.current) {
      setPaletteOpen(true);
      setInspectorOpen(true);
    }
    compactRef.current = compact;
  }, [compact]);

  const togglePalette = () => {
    setPaletteOpen((open) => {
      const next = !open;
      if (next && compact) setInspectorOpen(false);
      return next;
    });
  };
  const toggleInspector = () => {
    setInspectorOpen((open) => {
      const next = !open;
      if (next && compact) setPaletteOpen(false);
      return next;
    });
  };

  const confirmClear = () => {
    useGraphStore.getState().clear();
    void clearGraph();
    closeClear();
  };

  return (
    <Box style={{ height: "100vh", display: "flex", flexDirection: "column", minHeight: 0, background: DARK.bg }}>
      <style>{BUILDER_CSS}</style>
      <BuilderHeader
        paletteOpen={paletteOpen}
        inspectorOpen={inspectorOpen}
        onTogglePalette={togglePalette}
        onToggleInspector={toggleInspector}
        onImport={openImport}
        onExport={openExport}
        onDestinations={openDest}
        onEnv={openEnv}
        onServer={openServer}
        onLoadExample={() => useGraphStore.getState().setGraph(starterDocument())}
        onClear={openClear}
      />

      <Box ref={bodyRef} style={{ flex: 1, minHeight: 0, display: "flex", position: "relative" }}>
        {compact && (paletteOpen || inspectorOpen) && (
          <Box
            aria-hidden
            onClick={() => {
              setPaletteOpen(false);
              setInspectorOpen(false);
            }}
            style={{ position: "absolute", inset: 0, zIndex: 25, background: "rgba(20, 24, 31, 0.28)" }}
          />
        )}
        <SidePanel
          title="Nodes"
          open={paletteOpen}
          overlay={compact}
          side="left"
          width={paletteWidth}
          onResize={(w) => setPalette(w, inspectorOpen && !compact, inspectorWidth)}
          onClose={() => setPaletteOpen(false)}
        >
          <Palette />
        </SidePanel>
        <Box style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
          <Canvas />
        </Box>
        <SidePanel
          title="Inspector"
          open={inspectorOpen}
          overlay={compact}
          side="right"
          width={inspectorWidth}
          onResize={(w) => setInspector(w, paletteOpen && !compact, paletteWidth)}
          onClose={() => setInspectorOpen(false)}
        >
          <Inspector />
        </SidePanel>
      </Box>

      <ImportModal opened={importOpen} onClose={closeImport} />
      <ExportModal opened={exportOpen} onClose={closeExport} />
      <EnvPanel opened={envOpen} onClose={closeEnv} />
      <ServerPanel opened={serverOpen} onClose={closeServer} />
      <DestinationsPanel opened={destOpen} onClose={closeDest} />
      <Modal opened={clearOpen} onClose={closeClear} title="Clear the canvas?" size="sm" transitionProps={{ duration: 0 }}>
        <Stack gap="sm">
          <Text size="sm">
            This removes every node, edge, destination, and server setting from the saved draft. Import YAML again if you still have a copy.
          </Text>
          <Group justify="flex-end">
            <Button variant="subtle" onClick={closeClear}>
              Cancel
            </Button>
            <Button color="red" onClick={confirmClear}>
              Clear canvas
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Box>
  );
}
