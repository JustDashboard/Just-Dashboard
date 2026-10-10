import { chromium } from "../../../frontend/node_modules/playwright/index.mjs";
import { mockImages } from "../../../frontend/tests/browser/docker-images-fixture";

// Run against a production build of the tree whose appearance is being recorded.
const base = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43190";
const phase = process.argv[2] ?? "after";
if (!["before", "after"].includes(phase))
  throw new Error("Use before or after");
const directory = import.meta.dir;
const browser = await chromium.launch();
for (const width of [1440, 1280, 1720, 390]) {
  const page = await browser.newPage({
    viewport: { width, height: width === 390 ? 844 : 1000 },
    reducedMotion: "reduce",
  });
  await mockImages(page);
  await page.goto(`${base}/docker/images`);
  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${directory}/${phase}-page-${width}.png` });
  if (width === 1440 || width === 390) {
    await page.screenshot({
      path: `${directory}/${phase}-page-${width}-full.png`,
      fullPage: true,
    });
  }
  if (width === 1720) {
    await page.close();
    continue;
  }
  if (phase === "after") {
    await page
      .locator("[data-slot='table-container']")
      .scrollIntoViewIfNeeded();
    await page.screenshot({ path: `${directory}/after-table-${width}.png` });
  }
  await page
    .getByRole("button", {
      name: phase === "after" ? "nginx:1.27-alpine" : "Open nginx:1.27-alpine",
    })
    .first()
    .click();
  await page
    .getByRole("dialog")
    .getByText("/docker-entrypoint.sh")
    .first()
    .waitFor();
  await page.waitForTimeout(400);
  await page.screenshot({ path: `${directory}/${phase}-sheet-${width}.png` });
  await page.keyboard.press("Escape");
  if (width === 1440 && phase === "after") {
    await page
      .getByRole("button", { name: "Only images that are update available" })
      .click();
    await page.waitForTimeout(300);
    await page.screenshot({ path: `${directory}/after-filter-${width}.png` });
    await page
      .getByRole("button", { name: "More actions for nginx:1.27-alpine" })
      .click();
    await page.getByRole("menuitem", { name: "Tag…" }).click();
    await page.waitForTimeout(300);
    await page.screenshot({ path: `${directory}/after-tag-${width}.png` });
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: "Clear registry filter" }).click();
  }
  if (width === 1440) {
    await page.getByRole("button", { name: "Pull image" }).first().click();
    const dialog = page.getByRole("dialog", { name: "Pull an image" });
    await dialog.getByRole("textbox").fill("valkey/valkey:8");
    await dialog.getByRole("button", { name: "Pull", exact: true }).click();
    await page.waitForTimeout(600);
    await page.screenshot({ path: `${directory}/${phase}-pull-${width}.png` });
  }
  await page.close();
}
await browser.close();
