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
  expect(screen.getAllByText("filter")).toHaveLength(2);
  expect(screen.getAllByText("publish")).toHaveLength(3);
  expect(screen.getByText("http trigger")).toBeInTheDocument();
  for (const n of FLOW_NODES) {
    expect(screen.getByText(n.yamlId)).toBeInTheDocument();
    expect(screen.getByText(n.summary)).toBeInTheDocument();
  }
});

test("the diagram carries a text description for assistive tech", () => {
  wrap();
  const fig = screen.getByRole("img", { name: /fanning out/i });
  expect(fig).toBeInTheDocument();
});

test("on a wide viewport the nodes are drawn as one connected svg", () => {
  matchWide();
  const { container } = wrap();

  const svg = container.querySelector("svg")!;
  expect(svg).toBeInTheDocument();
  expect(svg.querySelectorAll("path")).toHaveLength(FLOW_EDGES.length);
  expect(svg.querySelectorAll("g")).toHaveLength(FLOW_NODES.length);
  expect(svg.getAttribute("viewBox")).toBe(`0 0 ${LAYOUT.width} ${LAYOUT.height}`);
  expect(svg.textContent).toContain("on error");
  expect(svg.querySelectorAll("circle").length).toBeGreaterThan(FLOW_NODES.length);
});

test("the wide and narrow layouts label the same nodes", () => {
  matchWide();
  const wide = wrap();
  const wideText = wide.container.querySelector("svg")!.textContent!;
  wide.unmount();

  Object.defineProperty(window, "matchMedia", { writable: true, value: narrowMatchMedia });
  const narrow = wrap();
  for (const n of FLOW_NODES) {
    expect(wideText).toContain(n.yamlId);
    expect(wideText).toContain(n.summary);
    expect(narrow.container.textContent).toContain(n.yamlId);
    expect(narrow.container.textContent).toContain(n.summary);
  }
});

test("the diagram has no travelling-edge animation", () => {
  matchWide();
  const { container } = wrap();
  expect(container.querySelectorAll(".hookway-pulse")).toHaveLength(0);
});
