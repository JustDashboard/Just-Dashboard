import { expect as baseExpect, test, type Page } from "@playwright/test"
import { json, loaded, type Mutation } from "./network-fixture"
import { blocks, ebpf, liveRoute, openMaturity as open } from "./network-traffic-maturity-fixture"

// Traffic, bandwidth and shaping, and the Connections page's operator
// workflows, against recorded API shapes. Every mutation is captured so a
// test reads back exactly what a control sent.

// Both pages draw a dozen reads on arrival, and on a host busy with other work
// the page shell alone can take fifteen seconds and the last read a few more.
// An assertion that holds is no slower for the longer wait, so every case gets
// the budget up front instead of each assertion asking for its own.
const expect = baseExpect.configure({ timeout: 15_000 })
test.describe.configure({ timeout: 120_000 })

/**
 * Opens a page and waits until it has hydrated and every section has landed.
 * Controls clicked before that are clicked on server-rendered markup that
 * does nothing, and a locator resolved before the first render can race it.
 */
async function visit(page: Page, path: string) {
  await page.goto(path)
  await loaded(page)
}

test("the live window says how old its reading is, with packets, TCP resent and round trip", async ({
  page,
}) => {
  const requests: string[] = []
  page.on("request", (r) => {
    if (r.url().includes("/traffic/live")) requests.push(r.url())
  })
  await open(page, [])
  await visit(page, "/network/traffic")
  const context = page.getByLabel("Live context")
  await expect(context).toContainText("Newest reading")
  await expect(context).toContainText(/\ds old/)
  await expect(context).toContainText("Packets on ens3")
  await expect(context).toContainText("TCP resent")
  // 30 of every 1000 segments are sent again, past the 2% warning.
  await expect(context).toContainText("3.0%")
  await expect(context).toContainText("23 ms")
  await expect(context).toContainText("median of 37 sockets · p90 118 ms")
  await expect(page.getByLabel("ens3 packets")).toContainText("packets in")
  await expect(page.getByLabel("ens3 packets")).toContainText("3 drops in 15 min")
  expect(requests.some((u) => u.includes("latency=1"))).toBe(true)
})

test("a stopped sampler reads as stale, not as a quiet link", async ({ page }) => {
  await open(page, [])
  await page.route("**/api/v1/network/traffic/live**", liveRoute(40))
  await visit(page, "/network/traffic")
  const context = page.getByLabel("Live context")
  await expect(context).toContainText(/4\ds old/)
  await expect(context).toContainText("the sampler has not stepped; figures are stale")
})

test("a typed range reads that window with its percentiles and the incidents in it", async ({
  page,
}) => {
  const history: URL[] = []
  const marks: URL[] = []
  page.on("request", (r) => {
    if (r.url().includes("/traffic/history")) history.push(new URL(r.url()))
    if (r.url().includes("/traffic/annotations")) marks.push(new URL(r.url()))
  })
  await open(page, [])
  await visit(page, "/network/traffic")
  await page.getByRole("radio", { name: "Range" }).click()
  const form = page.getByRole("form", { name: "Recorded range" })
  await form.getByLabel("From").fill("2026-10-01T08:00")
  await form.getByLabel("To").fill("2026-10-01T09:30")
  await form.getByRole("button", { name: "Show range" }).click()
  await expect(page.getByLabel("Chosen range")).toBeVisible()
  await expect
    .poll(() => history.some((u) => u.searchParams.get("from") && u.searchParams.get("to")))
    .toBe(true)
  const asked = history.find((u) => u.searchParams.get("from"))!
  expect(Number(asked.searchParams.get("to")) - Number(asked.searchParams.get("from"))).toBe(5400)
  await expect(page.getByLabel("ens3 percentiles")).toContainText("p95 ↓ 2.0 MB/s")
  await expect(page.getByLabel("ens3 percentiles")).toContainText("over 240 15s intervals")
  await expect
    .poll(() => marks.some((u) => u.searchParams.get("from") === asked.searchParams.get("from")))
    .toBe(true)

  // A reversed range is refused before anything is read.
  await page.getByRole("radio", { name: "Range" }).click()
  await form.getByLabel("From").fill("2026-10-01T10:00")
  await form.getByLabel("To").fill("2026-10-01T09:00")
  await expect(form.getByText("The range ends before it starts.")).toBeVisible()
  await expect(form.getByRole("button", { name: "Show range" })).toBeDisabled()
})

test("a transfer budget reads exact and estimated bytes, alerts, and is set and cleared", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await open(page, mutations)
  await visit(page, "/network/traffic")
  const budgets = page.getByLabel("Transfer budgets")
  await expect(budgets).toContainText("830.0 GB of 1.0 TB in and out this month")
  await expect(budgets).toContainText("40.0 GB estimated from older rows")
  await expect(budgets).toContainText("96% of the month so far was recorded")
  await expect(budgets).toContainText("over 80%")
  await expect(budgets).toContainText("Not measured")
  await expect(budgets).toContainText("alerts, never limits")

  const form = page.getByRole("form", { name: "Set a transfer budget" })
  await form.getByRole("radio", { name: "week" }).click()
  await form.getByRole("radio", { name: "out" }).click()
  await form.getByLabel("Budget", { exact: true }).fill("5 KB")
  await expect(form.getByRole("button", { name: "Set budget" })).toBeDisabled()
  await form.getByLabel("Budget", { exact: true }).fill("200 GB")
  await form.getByRole("button", { name: "Set budget" }).click()
  await expect
    .poll(() => mutations.find((m) => m.method === "PUT"))
    .toEqual({
      method: "PUT",
      path: "/network/traffic/quotas/ens3",
      body: { period: "week", direction: "tx", limitBytes: 200 * 2 ** 30 },
    })
  await budgets.getByRole("button", { name: "Clear the budget on wg0" }).click()
  expect(mutations.some((m) => m.method === "DELETE")).toBe(false)
  await page.getByRole("button", { name: "Clear", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.method === "DELETE")?.path)
    .toBe("/network/traffic/quotas/wg0")
})

test("programs say what a read could not see and list UDP peers without byte counters", async ({
  page,
}) => {
  await open(page, [])
  await visit(page, "/network/traffic")
  const limits = page.getByLabel("What the program read could not see")
  await expect(limits).toContainText("14 TCP connections opened and closed between two reads")
  await expect(limits).toContainText("3 sockets closed since the last read")
  await expect(limits).toContainText("Nothing before this page opened is kept")
  await page.getByRole("button", { name: "Who agent-cli talks to" }).click()
  const peers = page.getByLabel("Who agent-cli talks to", { exact: true }).last()
  await expect(peers).toContainText("UDP · 1 connection")
  await expect(peers).toContainText("no byte counters")
  await expect(peers).toContainText("median RTT 18 ms")
  await page.getByRole("button", { name: "Who agent-cli talks to" }).click()
  await page.getByRole("button", { name: "Who caddy talks to" }).click()
  await expect(page.getByText("2 unconnected UDP sockets — listeners and servers")).toBeVisible()
})

test("every container can be listed and one opens into its chart, service and peers", async ({
  page,
}) => {
  const flows: URL[] = []
  page.on("request", (r) => {
    if (r.url().includes("/network/flows/")) flows.push(new URL(r.url()))
  })
  await open(page, [])
  await visit(page, "/network/traffic")
  const band = page.getByLabel("Traffic by container", { exact: true })
  await expect(band.getByRole("button", { name: "Open registry's traffic" })).toHaveCount(0)
  await band.getByRole("button", { name: "Show all 7" }).click()
  await band.getByRole("button", { name: "Open registry's traffic" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet).toContainText("infra/registry")
  await expect(sheet).toContainText("registry:2")
  const talked = sheet.getByLabel("Who registry talked to")
  await expect(talked).toContainText("198.51.100.99")
  await expect(talked).toContainText("47.7 MiB")
  await expect(talked).toContainText("192.0.2.53")
  await expect
    .poll(() =>
      flows.find((u) => u.searchParams.get("containerId"))?.searchParams.get("containerId"),
    )
    .toBe("a".repeat(64))
})

test("shaping says what the next change does with each device's queues, and refuses a foreign tree", async ({
  page,
}) => {
  await open(page, [])
  await visit(page, "/network/traffic")
  const queues = page.getByRole("table").filter({ hasText: "tailscale0" })
  await expect(queues).toContainText("captures this fq_codel's parameters and puts them back")
  await expect(queues).toContainText("replaces the kernel's default queue")
  await expect(queues).toContainText("Changes are refused: jd-lab has an unmanaged queue hierarchy")
  // Drift names the parameter changed in place.
  await expect(queues).toContainText("rtt 50000 → 100000")
  await page.getByRole("button", { name: "Edit the limits on jd-lab" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("This device's queues belong to something else")).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Apply" })).toBeDisabled()
  await sheet.getByText("Queues and parameters").click()
  await expect(sheet.getByLabel("jd-lab queue tree")).toContainText("parent 1:10 · sfq 10:")
})

test("an upload profile goes with CAKE and the delay CAKE measured is shown", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, mutations)
  await visit(page, "/network/traffic")
  await expect(
    page.getByText("Upload CAKE delay 1.4 ms average in best effort · 41 ms peak"),
  ).toBeVisible()
  await page.getByRole("button", { name: "Edit the limits on ens3" }).click()
  const sheet = page.getByRole("dialog")
  const delay = sheet.getByLabel("Upload queue delay")
  await expect(delay).toContainText("Voice")
  await expect(delay).toContainText("6.2 ms")
  await expect(sheet.getByLabel("Overhead bytes")).toHaveValue("34")
  await sheet.getByLabel("Overhead bytes").fill("300")
  await expect(sheet.getByText("Overhead must be a whole number from -64 to 256.")).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Apply" })).toBeDisabled()
  await sheet.getByLabel("Overhead bytes").fill("18")
  await sheet.getByRole("switch", { name: "Filter redundant ACKs" }).click()
  await sheet.getByRole("button", { name: "Apply" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/network/shaping/ens3")?.body)
    .toEqual({
      qdisc: "cake",
      egressKbit: 18000,
      ingressKbit: 0,
      upload: {
        diffserv: "diffserv4",
        flowMode: "dual-srchost",
        nat: false,
        wash: false,
        ackFilter: true,
        linkLayer: "ptm",
        overhead: 18,
        mpu: 64,
        rttMillis: 50,
      },
    })
})

test("congestion control is compared on live sockets, now and before the last switch", async ({
  page,
}) => {
  await open(page, [])
  await visit(page, "/network/traffic")
  const panel = page.getByLabel("Congestion control on live sockets")
  const now = panel.getByLabel("Now")
  await expect(now.getByRole("row", { name: /bbr/ })).toContainText("22 ms")
  await expect(now.getByRole("row", { name: /cubic/ })).toContainText("0.8%")
  await expect(panel).toContainText("Before the last switch, cubic → bbr")
  await expect(panel.getByLabel("Before the last switch")).toContainText("52 ms")
  await expect(panel).toContainText("not a controlled test")
})

test("a loaded program opens into its maps, attachments and cost, and the observer is marked", async ({
  page,
}) => {
  // The figure is a spring that counts up once it is on screen, so under load
  // it reads "40" on its way to "41". Reduced motion writes the value at once;
  // the animation is the ticker's own, not this page's claim.
  await page.emulateMedia({ reducedMotion: "reduce" })
  await open(page, [])
  await visit(page, "/network/traffic")
  await expect(page.getByLabel("eBPF platform")).toContainText(
    "Kernel 6.14.0-37-generic · JIT on · unprivileged loading refused · BTF type information present · bpf filesystem mounted · run statistics off",
  )
  const cgroups = page.locator("[data-slot=stat-tile]").filter({ hasText: "On cgroups" })
  await cgroups.scrollIntoViewIfNeeded()
  await expect(cgroups).toContainText("41")
  const row = page.getByRole("row").filter({ hasText: "jd_flow_egress" })
  await expect(row).toContainText("this dashboard’s observer")
  await row.getByRole("button", { name: "Open program 9101" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByLabel("Where program 9101 is attached")).toContainText(
    "/sys/fs/cgroup/system.slice",
  )
  await expect(sheet.getByLabel("Maps of program 9101")).toContainText("jd_events")
  await expect(sheet.getByText("Run cost is not counted")).toBeVisible()
})

test("an install is followed by configured, active and verified phases, not by a claim", async ({
  page,
}) => {
  let installed = false
  await open(page, [], { "/network/ebpf": { ...ebpf, installed: false, package: "bpftool" } })
  await page.route("**/api/v1/network/ebpf", (route) =>
    json(
      route,
      installed
        ? {
            ...ebpf,
            error: "bpftool: can't get next program: Operation not permitted",
            programs: [],
            total: 0,
          }
        : { ...ebpf, installed: false, package: "bpftool", programs: [], total: 0 },
    ),
  )
  await page.route("**/api/v1/network/handoffs/bpftool", (route) =>
    json(route, {
      package: "bpftool",
      checkedAt: new Date().toISOString(),
      working: false,
      phases: [
        { key: "installed", status: "done", detail: "bpftool is on the host" },
        { key: "configured", status: "not_applicable", detail: "bpftool has no configuration" },
        { key: "active", status: "not_applicable", detail: "bpftool is a command, not a service" },
        {
          key: "verified",
          status: "failed",
          detail: "bpftool: can't get next program: Operation not permitted",
        },
      ],
    }),
  )
  const job = {
    id: "job-7",
    kind: "packages.install",
    title: "Installing bpftool",
    target: "bpftool",
    status: "running",
    exitCode: 0,
    startedAt: new Date().toISOString(),
    lines: 0,
  }
  await page.route("**/api/v1/packages/install", (route) => json(route, job))
  await page.routeWebSocket("**/api/v1/jobs/job-7/stream**", (socket) => {
    installed = true
    socket.send(
      JSON.stringify({
        type: "job",
        data: { ...job, status: "succeeded", endedAt: new Date().toISOString(), lines: 0 },
      }),
    )
  })
  await visit(page, "/network/traffic")
  await page.getByRole("button", { name: "Install bpftool" }).click()
  const phases = page.getByRole("list", { name: "After installing bpftool" }).first()
  await expect(phases).toContainText("Installed: done")
  await expect(phases).toContainText("Verified: failed")
  await expect(
    page.getByText("bpftool is installed; it is not a working service yet").first(),
  ).toBeVisible()
})

test("a peer opens into its tuples, ages, closes and the layers it crosses", async ({ page }) => {
  await open(page, [])
  await visit(page, "/network/connections")
  const row = page.getByRole("row").filter({ hasText: "198.51.100.23" })
  await expect(row).toContainText(
    "TCP/UDP · from 51022, 51023, 51024 +15 · 14 established, 3 time-wait, 1 connected",
  )
  await expect(page.getByLabel("What this read cannot see")).toContainText(
    "Read 10s after the last",
  )
  await expect(page.getByLabel("What this read cannot see")).toContainText(
    "5 connections closed since the last read",
  )
  await expect(page.getByLabel("What this read cannot see")).toContainText(
    "7 unconnected UDP sockets",
  )
  await row.getByRole("button", { name: "Inspect 198.51.100.23" }).click()
  const sheet = page.getByRole("dialog")
  const tuples = sheet.getByLabel("Connections with 198.51.100.23", { exact: true })
  await expect(tuples).toContainText("203.0.113.10:443")
  await expect(tuples).toContainText("51022")
  // The fixture's ages are fixed when it loads, so a long run reads a little older.
  await expect(tuples).toContainText(/≥ 1h \d+m/)
  await expect(tuples).toContainText("2.0 GB")
  await expect(tuples).toContainText("no counters")
  await expect(sheet.getByLabel("Closed connections with 198.51.100.23")).toContainText(
    "open at least 10m",
  )
  const layers = sheet.getByLabel("Across the layers")
  await expect(layers).toContainText("deny in from 198.51.100.0/24 to port 22")
  await expect(layers).toContainText("Answered through ens3 via 203.0.113.1 from 203.0.113.10")
  await expect(layers).toContainText("2 socket rows in the last day")
  await expect(sheet.getByRole("link", { name: "Trace the route" })).toHaveAttribute(
    "href",
    "/network/tools?tool=traceroute&target=198.51.100.23",
  )
})

test("a block asks why and until when, opens an incident, and is listed with its end", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await open(page, mutations)
  await page.route("**/api/v1/network/diagnostics/", (route) => {
    if (route.request().method() !== "POST") {
      return json(route, [{ id: "0123456789abcdef0123456789abcdef", name: "Uplink loss at night" }])
    }
    mutations.push({
      method: "POST",
      path: "/network/diagnostics/",
      body: route.request().postDataJSON(),
    })
    return json(route, { id: "fedcba9876543210fedcba9876543210", name: "Blocked" }, 202)
  })
  await visit(page, "/network/connections")
  const listed = page.getByLabel("Blocks from this page")
  await expect(listed).toContainText("credential stuffing on /login")
  await expect(listed).toContainText(/Ends in 2\dh|Ends in 1d/)
  await expect(listed).toContainText("Ended on schedule")
  await expect(listed.getByRole("link", { name: "incident" })).toHaveAttribute(
    "href",
    `/network/runs?run=${blocks[0].incidentRunId}`,
  )
  // Already blocked: no second block is offered.
  await expect(
    page
      .getByRole("row")
      .filter({ hasText: "203.0.113.77" })
      .getByRole("button", { name: "Block at the firewall" }),
  ).toHaveCount(0)

  await page
    .getByRole("row")
    .filter({ hasText: "192.0.2.145" })
    .getByRole("button", { name: "Block at the firewall" })
    .click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByLabel("Reason")).toHaveValue("connected to SSH on 22")
  await dialog.getByLabel("Reason").fill("")
  await expect(dialog.getByRole("button", { name: "Block 192.0.2.145" })).toBeDisabled()
  await dialog.getByLabel("Reason").fill("ssh brute force from a scanner")
  await dialog.getByRole("radio", { name: "7 days" }).click()
  expect(mutations.filter((m) => m.path === "/firewall/blocks")).toEqual([])
  await dialog.getByRole("button", { name: "Block 192.0.2.145" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/firewall/blocks")?.body)
    .toEqual({
      address: "192.0.2.145",
      reason: "ssh brute force from a scanner",
      durationSeconds: 7 * 86400,
      incidentRunId: "fedcba9876543210fedcba9876543210",
    })
  expect(mutations.find((m) => m.path === "/network/diagnostics/")?.body).toEqual({
    name: "Blocked 192.0.2.145: ssh brute force from a scanner",
    request: { tool: "asn", target: "192.0.2.145" },
  })
  // No permanent rule was written behind the record's back.
  expect(mutations.some((m) => m.path === "/firewall/rules")).toBe(false)

  await listed.getByRole("button", { name: "Lift the block on 203.0.113.77" }).click()
  await page.getByRole("button", { name: "Lift", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.method === "DELETE")?.path)
    .toBe(`/firewall/blocks/${blocks[0].id}`)
})

test("a past hour is read from the socket history in the live table's shape", async ({ page }) => {
  const flows: URL[] = []
  page.on("request", (r) => {
    if (r.url().includes("/network/flows/")) flows.push(new URL(r.url()))
  })
  await open(page, [])
  await visit(page, "/network/connections")
  await page.getByRole("radio", { name: "A past hour" }).click()
  const recorded = page.getByLabel("Recorded connections")
  await expect(recorded.getByRole("row").filter({ hasText: "198.51.100.23" })).toContainText(
    "2.0 GiB",
  )
  await expect(recorded.getByRole("row").filter({ hasText: "203.0.113.200" })).toContainText("sshd")
  await expect(recorded).toContainText(
    "Sockets that opened and closed between two samples are not in the record",
  )
  const asked = flows.find((u) => u.searchParams.get("from") && !u.searchParams.get("address"))!
  expect(asked.searchParams.get("from")).toMatch(/:00:00Z$/)
  await expect(page).toHaveURL(/when=recorded/)
  await recorded.getByRole("button", { name: "Inspect 203.0.113.200" }).click()
  await expect(page.getByRole("dialog")).toBeVisible()
})

test.describe("at a phone's width", () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test("the traffic and connections additions never scroll sideways", async ({
    page,
  }, testInfo) => {
    const overflow = () =>
      page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )
    await open(page, [])
    await visit(page, "/network/traffic")
    await expect(page.getByLabel("Live context")).toContainText("TCP round trip")
    await expect(page.getByLabel("Transfer budgets")).toContainText("over 80%")
    expect(await overflow(), "horizontal overflow on /network/traffic").toBeLessThanOrEqual(0)
    await page.screenshot({ path: testInfo.outputPath("traffic-390.png"), fullPage: true })
    await page.getByRole("button", { name: "Edit the limits on ens3" }).click()
    await expect(page.getByRole("dialog").getByLabel("Upload queue delay")).toBeVisible()
    // Settled after the sheet's entry animation, so the picture is the sheet.
    await page.waitForFunction(() =>
      document
        .getAnimations()
        .filter((a) => a.effect?.getTiming().iterations !== Infinity)
        .every((a) => a.playState !== "running"),
    )
    await page.screenshot({ path: testInfo.outputPath("upload-profile-390.png") })
    await page.keyboard.press("Escape")

    await visit(page, "/network/connections")
    expect(await overflow(), "horizontal overflow on /network/connections").toBeLessThanOrEqual(0)
    await page
      .getByRole("row")
      .filter({ hasText: "198.51.100.23" })
      .getByRole("button", { name: "Inspect 198.51.100.23" })
      .click()
    await expect(page.getByRole("dialog").getByLabel("Across the layers")).toBeVisible()
    // Settled after the sheet's entry animation, so the picture is the sheet.
    await page.waitForFunction(() =>
      document
        .getAnimations()
        .filter((a) => a.effect?.getTiming().iterations !== Infinity)
        .every((a) => a.playState !== "running"),
    )
    await page.screenshot({ path: testInfo.outputPath("peer-sheet-390.png") })
  })
})
