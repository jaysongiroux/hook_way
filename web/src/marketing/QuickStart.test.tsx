import { expect, test, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { QuickStart } from "./QuickStart";
import { QUICK_START_COMMAND } from "./quickStartCommand";
import { GITHUB_REPO } from "./links";

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <QuickStart />
    </MantineProvider>,
  );
}

// The command has to be the real one, built from the same repo constant the
// rest of the site links to, so it cannot drift from where the code lives.
test("the command clones the repo the site points at", () => {
  expect(QUICK_START_COMMAND).toContain(`${GITHUB_REPO}.git`);
  expect(QUICK_START_COMMAND).toContain("make dev-up");
});

test("the command is shown on the page", () => {
  wrap();
  expect(screen.getByText(QUICK_START_COMMAND)).toBeInTheDocument();
});

test("the copy button puts the command on the clipboard", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  wrap();

  fireEvent.click(screen.getByRole("button", { name: /copy/i }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith(QUICK_START_COMMAND));
});

test("the button reports success back to the reader", async () => {
  Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
  wrap();

  fireEvent.click(screen.getByRole("button", { name: /copy/i }));
  expect(await screen.findByRole("button", { name: /copied/i })).toBeInTheDocument();
});
