import { expect, test, type Page, type Request } from "@playwright/test"
import type {
  DiagnosticComparison,
  DiagnosticHistory,
  DiagnosticResult,
  DiagnosticRun,
  WakeDevice,
} from "../../src/lib/network-diagnostics"
import { admin, json, mockNetwork } from "./network-fixture"

type Call = { method: string; path: string; query: URLSearchParams; body: unknown }

const fingerprint = "SHA256:" + "A".repeat(43)

const pingUnknown: DiagnosticResult = {
  tool: "ping",
  target: "192.0.2.9",
  ok: false,
  verdict: "unknown",
  summary:
    "No ICMP echo replies to 4 requests. Reachability is unknown; this does not show the host or its services are down.",
  output: "4 packets transmitted, 0 received, 100% packet loss",
  duration: "12s",
  facts: [
    { label: "Probes", value: "4 ICMP echo requests, 2 s per-reply wait", basis: "configured" },
    { label: "Destination address", value: "192.0.2.9", basis: "observed" },
  ],
  metrics: [{ key: "loss", label: "Packet loss", value: 100, unit: "%" }],
  findings: [
    {
      id: "icmp-filtered",
      level: "notice",
      title: "ICMP echo may be filtered",
      detail: "Many hosts drop ICMP echo while serving TCP normally.",
      owner: "Target firewall or network path",
    },
  ],
  links: [
    { label: "Check a TCP port on this target", href: "/network/tools?tool=port&target=192.0.2.9" },
  ],
  limitations: ["ICMP echo is often rate-limited or filtered."],
  resultId: "r".repeat(32),
}

const savedRun: DiagnosticRun = {
  id: "saved-run",
  name: "Unanswered ping",
  request: { tool: "ping", target: "192.0.2.9" },
  scope: { vantage: "dashboard_host", target: "192.0.2.9", family: "inet", limitations: [] },
  status: "completed",
  outcome: "completed_with_unknowns",
  outcomeSource: "tool_result",
  createdAt: "2026-10-09T12:00:00Z",
  updatedAt: "2026-10-09T12:00:00Z",
  createdBy: "operator",
  stages: [],
  hasResult: true,
  resultTruncated: false,
  result: { ...pingUnknown, resultId: undefined },
}

async function tools(page: Page, answers: Record<string, DiagnosticResult>, session = admin) {
  const calls: Call[] = []
  const devices: WakeDevice[] = [
    {
      id: "d1",
      name: "NAS",
      mac: "02:11:22:33:44:55",
      interface: "eno1",
      verify: "192.168.1.50",
      port: 22,
      createdAt: "2026-10-09T10:00:00Z",
      updatedAt: "2026-10-09T10:00:00Z",
      createdBy: "operator",
    },
  ]
  await mockNetwork(page, [], { session })
  const record = (request: Request) => {
    const url = new URL(request.url())
    calls.push({
      method: request.method(),
      path: url.pathname.replace("/api/v1", ""),
      query: url.searchParams,
      body: request.postData() ? request.postDataJSON() : undefined,
    })
  }
  await page.route("**/api/v1/network/probe", async (route) => {
    record(route.request())
    const body = route.request().postDataJSON() as { tool: string }
    await json(
      route,
      answers[body.tool] ?? {
        tool: body.tool,
        target: "",
        ok: true,
        output: "done",
        duration: "1ms",
      },
    )
  })
  await page.route("**/api/v1/network/diagnostics/**", async (route) => {
    record(route.request())
    const request = route.request()
    const path = new URL(request.url()).pathname.replace("/api/v1/network/diagnostics", "")
    if (path === "/wol-devices" && request.method() === "GET") return json(route, devices)
    if (path === "/wol-devices" && request.method() === "POST")
      return json(route, { ...devices[0], id: "d2", ...request.postDataJSON() }, 201)
    if (path.startsWith("/wol-devices/") && request.method() === "DELETE")
      return route.fulfill({ status: 204 })
    if (path === "/ssh-trust" && request.method() === "PUT") {
      const body = request.postDataJSON()
      return json(route, {
        target: "host.example.test:22",
        keys: body.keys,
        source: body.source,
        savedAt: "2026-10-09T10:00:00Z",
        savedBy: "operator",
      })
    }
    if (path === "/ssh-trust" && request.method() === "DELETE")
      return route.fulfill({ status: 204 })
    if (path === "/results") return json(route, savedRun, 201)
    if (path === "/saved-run") return json(route, savedRun)
    if (path === "/policy") return json(route, { maxRuns: 100, maxAgeHours: 168 })
    return json(route, [])
  })
  await page.route("**/api/v1/network/ipam/preview", async (route) => {
    record(route.request())
    await json(route, {
      prefix: "10.20.30.0/24",
      status: "known_overlap",
      conflicts: [
        { prefix: "10.20.0.0/16", owner: "docker_network", resource: "backend", basis: "observed" },
      ],
      coverage: [],
      checkedAt: "2026-10-09T10:00:00Z",
      limitations: [],
    })
  })
  return calls
}

const panel = (page: Page) => page.locator("[data-slot=security-tools]")
const run = (page: Page) => panel(page).getByRole("button", { name: "Run", exact: true })
const probes = (calls: Call[]) => calls.filter((call) => call.path === "/network/probe")

test("an unanswered ping reads as inconclusive with its evidence, and its next step sends nothing", async ({
  page,
}) => {
  const calls = await tools(page, { ping: pingUnknown })
  await page.goto("/network/tools?tool=ping&target=192.0.2.9")
  await run(page).click()
  const result = panel(page)
  await expect(result.getByText("inconclusive", { exact: true })).toBeVisible()
  await expect(result.getByText(pingUnknown.summary!, { exact: true })).toBeVisible()
  await expect(result.getByText("100%", { exact: true })).toBeVisible()
  await expect(result.getByText("ICMP echo may be filtered", { exact: true })).toBeVisible()
  await expect(result.getByText("configured", { exact: true })).toBeVisible()
  await expect(result.getByText("4 packets transmitted", { exact: false })).toBeHidden()
  await result.getByText("Tool output", { exact: true }).click()
  await expect(result.getByText("4 packets transmitted", { exact: false })).toBeVisible()
  await result.getByRole("link", { name: "Check a TCP port on this target" }).click()
  await expect(page).toHaveURL(/tool=port&target=192\.0\.2\.9/)
  await expect(panel(page).getByRole("textbox", { name: "Target", exact: true })).toHaveValue(
    "192.0.2.9",
  )
  expect(probes(calls)).toHaveLength(1)
})

test("a quick result is saved from the server's hold without running the tool again", async ({
  page,
}) => {
  const calls = await tools(page, { ping: pingUnknown })
  await page.goto("/network/tools?tool=ping&target=192.0.2.9")
  await run(page).click()
  await panel(page).getByRole("button", { name: "Save this result", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Run name", { exact: true }).fill("Unanswered ping")
  await dialog.getByRole("button", { name: "Save result", exact: true }).click()
  await expect(page).toHaveURL(/\/network\/runs\?run=saved-run/)
  expect(calls.find((call) => call.path === "/network/diagnostics/results")?.body).toEqual({
    resultId: pingUnknown.resultId,
    name: "Unanswered ping",
  })
  expect(probes(calls)).toHaveLength(1)
})

test("a route lookup with a port sends its protocol and shows the joined path layers", async ({
  page,
}) => {
  const route: DiagnosticResult = {
    tool: "route",
    target: "192.0.2.1",
    ok: true,
    verdict: "ok",
    summary: "The kernel sends this address out eth0 via 192.0.2.254 from 192.0.2.10.",
    output: "192.0.2.1 via 192.0.2.254 dev eth0 src 192.0.2.10",
    duration: "3ms",
    facts: [{ label: "Firewall prediction (udp/53)", value: "allow", basis: "inferred" }],
    tables: [
      {
        id: "path",
        title: "Path layers",
        columns: ["Layer", "Basis", "State", "Summary", "Owner"],
        rows: [
          ["Policy rules", "observed", "observed", "Ordered rules", "Linux kernel"],
          ["Firewall", "modeled", "modeled", "OUTPUT allows udp/53", "ufw"],
        ],
        rowLinks: ["/network/routing", "/network/firewall"],
      },
    ],
  }
  const calls = await tools(page, { route })
  await page.goto("/network/tools?tool=route")
  await panel(page).getByRole("textbox", { name: "Target", exact: true }).fill("192.0.2.1")
  await panel(page).getByRole("textbox", { name: "Port", exact: true }).fill("53")
  await panel(page).getByRole("combobox", { name: "Protocol", exact: true }).click()
  await page.getByRole("option", { name: "UDP", exact: true }).click()
  await run(page).click()
  await expect(panel(page).getByRole("region", { name: "Path layers" })).toBeVisible()
  await expect(panel(page).getByRole("link", { name: "Open Firewall modeled" })).toHaveAttribute(
    "href",
    "/network/firewall",
  )
  await expect(panel(page).getByText("inferred", { exact: true })).toBeVisible()
  expect(probes(calls).at(-1)?.body).toEqual({
    tool: "route",
    target: "192.0.2.1",
    port: 53,
    option: "udp",
  })
})

test("listeners link each socket to Ports and hop tables render for traceroute", async ({
  page,
}) => {
  const listeners: DiagnosticResult = {
    tool: "listeners",
    target: "this host",
    ok: true,
    verdict: "ok",
    summary: "1 TCP and 0 UDP sockets; 1 are bound beyond loopback.",
    output: 'tcp LISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=1001,fd=3))',
    duration: "5ms",
    tables: [
      {
        id: "listeners",
        title: "Listening sockets",
        columns: ["Protocol", "Address", "Port", "Process", "Bound to"],
        rows: [["tcp", "0.0.0.0", "22", "sshd (1001)", "all addresses"]],
        rowLinks: ["/proxy/ports?q=%3A22&socket=tcp-ipv4-0.0.0.0-22"],
      },
    ],
    links: [{ label: "Ports: ownership and exposure", href: "/proxy/ports" }],
  }
  const traceroute: DiagnosticResult = {
    tool: "traceroute",
    target: "example.com",
    ok: true,
    verdict: "ok",
    summary: "Reached the destination in 3 hops; 2 of 3 hops answered.",
    output: "traceroute to example.com",
    duration: "4s",
    records: ["hop 1 192.168.1.1", "hop 2 no reply", "hop 3 93.184.216.34"],
    tables: [
      {
        id: "hops",
        title: "Hops",
        columns: ["Hop", "Address", "Round trip (ms)", "Note"],
        rows: [
          ["1", "192.168.1.1", "0.5", ""],
          ["2", "no reply", "", ""],
          ["3", "93.184.216.34", "11", "destination"],
        ],
      },
    ],
  }
  await tools(page, { listeners, traceroute })
  await page.goto("/network/tools?tool=listeners")
  await run(page).click()
  await expect(panel(page).getByRole("link", { name: "Open tcp 0.0.0.0" })).toHaveAttribute(
    "href",
    "/proxy/ports?q=%3A22&socket=tcp-ipv4-0.0.0.0-22",
  )
  await page.goto("/network/tools?tool=traceroute&target=example.com")
  await run(page).click()
  const hops = panel(page).getByRole("region", { name: "Hops" })
  await expect(hops.getByRole("cell", { name: "no reply", exact: true })).toBeVisible()
  await expect(hops.getByRole("row")).toHaveCount(4)
})

test("SSH host keys are trusted from a scan or entered by hand and forgotten with confirmation", async ({
  page,
}) => {
  const ssh: DiagnosticResult = {
    tool: "ssh",
    target: "host.example.test:22",
    ok: true,
    verdict: "unknown",
    summary:
      "Read 1 host key(s). None is saved as trusted for this host and port, so this does not authenticate the server.",
    output: `ssh-ed25519  ${fingerprint}`,
    duration: "1s",
    records: [`ssh-ed25519 ${fingerprint}`],
    facts: [{ label: "Saved trust", value: "none for host.example.test:22", basis: "unknown" }],
  }
  const calls = await tools(page, { ssh })
  await page.goto("/network/tools?tool=ssh&target=host.example.test")
  await run(page).click()
  await panel(page)
    .getByRole("button", { name: "Trust the keys this scan read", exact: true })
    .click()
  await expect(
    panel(page).getByText(/Saved 1 fingerprint for host\.example\.test:22/),
  ).toBeVisible()
  expect(calls.find((call) => call.path === "/network/diagnostics/ssh-trust")?.body).toEqual({
    target: "host.example.test",
    port: 22,
    source: "observed",
    keys: [{ type: "ssh-ed25519", fingerprint }],
  })
  await panel(page).getByLabel("Fingerprint", { exact: true }).fill("MD5:aa:bb")
  await expect(panel(page).getByText(/Paste the SHA256 fingerprint/)).toBeVisible()
  await expect(
    panel(page).getByRole("button", { name: "Save fingerprint", exact: true }),
  ).toBeDisabled()
  await panel(page).getByLabel("Fingerprint", { exact: true }).fill(fingerprint)
  await panel(page).getByRole("button", { name: "Save fingerprint", exact: true }).click()
  await expect
    .poll(() => calls.filter((call) => call.path === "/network/diagnostics/ssh-trust").at(-1)?.body)
    .toMatchObject({ source: "entered", keys: [{ type: "ssh-ed25519", fingerprint }] })

  ssh.facts = [
    { label: "Saved trust", value: "1 fingerprint(s) entered by hand", basis: "configured" },
  ]
  ssh.verdict = "ok"
  await run(page).click()
  await panel(page).getByRole("button", { name: "Forget saved fingerprints", exact: true }).click()
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Forget fingerprints", exact: true })
    .click()
  await expect
    .poll(() => calls.find((call) => call.method === "DELETE")?.query.get("target"))
    .toBe("host.example.test:22")
  expect(probes(calls)).toHaveLength(2)
})

test("saved Wake-on-LAN devices fill the inputs and verification is measured on request", async ({
  page,
}) => {
  const wol: DiagnosticResult = {
    tool: "wol",
    target: "02:11:22:33:44:55 on eno1",
    ok: true,
    verdict: "ok",
    summary: "192.168.1.50 answered 23s after the packet (TCP 22 connected).",
    output: "Sent one magic packet on eno1.",
    duration: "23s",
    stages: [
      { id: "precheck", label: "Before sending", status: "passed", detail: "not answering" },
      { id: "send", label: "Magic packet", status: "passed", detail: "one frame on eno1" },
      {
        id: "verify",
        label: "Wake verification",
        status: "passed",
        detail: "TCP 22 connected after 23s",
      },
    ],
    metrics: [{ key: "wake_seconds", label: "Answered after", value: 23, unit: "s" }],
  }
  const calls = await tools(page, { wol })
  await page.goto("/network/tools?tool=wol")
  const devices = panel(page).getByRole("region", { name: "Saved devices" })
  await devices.getByRole("button", { name: "Use NAS", exact: true }).click()
  await expect(panel(page).getByRole("textbox", { name: "MAC address", exact: true })).toHaveValue(
    "02:11:22:33:44:55",
  )
  await panel(page).getByLabel("Verify address", { exact: true }).fill("nas.local")
  await expect(
    panel(page).getByText("Use the device's IP address, not a name.", { exact: true }),
  ).toBeVisible()
  await expect(run(page)).toBeDisabled()
  await panel(page).getByLabel("Verify address", { exact: true }).fill("192.168.1.50")
  await run(page).click()
  await expect(
    panel(page).getByRole("region", { name: "Checks" }).getByText("Wake verification"),
  ).toBeVisible()
  await expect(panel(page).getByText("23 s", { exact: true })).toBeVisible()
  expect(probes(calls).at(-1)?.body).toEqual({
    tool: "wol",
    target: "02:11:22:33:44:55",
    option: "eno1",
    verify: "192.168.1.50",
    port: 22,
  })
  await devices.getByLabel("Device name", { exact: true }).fill("Printer")
  await devices.getByRole("button", { name: "Save as device", exact: true }).click()
  await expect
    .poll(
      () =>
        calls.find(
          (call) => call.method === "POST" && call.path === "/network/diagnostics/wol-devices",
        )?.body,
    )
    .toEqual({
      name: "Printer",
      mac: "02:11:22:33:44:55",
      interface: "eno1",
      verify: "192.168.1.50",
      port: 22,
    })
  await devices.getByRole("button", { name: "Delete", exact: true }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Delete device", exact: true }).click()
  await expect
    .poll(() => calls.find((call) => call.method === "DELETE")?.path)
    .toBe("/network/diagnostics/wol-devices/d1")
  expect(probes(calls)).toHaveLength(1)
})

test("a packet snapshot hands its settings to retained capture setup without capturing", async ({
  page,
}) => {
  const capture: DiagnosticResult = {
    tool: "capture",
    target: "eno1",
    ok: true,
    verdict: "ok",
    summary: "2 packet summaries on eno1 (udp).",
    output: "IP 192.0.2.1.53 > 192.0.2.2.4321: UDP",
    duration: "15s",
    links: [
      {
        label: "Capture a retained PCAP with these settings",
        href: "/network/captures?interface=eno1&protocol=udp",
      },
    ],
  }
  const posts: string[] = []
  await tools(page, { capture })
  await page.route("**/api/v1/network/captures/", (route) =>
    json(route, {
      runs: [],
      maxRunning: 2,
      maxRetained: 32,
      retentionHours: 24,
      maxArtifactBytes: 2097152,
    }),
  )
  await page.route("**/api/v1/network/captures/interfaces", (route) =>
    json(route, [{ name: "eno1", index: 2, up: true }]),
  )
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().includes("/network/captures"))
      posts.push(request.url())
  })
  await page.goto("/network/tools?tool=capture&target=eno1")
  await run(page).click()
  await panel(page)
    .getByRole("link", { name: "Capture a retained PCAP with these settings" })
    .click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole("combobox").first()).toContainText("eno1")
  await expect(dialog.getByLabel(/name/i).first()).toHaveValue("Snapshot follow-up · eno1")
  expect(posts).toEqual([])
})

test("the subnet calculator compares prefixes locally and checks shared IPAM for admins", async ({
  page,
}) => {
  const calls = await tools(page, {})
  await page.goto("/network/tools?tool=subnet")
  await page.getByLabel("Address with a prefix", { exact: true }).fill("10.20.30.40/24")
  await panel(page).getByRole("button", { name: "Run", exact: true }).click()
  await page.getByLabel("Compare with another prefix", { exact: true }).fill("10.0.0.0/8")
  await expect(
    page.getByText("10.20.30.0/24 sits inside 10.0.0.0/8; they overlap.", { exact: true }),
  ).toBeVisible()
  await page.getByRole("button", { name: "Check shared IPAM", exact: true }).click()
  await expect(page.getByText("Known overlap", { exact: false })).toBeVisible()
  await expect(page.getByText(/10\.20\.0\.0\/16 · docker_network · backend/)).toBeVisible()
  expect(calls.find((call) => call.path === "/network/ipam/preview")?.body).toEqual({
    prefix: "10.20.30.0/24",
  })
})

test("readers keep the local calculator and comparison but never query IPAM", async ({ page }) => {
  const reader = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
  const calls = await tools(page, {}, reader)
  await page.goto("/network/tools")
  await page.getByLabel("Address with a prefix", { exact: true }).fill("2001:db8::1/48")
  await panel(page).getByRole("button", { name: "Run", exact: true }).click()
  await page.getByLabel("Compare with another prefix", { exact: true }).fill("2001:db9::/48")
  await expect(
    page.getByText("2001:db8::/48 and 2001:db9::/48 do not overlap.", { exact: true }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Check shared IPAM" })).toHaveCount(0)
  await expect(
    page.getByText(/needs the admin capability; the comparison above stays in this browser/),
  ).toBeVisible()
  expect(calls).toEqual([])
})

test("a saved run shows its structured evidence, metric history and measurement changes", async ({
  page,
}) => {
  const now = "2026-10-09T12:00:00Z"
  const base = (id: string, loss: number): DiagnosticRun => ({
    id,
    name: "Ping watch",
    request: { tool: "ping", target: "192.0.2.9" },
    scope: {
      vantage: "dashboard_host",
      target: "192.0.2.9",
      family: "inet",
      protocol: "tool_selected",
      limitations: [],
    },
    status: "completed",
    outcome: loss ? "completed_with_findings" : "completed",
    outcomeSource: "tool_result",
    createdAt: id === "a" ? "2026-10-09T10:00:00Z" : now,
    updatedAt: now,
    createdBy: "operator",
    stages: [],
    hasResult: true,
    resultTruncated: false,
    result: {
      tool: "ping",
      target: "192.0.2.9",
      ok: true,
      verdict: loss ? "findings" : "ok",
      summary: `${4 - loss / 25} of 4 replies.`,
      output: "ping output",
      duration: "3s",
      metrics: [{ key: "loss", label: "Packet loss", value: loss, unit: "%" }],
    },
  })
  const runs = [base("b", 25), base("a", 0)]
  const history: DiagnosticHistory = {
    request: runs[0].request,
    points: [...runs].reverse().map((r) => ({
      id: r.id,
      name: r.name,
      createdAt: r.createdAt,
      status: r.status,
      outcome: r.outcome,
      verdict: r.result!.verdict,
      metrics: r.result!.metrics!,
    })),
    limitations: [
      "Only runs still inside the retention policy are listed; older runs were pruned.",
    ],
  }
  await mockNetwork(page)
  await page.route("**/api/v1/network/diagnostics/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1/network/diagnostics", "")
    if (path === "/") return json(route, runs)
    if (path === "/policy") return json(route, { maxRuns: 100, maxAgeHours: 168 })
    if (path === "/b/history") return json(route, history)
    if (path === "/compare") {
      const comparison: DiagnosticComparison = {
        beforeId: "a",
        afterId: "b",
        request: runs[0].request,
        status: { before: "completed", after: "completed" },
        outcome: { before: "completed", after: "completed_with_findings" },
        duration: { before: "3s", after: "3s" },
        records: {
          added: ["verdict: findings"],
          removed: ["verdict: ok"],
          unchanged: 1,
          truncated: false,
        },
        output: { added: [], removed: [], unchanged: 1, truncated: false },
        metrics: [{ key: "loss", label: "Packet loss", unit: "%", before: 0, after: 25 }],
        partial: false,
        limitations: [],
      }
      return json(route, comparison)
    }
    const id = path.slice(1)
    return json(route, runs.find((r) => r.id === id) ?? runs[0])
  })
  await page.goto("/network/runs?run=b")
  const evidence = page.getByLabel("Structured evidence")
  await expect(evidence.getByText("3 of 4 replies.", { exact: true })).toBeVisible()
  await expect(evidence.getByText("answered with findings", { exact: true })).toBeVisible()
  const historyPanel = page.getByLabel("Run history")
  await expect(historyPanel.getByRole("columnheader", { name: "Packet loss" })).toBeVisible()
  await expect(historyPanel.getByRole("cell", { name: "0%", exact: true })).toBeVisible()
  await expect(historyPanel.getByRole("cell", { name: "25%", exact: true })).toBeVisible()
  await page.getByRole("combobox", { name: "Baseline run", exact: true }).click()
  await page.getByRole("option").first().click()
  await page.getByRole("button", { name: "Compare", exact: true }).click()
  await expect(
    page.getByLabel("Measurement changes").getByText("0% → 25%", { exact: true }),
  ).toBeVisible()
})

for (const width of [375, 1280]) {
  test(`structured results stay inside the page at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    const wide: DiagnosticResult = {
      ...pingUnknown,
      tables: [
        {
          id: "chain",
          title: "Presented certificate chain",
          columns: [
            "Position",
            "Subject",
            "Issuer",
            "Valid from",
            "Valid until",
            "Key",
            "Signature",
            "SHA-256",
          ],
          rows: [
            [
              "leaf",
              "a-very-long-subject-name.example.test",
              "Example Issuing CA",
              "2026-01-01",
              "2027-01-01",
              "ECDSA P-256",
              "ECDSA-SHA256",
              "f".repeat(64),
            ],
          ],
        },
      ],
    }
    await tools(page, { ping: wide })
    await page.goto("/network/tools?tool=ping&target=192.0.2.9")
    await run(page).click()
    await expect(
      panel(page).getByRole("region", { name: "Presented certificate chain" }),
    ).toBeVisible()
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow).toBeLessThanOrEqual(0)
  })
}
