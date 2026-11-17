import { expect, test, type Page } from "@playwright/test"
import { mockKeys } from "./api-keys-fixture"
import { mockCommandSearch } from "./command-search-fixture"
import { mockFleet } from "./database-fleet-fixture"
import { mockNewProject, mockProject } from "./deploy-fixture"
import { mockDockerEngine } from "./docker-engine-fixture"
import { mockHost, user as operator } from "./host-fixture"
import { mockHostLogs } from "./host-logs-fixture"
import { PG, mockLensLogs } from "./logs-lens-fixture"
import { mockNetwork } from "./network-fixture"
import { mockOverview } from "./overview-fixture"
import { mockHost as mockPackagesHost } from "./packages-fixture"
import { mockShowcase, scan } from "./proxy-fixtures"
import { mockRemoteDesktop, overview as remoteOverview } from "./remote-desktop-fixture"
import { mockIntrusion } from "./security-intrusion-fixture"
import { mockServerMap } from "./server-map-fixture"
import { mockStacks } from "./stacks-fixture"
import { mockSystemUsers } from "./system-users-fixture"
import { jobs, mockWorkbench } from "./workbench-fixture"

/**
 * The README pictures (`docs/readme/`), taken from the fixtures the other
 * specs use. Not a check: it runs only when `JD_README_SHOTS` names the
 * directory the frames go to, against a build serving the release's version.
 *
 *   JD_README_SHOTS=/tmp/readme JD_BROWSER_BASE_URL=http://127.0.0.1:<port> \
 *     bunx playwright test tests/browser/readme-shots.spec.ts --workers=1
 *
 * Each frame is 1440×900 at twice the density; the frames are then saved
 * 2000 pixels wide as 256-colour palettes (see `docs/readme/` in the
 * repository map). `JD_README_ONLY` takes a comma-separated list of names.
 * Twelve frames need a helper a spec keeps to itself: files, git, terminal,
 * excalidraw, processes (Scheduled), security, and the six database ones.
 * Those are taken from an untracked copy of that spec with a capture
 * appended, and the three a database spec already takes under `@shots`
 * (`database-assistant`, `database-explain`, `database-pitr`) come from
 * running those with `JD_SHOTS` set.
 *
 * Nothing in a frame may name a real person, host, address or token: the
 * repository is public. Where a fixture signs in under a real name, the
 * frame overrides it below.
 */

const dir = process.env.JD_README_SHOTS
const only = process.env.JD_README_ONLY?.split(",").filter(Boolean)

test.skip(!dir, "set JD_README_SHOTS to a directory to take the README pictures")
test.use({
  viewport: { width: 1440, height: 900 },
  deviceScaleFactor: 2,
  colorScheme: "dark",
  contextOptions: { reducedMotion: "reduce" },
  launchOptions: { args: ["--font-render-hinting=none"] },
})
test.describe.configure({ timeout: 120_000 })

const json = (body: unknown) => ({
  status: 200,
  contentType: "application/json",
  body: JSON.stringify(body),
})

/** Waits for the network to go quiet and for every finite animation to finish. */
async function settle(page: Page) {
  await page.waitForLoadState("networkidle", { timeout: 8000 }).catch(() => {})
  await page.waitForTimeout(1500)
  await page
    .waitForFunction(
      () =>
        document
          .getAnimations()
          .filter((a) => a.effect?.getTiming().iterations !== Infinity)
          .every((a) => a.playState !== "running"),
      undefined,
      { timeout: 10_000 },
    )
    .catch(() => {})
}

async function shot(page: Page, name: string) {
  await settle(page)
  await page.addStyleTag({
    content:
      "[data-sonner-toaster]{display:none!important}*{caret-color:transparent!important}:focus-visible{outline:none!important;box-shadow:none!important}",
  })
  await page.mouse.move(0, 0)
  await page.waitForTimeout(300)
  await page.screenshot({ path: `${dir}/${name}.png` })
}

const frames: { name: string; take: (page: Page) => Promise<void> }[] = [
  {
    name: "overview",
    take: async (page) => {
      await mockOverview(page)
      await page.goto("/")
    },
  },
  {
    name: "metrics",
    take: async (page) => {
      await mockHost(page)
      await page.goto("/metrics")
    },
  },
  {
    name: "system-users",
    take: async (page) => {
      await mockSystemUsers(page)
      await page.goto("/system-users")
    },
  },
  {
    name: "packages",
    take: async (page) => {
      await mockPackagesHost(page)
      await mockHostLogs(page)
      await page.goto("/packages")
    },
  },
  {
    name: "backups",
    take: async (page) => {
      await mockWorkbench(page)
      await page.route("**/api/v1/auth/session", (route) => route.fulfill(json(operator)))
      await page.route("**/api/v1/backups/", (route) => route.fulfill(json(scrub(jobs))))
      await page.goto("/backups")
    },
  },
  {
    name: "api-keys",
    take: async (page) => {
      await mockKeys(page)
      await page.goto("/account/keys")
    },
  },
  {
    name: "command-palette",
    take: async (page) => {
      await mockCommandSearch(page)
      await page.goto("/account")
      await settle(page)
      await page
        .locator('[data-slot="sidebar-header"]')
        .getByRole("button", { name: /Search/ })
        .click()
      const palette = page.getByRole("dialog", { name: "Command palette" })
      await palette.getByRole("combobox", { name: "Search dashboard" }).fill("shop")
      await expect(palette.getByRole("option").first()).toBeVisible()
    },
  },
  {
    name: "logs",
    take: async (page) => {
      await mockLensLogs(page)
      await page.goto(`/logs?source=${encodeURIComponent(PG)}`)
    },
  },
  {
    name: "map",
    take: async (page) => {
      await mockServerMap(page)
      await page.goto("/map?system=system:project:1")
      await expect(page.locator(".react-flow__node").first()).toBeVisible()
    },
  },
  {
    name: "remote-desktop",
    take: async (page) => {
      await mockRemoteDesktop(page, { overview: scrub(remoteOverview()) })
      await page.goto("/remote-desktop")
    },
  },
  {
    name: "proxy",
    take: async (page) => {
      await mockShowcase(page)
      await proxyTraffic(page)
      await page.goto("/proxy")
    },
  },
  {
    name: "proxy-sites",
    take: async (page) => {
      await mockShowcase(page)
      await proxyTraffic(page)
      await page.goto("/proxy/sites")
    },
  },
  {
    name: "proxy-certificates",
    take: async (page) => {
      await mockShowcase(page)
      await page.goto("/proxy/certificates")
    },
  },
  {
    name: "proxy-tls",
    take: async (page) => {
      await mockShowcase(page)
      await page.route("**/api/v1/certificates/scan**", (route) =>
        route.fulfill(json({ ...scan, domain: "mail.example.com", port: 8443 })),
      )
      await page.goto("/proxy/tls?domain=mail.example.com%3A8443")
      await expect(page.getByRole("heading", { name: "mail.example.com:8443" })).toBeVisible()
    },
  },
  ...(
    [
      ["network", "/network"],
      ["network-firewall", "/network/firewall"],
      ["network-tools", "/network/tools"],
      ["network-vpn", "/network/vpn"],
    ] as const
  ).map(([name, path]) => ({
    name,
    take: async (page: Page) => {
      await mockNetwork(page)
      await mockHostLogs(page)
      await page.goto(path)
    },
  })),
  {
    name: "databases",
    take: async (page) => {
      await mockFleet(page, { dumps: { 4: null, 5: 60 } })
      await page.goto("/databases")
    },
  },
  ...(
    [
      ["deployments", "/deploy"],
      ["deployment", "/deploy/7"],
      ["deployment-releases", "/deploy/7/deployments"],
      ["deployment-runtime", "/deploy/7/runtime"],
      ["deployment-domains", "/deploy/7/settings/domains"],
      ["deployment-notifications", "/deploy/notifications"],
    ] as const
  ).map(([name, path]) => ({
    name,
    take: async (page: Page) => {
      await mockProject(page, { showcase: true })
      await page.goto(path)
    },
  })),
  {
    name: "deploy-new",
    take: async (page) => {
      await mockNewProject(page)
      await page.goto("/deploy/new?source=template")
    },
  },
  {
    name: "docker",
    take: async (page) => {
      await mockDockerEngine(page)
      await page.goto("/docker")
    },
  },
  {
    name: "stacks",
    take: async (page) => {
      await mockStacks(page)
      await page.goto("/docker/stacks")
    },
  },
  {
    name: "security-intrusion",
    take: async (page) => {
      await mockIntrusion(page)
      await page.goto("/security/intrusion")
    },
  },
  {
    name: "security-ssh",
    take: async (page) => {
      await mockIntrusion(page)
      await page.goto("/security/ssh")
    },
  },
]

/**
 * An hour of traffic over the showcase's three sites and one group in nginx's
 * error log: the showcase serves neither, and its pages would say they could
 * not read the access logs.
 */
async function proxyTraffic(page: Page) {
  const minute = (ago: number, total: number, failed: number) => {
    const start = new Date(Date.now() - ago * 60_000)
    start.setUTCSeconds(0, 0)
    return { start: start.toISOString(), total, refused: 0, failed, bytes: total * 18_000 }
  }
  const hour = (site: string, base: number, swing: number, p95: number) => {
    const points = Array.from({ length: 60 }, (_, i) => {
      const ago = 60 - i
      const total = Math.round(base + swing * Math.sin(i / 9) + ((i * 7) % 5))
      return minute(ago, total, i % 17 === 3 ? 1 : 0)
    })
    const requests = points.reduce((sum, p) => sum + p.total, 0)
    const failed = points.reduce((sum, p) => sum + p.failed, 0)
    return {
      site,
      file: `/etc/nginx/sites-available/${site}`,
      engine: "nginx",
      status: "available",
      requests,
      errorRate: failed / requests,
      bytes: requests * 18_000,
      complete: true,
      p95,
      points,
    }
  }
  await page.route("**/api/v1/proxy/traffic", (route) =>
    route.fulfill(
      json({
        observedAt: new Date().toISOString(),
        sites: [
          hour("app.example.com", 45, 12, 180),
          hour("shop.example.com", 24, 8, 140),
          hour("legacy.example.com", 9, 3, 410),
        ],
      }),
    ),
  )
  const at = new Date().toISOString()
  await page.route("**/api/v1/proxy/errors?**", (route) =>
    route.fulfill(
      json({
        scope: "main",
        path: "/var/log/nginx/error.log",
        exists: true,
        since: at,
        complete: true,
        lines: 3,
        groups: [
          {
            pattern: "upstream timed out",
            level: "error",
            count: 3,
            first: at,
            last: at,
            sample: "upstream timed out (110: Connection timed out) while reading response header",
            upstream: "http://127.0.0.1:3000",
            title: "The application took too long to answer",
            advice: "Look at what the application was doing.",
          },
        ],
      }),
    ),
  )
}

/**
 * A fixture's data with the account names, and the bucket named after one,
 * replaced by neutral ones, so no picture names a real account.
 */
function scrub<T>(value: T): T {
  if (Array.isArray(value)) return value.map(scrub) as T
  if (!value || typeof value !== "object") return value
  return Object.fromEntries(
    Object.entries(value).map(([key, inner]) => {
      if (typeof inner === "string" && inner && ["username", "login", "author"].includes(key))
        return [key, "operator"]
      if (typeof inner === "string" && key === "bucket" && inner.endsWith("-backups"))
        return [key, "acme-backups"]
      return [key, scrub(inner)]
    }),
  ) as T
}

for (const frame of frames) {
  if (only && !only.includes(frame.name)) continue
  test(`readme ${frame.name}`, async ({ page }) => {
    await frame.take(page)
    await shot(page, frame.name)
  })
}
