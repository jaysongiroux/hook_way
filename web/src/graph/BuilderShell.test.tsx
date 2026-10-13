import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { expect, test, vi } from "vitest";
import { theme } from "../theme";
import { SidePanel } from "./BuilderShell";

function panel(scrollKey: string, onResize = vi.fn()) {
  return (
    <MantineProvider theme={theme}>
      <SidePanel
        title="Nodes"
        open
        overlay={false}
        width={260}
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
  const { rerender, container } = render(panel("nodes"));
  const viewport = container.querySelector(".mantine-ScrollArea-viewport") as HTMLDivElement;
  viewport.scrollTop = 160;

  rerender(panel("templates"));

  expect(viewport.scrollTop).toBe(0);
});

test("the resize separator widens the left panel on ArrowRight", () => {
  const onResize = vi.fn();
  render(panel("nodes", onResize));

  fireEvent.keyDown(screen.getByRole("separator", { name: "Resize Nodes" }), { key: "ArrowRight" });

  expect(onResize).toHaveBeenCalledWith(276);
});

test("the separator advertises the left panel's own width bounds", () => {
  render(panel("nodes"));

  const separator = screen.getByRole("separator", { name: "Resize Nodes" });
  expect(separator).toHaveAttribute("aria-valuemin", "220");
  expect(separator).toHaveAttribute("aria-valuemax", "420");
});
