import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MCPTester } from "./MCPTester";

const mocks = vi.hoisted(() => ({ callTool: vi.fn(), close: vi.fn(), listTools: vi.fn() }));
vi.mock("@modelcontextprotocol/client", () => ({
  Client: class {
    connect = vi.fn().mockResolvedValue(undefined);
    listTools = mocks.listTools;
    callTool = mocks.callTool;
    close = mocks.close;
  },
  StreamableHTTPClientTransport: class {},
}));

beforeEach(() => {
  mocks.callTool.mockReset().mockResolvedValue({ content: [] });
  mocks.close.mockReset().mockResolvedValue(undefined);
  mocks.listTools.mockReset().mockResolvedValue({ tools: [{ name: "get_issue", annotations: { readOnlyHint: true } }] });
});

test("a new live connection requires fresh mutation acknowledgment and arguments", async () => {
  mocks.listTools.mockResolvedValue({ tools: [{ name: "update_issue", annotations: { readOnlyHint: false } }] });
  render(<MantineProvider><MCPTester endpoint="https://mcp.example/tools" anonymous /></MantineProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Connect and discover" }));
  const run = await screen.findByRole("button", { name: "Run live tool" });
  expect(run).toBeDisabled();
  fireEvent.change(screen.getByLabelText("Live tool arguments"), { target: { value: '{"issue_id":"old-issue"}' } });
  fireEvent.click(screen.getByRole("checkbox", { name: "I understand this live call can change upstream data" }));
  expect(run).toBeEnabled();
  fireEvent.click(screen.getByRole("button", { name: "Disconnect and clear" }));
  fireEvent.change(screen.getByLabelText("Deployed MCP URL"), { target: { value: "https://other.example/tools" } });
  fireEvent.click(screen.getByRole("button", { name: "Connect and discover" }));
  const reconnectedRun = await screen.findByRole("button", { name: "Run live tool" });
  expect(reconnectedRun).toBeDisabled();
  expect(screen.getByRole("checkbox")).not.toBeChecked();
  expect(screen.getByLabelText("Live tool arguments")).toHaveValue("{}");
  expect(mocks.callTool).not.toHaveBeenCalled();
});
afterEach(cleanup);

test("live calls reject numeric rounding and preserve quoted IDs and ordinary numbers", async () => {
  render(<MantineProvider><MCPTester endpoint="https://mcp.example/tools" anonymous /></MantineProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Connect and discover" }));
  const run = await screen.findByRole("button", { name: "Run live tool" });
  const args = screen.getByLabelText("Live tool arguments");

  for (const value of ['{"id":9007199254740993}', '{"amount":0.1234567890123456789}', '{"amount":1e400}']) {
    fireEvent.change(args, { target: { value } });
    expect(run).toBeDisabled();
    expect(screen.getByText(/numeric argument would lose precision/)).toBeInTheDocument();
    fireEvent.click(run);
    expect(mocks.callTool).not.toHaveBeenCalled();
  }

  fireEvent.change(args, { target: { value: '{"id":"9007199254740993","count":12,"amount":0.1}' } });
  expect(run).toBeEnabled();
  fireEvent.click(run);
  await waitFor(() => expect(mocks.callTool).toHaveBeenCalledWith({
    name: "get_issue", arguments: { id: "9007199254740993", count: 12, amount: 0.1 },
  }, { timeout: 125_000 }));
});
