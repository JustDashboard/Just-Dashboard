import AxeBuilder from "@axe-core/playwright"
import { expect, test, type Page, type Route } from "@playwright/test"
import { mockKeys } from "./api-keys-fixture"
import { mockAudit } from "./audit-fixture"
import { mockContainerPage, N8N } from "./container-page-fixture"
import { mockContainerExec } from "./container-shell-fixture"
import { DATABASE_SURFACES, mockDatabases } from "./database-fixture"
import { mockFleet } from "./database-fleet-fixture"
import {
  deployment,
  json as deployJson,
  mockNewProject,
  mockProject,
  now as deployNow,
  project,
} from "./deploy-fixture"
import { mockSections } from "./deploy-section-fixture"
import { mockEvents } from "./docker-events-fixture"
import { mockDockerHost } from "./docker-fixture"
import { mockImages } from "./docker-images-fixture"
import { mockNetworkPage } from "./docker-network-page-fixture"
import { mockNetworks } from "./docker-networks-fixture"
import { mockJobs } from "./docker-volume-page-fixture"
import { mockVolumes } from "./docker-volumes-fixture"
import { mockPanel } from "./git-panel-fixture"
import { mockHost } from "./host-fixture"
import { mockLensLogs, PG } from "./logs-lens-fixture"
import { overrides as dnsOverrides } from "./network-dns-fixture"
import { mockDNSServicePage } from "./network-dns-services-fixture"
import { mockNetwork } from "./network-fixture"
import { overrides as gatewayOverrides } from "./network-gateway-fixture"
import { openTraffic } from "./network-traffic-page-fixture"
import { mockOnboarding } from "./onboarding-fixture"
import { mockOverview } from "./overview-fixture"
import { mockHost as mockPackages } from "./packages-fixture"
import { mockPM2 } from "./pm2-fixture"
import { mockProxy, mockShowcase } from "./proxy-fixtures"
import { mockRemoteDesktop, OFFICE } from "./remote-desktop-fixture"
import { mockIntrusion } from "./security-intrusion-fixture"
import { mockLogins } from "./security-logins-fixture"
import { mockServerMap } from "./server-map-fixture"
import { mockServices } from "./services-fixture"
import { mockStackDetail } from "./stack-detail-fixture"
import { mockStacks } from "./stacks-fixture"
import { mockSystemUsers } from "./system-users-fixture"
import { mockWorkbench } from "./workbench-fixture"

/**
 * The accessibility gate: axe on every page the fixtures reach, failing on a
 * serious or a critical violation.
 *
 * Until 0.9.0 axe ran only in the UI-system analysis, and 169 of its 170
 * pages failed at least one rule. Two critical ones (a row group missing its
 * parent role on Git, an unnamed button on a board) and every skipped heading
 * level reached a release with nothing to say so. The rules here are axe's
 * own set as it ships, at the impact axe gives them: WCAG 2.0 to 2.2 A and
 * AA and axe's best practices, without the two it leaves off by default
 * (AAA contrast, and the 24px target size, which a person meets as the 44px
 * press box the touch-target checks hold). Moderate and minor findings are
 * left to review: axe reports some of them on markup that is right, and a
 * gate that cries wolf is one people learn to rerun.
 *
 * A route is opened with the fixture its own spec uses, so the page axe reads
 * is the one that spec asserts, with rows, figures and controls in it. A page
 * whose fixture is private to its spec is opened with the nearest shared one;
 * what it draws then (an empty list, an unavailable state) is still a state a
 * person meets, and is held to the same rules.
 *
 * Every page is read at a desk's width. The shell, a page of each register,
 * a detail page with its rail and a settings page are read on a touch phone
 * as well, where the shell trades its rail for a bottom bar and an identity
 * line trades its words for icons.
 */

type Mock = (page: Page) => Promise<unknown>

const now = new Date().toISOString()

const session = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    displayName: "Operator",
    avatarVersion: 0,
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: now,
    createdAt: now,
  },
}

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

/**
 * A signed-in shell whose every other read answers an empty list: the pages
 * whose fixtures are their spec's own (the account, the boards, the files,
 * the assistant) are read in their empty state.
 */
async function mockShell(page: Page) {
  await page.routeWebSocket(/\/api\/v1\//, () => {})
  await page.route("**/api/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, session)
    if (path === "/updates/self") return json(route, { current: "0.9.0", latest: "0.9.0" })
    if (path === "/docker/ping") return json(route, { available: true, serverVersion: "27.0.0" })
    return json(route, [])
  })
}

/** The sign-in page: the server says nobody is signed in. */
async function mockSignedOut(page: Page) {
  await page.route("**/api/v1/**", (route) =>
    json(route, { authenticated: false, needsTotp: false, needsEnrollment: false }),
  )
}

/** A game project: the project answers as a Minecraft server, three players on it. */
async function mockGame(page: Page) {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7", (route) =>
    deployJson(route, {
      project,
      running: false,
      deployment: { ...deployment, profile: "game", buildMethod: "image", activeRun: undefined },
    }),
  )
  await page.route("**/api/v1/deploy/7/game**", (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    const players = {
      supported: true,
      status: "available",
      online: 3,
      maximum: 20,
      names: ["Notch", "Alex", "Steve"],
      observedAt: deployNow,
    }
    if (path === "/deploy/7/game")
      return deployJson(route, {
        status: "available",
        blueprintId: "minecraft-java",
        edition: "java",
        containerId: "mc123",
        address: "play.example.test:25565",
        console: true,
        players,
        files: [],
      })
    if (path === "/deploy/7/game/players") return deployJson(route, players)
    if (path === "/deploy/7/game/properties")
      return deployJson(route, {
        status: "available",
        path: "/data/server.properties",
        values: { motd: "Old name", "max-players": "20", pvp: "true", difficulty: "normal" },
        restartRequired: true,
        known: [
          { key: "motd", kind: "text", label: "Server list description" },
          {
            key: "max-players",
            kind: "number",
            label: "Maximum players",
            minimum: 1,
            maximum: 1000,
          },
          { key: "pvp", kind: "boolean", label: "Player versus player" },
          {
            key: "difficulty",
            kind: "choice",
            label: "Difficulty",
            choices: [
              { value: "easy", label: "Easy" },
              { value: "normal", label: "Normal" },
            ],
          },
        ],
      })
    return route.fulfill({ status: 503, contentType: "application/json", body: "{}" })
  })
}

const SHOWCASE: Mock = (page) => mockProject(page, { showcase: true })
const NETWORK: Mock = (page) => mockNetwork(page)
const TRAFFIC: Mock = (page) => openTraffic(page)
const USERS: Mock = (page) => mockSystemUsers(page)
const PROXY: Mock = (page) => mockProxy(page, { included: true })
const DATABASE: Mock = (page) =>
  mockDatabases(page, { rows: { 1: { environment: "production", readOnly: true } } })

/**
 * Every route of the app, by the name its capture has in the analysis
 * (`data/coverage.json`): "/" is `overview`, a slash is `__`, a dynamic
 * segment its parameter's name, and `--` a second view of the same route.
 */
const ROUTES: [name: string, url: string, mock: Mock][] = [
  ["overview", "/", (page) => mockOverview(page)],
  ["missing", "/this-page-does-not-exist", mockHost],
  ["login", "/login", mockSignedOut],
  ["welcome", "/welcome", (page) => mockOnboarding(page)],

  ["account", "/account", mockShell],
  ["account__keys", "/account/keys", (page) => mockKeys(page)],
  ["account__security", "/account/security", mockShell],
  ["account__sessions", "/account/sessions", mockShell],
  ["account__users", "/account/users", mockShell],
  ["account__workspace", "/account/workspace", mockShell],
  ["assistant", "/assistant", mockShell],
  ["assistant--settings", "/assistant?view=settings", mockShell],
  ["audit", "/audit", (page) => mockAudit(page)],
  ["backups", "/backups", (page) => mockWorkbench(page)],
  ["backups__job", "/backups/1", (page) => mockWorkbench(page)],
  ["dashboard", "/dashboard", mockShell],
  ["dashboard__configuration", "/dashboard/configuration", mockShell],
  ["dashboard__hub", "/dashboard/hub", mockShell],
  ["dashboard__link", "/dashboard/link", mockShell],
  ["excalidraw", "/excalidraw", mockShell],
  ["excalidraw__id", "/excalidraw/1", mockShell],
  ["files", "/files?path=%2Fhome%2Fubuntu", mockShell],
  ["files__editor", "/files/editor?path=%2Fhome%2Fubuntu%2Fnotes.md", mockShell],
  ["git", "/git?repo=%2Fsrv%2Fapp", (page) => mockPanel(page)],
  ["logs", `/logs?source=${encodeURIComponent(PG)}`, (page) => mockLensLogs(page)],
  ["logs--insights", `/logs?source=${encodeURIComponent(PG)}&mode=insights`, mockLensLogs],
  ["map", "/map", (page) => mockServerMap(page)],
  ["metrics", "/metrics", mockHost],
  ["metrics__alerts", "/metrics/alerts", mockHost],
  ["terminal", "/terminal", mockShell],

  ["databases", "/databases", (page) => mockFleet(page)],
  ["databases__map", "/databases/map", (page) => mockFleet(page)],
  ["databases__new", "/databases/new", (page) => mockFleet(page)],
  ...(
    [
      ["databases__id", "/databases/1"],
      ["databases__id__missing", "/databases/1/nowhere-at-all"],
      ["databases__id__access", "/databases/1/access"],
      ["databases__id__access__account", "/databases/1/access/app"],
      ["databases__id__alerts", "/databases/1/alerts"],
      ["databases__id__assistant", "/databases/1/assistant"],
      ["databases__id__backups", "/databases/1/backups"],
      ["databases__id__data", "/databases/1/data?schema=public&table=orders"],
      ["databases__id__diagram", "/databases/1/diagram?schema=public"],
      ["databases__id__generate", "/databases/1/generate"],
      ["databases__id__import", "/databases/1/import"],
      ["databases__id__logs", "/databases/1/logs?view=queries"],
      ["databases__id__performance", "/databases/1/performance"],
      ["databases__id__performance--statements", "/databases/1/performance?view=statements"],
      ["databases__id__performance__sessions__pid", "/databases/1/performance/sessions/52"],
      ["databases__id__performance__statements__key", "/databases/1/performance/statements/s2"],
      ["databases__id__performance__tables__name", "/databases/1/performance/tables/public.orders"],
      ["databases__id__query", "/databases/1/query"],
      ["databases__id__schema", "/databases/1/schema?schema=public&table=orders"],
      ["databases__id__search", "/databases/1/search?q=pro"],
      ["databases__id__settings", "/databases/1/settings"],
      ["databases__id__settings__danger", "/databases/1/settings/danger"],
      ["databases__id__settings__extensions", "/databases/1/settings/extensions"],
      ["databases__id__settings__network", "/databases/1/settings/network"],
      ["databases__id__settings__parameters", "/databases/1/settings/parameters"],
      ["databases__id__settings__pooling", "/databases/1/settings/pooling"],
      ["databases__id__settings__resources", "/databases/1/settings/resources"],
      ["databases__id__settings__version", "/databases/1/settings/version"],
    ] as const
  ).map(([name, url]): [string, string, Mock] => [name, url, DATABASE]),
  // The other engines' pages the design-system walk opens (a document store,
  // a key-value store, an account list), and a database that is not there.
  ...DATABASE_SURFACES.filter((path) => /^\/databases\/(?!1\b)\d+/.test(path)).map(
    (path): [string, string, Mock] => [path.slice(1).replaceAll("/", "__"), path, DATABASE],
  ),

  ["deploy", "/deploy", SHOWCASE],
  ["deploy__id", "/deploy/7", SHOWCASE],
  ["deploy__id__assistant", "/deploy/7/assistant", SHOWCASE],
  [
    "deploy__id__console",
    "/deploy/7/console",
    async (page) => {
      await SHOWCASE(page)
      await mockContainerExec(page)
    },
  ],
  ["deploy__id__deployments", "/deploy/7/deployments", SHOWCASE],
  ["deploy__id__game-settings", "/deploy/7/game-settings", mockGame],
  ["deploy__id__logs", "/deploy/7/logs", SHOWCASE],
  ["deploy__id__logs--insights", "/deploy/7/logs?view=insights", SHOWCASE],
  ["deploy__id__players", "/deploy/7/players", mockGame],
  ["deploy__id__runs__run", "/deploy/7/runs/84", SHOWCASE],
  ["deploy__id__runtime", "/deploy/7/runtime", SHOWCASE],
  ["deploy__id__settings", "/deploy/7/settings", SHOWCASE],
  ...[
    "general",
    "build",
    "runtime",
    "domains",
    "variables",
    "databases",
    "storage",
    "automation",
    "danger",
  ].map((tab): [string, string, Mock] => [
    `deploy__id__settings__${tab}`,
    `/deploy/7/settings/${tab}`,
    SHOWCASE,
  ]),
  ["deploy__credentials", "/deploy/credentials", SHOWCASE],
  ["deploy__drains", "/deploy/drains", (page) => mockSections(page)],
  ["deploy__new", "/deploy/new", (page) => mockNewProject(page)],
  ["deploy__new--template", "/deploy/new?source=template", (page) => mockNewProject(page)],
  ["deploy__notifications", "/deploy/notifications", SHOWCASE],
  ["deploy__secret-stores", "/deploy/secret-stores", (page) => mockSections(page)],
  ["deploy__templates", "/deploy/templates", (page) => mockSections(page)],
  ["deploy__variables", "/deploy/variables", (page) => mockSections(page)],

  ["docker", "/docker", (page) => mockDockerHost(page)],
  ["docker__containers", "/docker/containers", (page) => mockDockerHost(page)],
  ["docker__containers__id", `/docker/containers/${N8N}?tab=overview`, mockContainerPage],
  ["docker__events", "/docker/events", (page) => mockEvents(page)],
  ["docker__images", "/docker/images", (page) => mockImages(page)],
  ["docker__images__id", `/docker/images/${"a1".repeat(32)}`, (page) => mockImages(page)],
  ["docker__networks", "/docker/networks", (page) => mockNetworks(page)],
  ["docker__networks__name", "/docker/networks/shop_backend", (page) => mockNetworkPage(page)],
  ["docker__stacks", "/docker/stacks", (page) => mockStacks(page)],
  ["docker__stacks__name", "/docker/stacks/shop", (page) => mockStackDetail(page)],
  ["docker__volumes", "/docker/volumes", (page) => mockVolumes(page)],
  [
    "docker__volumes__name",
    "/docker/volumes/shop_uploads",
    async (page) => {
      await mockJobs(page)
      await mockVolumes(page)
    },
  ],

  ["network", "/network", NETWORK],
  ["network__captures", "/network/captures", NETWORK],
  ["network__captures__id", `/network/captures/${"c".repeat(32)}`, NETWORK],
  ["network__connections", "/network/connections", TRAFFIC],
  ["network__connections__address", "/network/connections/198.51.100.23", TRAFFIC],
  ["network__dns", "/network/dns", (page) => mockNetwork(page, [], { overrides: dnsOverrides })],
  ["network__dns__services", "/network/dns/services", (page) => mockDNSServicePage(page)],
  [
    "network__dns__services__id",
    "/network/dns/services/dns-fixture",
    (page) => mockDNSServicePage(page),
  ],
  ["network__drift", "/network/drift", NETWORK],
  ["network__egress", "/network/egress", NETWORK],
  ["network__egress__name", "/network/egress/vpn%20clients", NETWORK],
  ["network__external", "/network/external", NETWORK],
  ["network__firewall", "/network/firewall", NETWORK],
  ["network__flows", "/network/flows", NETWORK],
  [
    "network__gateway",
    "/network/gateway",
    (page) => mockNetwork(page, [], { overrides: gatewayOverrides }),
  ],
  ["network__interfaces", "/network/interfaces", NETWORK],
  ["network__interfaces__name", "/network/interfaces/ens3", NETWORK],
  ["network__investigate", "/network/investigate", NETWORK],
  ["network__ipam", "/network/ipam", NETWORK],
  [
    "network__protection",
    "/network/protection",
    (page) => mockNetwork(page, [], { overrides: gatewayOverrides }),
  ],
  ["network__routing", "/network/routing", NETWORK],
  ["network__runs", "/network/runs", NETWORK],
  ["network__runs__id", "/network/runs/baseline", NETWORK],
  ["network__tools", "/network/tools?tool=ping&target=192.0.2.9", NETWORK],
  ["network__traffic", "/network/traffic", TRAFFIC],
  ["network__traffic--ebpf", "/network/traffic?tab=ebpf", TRAFFIC],
  ["network__traffic__containers__name", "/network/traffic/containers/registry", TRAFFIC],
  ["network__traffic__ebpf__id", "/network/traffic/ebpf/9101?type=cgroup_skb", TRAFFIC],
  ["network__vpn", "/network/vpn", NETWORK],
  ["network__vpn__tunnel", "/network/vpn/wg0", NETWORK],

  ["packages", "/packages", (page) => mockPackages(page)],
  ["packages__name", "/packages/nginx", (page) => mockPackages(page)],
  ["processes", "/processes", mockHost],
  ["processes__pid", "/processes/4021", mockHost],
  ["processes__pm2", "/processes/pm2", (page) => mockPM2(page)],
  ["processes__pm2__app", "/processes/pm2/api", (page) => mockPM2(page)],
  ["processes__scheduled", "/processes/scheduled", mockHost],
  ["processes__services", "/processes/services", (page) => mockServices(page)],
  [
    "processes__services__unit",
    "/processes/services/postgresql.service",
    (page) => mockServices(page),
  ],

  ["proxy", "/proxy", (page) => mockShowcase(page)],
  ["proxy__alerts", "/proxy/alerts", PROXY],
  ["proxy__certificates", "/proxy/certificates", PROXY],
  ["proxy__certificates__name", "/proxy/certificates/app.example.com", PROXY],
  ["proxy__config", "/proxy/config", PROXY],
  ["proxy__ports", "/proxy/ports", PROXY],
  ["proxy__ports__port", "/proxy/ports/tcp-5432", PROXY],
  ["proxy__sites", "/proxy/sites", (page) => mockShowcase(page)],
  ["proxy__sites__name", "/proxy/sites/app.example.com", (page) => mockShowcase(page)],
  ["proxy__streams", "/proxy/streams", PROXY],
  ["proxy__streams__name", "/proxy/streams/postgres-replica", PROXY],
  ["proxy__tls", "/proxy/tls", (page) => mockShowcase(page)],
  ["proxy__traffic", "/proxy/traffic", (page) => mockShowcase(page)],
  ["proxy__traffic--errors", "/proxy/traffic?tab=errors", (page) => mockShowcase(page)],
  ["proxy__tunnel", "/proxy/tunnel", PROXY],

  ["remote-desktop", "/remote-desktop", (page) => mockRemoteDesktop(page)],
  ["remote-desktop__id", `/remote-desktop/${OFFICE}`, (page) => mockRemoteDesktop(page)],
  ["remote-desktop__new", "/remote-desktop/new", (page) => mockRemoteDesktop(page)],

  ["security", "/security", (page) => mockLogins(page)],
  ["security__intrusion", "/security/intrusion", (page) => mockIntrusion(page)],
  ["security__intrusion__jails__jail", "/security/intrusion/jails/sshd", mockIntrusion],
  ["security__logins", "/security/logins", (page) => mockLogins(page)],
  ["security__ssh", "/security/ssh", (page) => mockLogins(page)],

  ["system-users", "/system-users", USERS],
  ["system-users__defaults", "/system-users/defaults", USERS],
  ["system-users__groups", "/system-users/groups", USERS],
  ["system-users__groups__name", "/system-users/groups/developers", USERS],
  ["system-users__new", "/system-users/new", USERS],
  ["system-users__user__name", "/system-users/user/deploy", USERS],
]

/**
 * Read on a touch phone too: the shell, a reading page, a flow page, a detail
 * page with its rail, a settings page, and the identity lines whose verbs
 * become icons there.
 */
const ON_A_PHONE = new Set([
  "audit",
  "account",
  "account__security",
  "docker__containers",
  "deploy__id",
  "deploy__new",
  "deploy__id__settings__general",
  "docker__containers__id",
  "processes__pm2__app",
  "proxy",
  "databases__id__settings__resources",
  "excalidraw__id",
])

/**
 * Waits for the page at rest: drawn, its reads landed (no bone and nothing
 * busy left), and nothing still on its way in. A page that polls is never
 * idle on the network, so that wait is short and its end is not an error.
 */
async function settle(page: Page) {
  await expect(page.locator("main, [data-slot=page]").first()).toBeVisible({ timeout: 20_000 })
  await expect
    .poll(
      () =>
        page.locator("[data-slot=skeleton], [aria-busy=true]").filter({ visible: true }).count(),
      {
        timeout: 8_000,
      },
    )
    .toBe(0)
    .catch(() => undefined)
  await page.waitForLoadState("networkidle", { timeout: 2_500 }).catch(() => undefined)
  // A figure that has just landed is still rising, and a colour mid-fade is a
  // contrast axe would measure on the way in rather than at rest.
  await page.evaluate(
    () =>
      new Promise<void>((done) => {
        const until = Date.now() + 2_000
        const check = () => {
          const moving = document
            .getAnimations()
            .some(
              (pass) =>
                pass.playState === "running" && pass.effect?.getTiming().iterations !== Infinity,
            )
          if (!moving || Date.now() > until) done()
          else requestAnimationFrame(check)
        }
        check()
      }),
  )
}

/**
 * What axe finds today that the pages' owners fix, by route and rule, each
 * with the analysis finding that holds it. Nothing outside this list may
 * fail, and an entry that no longer fails fails the gate too, so the list
 * only ever shrinks: a fix lands with its line taken out.
 *
 * A11Y-05, label in name (WCAG 2.5.3): a control whose `aria-label` does not
 * begin with the words it shows, so a person who speaks the words they see
 * is not understood. The shell's were fixed in 0.9.0 (the account card, the
 * search); these are the pages' own, most of them from three shared rows: a
 * rail's row (`components/rail.tsx`, named by `label` over its title), a
 * `ChoiceRow`'s link (named by its `verb`, "Open ens3", over a title with
 * more words in it) and the process and service tables' row links.
 */
const LABEL_IN_NAME = "label-content-name-mismatch"
const KNOWN: Record<string, { rule: string; finding: string }[]> = Object.fromEntries(
  [
    "overview",
    "backups__job",
    "logs--insights",
    "metrics",
    "databases__id",
    "databases__4",
    "deploy__id",
    "deploy__id__deployments",
    "docker__containers",
    "docker__containers__id",
    "docker__events",
    "docker__images",
    "docker__images__id",
    "docker__networks__name",
    "docker__stacks",
    "docker__volumes",
    "docker__volumes__name",
    "network__connections",
    "network__connections__address",
    "network__dns__services",
    "network__dns__services__id",
    "network__gateway",
    "network__interfaces",
    "network__interfaces__name",
    "network__traffic--ebpf",
    "network__traffic__containers__name",
    "network__traffic__ebpf__id",
    "network__vpn__tunnel",
    "packages__name",
    "processes__pm2__app",
    "processes__services__unit",
    "proxy__certificates__name",
    "proxy__ports",
    "proxy__ports__port",
    "proxy__sites",
    "proxy__sites__name",
    "security__intrusion__jails__jail",
    "system-users__groups",
    "system-users__groups__name",
    "system-users__user__name",
    "deploy__id at 390",
    "processes__pm2__app at 390",
  ].map((name) => [name, [{ rule: LABEL_IN_NAME, finding: "A11Y-05" }]]),
)

async function serious(page: Page) {
  const result = await new AxeBuilder({ page }).analyze()
  return result.violations
    .filter((violation) => violation.impact === "serious" || violation.impact === "critical")
    .map((violation) => ({
      rule: violation.id,
      impact: violation.impact,
      help: violation.help,
      nodes: violation.nodes.slice(0, 5).map((node) => ({
        target: node.target.join(" "),
        html: node.html.slice(0, 160),
        why: node.failureSummary?.split("\n").slice(1, 3).join(" ").trim(),
      })),
    }))
}

/**
 * Reads the page with axe and holds it to the gate, less what `KNOWN` names for it.
 *
 * A known violation is often in something a later read draws with no bone
 * standing for it (the ad-blocker's row on DNS services), so on a busy
 * machine it can land after `settle` has stopped waiting. The page then read
 * as fixed, and the gate told whoever was running it to take a line out of
 * `KNOWN` that is still needed. So a route is called fixed only after it has
 * been read again, for long enough for that read to land.
 */
async function gate(page: Page, key: string, url: string) {
  let found = await serious(page)
  const known = KNOWN[key] ?? []
  const gone = () =>
    known.filter((entry) => !found.some((violation) => violation.rule === entry.rule))
  if (gone().length > 0) {
    await expect
      .poll(
        async () => {
          found = await serious(page)
          return gone().length
        },
        { timeout: 12_000, intervals: [1_000, 2_000, 3_000] },
      )
      .toBe(0)
      .catch(() => undefined)
  }
  const fresh = found.filter((violation) => !known.some((entry) => entry.rule === violation.rule))
  expect(fresh, `serious or critical axe violations on ${url}`).toEqual([])
  const fixed = gone()
  expect(
    fixed.map((entry) => `${entry.rule} (${entry.finding})`),
    `fixed on ${key}: take these out of KNOWN in axe-gate.spec.ts`,
  ).toEqual([])
}

for (const [name, url, mock] of ROUTES) {
  test(`axe finds nothing serious on ${name}`, async ({ page }) => {
    test.setTimeout(60_000)
    await page.setViewportSize({ width: 1440, height: 900 })
    await mock(page)
    await page.goto(url)
    await settle(page)
    await gate(page, name, url)
  })
}

test.describe("on a touch phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  for (const [name, url, mock] of ROUTES.filter(([name]) => ON_A_PHONE.has(name))) {
    test(`axe finds nothing serious on ${name}`, async ({ page }) => {
      test.setTimeout(60_000)
      await mock(page)
      await page.goto(url)
      await settle(page)
      await gate(page, `${name} at 390`, `${url} at 390`)
    })
  }
})
