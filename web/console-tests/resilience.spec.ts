import {
  expect,
  test,
  type APIRequestContext,
  type Page,
} from "@playwright/test";

const API_URL = "http://127.0.0.1:5184";
const OPS = "console-ops-token-0123456789abcdefghijkl";
const READ = "console-read-token-0123456789abcdefghijkl";
const recordID = (n: number) =>
  `01926d3c-8d6f-7b3a-9c1e-${String(n).padStart(12, "0")}`;

async function signIn(page: Page) {
  await page.goto("/");
  await page.getByLabel("Replay API token").fill(OPS);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Failure inbox" }),
  ).toBeVisible();
  await page.getByLabel("Flow", { exact: true }).fill("orders");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(5);
}

async function detail(request: APIRequestContext, n: number) {
  const response = await request.get(`${API_URL}/v1/records/${recordID(n)}`, {
    headers: { Authorization: `Bearer ${OPS}` },
  });
  expect(response.status()).toBe(200);
  return response.json();
}

test.beforeEach(async ({ request }) => {
  expect((await request.post(`${API_URL}/test/reset`)).status()).toBe(204);
});

test("real API authentication rejection during an action clears session and evidence", async ({
  page,
  request,
}) => {
  await signIn(page);
  await page
    .getByRole("button", { name: `Inspect ${recordID(1)}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Preview payload", exact: true })
    .click();
  await expect(page.getByRole("dialog").locator("pre")).toContainText(
    "9007199254740993",
  );
  await page.keyboard.press("Escape");
  await page
    .getByRole("button", { name: "Retry delivery", exact: true })
    .click();
  // Fault only credential transport; the production authenticator emits 401.
  // This proves the rejected/revoked-key boundary, not hot config revocation.
  await page.route(`**/v1/records/${recordID(1)}/retries`, (route) =>
    route.continue({
      headers: {
        ...route.request().headers(),
        authorization: "Bearer rejected-synthetic-key",
      },
    }),
  );
  const rejected = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/v1/records/${recordID(1)}/retries`) &&
      response.status() === 401,
  );
  await page.getByRole("button", { name: "Queue retry", exact: true }).click();
  await rejected;
  await expect(page.getByRole("alert")).toContainText(
    "Your API key was rejected",
  );
  await expect(page.getByLabel("Replay API token")).toHaveValue("");
  await expect(
    page.getByRole("region", { name: "Selected failure" }),
  ).toHaveCount(0);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Continue uncertain request" }),
  ).toHaveCount(0);
  await expect(page.getByText(/signal is aborted|AbortError/)).toHaveCount(0);
  expect((await detail(request, 1)).runs).toHaveLength(0);
  expect(
    await page.evaluate(() => ({
      local: localStorage.length,
      session: sessionStorage.length,
      cookie: document.cookie,
    })),
  ).toEqual({ local: 0, session: 0, cookie: "" });
});

test("real API forbidden action preserves evidence without treating permission loss as uncertainty", async ({
  page,
  request,
}) => {
  await signIn(page);
  await page.getByRole("tab", { name: "Unknown outcome", exact: true }).click();
  await page
    .getByRole("button", { name: `Inspect ${recordID(3)}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Confirm outcome", exact: true })
    .click();
  const note = page.getByLabel("Evidence note");
  await note.fill(
    "Independent receipt retained despite a scoped permission rejection.",
  );
  // A real read-only key reaches the production scope check. No mocked 403.
  await page.route(`**/v1/records/${recordID(3)}/resolve`, (route) =>
    route.continue({
      headers: {
        ...route.request().headers(),
        authorization: `Bearer ${READ}`,
      },
    }),
  );
  const forbidden = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/v1/records/${recordID(3)}/resolve`) &&
      response.status() === 403,
  );
  await page
    .getByRole("button", { name: "Record confirmation", exact: true })
    .click();
  await forbidden;
  await expect(page.getByRole("dialog").getByRole("alert")).toContainText(
    "required scope",
  );
  await expect(note).toHaveValue(
    "Independent receipt retained despite a scoped permission rejection.",
  );
  await expect(note).toBeEnabled();
  await expect(
    page.getByRole("button", { name: "Resubmit same request" }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("heading", { name: "Failure inbox" }),
  ).toBeVisible();
  const record = await detail(request, 3);
  expect(record.delivery_status).toBe("unknown");
  expect(record.resolution).toBeUndefined();
  expect(
    record.history.filter(
      (event: { event: string }) => event.event === "operator_resolved",
    ),
  ).toHaveLength(0);
});

test("permission rejection after a committed lost reply must retain the original uncertain action", async ({
  page,
  request,
}, testInfo) => {
  await signIn(page);
  await page.getByRole("tab", { name: "Unknown outcome", exact: true }).click();
  await page
    .getByRole("button", { name: `Inspect ${recordID(3)}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Confirm outcome", exact: true })
    .click();
  const note =
    "Retained evidence for the original accepted, response-lost action.";
  await page.getByLabel("Evidence note").fill(note);
  const sent: {
    key: string | undefined;
    body: string | null;
    authorization: string | undefined;
  }[] = [];
  await page.route(`**/v1/records/${recordID(3)}/resolve`, async (route) => {
    sent.push({
      key: route.request().headers()["idempotency-key"],
      body: route.request().postData(),
      authorization: route.request().headers().authorization,
    });
    if (sent.length === 1) {
      expect((await route.fetch()).status()).toBe(200);
      await route.abort("failed");
    } else if (sent.length === 2) {
      await route.continue({
        headers: {
          ...route.request().headers(),
          authorization: `Bearer ${READ}`,
        },
      });
    } else await route.continue();
  });
  await page
    .getByRole("button", { name: "Record confirmation", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toContainText("Response uncertain");
  const forbidden = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/v1/records/${recordID(3)}/resolve`) &&
      response.status() === 403,
  );
  await page.getByRole("button", { name: "Resubmit same request" }).click();
  await forbidden;
  await expect(page.getByRole("dialog")).toContainText("required scope");
  await expect(
    page.getByRole("button", { name: "Resubmit same request" }),
  ).toBeVisible();
  await expect(page.getByLabel("Evidence note")).toBeDisabled();
  await expect(page.getByLabel("Evidence note")).toHaveValue(note);
  await expect(page.getByRole("dialog")).toContainText(
    `Submission key: ${sent[0].key}`,
  );
  await page.screenshot({
    path: testInfo.outputPath("console-uncertain-permission-retained.png"),
    animations: "disabled",
    fullPage: true,
  });
  await page.getByRole("button", { name: "Resubmit same request" }).click();
  await expect(
    page.getByRole("dialog").getByRole("alert", { name: "Action results" }),
  ).toContainText("Resolved — operator confirmed");
  expect(sent).toHaveLength(3);
  expect(sent[0].authorization).toBe(`Bearer ${OPS}`);
  expect(sent[1]).toEqual(sent[0]);
  expect(sent[2]).toEqual(sent[0]);
  const record = await detail(request, 3);
  expect(record.resolution.actor).toBe("demo-operator");
  expect(record.resolution.note).toBe(note);
  expect(
    record.history.filter(
      (event: { event: string }) => event.event === "operator_resolved",
    ),
  ).toHaveLength(1);
});

test("API transport loss marks loaded data stale and explicit refresh recovers", async ({
  page,
}) => {
  await signIn(page);
  await page.route("**/v1/records?*", (route) =>
    route.abort("connectionfailed"),
  );
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("could not be reached");
  await expect(page.getByRole("alert")).toContainText(
    "Previously loaded data may be stale",
  );
  await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(5);
  await page.unroute("**/v1/records?*");
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(5);
});

test("interrupted payload response is an error rather than an empty successful preview", async ({
  page,
  request,
}) => {
  await signIn(page);
  await page
    .getByRole("button", { name: `Inspect ${recordID(1)}`, exact: true })
    .click();
  let interruptions = 0;
  await page.route(`**/v1/records/${recordID(1)}/payload`, async (route) => {
    if (interruptions++ === 0) {
      expect((await route.fetch()).status()).toBe(200);
      await route.abort("failed"); // after the real API supplied/audited bytes
    } else await route.continue();
  });
  await page
    .getByRole("button", { name: "Preview payload", exact: true })
    .click();
  await expect(
    page.getByRole("alert", { name: "Payload unavailable", exact: true }),
  ).toContainText("could not be reached");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page
    .getByRole("button", { name: "Preview payload", exact: true })
    .click();
  await expect(page.getByRole("dialog").locator("pre")).toContainText(
    "9007199254740993",
  );
  expect(
    (await detail(request, 1)).history.filter(
      (event: { event: string }) => event.event === "payload_downloaded",
    ),
  ).toHaveLength(2);
});

test("a late payload read cannot appear on a different selected record", async ({
  page,
}) => {
  await signIn(page);
  await page
    .getByRole("button", { name: `Inspect ${recordID(1)}`, exact: true })
    .click();
  let entered = false;
  let release!: () => void;
  const released = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**/v1/records/${recordID(1)}/payload`, async (route) => {
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    entered = true;
    await released;
    await route.fulfill({ response });
  });
  try {
    await page
      .getByRole("button", { name: "Preview payload", exact: true })
      .click();
    await expect.poll(() => entered).toBe(true);
    await page
      .getByRole("button", { name: `Inspect ${recordID(2)}`, exact: true })
      .click();
    await expect(
      page.getByRole("region", { name: "Selected failure" }),
    ).toContainText("The retry was blocked before delivery.");
    const response = page.waitForResponse((response) =>
      response.url().endsWith(`/v1/records/${recordID(1)}/payload`),
    );
    release();
    await response;
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Preview payload", exact: true }),
    ).toBeEnabled();
  } finally {
    release();
  }
});

for (const width of [1272, 390]) {
  test(`keyboard duplicate submission is fenced while the real committed response waits at ${width}px`, async ({
    page,
    request,
  }) => {
    await page.setViewportSize({ width, height: 900 });
    await signIn(page);
    await page
      .getByRole("tab", { name: "Unknown outcome", exact: true })
      .click();
    const inspect = page.getByRole("button", {
      name: `Inspect ${recordID(3)}`,
      exact: true,
    });
    await inspect.press("Enter");
    const confirm = page.getByRole("button", {
      name: "Confirm outcome",
      exact: true,
    });
    await confirm.press("Enter");
    const note = page.getByLabel("Evidence note");
    await expect(note).toBeFocused();
    await note.fill(
      "Destination independently confirmed receipt; one deliberate keyboard submission.",
    );
    let requests = 0;
    let committed = false;
    let release!: () => void;
    const released = new Promise<void>((resolve) => {
      release = resolve;
    });
    let refreshPending = false;
    let releaseRefresh!: () => void;
    const refreshReleased = new Promise<void>((resolve) => {
      releaseRefresh = resolve;
    });
    await page.route(`**/v1/records/${recordID(3)}/resolve`, async (route) => {
      requests++;
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      await page.route(`**/v1/records/${recordID(3)}`, async (refreshRoute) => {
        const current = await refreshRoute.fetch();
        expect(current.status()).toBe(200);
        refreshPending = true;
        await refreshReleased;
        await refreshRoute.fulfill({ response: current });
      });
      committed = true;
      await released;
      await route.fulfill({ response });
    });
    try {
      const submit = page.getByRole("button", {
        name: "Record confirmation",
        exact: true,
      });
      await submit.focus();
      await expect(submit).toBeFocused();
      await submit.press("Enter");
      await page.keyboard.press("Enter");
      await expect.poll(() => committed).toBe(true);
      await expect(submit).toBeDisabled();
      expect(requests).toBe(1);
      const record = await detail(request, 3);
      expect(
        record.history.filter(
          (event: { event: string }) => event.event === "operator_resolved",
        ),
      ).toHaveLength(1);
      release();
      await expect(
        page.getByRole("dialog").getByRole("alert", { name: "Action results" }),
      ).toContainText("Resolved — operator confirmed");
      await expect.poll(() => refreshPending).toBe(true);
      await page.getByRole("button", { name: "Done", exact: true }).click();
      // Keep the original trigger temporarily mounted, then let the refreshed
      // resolved state remove it. Both sides must retain investigation focus.
      const selected = page.getByRole("region", { name: "Selected failure" });
      await expect(selected).toBeFocused();
      releaseRefresh();
      await expect(confirm).toHaveCount(0);
      await expect
        .poll(() =>
          selected.evaluate((region) =>
            region.contains(document.activeElement),
          ),
        )
        .toBe(true);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
    } finally {
      release();
      releaseRefresh();
    }
  });
}
