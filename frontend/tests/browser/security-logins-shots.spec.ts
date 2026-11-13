import { test, type Page } from "@playwright/test"
import {
  PENDING,
  failure,
  firewall,
  manySessions,
  mockLogins,
  viewer,
} from "./security-logins-fixture"

/**
 * Full-height captures of every state of the Logins page and the Security
 * overview, at a laptop's width and a phone's, for looking at rather than
 * asserting on. It runs only when `JD_SECURITY_SHOTS` names a folder:
 *
 *   JD_SECURITY_SHOTS=/tmp/shots bunx playwright test tests/browser/security-logins-shots.spec.ts
 *
 * `JD_SECURITY_SHOTS_PREFIX` names the set ("before" from the base build).
 */
const SHOTS = process.env.JD_SECURITY_SHOTS
const PREFIX = process.env.JD_SECURITY_SHOTS_PREFIX ?? "after"

const unreadable = failure(503, "unavailable", "the host could not be read")
const noRecord = failure(
  503,
  "login_history_unavailable",
  "the host's login record could not be read on this system",
)

type State = {
  name: string
  path: string
  session?: typeof viewer
  overrides?: Record<string, unknown>
  /** The page is caught waiting, so there is nothing to wait for. */
  waiting?: boolean
}

const STATES: State[] = [
  { name: "logins-full", path: "/security/logins" },
  {
    name: "logins-busy",
    path: "/security/logins",
    overrides: { "/ssh-sessions": manySessions },
  },
  {
    name: "logins-failed-attempts",
    path: "/security/logins?failed=1",
  },
  {
    name: "logins-empty",
    path: "/security/logins",
    overrides: {
      "/ssh-sessions": [],
      "/logins": [],
      "/logins/attackers": {
        attempts: 0,
        addresses: 0,
        windowHours: 168,
        capped: false,
        attackers: [],
      },
    },
  },
  {
    name: "logins-loading",
    path: "/security/logins",
    waiting: true,
    overrides: { "/ssh-sessions": PENDING, "/logins": PENDING, "/logins/attackers": PENDING },
  },
  {
    name: "logins-failed",
    path: "/security/logins",
    overrides: {
      "/ssh-sessions": unreadable,
      "/logins": unreadable,
      "/logins/attackers": unreadable,
    },
  },
  {
    name: "logins-no-record",
    path: "/security/logins",
    overrides: { "/logins": noRecord, "/logins/attackers": noRecord },
  },
  { name: "logins-readonly", path: "/security/logins", session: viewer },
  { name: "overview-full", path: "/security" },
  {
    name: "overview-loading",
    path: "/security",
    waiting: true,
    overrides: {
      "/exposure": PENDING,
      "/firewall/": PENDING,
      "/fail2ban/": PENDING,
      "/connections": PENDING,
      "/ssh-sessions": PENDING,
      "/ssh/config": PENDING,
      "/security/boundary": PENDING,
    },
  },
  {
    name: "overview-unprotected",
    path: "/security",
    overrides: {
      "/exposure": {
        grade: "open",
        summary: "Reachable from anywhere.",
        allowlist: [],
        interfaces: ["eth0"],
        client: "203.0.113.77",
      },
      "/firewall/": { ...firewall, enabled: false },
      "/fail2ban/": { available: false, running: false, jails: [] },
      "/ssh-sessions": [],
    },
  },
  {
    name: "overview-failed",
    path: "/security",
    overrides: {
      "/fail2ban/": unreadable,
      "/connections": unreadable,
      "/ssh-sessions": unreadable,
      "/ssh/config": unreadable,
    },
  },
  { name: "overview-readonly", path: "/security", session: viewer },
]

async function settle(page: Page, waiting: boolean | undefined) {
  await page.waitForSelector("[data-slot=page]")
  if (!waiting)
    await page.waitForFunction(
      () => !document.querySelector("[data-slot=page] [data-slot=skeleton]"),
    )
  // The page's rise and a picture's first lines.
  await page.waitForTimeout(900)
}

test.describe("captures", () => {
  test.skip(!SHOTS, "set JD_SECURITY_SHOTS to a folder to take the captures")

  // A small laptop's width too, for the two full pages: five marks in a row
  // have the least room there.
  for (const width of [1280, 390, 1024]) {
    for (const state of STATES) {
      if (width === 1024 && !state.name.endsWith("-full")) continue
      test(`${state.name} at ${width}`, async ({ page }) => {
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
        await mockLogins(page, [], { session: state.session, overrides: state.overrides })
        await page.goto(state.path)
        await settle(page, state.waiting)
        // The shell scrolls inside itself, so the window is made as tall as
        // the page for the frame to hold all of it.
        const height = await page.evaluate(() => {
          const scroller = document.querySelector("[data-workspace-shell-scroll]")
          return scroller ? scroller.scrollHeight - scroller.clientHeight + window.innerHeight : 0
        })
        if (height > 0) {
          await page.setViewportSize({ width, height: Math.min(height, 6000) })
          await page.waitForTimeout(300)
        }
        await page.screenshot({
          path: `${SHOTS}/${PREFIX}-${state.name}-${width}.png`,
          fullPage: true,
        })
      })
    }
  }
})
