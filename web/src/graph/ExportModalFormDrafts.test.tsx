import { beforeEach, expect, test, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { ExportModal } from "./ExportModal";
import { useGraphStore } from "./store";
import { emptyDocument } from "./model";
import {
  validateReplayYaml,
  validateBundle,
  validateYaml,
  type ValidationIssue,
} from "./validate";

vi.mock("./validate", () => ({
  validateYaml: vi.fn(),
  validateBundle: vi.fn(),
  validateReplayYaml: vi.fn(),
}));

const validateCore = vi.mocked(validateYaml);
const validateReplay = vi.mocked(validateReplayYaml);

function renderExport(onIssue?: (field: string) => void) {
  return render(
    <MantineProvider theme={theme}>
      <ExportModal opened onClose={() => {}} onIssue={onIssue} />
    </MantineProvider>,
  );
}

beforeEach(() => {
  useGraphStore.setState({
    ...emptyDocument(),
    serverConfig: { addr: ":9090" },
  });
  vi.mocked(validateBundle).mockReset();
  vi.mocked(validateBundle).mockResolvedValue([]);
  validateCore.mockReset();
  validateReplay.mockReset();
  validateCore.mockResolvedValue([]);
  validateReplay.mockResolvedValue([]);
});

test("unresolved core drafts block both exports and explain that preview uses last valid values", async () => {
  useGraphStore.getState().setFormDraft("node/main/request|options.path", {
    text: "/unfinished",
    error: "Invalid path",
  });

  renderExport();

  expect(await screen.findByTestId("validation-ok")).toBeInTheDocument();
  expect(
    screen.getByText(/preview contains the last valid values/i),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/node\/main\/request\|options.path: Invalid path/),
  ).toBeInTheDocument();
  expect(screen.getByTestId("export-core.yaml").textContent).toContain(":9090");
  expect(screen.getByRole("button", { name: "Copy" })).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Download core.yaml" }),
  ).toBeDisabled();
});

test("validation suggestions render and affected-node action forwards the issue field", async () => {
  const issue: ValidationIssue = {
    field: "flows.main.nodes.request.path",
    message: "must start with a slash",
    suggestion: "Use an absolute request path, such as /events.",
    severity: "error",
  };
  validateCore.mockResolvedValue([issue]);
  const onIssue = vi.fn();
  renderExport(onIssue);

  expect(await screen.findByTestId("validation-errors")).toBeInTheDocument();
  expect(screen.getByText(issue.suggestion!)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Open affected node" }));
  expect(onIssue).toHaveBeenCalledWith(issue.field);
});

test("pending validation is distinct from an unavailable validator", async () => {
  let resolveValidation!: (issues: ValidationIssue[] | null) => void;
  validateCore.mockReturnValue(
    new Promise((resolve) => {
      resolveValidation = resolve;
    }),
  );
  renderExport();

  expect(screen.getByRole("status")).toHaveTextContent(
    "Checking configuration",
  );
  expect(screen.getByRole("button", { name: "Copy" })).toBeDisabled();
  resolveValidation(null);
  expect(
    await screen.findByTestId("validation-unavailable"),
  ).toBeInTheDocument();
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
});

test("warnings allow export and replay document uses its own validator", async () => {
  validateCore.mockResolvedValue([
    {
      field: "server.addr",
      message: "uses a development port",
      suggestion: "Review the deployment listener port.",
      severity: "warning",
    },
  ]);
  useGraphStore
    .getState()
    .setReplayServiceConfig({ server: { addr: ":8080" } });
  renderExport();

  expect(await screen.findByTestId("validation-warnings")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Copy" })).toBeEnabled();
  expect(
    screen.getByRole("button", { name: "Download hookway-config.zip" }),
  ).toBeEnabled();
  fireEvent.click(screen.getByRole("tab", { name: "Replay YAML" }));
  await waitFor(() =>
    expect(validateReplay).toHaveBeenCalledWith(
      expect.stringContaining("server:"),
    ),
  );
});

test("split validation selects the YAML file identified by an issue", async () => {
  useGraphStore.getState().setYAMLMode("split");
  vi.mocked(validateBundle).mockResolvedValue([
    {
      source: "flows/main.yaml",
      field: "flows.main.nodes[0]",
      message: "Invalid node",
      severity: "error",
    },
  ]);
  renderExport();
  await screen.findByTestId("validation-errors");
  fireEvent.click(screen.getByRole("button", { name: "Show flows/main.yaml" }));
  expect(screen.getByTestId("export-flows/main.yaml")).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Download hookway-config.zip" }),
  ).toBeDisabled();
});

test("replay validation selects its YAML preview from the core panel", async () => {
  useGraphStore
    .getState()
    .setReplayServiceConfig({ server: { addr: ":8080" } });
  validateReplay.mockResolvedValue([
    { field: "database.url", message: "Required", severity: "error" },
  ]);
  renderExport();
  await screen.findByTestId("validation-errors");
  fireEvent.click(screen.getByRole("button", { name: "Show replay.yaml" }));
  expect(screen.getByTestId("export-replay.yaml")).toBeInTheDocument();
});
