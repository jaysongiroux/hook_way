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
 * the flow list leaves you halfway down a form otherwise. The cap has to be on
 * the viewport: max-height on the root only clips, because that root is
 * overflow:hidden and the viewport's height is 100% of an auto-sized parent,
 * so it grows with the form and never becomes a scrollport.
 */
export function InspectorModal({
  opened,
  onClose,
  focusPath,
}: {
  opened: boolean;
  onClose: () => void;
  focusPath?: string;
}) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const scrollKey = useGraphStore(
    (s) =>
      `${s.activeFlow}:${s.selectedId ?? "flow"}:${s.selectedEdgeId ?? "no-edge"}`,
  );

  useEffect(() => {
    if (viewportRef.current) viewportRef.current.scrollTop = 0;
  }, [scrollKey]);

  useEffect(() => {
    if (!opened || focusPath === undefined) return;
    const timer = window.setTimeout(() => {
      const root = viewportRef.current;
      if (!root) return;
      if (focusPath)
        root
          .querySelectorAll<HTMLButtonElement>('button[aria-expanded="false"]')
          .forEach((button) => button.click());
      requestAnimationFrame(() => {
        const input = focusPath
          ? Array.from(
              root.querySelectorAll<HTMLElement>("[data-field-path]"),
            ).find((el) => el.dataset.fieldPath === focusPath)
          : root.querySelector<HTMLElement>("input,textarea");
        input?.focus();
        input?.scrollIntoView({ block: "center" });
      });
    }, 100);
    return () => clearTimeout(timer);
  }, [opened, focusPath, scrollKey]);

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Inspector"
      size="xl"
      closeButtonProps={{ "aria-label": "Close Inspector" }}
      styles={{ body: { padding: 0 } }}
    >
      <ScrollArea
        viewportRef={viewportRef}
        type="auto"
        styles={{ viewport: { maxHeight: "70vh" } }}
        px="md"
        py="md"
      >
        <Inspector />
      </ScrollArea>
    </Modal>
  );
}
