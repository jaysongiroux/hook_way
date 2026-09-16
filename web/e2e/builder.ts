import { expect, type Locator, type Page } from "@playwright/test";

export class BuilderPage {
  constructor(readonly page: Page) {}

  inspector(): Locator {
    return this.page.getByTestId("inspector");
  }

  async gotoReady(): Promise<void> {
    await this.page.goto("/builder");
    const inspectorToggle = this.page.getByRole("button", { name: "Inspector", exact: true });
    await expect(inspectorToggle).toBeVisible();
    if ((await inspectorToggle.getAttribute("aria-pressed")) !== "true") {
      await inspectorToggle.click();
    }
    await expect(this.inspector()).toBeVisible();
    await expect(this.inspector().getByRole("button", { name: /^Select / }).first()).toBeVisible({ timeout: 15_000 });
  }

  async importYaml(yaml: string): Promise<void> {
    await this.page.getByRole("button", { name: "Config" }).click();
    await this.page.getByRole("menuitem", { name: "Import YAML" }).click();
    const dialog = this.page.getByRole("dialog", { name: "Import config" });
    await expect(dialog).toBeVisible();
    await dialog.getByLabel("Config YAML").fill(yaml);
    await dialog.getByRole("button", { name: "Import" }).click();
    await expect(dialog).toBeHidden();
  }

  async openConfigItem(name: string): Promise<Locator> {
    await this.closeDialog();
    await this.page.getByRole("button", { name: "Config" }).click();
    await this.page.getByRole("menuitem", { name }).click();
    const dialog = this.page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    return dialog;
  }

  async closeDialog(): Promise<void> {
    const dialog = this.page.getByRole("dialog");
    if ((await dialog.count()) === 0) return;
    await this.page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
  }

  flowMenuItem(name: string): Locator {
    const pattern = new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?:\\s*✓)?$`);
    return this.page.getByRole("menuitem", { name: pattern });
  }

  async expectFlows(names: string[]): Promise<void> {
    await this.page.getByRole("button", { name: "Active flow" }).click();
    for (const name of names) {
      await expect(this.flowMenuItem(name)).toBeVisible();
    }
    await this.page.keyboard.press("Escape");
  }

  async selectFlow(name: string): Promise<void> {
    const active = this.page.getByRole("button", { name: "Active flow" });
    if ((await active.innerText()).trim() === name) {
      await expect(this.inspector().getByLabel("Flow name")).toHaveValue(name);
      return;
    }
    await active.click();
    await this.flowMenuItem(name).click();
    await expect(active).toHaveText(name);
    await expect(this.inspector().getByLabel("Flow name")).toHaveValue(name);
  }

  async selectNode(id: string): Promise<void> {
    if ((await this.inspector().getByRole("button", { name: "Back to flow" }).count()) > 0) {
      await this.inspector().getByRole("button", { name: "Back to flow" }).click();
    }
    await this.inspector().getByRole("button", { name: `Select ${id}` }).click();
    await expect(this.inspector().getByLabel("Id", { exact: true })).toHaveValue(id);
  }

  async backToFlow(): Promise<void> {
    const back = this.inspector().getByRole("button", { name: "Back to flow" });
    if ((await back.count()) === 0) return;
    await back.click();
    await expect(this.inspector().getByLabel("Flow name")).toBeVisible();
  }
}
