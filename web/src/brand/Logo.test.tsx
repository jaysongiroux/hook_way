import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import { theme } from "../theme";
import { Logo } from "./Logo";

test("renders the Hookway wordmark", () => {
  render(
    <MantineProvider theme={theme}>
      <Logo />
    </MantineProvider>,
  );
  expect(screen.getByText("Hookway")).toBeInTheDocument();
  expect(document.querySelector('img[src="/hookway-icon-512.png"]')).toBeInTheDocument();
  expect(screen.queryByRole("link")).not.toBeInTheDocument();
});

test("links home when a destination is provided", () => {
  render(
    <MantineProvider theme={theme}>
      <MemoryRouter>
        <Logo to="/" />
      </MemoryRouter>
    </MantineProvider>,
  );
  expect(screen.getByRole("link", { name: "Hookway home" })).toHaveAttribute("href", "/");
});
