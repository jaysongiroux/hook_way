import { afterEach, expect, test, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import { theme } from "../theme";
import { FlowDiagram } from "./FlowDiagram";
import { FLOW_EDGES, FLOW_NODES, LAYOUT } from "./flowGraph";

// jsdom reports no media match, so the component renders its narrow-screen
// variant by default. matchWide() flips that to cover the SVG branch.
function matchWide() {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: true,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }),
  });
}

const narrowMatchMedia = window.matchMedia;
afterEach(() => {
  Object.defineProperty(window, "matchMedia", { writable: true, value: narrowMatchMedia });
});

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <MemoryRouter>
        <FlowDiagram />
      </MemoryRouter>
    </MantineProvider>,
  );
}

test("every node in the graph is labelled on the page", () => {
  wrap();
  expect(screen.getAllByText("Filter")).toHaveLength(2);
  for (const n of FLOW_NODES) {
    // Both filters share a label, so assert the detail line that differs.
    expect(screen.getByText(n.sub)).toBeInTheDocument();
  }
  for (const n of FLOW_NODES.filter((n) => n.kind !== "filter")) {
    expect(screen.getByText(n.label)).toBeInTheDocument();
  }
});

test("the diagram carries a text description for assistive tech", () => {
  wrap();
  const fig = screen.getByRole("img", { name: /fan.?out|endpoint/i });
  expect(fig).toBeInTheDocument();
});

test("the section invites the reader into the builder", () => {
  wrap();
  const cta = screen.getByRole("link", { name: /builder/i });
  expect(cta).toHaveAttribute("href", "/builder");
});

test("on a wide viewport the nodes are drawn as one connected svg", () => {
  matchWide();
  const { container } = wrap();

  const svg = container.querySelector("svg")!;
  expect(svg).toBeInTheDocument();
  // One rounded card plus one accent bar per node, one base path per edge
  // (the pulse overlays add a second path each, counted separately below).
  expect(svg.querySelectorAll("path:not(.hookway-pulse)")).toHaveLength(FLOW_EDGES.length);
  expect(svg.querySelectorAll("g")).toHaveLength(FLOW_NODES.length);
  expect(svg.getAttribute("viewBox")).toBe(`0 0 ${LAYOUT.width} ${LAYOUT.height}`);
});

test("the wide and narrow layouts label the same nodes", () => {
  matchWide();
  const wide = wrap();
  const wideText = wide.container.querySelector("svg")!.textContent!;
  wide.unmount();

  Object.defineProperty(window, "matchMedia", { writable: true, value: narrowMatchMedia });
  const narrow = wrap();
  for (const n of FLOW_NODES) {
    expect(wideText).toContain(n.sub);
    expect(narrow.container.textContent).toContain(n.sub);
  }
});

// The pulse is the section's only motion. It has to exist on every edge and it
// has to be switchable off for readers who ask for reduced motion.
test("a pulse travels every edge and honours reduced-motion", () => {
  matchWide();
  const { container } = wrap();

  const svg = container.querySelector("svg")!;
  expect(svg.querySelectorAll(".hookway-pulse")).toHaveLength(FLOW_EDGES.length);

  // Scope to the diagram's own stylesheet; Mantine injects one of its own.
  const css = svg.querySelector("style")!.textContent!;
  expect(css).toContain("prefers-reduced-motion");
  // Under reduced motion the travelling dash must stop, not just slow down.
  expect(css).toMatch(/prefers-reduced-motion[\s\S]*animation:\s*none/);
});

test("the pulses are staggered so the fan-out reads as parallel branches", () => {
  matchWide();
  const { container } = wrap();
  const delays = [...container.querySelectorAll(".hookway-pulse")].map(
    (el) => (el as SVGElement).style.animationDelay,
  );
  expect(new Set(delays).size).toBeGreaterThan(1);
});
