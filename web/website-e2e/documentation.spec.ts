import { test, expect, type Page } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { parse } from 'yaml';
import { BuilderPage } from '../e2e/builder';

const browserErrors = new WeakMap<Page, string[]>();
test.beforeEach(({ page }) => {
  const errors: string[] = [];
  browserErrors.set(page, errors);
  page.on('pageerror', (error) => errors.push(error.message));
});
test.afterEach(({ page }) => expect(browserErrors.get(page)).toEqual([]));

async function openSearch(page: Page) {
  const browse = page.getByRole('button', { name: 'Documentation menu', exact: true, includeHidden: true });
  // Direct documentation loads initially contain the static article only.
  // Wait for the app navigation before deciding whether to open its mobile menu.
  await expect(browse).toBeAttached();
  if (await browse.isVisible()) await browse.click();
  await page.getByRole('navigation', { name: 'Documentation', exact: true })
    .getByRole('button', { name: 'Search', exact: true }).click();
}

test('marketing links open docs in the same tab and deep links survive refresh', async ({ page }) => {
  await page.goto('/');
  await page.evaluate(() => { (window as unknown as { marketingSession: string }).marketingSession = 'same-app'; });
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('link', { name: 'Docs', exact: true }).click();
  await expect(page).toHaveURL(/\/docs\/$/);
  expect(await page.evaluate(() => (window as unknown as { marketingSession?: string }).marketingSession)).toBe('same-app');
  await expect(page.getByRole('heading', { level: 1, name: 'Documentation', exact: true })).toBeVisible();
  await page.getByRole('article').getByRole('link', { name: 'Configuration', exact: true }).click();
  await expect(page).toHaveURL(/\/docs\/reference\/configuration\/$/);
  await expect(page.getByRole('navigation', { name: 'Primary' }).getByRole('link', { name: 'Use cases', exact: true })).toBeVisible();
  await expect(page.getByRole('contentinfo')).toBeVisible();
  await page.reload();
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Configuration');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.goto('/#mcp-gateway');
  await page.getByRole('link', { name: 'Try the issue event demo', exact: true }).click();
  await expect(page).toHaveURL(/\/docs\/examples\/mcp-events\/demo\/$/);
  await expect(page.getByRole('article').getByRole('heading', { level: 1 })).toBeVisible();
  await page.goto('/#mcp-gateway');
  await page.getByRole('link', { name: 'Explore event operations', exact: true }).click();
  await expect(page).toHaveURL(/\/docs\/operations\/mcp-events-console\/$/);
  await expect(page.getByRole('article').getByRole('heading', { level: 1 })).toBeVisible();
});

test('examples preserve multi-file routes and original YAML downloads', async ({ page }, testInfo) => {
  await page.goto('/docs/examples/multi-yaml/');
  await expect(page.getByRole('main').getByRole('heading', { level: 1 })).toBeVisible();
  await page.goto('/docs/examples/multi-yaml/flows/orders/');
  await expect(page.getByRole('button', { name: 'Copy YAML', exact: true })).toBeVisible();
  const downloadPromise = page.waitForEvent('download');
  await page.getByRole('link', { name: 'Download YAML', exact: true }).click();
  const download = await downloadPromise;
  const destination = testInfo.outputPath('orders.yaml');
  await download.saveAs(destination);
  expect(await readFile(destination)).toEqual(await readFile(path.resolve('../examples/multi-yaml/flows/orders.yaml')));
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test('static search finds documentation and an example', async ({ page }) => {
  await page.goto('/docs/');
  await openSearch(page);
  const input = page.getByRole('dialog').getByRole('textbox');
  await input.fill('idempotency');
  await expect(page.getByRole('dialog').getByRole('link').first()).toBeVisible();
  await input.fill('multi-yaml');
  await expect(page.getByRole('dialog').getByRole('link').first()).toBeVisible();
});

test('copy preserves the original YAML including placeholders and trailing newline', async ({ context, page }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.goto('/docs/examples/config/');
  await page.getByRole('button', { name: 'Copy YAML', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Copied', exact: true })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(await readFile(path.resolve('../examples/config.yaml'), 'utf8'));
});

test('excluded paths and unknown docs return 404; excluded references retain GitHub links', async ({ page, request }) => {
  for (const url of ['/docs/missing/', '/docs/dockerhub/', '/docs/api/editor.openapi.json', '/docs/raw/docs/api/editor.openapi.json', '/docs/local-plans/']) {
    expect((await request.get(url)).status(), url).toBe(404);
  }
  await page.goto('/docs/');
  await expect(page.getByRole('main').getByRole('link', { name: 'Docker images', exact: true })).toHaveAttribute('href', 'https://github.com/jaysongiroux/hook_way/blob/main/docs/dockerhub/README.md');
  await page.goto('/docs/missing/');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('404');
});

test('overview renders its diagram and narrow-screen navigation works', async ({ page }, testInfo) => {
  await page.goto('/docs/overview/');
  await expect(page.getByLabel('Hookway flow diagram').locator('svg')).toBeVisible();
  if (testInfo.project.name === 'mobile') {
    await page.getByRole('button', { name: 'Documentation menu', exact: true }).click();
    await expect(page.getByRole('navigation', { name: 'Documentation', exact: true }).getByRole('link', { name: 'Documentation', exact: true })).toBeVisible();
    await page.getByRole('navigation', { name: 'Documentation', exact: true }).getByRole('link', { name: 'Documentation', exact: true }).click();
    await expect(page).toHaveURL(/\/docs\/$/);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});


test('leaving a directly loaded doc restores marketing metadata and canonical URLs', async ({ page }) => {
  await page.goto('/');
  const heading = await page.getByRole('heading', { level: 1 }).innerText();
  const description = await page.locator('meta[name="description"]').getAttribute('content');
  const origin = new URL((await page.locator('link[rel="canonical"]').getAttribute('href'))!).origin;
  for (const [name, pathname, title] of [['Use cases', '/use-cases', 'Use cases — Hookway'], ['Compare', '/compare', 'Compare — Hookway']] as const) {
    await page.goto('/docs/reference/configuration/');
    await expect(page).toHaveTitle('Configuration | Hookway');
    await expect(page.locator('meta[name="description"]')).not.toHaveAttribute('content', /<[^>]+>/);
    await page.getByRole('navigation', { name: 'Primary' }).getByRole('link', { name, exact: true }).click();
    await expect(page).toHaveTitle(title);
    await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', origin + pathname);
    await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', description!);
  }
  await page.goto('/docs/');
  await page.getByRole('link', { name: 'Hookway home', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(heading);
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', origin + '/');
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', description!);
});

test('a docs example still imports, validates, and exports in the production builder', async ({ page, request }) => {
  const raw = await request.get('/docs/raw/examples/filters.yaml');
  await expect(raw).toBeOK();
  const yaml = await raw.text();
  await page.goto('/docs/examples/filters/');
  await expect(page.getByRole('button', { name: 'Copy YAML', exact: true })).toBeVisible();
  const docCanonical = await page.locator('link[rel="canonical"]').getAttribute('href');
  await page.getByRole('navigation', { name: 'Primary' }).getByRole('link', { name: 'Builder', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Inspector', exact: true })).toBeVisible();
  await expect(page).toHaveTitle('Hookway Config');
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', new URL(docCanonical!).origin + '/builder');
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', /self-hosted gateway/);
  const builder = new BuilderPage(page);
  await builder.importYaml(yaml);
  const dialog = await builder.openConfigItem('Export YAML');
  await expect(dialog.getByTestId('validation-warnings')).toContainText('has no rate_limit node');
  await expect(dialog.getByTestId('validation-errors')).toHaveCount(0);
  expect(parse(await dialog.getByTestId('export-core.yaml').innerText())).toMatchObject({ flows: parse(yaml).flows });
});

test('a malformed documentation fragment keeps the reader usable', async ({ page }) => {
  await page.goto('/docs/#%');
  await expect(page.getByRole('heading', { level: 1, name: 'Documentation', exact: true })).toBeVisible();
  await openSearch(page);
  await expect(page.getByRole('dialog', { name: 'Search documentation', exact: true })).toBeVisible();
});
