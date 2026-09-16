import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { ActionIcon, Box, Group, ScrollArea, Text } from "@mantine/core";

const PALETTE_MIN = 180;
const PALETTE_MAX = 420;
const INSPECTOR_MIN = 240;
const INSPECTOR_MAX = 520;
const CANVAS_MIN = 280;

function clamp(n: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, n));
}

function readWidth(key: string, fallback: number, min: number, max: number): number {
  try {
    const raw = localStorage.getItem(key);
    const n = raw ? Number(raw) : fallback;
    return Number.isFinite(n) ? clamp(n, min, max) : fallback;
  } catch {
    return fallback;
  }
}

function persist(key: string, value: number) {
  try {
    localStorage.setItem(key, String(value));
  } catch {
    /* private mode */
  }
}

export function usePanelWidths() {
  const [paletteWidth, setPaletteWidth] = useState(() => readWidth("hookway.builder.paletteWidth", 260, PALETTE_MIN, PALETTE_MAX));
  const [inspectorWidth, setInspectorWidth] = useState(() => readWidth("hookway.builder.inspectorWidth", 300, INSPECTOR_MIN, INSPECTOR_MAX));
  const bodyRef = useRef<HTMLDivElement>(null);

  useEffect(() => persist("hookway.builder.paletteWidth", paletteWidth), [paletteWidth]);
  useEffect(() => persist("hookway.builder.inspectorWidth", inspectorWidth), [inspectorWidth]);

  const fit = useCallback(() => bodyRef.current?.clientWidth ?? 1200, []);

  const setPalette = useCallback(
    (next: number, inspectorOpen: boolean, inspectorW: number) => {
      const reserved = (inspectorOpen ? inspectorW : 0) + CANVAS_MIN;
      setPaletteWidth(clamp(next, PALETTE_MIN, Math.min(PALETTE_MAX, Math.max(PALETTE_MIN, fit() - reserved))));
    },
    [fit],
  );

  const setInspector = useCallback(
    (next: number, paletteOpen: boolean, paletteW: number) => {
      const reserved = (paletteOpen ? paletteW : 0) + CANVAS_MIN;
      setInspectorWidth(clamp(next, INSPECTOR_MIN, Math.min(INSPECTOR_MAX, Math.max(INSPECTOR_MIN, fit() - reserved))));
    },
    [fit],
  );

  return { bodyRef, paletteWidth, inspectorWidth, setPalette, setInspector };
}

export function SidePanel({
  title,
  open,
  overlay,
  side,
  width,
  onResize,
  onClose,
  children,
}: {
  title: string;
  open: boolean;
  overlay: boolean;
  side: "left" | "right";
  width: number;
  onResize: (next: number) => void;
  onClose: () => void;
  children: ReactNode;
}) {
  const start = useRef({ x: 0, w: 0 });
  if (!open) return null;
  const grow = side === "left" ? 1 : -1;
  return (
    <Box
      style={{
        width,
        flexShrink: 0,
        display: "flex",
        flexDirection: "column",
        position: overlay ? "absolute" : "relative",
        top: overlay ? 0 : undefined,
        bottom: overlay ? 0 : undefined,
        left: overlay && side === "left" ? 0 : undefined,
        right: overlay && side === "right" ? 0 : undefined,
        zIndex: overlay ? 30 : 2,
        background: "var(--mantine-color-body)",
        borderRight: side === "left" ? "1px solid var(--mantine-color-dark-6)" : undefined,
        borderLeft: side === "right" ? "1px solid var(--mantine-color-dark-6)" : undefined,
        boxShadow: overlay ? "0 0 28px rgba(0,0,0,0.4)" : undefined,
      }}
    >
      <Group px="sm" py={8} justify="space-between" wrap="nowrap">
        <Text fw={600} size="sm" tt="uppercase" c="dimmed">
          {title}
        </Text>
        <ActionIcon aria-label={`Close ${title}`} variant="subtle" size="sm" onClick={onClose}>
          ×
        </ActionIcon>
      </Group>
      <ScrollArea type="auto" style={{ flex: 1, minHeight: 0 }} px="sm" pb="sm">
        {children}
      </ScrollArea>
      <Box
        role="separator"
        aria-orientation="vertical"
        aria-label={`Resize ${title}`}
        onPointerDown={(e) => {
          e.preventDefault();
          (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
          start.current = { x: e.clientX, w: width };
        }}
        onPointerMove={(e) => {
          if (e.buttons !== 1) return;
          onResize(start.current.w + grow * (e.clientX - start.current.x));
        }}
        style={{
          position: "absolute",
          top: 0,
          bottom: 0,
          width: 10,
          [side === "left" ? "right" : "left"]: -5,
          cursor: "col-resize",
          zIndex: 4,
          display: "flex",
          justifyContent: "center",
        }}
      >
        <Box
          aria-hidden
          style={{
            width: 3,
            marginTop: "35%",
            marginBottom: "35%",
            borderRadius: 2,
            background: "var(--mantine-color-dark-2)",
          }}
        />
      </Box>
    </Box>
  );
}
