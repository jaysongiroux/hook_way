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
  expect(await screen.findByRole("button", { name: "Inspector" }, { timeout: 15_000 })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Hookway home" })).toBeInTheDocument();
}, 20_000);

test("an unknown path shows the not-found page", () => {
  renderAt("/nope");
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("404");
  expect(screen.queryByRole("button", { name: "Inspector" })).not.toBeInTheDocument();
});

test("/use-cases and /compare are first-class marketing routes", () => {
  const useCases = renderAt("/use-cases");
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(/patterns that stay in your infrastructure/i);
  expect(screen.getByRole("link", { name: "Use cases" })).toBeInTheDocument();
  useCases.unmount();

  renderAt("/compare");
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent(/how hookway compares/i);
  expect(screen.getByRole("link", { name: "Compare" })).toBeInTheDocument();
});

// The bundle serves both routes from one index.html, so each route has to claim
// the tab title itself; otherwise the overview inherits the builder's name.
test("the overview and the builder set their own document titles", async () => {
  const overview = renderAt("/");
  expect(document.title).toMatch(/hookway/i);
  expect(document.title).not.toBe("Hookway Config");
  overview.unmount();

  renderAt("/builder");
  await screen.findByRole("button", { name: "Inspector" }, { timeout: 15_000 });
  expect(document.title).toBe("Hookway Config");
});

test("use-cases and compare set distinct document titles", () => {
  const useCases = renderAt("/use-cases");
  expect(document.title).toMatch(/use cases/i);
  useCases.unmount();

  renderAt("/compare");
  expect(document.title).toMatch(/compare/i);
});
