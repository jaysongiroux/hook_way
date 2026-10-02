import { expect, test } from "@playwright/test";
import { BuilderPage } from "./builder";

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 390, height: 844 },
]) {
  for (const panelWidth of [220, 260]) {
    test(`palette Add labels fit at ${viewport.width}px with a ${panelWidth}px panel`, async ({
      page,
    }) => {
      await page.setViewportSize(viewport);
      await page.addInitScript((width) => {
        localStorage.setItem("hookway.builder.paletteWidth", String(width));
      }, panelWidth);
      const builder = new BuilderPage(page);
      await builder.gotoReady();
      const nodes = page.getByRole("button", { name: "Nodes", exact: true });
      if ((await nodes.getAttribute("aria-pressed")) !== "true")
        await nodes.click();
      const cards = page.locator(".hookway-palette-item");
      await expect(cards).toHaveCount(21);
      for (const tab of ["Nodes", "Templates"]) {
        await page.getByRole("tab", { name: tab, exact: true }).click();
        await expect(cards).toHaveCount(tab === "Nodes" ? 21 : 5);
        const measurements = await cards.evaluateAll((items) =>
          items.map((item) => {
            const button = item.querySelector("button")!;
            const label = button.querySelector<HTMLElement>(
              ".mantine-Button-label",
            )!;
            const cardRect = item.getBoundingClientRect();
            const buttonRect = button.getBoundingClientRect();
            return {
              name: button.getAttribute("aria-label"),
              labelWidth: label.getBoundingClientRect().width,
              labelScrollWidth: label.scrollWidth,
              buttonLeft: buttonRect.left,
              buttonRight: buttonRect.right,
              cardLeft: cardRect.left,
              cardRight: cardRect.right,
              cardWidth: cardRect.width,
              cardScrollWidth: item.scrollWidth,
              viewportWidth: document.documentElement.clientWidth,
            };
          }),
        );
        for (const measurement of measurements) {
          expect(
            measurement.labelWidth + 1,
            measurement.name!,
          ).toBeGreaterThanOrEqual(measurement.labelScrollWidth);
          expect(measurement.buttonLeft, measurement.name!).toBeGreaterThan(
            measurement.cardLeft,
          );
          expect(measurement.buttonRight, measurement.name!).toBeLessThan(
            measurement.cardRight,
          );
          expect(
            measurement.cardScrollWidth,
            measurement.name!,
          ).toBeLessThanOrEqual(measurement.cardWidth + 1);
          expect(measurement.cardRight, measurement.name!).toBeLessThanOrEqual(
            measurement.viewportWidth,
          );
        }
      }
      await page.getByRole("tab", { name: "Nodes", exact: true }).click();
      await page
        .getByRole("button", { name: "Add HTTP endpoint", exact: true })
        .click();
      await expect(builder.inspector()).toBeVisible();
      await expect(
        builder.inspector().getByLabel("Request path", { exact: true }),
      ).toBeVisible();
      await expect(builder.inspector()).toContainText(
        "YAML type: http_trigger",
      );
    });
  }
}
