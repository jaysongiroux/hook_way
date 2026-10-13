import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { emptyDocument, createNode, type GraphDocument } from "./model";
import { useGraphStore } from "./store";
import {
  BuilderValidationProvider,
  locateIssue,
  useBuilderValidation,
} from "./BuilderValidation";
import { validateYaml, type ValidationIssue } from "./validate";

vi.mock("./validate", () => ({ validateYaml: vi.fn() }));

const validateMock = vi.mocked(validateYaml);

function Observer() {
  const value = useBuilderValidation();
  return <output data-testid="validation">{JSON.stringify(value)}</output>;
}

function renderProvider() {
  return render(
    <BuilderValidationProvider>
      <Observer />
    </BuilderValidationProvider>,
  );
}

function value() {
  return JSON.parse(screen.getByTestId("validation").textContent ?? "{}") as {
    status: string;
    issues: ValidationIssue[];
    draftIssues: { key: string; error: string }[];
  };
}

function deferred<T>() {
  let resolve!: (result: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

beforeEach(() => {
  useGraphStore.setState(emptyDocument());
  validateMock.mockReset();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

test("debounces validation and ignores a response for an obsolete YAML draft", async () => {
  const first = deferred<ValidationIssue[] | null>();
  const second = deferred<ValidationIssue[] | null>();
  validateMock
    .mockReturnValueOnce(first.promise)
    .mockReturnValueOnce(second.promise);
  renderProvider();

  expect(value().status).toBe("checking");
  await act(async () => {
    vi.advanceTimersByTime(249);
  });
  expect(validateMock).not.toHaveBeenCalled();
  await act(async () => {
    vi.advanceTimersByTime(1);
  });
  expect(validateMock).toHaveBeenCalledTimes(1);
  expect(validateMock.mock.calls[0][0]).toContain("addr: :8080");

  act(() =>
    useGraphStore.setState({
      serverConfig: { ...useGraphStore.getState().serverConfig, addr: ":9090" },
    }),
  );
  expect(value().status).toBe("checking");
  await act(async () => {
    vi.advanceTimersByTime(250);
  });
  expect(validateMock).toHaveBeenCalledTimes(2);
  expect(validateMock.mock.calls[1][0]).toContain("addr: :9090");

  await act(async () => {
    second.resolve([]);
    await second.promise;
  });
  expect(value().status).toBe("valid");
  await act(async () => {
    first.resolve([
      { field: "flows.main", message: "obsolete", severity: "error" },
    ]);
    await first.promise;
  });
  expect(value().status).toBe("valid");
  expect(value().issues).toEqual([]);
});

test("reports invalid drafts separately from authoritative validation issues", async () => {
  validateMock.mockResolvedValue([
    { field: "flows.main", message: "No entry point", severity: "error" },
  ]);
  useGraphStore.setState({
    formDrafts: { "node/main/n1|options.schema": { error: "Invalid JSON" } },
  });
  renderProvider();

  expect(value().draftIssues).toEqual([
    { key: "node/main/n1|options.schema", error: "Invalid JSON" },
  ]);
  await act(async () => {
    vi.advanceTimersByTime(250);
    await Promise.resolve();
  });
  expect(value().status).toBe("invalid");
  expect(value().issues[0].message).toBe("No entry point");
});

test("editing a recoverable form draft does not restart YAML validation", async () => {
  validateMock.mockResolvedValue([]);
  renderProvider();
  await act(async () => {
    vi.advanceTimersByTime(250);
    await Promise.resolve();
  });
  expect(value().status).toBe("valid");

  act(() =>
    useGraphStore.setState({
      formDrafts: { "node/main/n1|options.schema": { error: "Invalid JSON" } },
    }),
  );
  expect(value().draftIssues).toHaveLength(1);
  expect(value().status).toBe("valid");
  expect(validateMock).toHaveBeenCalledTimes(1);
});

test("keeps validator unavailability distinct from a valid document", async () => {
  validateMock.mockResolvedValue(null);
  renderProvider();
  await act(async () => {
    vi.advanceTimersByTime(250);
    await Promise.resolve();
  });
  expect(value().status).toBe("unavailable");
  expect(value().issues).toEqual([]);
});

describe("locateIssue", () => {
  const node = createNode("http_trigger", { x: 0, y: 0 });
  node.data.yamlId = "entry";
  const doc: GraphDocument = { ...emptyDocument(), nodes: [node] };

  test("maps an issue addressed by YAML node id to its editor node and field path", () => {
    expect(locateIssue("flows.main.nodes.entry.path", doc)).toEqual({
      flowId: "main",
      nodeId: node.id,
      path: "path",
    });
  });

  test("maps an issue addressed by exported node index", () => {
    expect(locateIssue("flows.main.nodes[0].path", doc)).toEqual({
      flowId: "main",
      nodeId: node.id,
      path: "path",
    });
  });

  test("finds nodes in a saved inactive flow", () => {
    const otherDoc = {
      ...emptyDocument(),
      activeFlow: "main",
      otherFlows: { archived: { nodes: [node], edges: [] } },
    };
    expect(locateIssue("flows.archived.nodes.entry.path", otherDoc)).toEqual({
      flowId: "archived",
      nodeId: node.id,
      path: "path",
    });
  });

  test("matches the longest configured flow id when it contains .nodes", () => {
    const longFlow = "phase.nodes.archive";
    const specialNode = createNode("http_trigger", { x: 0, y: 0 });
    specialNode.data.yamlId = "entry";
    const specialDoc: GraphDocument = {
      ...emptyDocument(),
      activeFlow: longFlow,
      nodes: [specialNode],
      otherFlows: { phase: { nodes: [node], edges: [] } },
    };
    expect(
      locateIssue(`flows.${longFlow}.nodes.entry.path`, specialDoc),
    ).toEqual({
      flowId: longFlow,
      nodeId: specialNode.id,
      path: "path",
    });
  });

  test("returns undefined for a flow issue without a node location", () => {
    expect(locateIssue("flows.main", doc)).toBeUndefined();
  });
});
