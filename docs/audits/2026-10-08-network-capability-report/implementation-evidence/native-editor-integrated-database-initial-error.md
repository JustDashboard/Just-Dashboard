# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: design-system.spec.ts >> the databases pages keep the rules
- Location: tests/browser/design-system.spec.ts:433:7

# Error details

```
Test timeout of 250000ms exceeded.
```

```
Error: page.waitForLoadState: Test timeout of 250000ms exceeded.
```

# Test source

```ts
  340 |   const fills = await page.evaluate(() => {
  341 |     const paint = (attrs: Record<string, string>) => {
  342 |       const table = document.createElement("table")
  343 |       table.style.cssText = "position:fixed;top:0;left:0"
  344 |       const body = document.createElement("tbody")
  345 |       const tr = document.createElement("tr")
  346 |       tr.className =
  347 |         "border-b border-hairline transition-colors hover:bg-row-hover data-[state=selected]:bg-accent"
  348 |       for (const [k, v] of Object.entries(attrs)) tr.setAttribute(k, v)
  349 |       tr.innerHTML = "<td>row</td>"
  350 |       body.append(tr)
  351 |       table.append(body)
  352 |       document.body.append(table)
  353 |       const bg = getComputedStyle(tr).backgroundColor
  354 |       table.remove()
  355 |       return bg
  356 |     }
  357 |     // The hover value is read from a `--row-hover` probe rather than by moving
  358 |     // the pointer, so the two colours are compared without a hit-test race.
  359 |     const swatch = document.createElement("div")
  360 |     swatch.className = "bg-row-hover"
  361 |     document.body.append(swatch)
  362 |     const hover = getComputedStyle(swatch).backgroundColor
  363 |     swatch.remove()
  364 |     return { rest: paint({}), selected: paint({ "data-state": "selected" }), hover }
  365 |   })
  366 |
  367 |   expect(fills.selected, "no fill defined for a selected row").not.toBe(fills.rest)
  368 |   expect(fills.selected, "selection paints the hover value").not.toBe(fills.hover)
  369 | })
  370 |
  371 | /**
  372 |  * `IconAction` exists so that an icon-only control carries its label in a
  373 |  * tooltip *and* in `aria-label`. A native `title` is neither: the browser holds
  374 |  * it back for about a second, and a screen reader announces it only weakly.
  375 |  */
  376 | for (const path of SURFACES) {
  377 |   test(`every icon-only control on ${path} has an accessible name`, async ({ page }) => {
  378 |     await mockShell(page)
  379 |     await page.goto(path)
  380 |     await page.waitForLoadState("networkidle")
  381 |
  382 |     const unnamed = await unnamedControls(page)
  383 |     expect(unnamed, `unlabelled icon-only controls on ${path}`).toEqual([])
  384 |   })
  385 | }
  386 |
  387 | for (const path of PROJECT_SURFACES) {
  388 |   test(`every icon-only control on ${path} has an accessible name, with a project`, async ({
  389 |     page,
  390 |   }) => {
  391 |     await mockProject(page, { showcase: true })
  392 |     await page.goto(path)
  393 |     await page.waitForLoadState("networkidle")
  394 |
  395 |     const unnamed = await unnamedControls(page)
  396 |     expect(unnamed, `unlabelled icon-only controls on ${path}`).toEqual([])
  397 |   })
  398 | }
  399 |
  400 | /**
  401 |  * The same rule at a phone's width, where the deployment pages trade words
  402 |  * for glyphs: a toggle that reads "HTTPS" at 1280 is a padlock at 390, and a
  403 |  * padlock with no name is a button a screen reader calls "button".
  404 |  */
  405 | test.describe("at a phone's width", () => {
  406 |   test.use({ viewport: { width: 390, height: 844 } })
  407 |
  408 |   for (const path of PROJECT_SURFACES) {
  409 |     test(`every icon-only control on ${path} has an accessible name`, async ({ page }) => {
  410 |       await mockProject(page, { showcase: true })
  411 |       await page.goto(path)
  412 |       await page.waitForLoadState("networkidle")
  413 |
  414 |       const unnamed = await unnamedControls(page)
  415 |       expect(unnamed, `unlabelled icon-only controls on ${path} at 390`).toEqual([])
  416 |     })
  417 |   }
  418 | })
  419 |
  420 | /**
  421 |  * A database's pages need a connection to draw anything — its strip, its
  422 |  * rail, the page under them — so they are walked with the database fixture,
  423 |  * on a connection that carries both of its labels: the strip's tags, its
  424 |  * status and its three controls are the row these rules are most easily
  425 |  * broken on, and they are on every page.
  426 |  */
  427 | const LABELLED = { rows: { 1: { environment: "production", readOnly: true } } }
  428 |
  429 | for (const [label, viewport] of [
  430 |   ["", { width: 1280, height: 800 }],
  431 |   [" at a phone's width", { width: 390, height: 844 }],
  432 | ] as const) {
  433 |   test(`the databases pages keep the rules${label}`, async ({ page }) => {
  434 |     test.setTimeout(DATABASE_SURFACES.length * 10_000)
  435 |     await page.setViewportSize(viewport)
  436 |     await mockDatabases(page, LABELLED)
  437 |
  438 |     for (const path of DATABASE_SURFACES) {
  439 |       await page.goto(path)
> 440 |       await page.waitForLoadState("networkidle")
      |                  ^ Error: page.waitForLoadState: Test timeout of 250000ms exceeded.
  441 |       await expect(page.locator("[data-slot=page]").first()).toBeVisible({ timeout: 15_000 })
  442 |
  443 |       expect(await unnamedControls(page), `unlabelled icon-only controls on ${path}`).toEqual([])
  444 |       expect(await offCentreText(page), `text off its row's centre line on ${path}`).toEqual([])
  445 |       expect(await filledPills(page), `fully rounded filled chips on ${path}`).toEqual([])
  446 |       const seen = await registers(page)
  447 |       expectOneRegister(path, seen)
  448 |       // Adding a database is the section's one sequence with an outcome.
  449 |       expect(seen.registers.includes("flow"), `${path} is in the wrong register`).toBe(
  450 |         path === "/databases/new",
  451 |       )
  452 |       const sideways = await page.evaluate(() =>
  453 |         [document.documentElement, ...document.querySelectorAll("[data-slot=page]")]
  454 |           .map((el) => el.parentElement ?? el)
  455 |           .some((el) => el.scrollWidth > el.clientWidth + 1),
  456 |       )
  457 |       expect(sideways, `${path} scrolls sideways`).toBe(false)
  458 |     }
  459 |   })
  460 |
  461 |   test(`a database's switcher and its Connect popover keep the rules${label}`, async ({ page }) => {
  462 |     await page.setViewportSize(viewport)
  463 |     await mockDatabases(page, LABELLED)
  464 |     await page.goto("/databases/1/performance")
  465 |
  466 |     await page.getByRole("button", { name: /^Database: shop/ }).click()
  467 |     await expect(page.getByRole("option").first()).toBeVisible()
  468 |     await page.waitForLoadState("networkidle")
  469 |     expect(await unnamedControls(page), "unlabelled controls in the switcher").toEqual([])
  470 |     expect(await offCentreText(page), "text off its centre line in the switcher").toEqual([])
  471 |     expect(await filledPills(page), "a filled chip in the switcher").toEqual([])
  472 |     await page.keyboard.press("Escape")
  473 |
  474 |     await page.getByRole("button", { name: "Connect" }).click()
  475 |     await expect(page.locator("[data-slot=connection-string]")).toBeVisible()
  476 |     await page.waitForLoadState("networkidle")
  477 |     expect(await unnamedControls(page), "unlabelled controls in Connect").toEqual([])
  478 |     expect(await offCentreText(page), "text off its centre line in Connect").toEqual([])
  479 |     expect(await filledPills(page), "a filled chip in Connect").toEqual([])
  480 |   })
  481 | }
  482 |
  483 | /**
  484 |  * The reveal rule's touch clause. A cluster shown only on `group-hover` is
  485 |  * permanently invisible on a device that has no hover, which is how five of
  486 |  * these ended up unreachable on a phone.
  487 |  */
  488 | test.describe("with no hover available", () => {
  489 |   test.use({ hasTouch: true, viewport: { width: 390, height: 844 } })
  490 |
  491 |   for (const path of ["/audit", "/system-users"] as const) {
  492 |     test(`row actions on ${path} are visible without a pointer`, async ({ page }) => {
  493 |       await mockShell(page)
  494 |       await page.goto(path)
  495 |       await page.waitForLoadState("networkidle")
  496 |
  497 |       const hidden = await page.evaluate(() => {
  498 |         const bad: string[] = []
  499 |         for (const el of document.querySelectorAll<HTMLElement>(
  500 |           "[data-slot='table-row'] button, [data-slot='table-row'] a",
  501 |         )) {
  502 |           if (parseFloat(getComputedStyle(el).opacity) < 0.1) {
  503 |             bad.push(el.getAttribute("aria-label") ?? el.outerHTML.slice(0, 120))
  504 |           }
  505 |         }
  506 |         return bad
  507 |       })
  508 |       expect(hidden, `controls hidden behind hover on ${path}`).toEqual([])
  509 |     })
  510 |   }
  511 | })
  512 |
  513 | test("a centred row sets every item's text on its centre line", async ({ page }) => {
  514 |   test.setTimeout(SURFACES.length * 10_000)
  515 |   await mockShell(page)
  516 |
  517 |   for (const path of SURFACES) {
  518 |     await page.goto(path)
  519 |     await page.waitForLoadState("networkidle")
  520 |     expect(await offCentreText(page), `text off its row's centre line on ${path}`).toEqual([])
  521 |   }
  522 | })
  523 |
  524 | test("a centred row sets every item's text on its centre line, with a project", async ({
  525 |   page,
  526 | }) => {
  527 |   test.setTimeout(PROJECT_SURFACES.length * 10_000)
  528 |   await mockProject(page, { showcase: true })
  529 |
  530 |   for (const path of PROJECT_SURFACES) {
  531 |     await page.goto(path)
  532 |     await page.waitForLoadState("networkidle")
  533 |     await expect(page.locator("[data-slot=page]").first()).toBeVisible({ timeout: 15_000 })
  534 |     expect(await offCentreText(page), `text off its row's centre line on ${path}`).toEqual([])
  535 |   }
  536 | })
  537 |
  538 | /**
  539 |  * Rule 2: there is no pill. The one status indicator is a dot and a word, and a
  540 |  * fixed property is a squared hairline `Tag`. A fully rounded filled chip is the
```
