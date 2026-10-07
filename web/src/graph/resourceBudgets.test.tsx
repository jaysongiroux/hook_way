import { useState } from "react";
import { MantineProvider } from "@mantine/core";
import { fireEvent, render, screen } from "@testing-library/react";
import { test, expect } from "vitest";
import { DestinationEditor } from "./DestinationEditor";
import { fromConfig } from "./fromConfig";
import { toConfig } from "./toConfig";

test("JSON parser and async queue byte overrides survive import and export", () => {
  for (const maxQueueBytes of [0, 67108864, 134217728]) {
    const config = {
      server: { addr: ":8080", max_body_bytes: 1048576 },
      destinations: {
        sink: {
          type: "webhook",
          webhook: {
            url: "http://localhost:9000",
            max_queue_bytes: maxQueueBytes,
          },
        },
      },
      flows: {
        main: {
          nodes: [
            {
              id: "parse",
              type: "envelope",
              json: { max_depth: 512, max_nodes: 10000 },
            },
          ],
          edges: [],
        },
      },
    };
    expect(toConfig(fromConfig(config)).config).toEqual(config);
  }
});

test("async queue byte limits are editable and omission preserves the runtime default", () => {
  let exported: Record<string, unknown> = {};
  function Controlled() {
    const [entry, setEntry] = useState<Record<string, unknown>>({
      type: "webhook",
      webhook: { url: "http://localhost:9000" },
    });
    exported = entry;
    return (
      <MantineProvider>
        <DestinationEditor
          name="sink"
          entry={entry}
          taken={["sink"]}
          onRename={() => {}}
          onRemove={() => {}}
          onChange={setEntry}
        />
      </MantineProvider>
    );
  }
  render(<Controlled />);
  const field = screen.getByLabelText("max queue bytes");
  expect(field).toHaveValue("");
  expect(screen.getByText(/67108864 \(64 MiB\)/)).toBeInTheDocument();
  expect(exported).toEqual({
    type: "webhook",
    webhook: { url: "http://localhost:9000" },
  });
  fireEvent.change(field, { target: { value: "134217728" } });
  expect(exported).toEqual({
    type: "webhook",
    webhook: { url: "http://localhost:9000", max_queue_bytes: 134217728 },
  });
  fireEvent.change(field, { target: { value: "" } });
  expect(
    toConfig(fromConfig({ destinations: { sink: exported } })).config
      .destinations,
  ).toEqual({
    sink: { type: "webhook", webhook: { url: "http://localhost:9000" } },
  });
});
