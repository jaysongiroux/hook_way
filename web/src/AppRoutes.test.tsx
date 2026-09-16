import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import { theme } from "./theme";
import { AppRoutes } from "./AppRoutes";

function renderAt(path: string) {
  return render(
    <MantineProvider theme={theme}>
      <MemoryRouter initialEntries={[path]}>
        <AppRoutes />
      </MemoryRouter>
    </MantineProvider>,
  );
}

test("the root url serves the marketing overview, not the builder", () => {
  renderAt("/");
  expect(screen.getByRole("heading", { level: 1 })).toBeInTheDocument();
  // The inspector toggle is builder chrome; the overview must not mount it.
  expect(screen.queryByRole("button", { name: "Inspector" })).not.toBeInTheDocument();
});

test("/builder mounts the config builder", async () => {
  renderAt("/builder");
  // The builder is lazy-loaded, so it arrives after the dynamic import settles.
  expect(await screen.findByRole("button", { name: "Inspector" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Hookway home" })).toBeInTheDocument();
});

test("an unknown path falls back to the overview", () => {
  renderAt("/nope");
  expect(screen.getByRole("heading", { level: 1 })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Inspector" })).not.toBeInTheDocument();
});

// The bundle serves both routes from one index.html, so each route has to claim
// the tab title itself; otherwise the overview inherits the builder's name.
test("the overview and the builder set their own document titles", async () => {
  renderAt("/");
  expect(document.title).toMatch(/hookway/i);
  expect(document.title).not.toBe("Hookway Config");

  renderAt("/builder");
  await screen.findByRole("button", { name: "Inspector" });
  expect(document.title).toBe("Hookway Config");
});
