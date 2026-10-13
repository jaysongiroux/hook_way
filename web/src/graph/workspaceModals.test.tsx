import { beforeEach, expect, test } from "vitest";
import { render } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { ServerPanel } from "./ServerPanel";
import { EnvPanel } from "./EnvPanel";
import { DestinationsPanel } from "./DestinationsPanel";
import { useGraphStore } from "./store";

beforeEach(() => {
  useGraphStore.getState().clear();
});

const SURFACES = [
  { name: "Core server settings", render: () => <ServerPanel opened onClose={() => {}} /> },
  { name: "Environment variables", render: () => <EnvPanel opened onClose={() => {}} /> },
  { name: "Destinations", render: () => <DestinationsPanel opened onClose={() => {}} /> },
];

for (const surface of SURFACES) {
  test(`${surface.name} opens as a centered modal rather than a side drawer`, () => {
    render(<MantineProvider theme={theme}>{surface.render()}</MantineProvider>);

    expect(document.querySelector(".mantine-Modal-content")).not.toBeNull();
    expect(document.querySelector(".mantine-Drawer-content")).toBeNull();
  });

  test(`${surface.name} names its close button`, () => {
    const { baseElement } = render(<MantineProvider theme={theme}>{surface.render()}</MantineProvider>);

    expect(baseElement.querySelector(`[aria-label="Close ${surface.name}"]`)).not.toBeNull();
  });
}
