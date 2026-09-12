import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import { theme } from "../theme";
import { Marketing } from "./Marketing";
import { DOC_LINKS, GITHUB_REPO } from "./links";

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <MemoryRouter>
        <Marketing />
      </MemoryRouter>
    </MantineProvider>,
  );
}

test("the hero states what Hookway does and offers both calls to action", () => {
  wrap();
  const heading = screen.getByRole("heading", { level: 1 });
  expect(heading).toBeInTheDocument();

  const builderCta = screen.getByRole("link", { name: /open the builder/i });
  expect(builderCta).toHaveAttribute("href", "/builder");

  const repoCta = screen.getByRole("link", { name: /github/i });
  expect(repoCta).toHaveAttribute("href", expect.stringContaining(GITHUB_REPO));
});

test("a real config sample is shown rather than described", () => {
  wrap();
  // The sample must look like the YAML a user would actually write.
  const sample = screen.getByLabelText("Example Hookway configuration");
  expect(sample.textContent).toContain("endpoints:");
  expect(sample.textContent).toContain("hmac_sha256");
  expect(sample.textContent).toContain("destinations:");
});

test("every destination type the builder supports is listed", () => {
  wrap();
  for (const t of ["rabbitmq", "sqs", "kafka", "temporal", "redis_stream", "webhook"]) {
    expect(screen.getByText(t)).toBeInTheDocument();
  }
});

test("documentation links open the GitHub copies in a new tab safely", () => {
  wrap();
  for (const link of DOC_LINKS) {
    const el = screen.getByRole("link", { name: new RegExp(link.title, "i") });
    expect(el).toHaveAttribute("href", link.href);
    expect(el).toHaveAttribute("target", "_blank");
    expect(el).toHaveAttribute("rel", expect.stringContaining("noreferrer"));
  }
});

test("the page does not restate the documentation it links to", () => {
  wrap();
  // Guard against the site drifting into a docs rewrite: keep it a signpost.
  // Measured on the content region, since Mantine injects a stylesheet into
  // the render container.
  const copy = screen.getByRole("main").textContent!;
  // The bento rewrite cut this from ~6,400 to ~4,300 characters, so the bound
  // comes back down to stay meaningful. It exists to keep the page a signpost:
  // docs/configuration.md alone is ~95KB, and the site must not duplicate it.
  expect(copy.length).toBeLessThan(5500);
});
