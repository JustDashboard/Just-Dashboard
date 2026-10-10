import { chromium } from "../../../frontend/node_modules/playwright/index.mjs";
import { mockVolumes } from "../../../frontend/tests/browser/docker-volumes-fixture";

// Run against a production build of the tree whose appearance is being recorded:
//   JD_BROWSER_BASE_URL=http://127.0.0.1:<port> bun docs/audits/2026-10-08-docker-volumes-overhaul/capture.ts before|after
const base = process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:43117";
const phase = process.argv[2] ?? "after";
if (!["before", "after"].includes(phase))
  throw new Error("Use before or after");
const directory = import.meta.dir;
const browser = await chromium.launch();

for (const width of [1440, 1280, 1720, 1024, 390]) {
  const page = await browser.newPage({
    viewport: { width, height: width === 390 ? 844 : 1000 },
    reducedMotion: "reduce",
  });
  await mockVolumes(page);
  await page.goto(`${base}/docker/volumes`);
  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(600);
  await page.screenshot({ path: `${directory}/${phase}-page-${width}.png` });
  if (width === 1440 || width === 390) {
    await page.screenshot({
      path: `${directory}/${phase}-page-${width}-full.png`,
      fullPage: true,
    });
  }
  if (width === 1720 || width === 1024) {
    await page.close();
    continue;
  }
  if (phase === "after") {
    await page
      .locator("[data-slot='table-container']")
      .scrollIntoViewIfNeeded();
    await page.waitForTimeout(200);
    await page.screenshot({ path: `${directory}/after-table-${width}.png` });
  }
  await page
    .getByRole("button", { name: "shop_pgdata", exact: true })
    .first()
    .click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "postgresql.conf" })
    .waitFor();
  await page.waitForTimeout(500);
  await page.screenshot({ path: `${directory}/${phase}-sheet-${width}.png` });
  await page.keyboard.press("Escape");
  await page.waitForTimeout(300);

  if (phase === "after" && width === 1440) {
    // A stack taken down: its sheet says what a prune would do to it.
    await page
      .getByRole("button", { name: "nextcloud_db", exact: true })
      .first()
      .click();
    await page
      .getByRole("dialog")
      .getByText("Nothing mounts this volume")
      .waitFor();
    await page.waitForTimeout(400);
    await page.screenshot({
      path: `${directory}/after-sheet-down-${width}.png`,
    });
    await page.keyboard.press("Escape");
    await page.waitForTimeout(300);

    await page
      .getByRole("button", { name: "Only the volumes shop holds" })
      .first()
      .click();
    await page.waitForTimeout(400);
    await page.screenshot({ path: `${directory}/after-holder-${width}.png` });
    await page.keyboard.press("Escape");

    await page.getByRole("button", { name: /^Prune / }).click();
    await page.getByRole("dialog", { name: "Prune volumes" }).waitFor();
    await page.waitForTimeout(300);
    await page.screenshot({ path: `${directory}/after-prune-${width}.png` });
    await page.keyboard.press("Escape");
  }
  await page.close();
}
await browser.close();
