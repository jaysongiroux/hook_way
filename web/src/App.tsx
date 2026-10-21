import { Alert, Box, Button, Group, Modal, Stack, Text } from "@mantine/core";
import { useDisclosure, useDocumentTitle, useMediaQuery } from "@mantine/hooks";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Palette, PaletteTabs, type PaletteTab } from "./graph/Palette";
import { Canvas } from "./graph/Canvas";
import { InspectorModal } from "./graph/InspectorModal";
import { ExportModal } from "./graph/ExportModal";
import { EnvPanel } from "./graph/EnvPanel";
import { ServerPanel } from "./graph/ServerPanel";
import { ReplaySettingsPanel } from "./graph/ReplaySettingsPanel";
import { DestinationsPanel } from "./graph/DestinationsPanel";
import { ConnectionsPanel } from "./graph/ConnectionsPanel";
import { OpenAPIWizard } from "./graph/OpenAPIWizard";
import { ImportModal } from "./graph/ImportModal";
import { BuilderHeader } from "./graph/BuilderHeader";
import { SidePanel, usePanelWidths } from "./graph/BuilderShell";
import { BUILDER_CSS } from "./graph/builderStyles";
import { usePersistence } from "./graph/usePersistence";
import { useGraphStore } from "./graph/store";
import { clearGraph } from "./graph/persistence";
import { retryWasm } from "./shared/wasm/loader";
import { VALIDATOR_RETRY_EVENT } from "./shared/wasm/validationMode";
import { loadValidator } from "./graph/validate";
import { starterDocument } from "./graph/starter";
import { RoutingPanel } from "./graph/RoutingPanel";
import { LaunchDarklyPanel } from "./graph/LaunchDarklyForm";
import { LookupsPanel } from "./graph/LookupsPanel";
import { StateStoresPanel } from "./graph/StateStoresPanel";
import { CredentialsPanel } from "./graph/CredentialsPanel";
import {
  WorkspaceActions,
  type WorkspacePanel,
} from "./graph/FormPresentation";
import {
  BuilderValidationProvider,
  locateIssue,
} from "./graph/BuilderValidation";
import { createNode } from "./graph/model";
import { templateSubgraph } from "./graph/templates";
import { DARK } from "./tokens";

export default function App({ product = false, headerActions }: { product?: boolean; headerActions?: ReactNode }) {
  const [validator, setValidator] = useState<"loading" | "ready" | "error">("loading");
  const initializeValidator = async (retry = false) => {
    setValidator("loading");
    try {
      await (retry ? retryWasm() : loadValidator());
      setValidator("ready");
      if (retry) window.dispatchEvent(new Event(VALIDATOR_RETRY_EVENT));
    } catch { setValidator("error"); }
  };
  useDocumentTitle("Hookway Config");
  usePersistence(product);
  useEffect(() => { void initializeValidator(); }, []);
  const [openapiOpen, { open: openOpenAPI, close: closeOpenAPI }] = useDisclosure(false);
  const [importOpen, { open: openImport, close: closeImport }] =
    useDisclosure(false);
  const [exportOpen, { open: openExport, close: closeExport }] =
    useDisclosure(false);
  const [envOpen, { open: openEnv, close: closeEnv }] = useDisclosure(false);
  const [serverOpen, { open: openServer, close: closeServer }] =
    useDisclosure(false);
  const [replayOpen, { open: openReplay, close: closeReplay }] =
    useDisclosure(false);
  const [destOpen, { open: openDest, close: closeDest }] = useDisclosure(false);
  const [routingOpen, { open: openRouting, close: closeRouting }] =
    useDisclosure(false);
  const [credentialsOpen, { open: openCredentials, close: closeCredentials }] =
    useDisclosure(false);
  const [lookupsOpen, { open: openLookups, close: closeLookups }] =
    useDisclosure(false);
  const [launchDarklyOpen, { open: openLaunchDarkly, close: closeLaunchDarkly }] = useDisclosure(false);
 const [connOpen, { open: openConn, close: closeConn }] = useDisclosure(false);
  const [stateStoresOpen, { open: openStateStores, close: closeStateStores }] = useDisclosure(false);
  const [clearOpen, { open: openClear, close: closeClear }] =
    useDisclosure(false);
  const [inspectorOpen, { open: openInspector, close: closeInspector }] =
    useDisclosure(false);
  const compact = useMediaQuery("(max-width: 64em)") ?? false;
  const compactRef = useRef(compact);
  const [paletteOpen, setPaletteOpen] = useState(true);
  const [returnToInspector, setReturnToInspector] = useState(false);
  const [focusPath, setFocusPath] = useState<string>();
  const [paletteTab, setPaletteTab] = useState<PaletteTab>("nodes");
  const { bodyRef, paletteWidth, setPalette } = usePanelWidths();

  // The palette is docked on wide screens and an overlay on narrow ones, where
  // it would otherwise eat most of the canvas.
  useEffect(() => {
    if (compact !== compactRef.current) setPaletteOpen(!compact);
    compactRef.current = compact;
  }, [compact]);

  const clearWorkspace = () => {
    useGraphStore.getState().clear();
    void clearGraph();
    closeClear();
  };

  const clearCore = () => {
    useGraphStore.getState().clearCore();
    closeClear();
  };

  const removeReplay = () => {
    useGraphStore.getState().removeReplayConfiguration();
    closeClear();
  };

  const openWorkspace = (panel: WorkspacePanel) => {
    setReturnToInspector(true);
    closeInspector();
    ({
      lookups: openLookups,
 launchdarkly: openLaunchDarkly,
      state_stores: openStateStores,
      routing: openRouting,
      credentials: openCredentials,
      destinations: openDest,
    })[panel]();
  };
  const closeWorkspace = (close: () => void) => {
    close();
    if (returnToInspector) openInspector();
    setReturnToInspector(false);
  };
  return (
    <BuilderValidationProvider>
      <WorkspaceActions.Provider value={openWorkspace}>
        <Box
          className="hookway-builder-root"
          style={{
            height: product ? "100%" : "100dvh",
            flex: product ? 1 : undefined,
            display: "flex",
            flexDirection: "column",
            minHeight: 0,
            background: DARK.bg,
          }}
        >
          <style>{BUILDER_CSS}</style>
          <BuilderHeader
            actions={headerActions}
            paletteOpen={paletteOpen}
            onTogglePalette={() => setPaletteOpen((open) => !open)}
            onInspector={openInspector}
            onFlowSettings={() => {
              // The inspector's flow view is what shows with nothing selected, so
              // reaching it means dropping the current selection.
              useGraphStore.getState().setSelected(null);
              openInspector();
            }}
            onOpenAPI={openOpenAPI}
            onImport={openImport}
            onExport={openExport}
            onDestinations={openDest}
            onConnections={openConn}
            onRouting={openRouting}
            onCredentials={openCredentials}
            onLookups={openLookups}
 onLaunchDarkly={openLaunchDarkly}
            onStateStores={openStateStores}
            onEnv={openEnv}
            onServer={openServer}
            onReplay={openReplay}
            onLoadExample={() =>
              useGraphStore.getState().setGraph(starterDocument())
            }
            onClear={openClear}
          />

          {product && validator === "error" && <Alert color="red" title="Local validation unavailable" m="sm">
            This configuration has not been validated. Editing, imports and exports remain available. Restore the matching WASM assets and retry.
            <Button variant="light" size="xs" ml="sm" onClick={() => void initializeValidator(true)}>Retry local validation</Button>
          </Alert>}

          <Box
            ref={bodyRef}
            style={{
              flex: 1,
              minHeight: 0,
              display: "flex",
              position: "relative",
            }}
          >
            {compact && paletteOpen && (
              <Box
                aria-hidden
                onClick={() => setPaletteOpen(false)}
                style={{
                  position: "absolute",
                  inset: 0,
                  zIndex: 25,
                  background: "rgba(20, 24, 31, 0.28)",
                }}
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
              headerContent={
                <PaletteTabs value={paletteTab} onChange={setPaletteTab} />
              }
            >
              <Palette
                tab={paletteTab}
                onAddNode={(type) => {
                  const store = useGraphStore.getState();
                  const node = createNode(type, {
                    x: 80,
                    y: 80 + store.nodes.length * 40,
                  });
                  store.addNode(node);
                  store.setSelected(node.id);
                  if (compact) setPaletteOpen(false);
                  openInspector();
                }}
                onAddTemplate={(id) => {
                  const sub = templateSubgraph(id);
                  if (sub) {
                    const store = useGraphStore.getState();
                    store.insertSubgraph(sub, {
                      x: 80,
                      y: 80 + store.nodes.length * 40,
                    });
                    store.arrangeNodes();
                    if (compact) setPaletteOpen(false);
                  }
                }}
              />
            </SidePanel>
            <Box style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
              <Canvas onNodeActivate={openInspector} onOpenAPI={openOpenAPI} />
            </Box>
          </Box>

          <RoutingPanel
            opened={routingOpen}
            onClose={() => closeWorkspace(closeRouting)}
          />
          <LookupsPanel
            opened={lookupsOpen}
            onClose={() => closeWorkspace(closeLookups)}
          />
          <LaunchDarklyPanel opened={launchDarklyOpen} onClose={() => closeWorkspace(closeLaunchDarkly)} />
 <StateStoresPanel opened={stateStoresOpen} onClose={() => closeWorkspace(closeStateStores)} />
          <CredentialsPanel
            opened={credentialsOpen}
            onClose={() => closeWorkspace(closeCredentials)}
          />
          <InspectorModal
            opened={inspectorOpen}
            onClose={() => {
              setFocusPath(undefined);
              closeInspector();
            }}
            focusPath={focusPath}
          />
          {openapiOpen && <OpenAPIWizard onClose={closeOpenAPI} />}
          <ImportModal opened={importOpen} onClose={closeImport} />
          <ExportModal
            opened={exportOpen}
            onClose={closeExport}
            onIssue={(field) => {
              const store = useGraphStore.getState();
              const location = locateIssue(field, store);
              if (!location) return;
              if (location.flowId !== store.activeFlow)
                store.setActiveFlow(location.flowId);
              store.setSelected(location.nodeId ?? null);
              setFocusPath(location.path ?? "");
              closeExport();
              openInspector();
            }}
          />
          <EnvPanel opened={envOpen} onClose={closeEnv} />
          <ServerPanel opened={serverOpen} onClose={closeServer} />
          <ReplaySettingsPanel opened={replayOpen} onClose={closeReplay} />
          <DestinationsPanel
            opened={destOpen}
            onClose={() => closeWorkspace(closeDest)}
          />
          <ConnectionsPanel opened={connOpen} onClose={closeConn} />
          <Modal
            opened={clearOpen}
            onClose={closeClear}
            title="Clear configuration?"
            size="sm"
            transitionProps={{ duration: 0 }}
          >
            <Stack gap="md">
              <Text size="sm" c="dimmed">
                Choose the document boundary deliberately. These actions cannot
                be undone unless you exported a copy.
              </Text>
              <Stack gap={4}>
                <Button
                  color="red"
                  variant="light"
                  justify="space-between"
                  onClick={clearCore}
                >
                  Clear core configuration
                </Button>
                <Text size="xs" c="dimmed">
                  Reset flows, destinations, connections, core server settings,
                  and core&apos;s replay connection. Keep replay.yaml.
                </Text>
              </Stack>
              <Stack gap={4}>
                <Button
                  color="red"
                  variant="light"
                  justify="space-between"
                  onClick={removeReplay}
                >
                  Remove replay configuration
                </Button>
                <Text size="xs" c="dimmed">
                  Delete replay.yaml and remove core&apos;s replay integration
                  block.
                </Text>
              </Stack>
              <Stack gap={4}>
                <Button color="red" onClick={clearWorkspace}>
                  Clear entire workspace
                </Button>
                <Text size="xs" c="dimmed">
                  Reset both YAML documents and remove the saved draft.
                </Text>
              </Stack>
              <Group justify="flex-end">
                <Button variant="default" onClick={closeClear}>
                  Cancel
                </Button>
              </Group>
            </Stack>
          </Modal>
        </Box>
      </WorkspaceActions.Provider>
    </BuilderValidationProvider>
  );
}
