import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { expect, test, vi } from "vitest";
import { theme } from "../theme";
import { SidePanel } from "./BuilderShell";

function panel(scrollKey: string, onResize = vi.fn()) {
  return (
    <MantineProvider theme={theme}>
      <SidePanel
        title="Inspector"
        open
        overlay={false}
        side="right"
        width={340}
        scrollKey={scrollKey}
        onResize={onResize}
        onClose={() => undefined}
      >
        <div>Panel content</div>
      </SidePanel>
    </MantineProvider>
  );
}

test("resets the panel viewport when its context changes", () => {
  const { rerender, container } = render(panel("flow"));
  const viewport = container.querySelector(".mantine-ScrollArea-viewport") as HTMLDivElement;
  viewport.scrollTop = 160;

  rerender(panel("node:auth"));

  expect(viewport.scrollTop).toBe(0);
});

test("the resize separator supports the keyboard", () => {
  const onResize = vi.fn();
  render(panel("flow", onResize));

  fireEvent.keyDown(screen.getByRole("separator", { name: "Resize Inspector" }), { key: "ArrowLeft" });

  expect(onResize).toHaveBeenCalledWith(356);
});
