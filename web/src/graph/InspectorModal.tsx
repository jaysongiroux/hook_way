import { useEffect, useRef } from "react";
import { Modal, ScrollArea } from "@mantine/core";
import { Inspector } from "./Inspector";
import { useGraphStore } from "./store";
import { yamlIdOf } from "./model";
import { nodeLabel } from "./nodeSchema";
import { destinationTypeOf } from "./nodeIcon";
import { destinationPresentation } from "../shared/ui/destinationIdentity";

/**
 * The inspector lives in a modal rather than a side panel so its forms get the
 * full width of the screen. The title identifies the selected node using its
 * YAML ID and the same readable type or destination service as the canvas.
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
  const node = useGraphStore((s) =>
    s.selectedEdgeId ? undefined : s.nodes.find((n) => n.id === s.selectedId),
  );
  const destinations = useGraphStore((s) => s.destinations);
  const destinationType = node?.data.nodeType === "publish"
    ? destinationTypeOf(destinations, String(node.data.options.destination ?? ""))
    : "";
  const label = node
    ? destinationType
      ? `Send to ${destinationPresentation(destinationType).label}`
      : nodeLabel(node.data.nodeType)
    : "";
  const title = node ? `Inspector · ${yamlIdOf(node)} · ${label}` : "Inspector";
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
      title={title}
      size="xl"
      closeButtonProps={{ "aria-label": "Close Inspector" }}
      styles={{
        title: { minWidth: 0, overflowWrap: "anywhere" },
        body: { padding: 0 },
      }}
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
