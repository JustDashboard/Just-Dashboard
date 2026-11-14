import { test, type Page, type Route } from "@playwright/test"
import { iso, json, mockHost, now, user } from "./host-fixture"
import { mockOverview } from "./overview-fixture"

/**
 * Full-height captures of the host Overview, the missing page, the assistant's
 * settings, Metrics and the host's Alerts, in every state each has, at a
 * laptop's width and a phone's: for looking at rather than asserting on. It
 * runs only when `JD_OVERVIEW_SHOTS` names a folder:
 *
 *   JD_OVERVIEW_SHOTS=/tmp/shots bunx playwright test --workers=1 tests/browser/overview-metrics-shots.spec.ts
 *
 * `JD_OVERVIEW_SHOTS_PREFIX` names the set ("before" from the base build).
 */
const SHOTS = process.env.JD_OVERVIEW_SHOTS
const PREFIX = process.env.JD_OVERVIEW_SHOTS_PREFIX ?? "after"

const viewer = { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }

/** A read that never answers, so the page is caught waiting. */
const PENDING = Symbol("pending")
/** A read the server refuses. */
const FAILED = Symbol("failed")

type Answer = unknown | typeof PENDING | typeof FAILED

const answer = (route: Route, body: Answer) => {
  if (body === PENDING) return new Promise<void>(() => {})
  if (body === FAILED)
    return route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code: "unavailable", message: "the host could not be read" },
      }),
    })
  return json(route, body)
}

const slack = {
  id: 1,
  name: "#ops",
  kind: "slack",
  url: "",
  target: "hooks.slack.com/services/T0",
  events: [],
  enabled: true,
  createdAt: iso(now),
  updatedAt: iso(now),
}
const mail = {
  ...slack,
  id: 2,
  name: "On-call mail",
  kind: "email",
  target: "oncall@acme.test",
  enabled: false,
}

const rule = (id: number, kind: string, params: object, extra: object = {}) => ({
  id,
  kind,
  params,
  channels: [],
  enabled: true,
  createdAt: iso(now),
  updatedAt: iso(now),
  ...extra,
})

const alertsFull = {
  intervalSeconds: 60,
  metrics: true,
  lastPass: iso(now - 20_000),
  rules: [
    rule(1, "container_down", { minutes: 2 }),
    rule(
      2,
      "cpu_high",
      { threshold: 90, minutes: 10 },
      {
        channels: [1],
        unjudged: "Only 3 of the 10 minutes it averages are recorded so far.",
      },
    ),
    rule(3, "disk_filling", { threshold: 85 }),
    rule(4, "backup_failed", {}, { enabled: false }),
  ],
  subjects: [
    {
      ruleId: 1,
      kind: "container_down",
      subject: "shop-api-1",
      label: "shop-api-1",
      detail: "It exited with code 1.",
      state: "firing",
      since: iso(now - 300_000),
      muted: false,
    },
  ],
  history: [
    {
      id: 1,
      ruleId: 1,
      kind: "container_down",
      subject: "shop-api-1",
      label: "shop-api-1",
      event: "host.alert.firing",
      detail: "It exited with code 1.",
      delivered: 1,
      failed: 0,
      muted: false,
      test: false,
      createdAt: iso(now - 240_000),
    },
  ],
}

const alertsEmpty = { rules: [], subjects: [], history: [], intervalSeconds: 60, metrics: true }

const PRESETS = [
  ["anthropic", "Anthropic", "Claude, with your API key", "claude"],
  ["openai", "OpenAI", "GPT models, with your API key", "openai"],
  ["openrouter", "OpenRouter", "Hundreds of models behind one key", "openrouter"],
  ["gemini", "Google Gemini", "Gemini, with an AI Studio key", "gemini"],
  ["ollama", "Ollama", "A model running on this machine", "ollama"],
].map(([id, label, detail, product]) => ({
  id,
  label,
  detail,
  product,
  provider: id === "anthropic" ? "anthropic" : "openai",
  keyRequired: id !== "ollama",
}))

const assistantOn = {
  configured: true,
  provider: "anthropic",
  preset: "anthropic",
  tools: true,
  model: "claude-opus-5-5",
  modelLabel: "Claude Opus 5.5",
  keySet: true,
  models: [{ id: "claude-opus-5-5", label: "Claude Opus 5.5" }],
  presets: PRESETS,
  defaultMode: "changes",
  updatedAt: "2026-10-30T09:00:00Z",
  updatedBy: "operator",
  profiles: [
    {
      preset: "anthropic",
      provider: "anthropic",
      model: "claude-opus-5-5",
      modelLabel: "Claude Opus 5.5",
      keySet: true,
      active: true,
    },
    {
      preset: "openrouter",
      provider: "openai",
      model: "deepseek/deepseek-r2",
      keySet: true,
      active: false,
    },
  ],
}
const assistantOff = {
  configured: false,
  models: [],
  presets: PRESETS,
  profiles: [],
  defaultMode: "changes",
}

type State = {
  name: string
  path: string
  session?: typeof viewer
  /** Reads answered otherwise than the fixture does, by the end of their path. */
  overrides?: Record<string, Answer>
  /** The page is caught waiting, so there is nothing to wait for. */
  waiting?: boolean
  /** Also taken from the base build, as the `before` set. */
  before?: boolean
}

const MODULES = [
  "/docker/containers/",
  "/databases/fleet",
  "/certificates/",
  "/exposure",
  "/packages/updates",
  "/backups/",
  "/git/",
  "/systemd/",
  "/terminal/",
  "/boards/",
  "/remote-desktop/",
  "/dashboard/update",
  "/processes/",
]
const every = (body: Answer) => Object.fromEntries(MODULES.map((path) => [path, body]))

const STATES: State[] = [
  { name: "overview-full", path: "/", before: true },
  {
    name: "overview-loading",
    path: "/",
    waiting: true,
    overrides: { ...every(PENDING), "/deploy/": PENDING, "/system/metrics/events": PENDING },
  },
  {
    name: "overview-empty",
    path: "/",
    overrides: {
      "/docker/containers/": [],
      "/databases/fleet": { connections: [] },
      "/certificates/": [],
      "/backups/": [],
      "/git/": { available: true, repos: [] },
      "/terminal/": {
        enabled: true,
        sessions: [],
        login: { user: "operator", home: "/home/operator" },
      },
      "/boards/": [],
      "/remote-desktop/": { engine: { state: "stopped" }, computers: [] },
      "/system/metrics/events": [],
    },
  },
  { name: "overview-failed", path: "/", overrides: every(FAILED) },
  { name: "overview-readonly", path: "/", session: viewer },
  { name: "missing", path: "/this-page-does-not-exist", before: true },
  { name: "missing-in-section", path: "/metrics/no-such-view" },
  {
    name: "assistant-settings",
    path: "/assistant?view=settings",
    overrides: { "/assistant": assistantOn },
    before: true,
  },
  {
    name: "assistant-settings-off",
    path: "/assistant?view=settings",
    overrides: { "/assistant": assistantOff },
    before: true,
  },
  {
    name: "assistant-settings-readonly",
    path: "/assistant?view=settings",
    session: viewer,
    overrides: { "/assistant": assistantOn },
  },
  { name: "metrics-full", path: "/metrics", before: true },
  {
    name: "alerts-full",
    path: "/metrics/alerts",
    overrides: { "/system/alerts": alertsFull, "/deploy/notifications": [slack, mail] },
    before: true,
  },
  {
    name: "alerts-empty",
    path: "/metrics/alerts",
    overrides: { "/system/alerts": alertsEmpty, "/deploy/notifications": [] },
    before: true,
  },
  {
    name: "alerts-loading",
    path: "/metrics/alerts",
    waiting: true,
    overrides: { "/system/alerts": PENDING, "/deploy/notifications": PENDING },
  },
  {
    name: "alerts-failed",
    path: "/metrics/alerts",
    overrides: { "/system/alerts": FAILED, "/deploy/notifications": FAILED },
  },
  {
    name: "alerts-readonly",
    path: "/metrics/alerts",
    session: viewer,
    overrides: { "/system/alerts": alertsFull, "/deploy/notifications": [slack, mail] },
  },
]

async function mock(page: Page, state: State) {
  if (state.path === "/") await mockOverview(page)
  else await mockHost(page)
  if (state.session)
    await page.route("**/api/v1/auth/session", (route) => json(route, state.session))
  for (const [path, body] of Object.entries(state.overrides ?? {})) {
    await page.route(
      (url) => url.pathname === `/api/v1${path}` || url.pathname === `/api/v1${path}/`,
      (route) => {
        // The fleet is the one read told apart by its query.
        if (path === "/deploy/" && !route.request().url().includes("view=fleet"))
          return route.fallback()
        return answer(route, body)
      },
    )
  }
}

async function settle(page: Page, waiting: boolean | undefined) {
  await page.waitForSelector("[data-slot=page]")
  if (!waiting)
    await page.waitForFunction(
      () => !document.querySelector("[data-slot=page] [data-slot=skeleton]"),
    )
  // The page's rise, a picture's first lines and the readings' count up.
  await page.waitForTimeout(1500)
}

test.describe("captures", () => {
  test.skip(!SHOTS, "set JD_OVERVIEW_SHOTS to a folder to take the captures")

  for (const width of [1280, 390]) {
    for (const state of STATES) {
      if (PREFIX === "before" && !state.before) continue
      test(`${state.name} at ${width}`, async ({ page }) => {
        test.setTimeout(90_000)
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
        await mock(page, state)
        await page.goto(state.path)
        await settle(page, state.waiting)
        // The shell scrolls inside itself, so the window is made as tall as
        // the page for the frame to hold all of it.
        const height = await page.evaluate(() => {
          const scroller = document.querySelector("[data-workspace-shell-scroll]")
          return scroller ? scroller.scrollHeight - scroller.clientHeight + window.innerHeight : 0
        })
        if (height > 0) {
          await page.setViewportSize({ width, height: Math.min(height, 8000) })
          await page.waitForTimeout(400)
        }
        await page.screenshot({
          path: `${SHOTS}/${PREFIX}-${state.name}-${width}.png`,
          fullPage: true,
        })
      })
    }
  }
})
