import { expect, type Locator, type Page } from "@playwright/test";

export class BuilderPage {
  constructor(readonly page: Page) {}

  /**
   * The inspector is a modal, so it is addressed by the dialog's accessible
   * name rather than a testid on a docked panel. The title starts with
   * "Inspector" and includes the selected node's identity and type.
   */
  inspector(): Locator {
    return this.page.getByRole("dialog", { name: "Inspector" });
  }

  async openInspector(): Promise<void> {
    if (await this.inspector().isVisible()) return;
    await this.page
      .getByRole("button", { name: "Inspector", exact: true })
      .click();
    await expect(this.inspector()).toBeVisible();
  }

  async closeInspector(): Promise<void> {
    if (!(await this.inspector().isVisible())) return;
    await this.inspector()
      .getByRole("button", { name: "Close Inspector" })
      .click();
    await expect(this.inspector()).toBeHidden();
  }

  async gotoReady(): Promise<void> {
    await this.page.goto("/builder");
    await expect(
      this.page.getByRole("button", { name: "Inspector", exact: true }),
    ).toBeVisible();
    // The node list only fills in once the persisted (or starter) graph has
    // loaded, so opening the inspector once is how we wait for a ready app.
    await this.openInspector();
    await expect(
      this.inspector()
        .getByRole("button", { name: /^Select / })
        .first(),
    ).toBeVisible({ timeout: 15_000 });
    // Left closed: the header and the Config dialogs sit behind the modal's
    // overlay, so every other helper starts from a clear canvas.
    await this.closeInspector();
  }

  async importYaml(yaml: string): Promise<void> {
    await this.closeInspector();
    await this.page.getByRole("button", { name: "Config" }).click();
    await this.page.getByRole("menuitem", { name: "Import YAML" }).click();
    const dialog = this.page.getByRole("dialog", { name: "Import configs" });
    await expect(dialog).toBeVisible();
    await dialog.getByPlaceholder("Paste core.yaml here").fill(yaml);
    await dialog.getByRole("button", { name: "Import core YAML" }).click();
    await expect(dialog).toBeHidden();
  }

  async importReplayYaml(yaml: string): Promise<void> {
    await this.closeInspector();
    await this.page.getByRole("button", { name: "Config" }).click();
    await this.page.getByRole("menuitem", { name: "Import YAML" }).click();
    const dialog = this.page.getByRole("dialog", { name: "Import configs" });
    await expect(dialog).toBeVisible();
    await dialog.getByRole("tab", { name: "Replay YAML" }).click();
    await dialog.getByPlaceholder("Paste replay.yaml here").fill(yaml);
    await dialog.getByRole("button", { name: "Import replay YAML" }).click();
    await expect(dialog).toBeHidden();
  }

  async openConfigItem(name: string): Promise<Locator> {
    await this.closeInspector();
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
    const pattern = new RegExp(
      `^${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?:\\s*✓)?$`,
    );
    return this.page.getByRole("menuitem", { name: pattern });
  }

  async expectFlows(names: string[]): Promise<void> {
    await this.closeInspector();
    await this.page.getByRole("button", { name: "Active flow" }).click();
    for (const name of names) {
      await expect(this.flowMenuItem(name)).toBeVisible();
    }
    await this.page.keyboard.press("Escape");
  }

  async selectFlow(name: string): Promise<void> {
    const active = this.page.getByRole("button", { name: "Active flow" });
    if ((await active.innerText()).trim() !== name) {
      // The flow switcher lives in the header, behind the inspector's overlay.
      await this.closeInspector();
      await active.click();
      await this.flowMenuItem(name).click();
      await expect(active).toHaveText(name);
    }
    await this.backToFlow();
    await expect(this.inspector().getByLabel("Flow name")).toHaveValue(name);
  }

  async selectNode(id: string): Promise<void> {
    await this.backToFlow();
    // exact: true -- see the note in import-panels.spec.ts: substring name
    // matching makes "Select body" ambiguous with "Select body2".
    await this.inspector()
      .getByRole("button", { name: `Select ${id}`, exact: true })
      .click();
    const metadata = this.inspector().getByRole("button", {
      name: "Identity and description",
    });
    if ((await metadata.getAttribute("aria-expanded")) !== "true")
      await metadata.click();
    await expect(
      this.inspector().getByLabel("Node ID", { exact: true }),
    ).toHaveValue(id);
  }

  /**
   * The inspector shows the flow -- its name, its policy, its node list --
   * only when nothing is selected. There is no in-modal button for that any
   * more, so this takes the same route a user does: Config > Flow settings.
   */
  async backToFlow(): Promise<void> {
    await this.openInspector();
    if (await this.inspector().getByLabel("Flow name").isVisible()) return;
    await this.closeInspector();
    await this.page.getByRole("button", { name: "Config" }).click();
    await this.page.getByRole("menuitem", { name: "Flow settings" }).click();
    await expect(this.inspector().getByLabel("Flow name")).toBeVisible();
  }
}
