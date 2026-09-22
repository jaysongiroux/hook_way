import { Box, Button, Group, Modal, Stack, Text } from "@mantine/core";
import { useDisclosure, useDocumentTitle, useMediaQuery } from "@mantine/hooks";
import { useEffect, useRef, useState } from "react";
import { Palette, PaletteTabs, type PaletteTab } from "./graph/Palette";
import { Canvas } from "./graph/Canvas";
import { InspectorModal } from "./graph/InspectorModal";
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
  const [inspectorOpen, { open: openInspector, close: closeInspector }] = useDisclosure(false);
  const compact = useMediaQuery("(max-width: 64em)") ?? false;
  const compactRef = useRef(compact);
  const [paletteOpen, setPaletteOpen] = useState(true);
  const [paletteTab, setPaletteTab] = useState<PaletteTab>("nodes");
  const { bodyRef, paletteWidth, setPalette } = usePanelWidths();

  // The palette is docked on wide screens and an overlay on narrow ones, where
  // it would otherwise eat most of the canvas.
  useEffect(() => {
    if (compact !== compactRef.current) setPaletteOpen(!compact);
    compactRef.current = compact;
  }, [compact]);

  const confirmClear = () => {
    useGraphStore.getState().clear();
    void clearGraph();
    closeClear();
  };

  return (
    <Box
      className="hookway-builder-root"
      style={{ height: "100vh", display: "flex", flexDirection: "column", minHeight: 0, background: DARK.bg }}
    >
      <style>{BUILDER_CSS}</style>
      <BuilderHeader
        paletteOpen={paletteOpen}
        onTogglePalette={() => setPaletteOpen((open) => !open)}
        onInspector={openInspector}
        onFlowSettings={() => {
          // The inspector's flow view is what shows with nothing selected, so
          // reaching it means dropping the current selection.
          useGraphStore.getState().setSelected(null);
          openInspector();
        }}
        onImport={openImport}
        onExport={openExport}
        onDestinations={openDest}
        onEnv={openEnv}
        onServer={openServer}
        onLoadExample={() => useGraphStore.getState().setGraph(starterDocument())}
        onClear={openClear}
      />

      <Box ref={bodyRef} style={{ flex: 1, minHeight: 0, display: "flex", position: "relative" }}>
        {compact && paletteOpen && (
          <Box
            aria-hidden
            onClick={() => setPaletteOpen(false)}
            style={{ position: "absolute", inset: 0, zIndex: 25, background: "rgba(20, 24, 31, 0.28)" }}
          />
        )}
        <SidePanel
          title="Nodes"
          open={paletteOpen}
          overlay={compact}
          width={paletteWidth}
          scrollKey={paletteTab}
          onResize={setPalette}
          onClose={() => setPaletteOpen(false)}
          headerContent={<PaletteTabs value={paletteTab} onChange={setPaletteTab} />}
        >
          <Palette tab={paletteTab} />
        </SidePanel>
        <Box style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
          <Canvas onNodeActivate={openInspector} />
        </Box>
      </Box>

      <InspectorModal opened={inspectorOpen} onClose={closeInspector} />
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
