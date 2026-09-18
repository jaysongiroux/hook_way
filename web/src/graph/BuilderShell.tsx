import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Box, CloseButton, Group, ScrollArea, Text } from "@mantine/core";
import { DARK } from "../tokens";

const PALETTE_MIN = 220;
const PALETTE_MAX = 420;
const INSPECTOR_MIN = 300;
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
  const [inspectorWidth, setInspectorWidth] = useState(() => readWidth("hookway.builder.inspectorWidth", 340, INSPECTOR_MIN, INSPECTOR_MAX));
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
  headerContent,
  open,
  overlay,
  side,
  width,
  scrollKey,
  onResize,
  onClose,
  children,
}: {
  title: string;
  /**
   * Replaces the header row's title text — the palette puts its tab strip
   * here so it stays fixed while the list scrolls. `title` is still required
   * either way: it names the close and resize controls.
   */
  headerContent?: ReactNode;
  open: boolean;
  overlay: boolean;
  side: "left" | "right";
  width: number;
  /** Reset the panel viewport when its primary context changes. */
  scrollKey?: string;
  onResize: (next: number) => void;
  onClose: () => void;
  children: ReactNode;
}) {
  const start = useRef({ x: 0, w: 0 });
  const viewportRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (viewportRef.current) viewportRef.current.scrollTop = 0;
  }, [scrollKey]);

  if (!open) return null;
  const grow = side === "left" ? 1 : -1;
  const minWidth = side === "left" ? PALETTE_MIN : INSPECTOR_MIN;
  const maxWidth = side === "left" ? PALETTE_MAX : INSPECTOR_MAX;
  return (
    <Box
      style={{
        width: overlay ? `min(${width}px, calc(100% - 40px))` : width,
        flexShrink: 0,
        display: "flex",
        flexDirection: "column",
        position: overlay ? "absolute" : "relative",
        top: overlay ? 0 : undefined,
        bottom: overlay ? 0 : undefined,
        left: overlay && side === "left" ? 0 : undefined,
        right: overlay && side === "right" ? 0 : undefined,
        zIndex: overlay ? 30 : 2,
        background: DARK.surface,
        borderRight: side === "left" ? `1px solid ${DARK.border}` : undefined,
        borderLeft: side === "right" ? `1px solid ${DARK.border}` : undefined,
        boxShadow: overlay
          ? side === "left"
            ? "4px 0 16px rgba(20, 24, 31, 0.12)"
            : "-4px 0 16px rgba(20, 24, 31, 0.12)"
          : undefined,
      }}
    >
      <Group
        px="sm"
        py={10}
        justify="space-between"
        wrap="nowrap"
        style={{ borderBottom: `1px solid ${DARK.border}`, background: DARK.recessed, flexShrink: 0 }}
      >
        {headerContent ?? (
          <Text fw={600} size="xs" tt="uppercase" c={DARK.muted} style={{ letterSpacing: "0.08em" }}>
            {title}
          </Text>
        )}
        <CloseButton aria-label={`Close ${title}`} variant="subtle" size="sm" onClick={onClose} />
      </Group>
      <ScrollArea viewportRef={viewportRef} type="auto" style={{ flex: 1, minHeight: 0 }} px="sm" pt="sm" pb="sm">
        {children}
      </ScrollArea>
      <Box
        role="separator"
        tabIndex={0}
        aria-orientation="vertical"
        aria-label={`Resize ${title}`}
        aria-valuemin={minWidth}
        aria-valuemax={maxWidth}
        aria-valuenow={width}
        aria-valuetext={`${width} pixels`}
        onPointerDown={(e) => {
          e.preventDefault();
          (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
          start.current = { x: e.clientX, w: width };
        }}
        onPointerMove={(e) => {
          if (e.buttons !== 1) return;
          onResize(start.current.w + grow * (e.clientX - start.current.x));
        }}
        onKeyDown={(e) => {
          const delta = e.key === "ArrowRight" ? 16 : e.key === "ArrowLeft" ? -16 : 0;
          if (!delta) return;
          e.preventDefault();
          onResize(width + grow * delta);
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
            background: DARK.border,
          }}
        />
      </Box>
    </Box>
  );
}
