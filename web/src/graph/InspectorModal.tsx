import { useEffect, useRef } from "react";
import { Modal, ScrollArea } from "@mantine/core";
import { Inspector } from "./Inspector";
import { useGraphStore } from "./store";

/**
 * The inspector lives in a modal rather than a side panel so its forms get the
 * full width of the screen. The title is the constant "Inspector" even though
 * the body names the selection: the accessible name is what tests and screen
 * readers key off, and swapping it on every click made the dialog hard to
 * address.
 *
 * The scrolling is the modal's own ScrollArea rather than the body element so
 * the viewport can be reset when the selection changes -- clicking a node in
 * the flow list leaves you halfway down a form otherwise.
 */
export function InspectorModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const scrollKey = useGraphStore((s) => `${s.activeFlow}:${s.selectedId ?? "flow"}:${s.selectedEdgeId ?? "no-edge"}`);

  useEffect(() => {
    if (viewportRef.current) viewportRef.current.scrollTop = 0;
  }, [scrollKey]);

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Inspector"
      size="xl"
      closeButtonProps={{ "aria-label": "Close Inspector" }}
      styles={{ body: { padding: 0 } }}
    >
      <ScrollArea viewportRef={viewportRef} type="auto" style={{ maxHeight: "70vh" }} px="md" py="md">
        <Inspector />
      </ScrollArea>
    </Modal>
  );
}
