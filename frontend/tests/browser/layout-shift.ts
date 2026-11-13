import { expect, type Page } from "@playwright/test"

/**
 * The most a page may move while it loads. The product's rule is that nothing
 * already on a page moves when a read lands; Google counts anything under 0.1
 * as good. A fiftieth of the viewport is the room left for a line that wraps
 * when its figure arrives.
 */
export const SHIFT_BUDGET = 0.02

export type Shift = { at: number; value: number; moved: string[] }

declare global {
  interface Window {
    __shifts?: Shift[]
  }
}

/** The answers land in one order, then in the other: a page that moves does so in one of them. */
export type Arrival = "forward" | "backward"

const STEP_MS = 40
const STEPS = 12

/** How long a read waits: by its path, so two reads of one page land apart and the same every run. */
function waitFor(path: string, order: Arrival) {
  let hash = 0
  for (const letter of path) hash = (hash * 31 + letter.charCodeAt(0)) >>> 0
  const step = hash % STEPS
  return STEP_MS * (1 + (order === "forward" ? step : STEPS - 1 - step))
}

/**
 * Watches a page for layout shift while its reads land apart.
 *
 * A page whose mocks all answer at once never shows its skeleton, and so never
 * shows the jump from the skeleton to what replaces it. This holds every GET
 * of the API for a wait of its own, between 40 and 480 ms, before handing it
 * to the mocks, and records what the browser's `layout-shift` observer says
 * moved, the measure the audit took (UI-18).
 *
 * Call it after the page's mocks are in place, since the route registered last
 * is asked first, and before `page.goto`.
 */
export async function watchShifts(page: Page) {
  let order: Arrival = "forward"
  let waiting = 0
  await page.addInitScript(() => {
    const shifts: Shift[] = []
    window.__shifts = shifts
    const name = (node: Node | null) => {
      const el = node instanceof Element ? node : (node?.parentElement ?? null)
      if (!el) return "?"
      const classes = String(el.getAttribute("class") ?? "")
        .split(" ")
        .slice(0, 2)
        .join(".")
      return `${el.nodeName.toLowerCase()}.${classes} "${(el.textContent ?? "").trim().slice(0, 40)}"`
    }
    new PerformanceObserver((list) => {
      for (const entry of list.getEntries()) {
        const shift = entry as PerformanceEntry & {
          value: number
          hadRecentInput: boolean
          sources?: { node: Node | null; previousRect: DOMRect; currentRect: DOMRect }[]
        }
        if (shift.hadRecentInput) continue
        shifts.push({
          at: Math.round(shift.startTime),
          value: shift.value,
          moved: (shift.sources ?? [])
            .slice(0, 3)
            .map(
              (source) =>
                `${name(source.node)} ${Math.round(source.previousRect.y)}→${Math.round(source.currentRect.y)}`,
            ),
        })
      }
    }).observe({ type: "layout-shift", buffered: true })
  })
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    // The session is the shell's, not the page's: every page waits for it alike.
    if (request.method() !== "GET" || path.endsWith("/auth/session")) return route.fallback()
    waiting++
    await new Promise((done) => setTimeout(done, waitFor(path, order)))
    waiting--
    await route.fallback().catch(() => {})
  })

  return {
    /** Which way round the next page's reads land. */
    arrive(next: Arrival) {
      order = next
    },
    /**
     * Waits until every read has been answered, nothing is held any more and
     * the page has drawn what landed, then says what moved.
     */
    async moved() {
      let quiet = 0
      await expect
        .poll(
          async () => {
            const held = await page.locator("[data-hold]").count()
            quiet = waiting === 0 && held === 0 ? quiet + 1 : 0
            return quiet
          },
          { intervals: [200], timeout: 20_000 },
        )
        .toBeGreaterThanOrEqual(3)
      const shifts = await page.evaluate(
        () =>
          new Promise<Shift[]>((done) =>
            requestAnimationFrame(() =>
              requestAnimationFrame(() => done((window.__shifts ?? []).slice())),
            ),
          ),
      )
      return {
        total: shifts.reduce((sum, shift) => sum + shift.value, 0),
        shifts: shifts.filter((shift) => shift.value > 0.001),
      }
    },
  }
}
