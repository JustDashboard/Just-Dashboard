import { expect, type Locator, type Page } from "@playwright/test"
import { mkdir, writeFile } from "node:fs/promises"
import { join } from "node:path"

/** Hold the real UI's request; recordings never prune or restore a live host. */
export async function heldResponse(
  page: Page,
  path: string,
  body: unknown,
  status = 200,
  method?: string,
) {
  let release!: () => void
  const waiting = new Promise<void>((resolve) => {
    release = resolve
  })
  let calls = 0
  await page.route(
    (url) => url.pathname === `/api/v1${path}`,
    async (route) => {
      if (method ? route.request().method() !== method : route.request().method() === "GET")
        return route.fallback()
      calls++
      await waiting
      await route.fulfill({ status, json: body })
    },
  )
  return { release, calls: () => calls }
}

/** A bounded byte stream lets the real export reader report measured bytes. */
export async function streamedExport(page: Page, path: string) {
  await page.addInitScript((path) => {
    const original = window.fetch
    window.fetch = async (...args) => {
      const request = args[0]
      const url = new URL(
        typeof request === "string" ? request : request instanceof URL ? request.href : request.url,
        location.href,
      )
      if (url.pathname !== `/api/v1${path}`) return original(...args)
      let timer: ReturnType<typeof setInterval>
      const body = new ReadableStream<Uint8Array>({
        start(controller) {
          const chunk = new TextEncoder().encode("id,name\n1,Example\n".repeat(64))
          timer = setInterval(() => controller.enqueue(chunk), 300)
          const finish = () => {
            clearInterval(timer)
            controller.close()
          }
          Object.assign(window, { finishProgressExport: finish })
          args[1]?.signal?.addEventListener(
            "abort",
            () => {
              clearInterval(timer)
              controller.error(new DOMException("Cancelled", "AbortError"))
            },
            { once: true },
          )
        },
        cancel() {
          clearInterval(timer)
        },
      })
      return new Response(body, {
        headers: {
          "Content-Type": "text/csv",
          "Content-Disposition": 'attachment; filename="progress.csv"',
        },
      })
    }
  }, path)
  return () =>
    page.evaluate(() =>
      (window as unknown as { finishProgressExport: () => void }).finishProgressExport(),
    )
}

export async function recordProgress(
  page: Page,
  name: string,
  start: () => Promise<unknown>,
  progress: Locator,
  finish: () => Promise<unknown>,
) {
  const recording = process.env.JD_PROGRESS_RECORD === "1"
  const folder = join(process.env.JD_PROGRESS_FRAMES ?? "test-results/operation-progress", name)
  // Chromium's direct capture keeps a chart-heavy page's repeated recordings
  // from spending their budget on screenshot preparation and PNG compression.
  const capture =
    recording && process.env.JD_PROGRESS_CDP === "1"
      ? await page.context().newCDPSession(page)
      : undefined
  let frame = 0
  const snapshot = async () => {
    if (!recording) return
    const path = join(folder, `${String(frame++).padStart(3, "0")}.png`)
    if (capture) {
      const result = await capture.send("Page.captureScreenshot", {
        format: "png",
        captureBeyondViewport: false,
        optimizeForSpeed: true,
      })
      await writeFile(path, Buffer.from(result.data, "base64"))
    } else {
      await page.screenshot({ path })
    }
  }
  if (recording) await mkdir(folder, { recursive: true })
  await snapshot()
  await start()
  await expect(progress).toBeVisible()
  await progress.scrollIntoViewIfNeeded()
  await expect(progress).toBeInViewport()
  await expect(progress.getByRole("progressbar")).toBeVisible()
  // The timer is visible but excluded from the live region, so each second
  // does not interrupt a screen reader while it describes the operation.
  await expect(progress.locator("[aria-hidden]")).toContainText(/elapsed/)
  if (recording) {
    for (let index = 0; index < 20; index++) {
      await snapshot()
      await page.waitForTimeout(150)
    }
  }
  await finish()
  await expect(progress).toHaveCount(0)
  await snapshot()
  await capture?.detach()
}
