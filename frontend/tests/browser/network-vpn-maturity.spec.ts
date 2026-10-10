import { expect, test } from "@playwright/test"
import { json, vpn, type Mutation } from "./network-fixture"
import {
  base,
  maturityVPN,
  now,
  openMaturity as open,
  peerNamed,
} from "./network-vpn-maturity-fixture"

// The WireGuard record, peer lifecycle, site checks, archive and the tailnet's
// approval on the VPN page, against recorded API shapes. Every mutation is
// captured so a test reads back exactly what a control sent.

test("stale handshakes, captured transports and passed budgets are called out on the tunnel", async ({
  page,
}) => {
  await open(page, [])
  await page.goto("/network/vpn")
  const alerts = page.getByLabel("wg0 alerts", { exact: true })
  await expect(alerts).toContainText("Office keeps its session alive")
  await expect(alerts).toContainText("goes into the WireGuard device wg0")
  await expect(alerts).toContainText("does not disconnect")
  await expect(page.getByText("3 things on wg0 need a look")).toBeVisible()
})

test("a peer's record shows its trend, where it dialled from and its history", async ({ page }) => {
  const calls: Mutation[] = []
  await open(page, calls)
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: "Open Office" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("Handshake stale")).toBeVisible()
  await expect(sheet.getByText("Office · 24h")).toBeVisible()
  const seen = sheet.getByLabel("Where Office was seen from")
  await expect(seen).toContainText("198.51.100.50:51820")
  await expect(seen).toContainText("203.0.113.77:40001")
  const events = sheet.getByLabel("Office history")
  await expect(events).toContainText("Handshake went stale")
  await expect(events).toContainText("Peer not added")
  await sheet.getByRole("radio", { name: "7d" }).click()
  await expect.poll(() => calls.some((c) => c.path.includes("window=7d"))).toBe(true)
  expect(calls.every((c) => !c.path.startsWith("history") || c.path.includes("peer="))).toBe(true)
})

test("a budget is set per period, read as an alert and cleared after confirmation", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await open(page, mutations)
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: "Open Office" }).click()
  const sheet = page.getByRole("dialog")
  const budget = sheet.getByLabel("Office budget", { exact: true })
  await expect(budget).toContainText("3.0 GB of 2.0 GB")
  await expect(budget).toContainText("passed")
  await expect(budget).toContainText("An alert, not a limit")
  await sheet.getByRole("radio", { name: "week" }).click()
  await sheet.getByLabel("New budget", { exact: true }).fill("10 KB")
  await expect(sheet.getByRole("button", { name: "Set budget" })).toBeDisabled()
  await sheet.getByLabel("New budget", { exact: true }).fill("50 GiB")
  await sheet.getByRole("button", { name: "Set budget" }).click()
  await expect
    .poll(() => mutations.find((m) => m.method === "PUT")?.body)
    .toEqual({ period: "week", limitBytes: 50 * 2 ** 30 })
  await sheet.getByRole("button", { name: "Clear the budget" }).click()
  expect(mutations.some((m) => m.method === "DELETE")).toBe(false)
  await page.getByRole("button", { name: "Clear", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.method === "DELETE")?.path)
    .toBe("/network/vpn/wireguard/wg0/peers/4/quota")
})

test("editing a device's routes regenerates its configuration with the same keys", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await open(page, mutations)
  await page.route("**/api/v1/network/vpn/wireguard/wg0/peers/3", (route) => {
    mutations.push({
      method: route.request().method(),
      path: "edit",
      body: route.request().postDataJSON(),
    })
    return json(route, {
      peer: { ...peerNamed("Tablet"), clientRoutes: ["10.8.0.0/24"] },
      clientChanged: true,
      config: "[Interface]\nPrivateKey = redacted\n\n[Peer]\nAllowedIPs = 10.8.0.0/24\n",
      qr: "data:image/png;base64,iVBORw0KGgo=",
      reloaded: false,
      warnings: ["Tablet keeps its old configuration until the new one is imported on it."],
    })
  })
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: "Open Tablet" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Edit", exact: true }).click()
  const modal = page.getByRole("dialog", { name: "Edit Tablet" })
  await expect(modal.getByLabel("Networks it may reach here")).toHaveValue("172.17.0.0/16")
  await modal.getByLabel("Networks it may reach here").fill("172.17.0.0/16, 10.0.4.0/33")
  await expect(modal.getByRole("button", { name: "Save changes" })).toBeDisabled()
  await modal.getByLabel("Networks it may reach here").fill("")
  await modal.getByRole("button", { name: "Save changes" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "edit")?.body)
    .toEqual({ shareNetworks: [] })
  await expect(page.getByRole("img", { name: /QR code for Tablet/ })).toBeVisible()
  await expect(page.getByText("Same keys, new routes")).toBeVisible()
})

test("taking a network away from a site is confirmed before the edit is sent", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, mutations)
  await page.route("**/api/v1/network/vpn/wireguard/wg0/peers/4", (route) => {
    mutations.push({
      method: route.request().method(),
      path: "edit",
      body: route.request().postDataJSON(),
    })
    return json(route, {
      peer: peerNamed("Office"),
      clientChanged: false,
      reloaded: true,
      warnings: [],
    })
  })
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: "Open Office" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Edit", exact: true }).click()
  const modal = page.getByRole("dialog", { name: "Edit Office" })
  await expect(modal.getByLabel("Its networks")).toHaveValue("192.168.1.0/24")
  await modal.getByLabel("Its networks").fill("192.168.2.0/24")
  await modal.getByRole("button", { name: "Save changes" }).click()
  await expect(
    page.getByRole("alertdialog").or(page.getByRole("dialog", { name: "Withdraw from Office" })),
  ).toBeVisible()
  expect(mutations.some((m) => m.path === "edit")).toBe(false)
  await page.getByRole("button", { name: "Change", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "edit")?.body)
    .toEqual({ remoteNetworks: ["192.168.2.0/24"] })
})

test("a site is verified end to end with the steps for its own end", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, mutations)
  await page.route("**/api/v1/network/vpn/wireguard/wg0/peers/4/verify", (route) => {
    mutations.push({ method: "POST", path: "verify", body: route.request().postDataJSON() })
    return json(route, {
      peer: peerNamed("Office").publicKey,
      peerName: "Office",
      at: now(),
      outcome: "partial",
      checks: [
        { name: "handshake", status: "pass", detail: "handshake 12s ago" },
        { name: "route 192.168.1.0/24", status: "pass", detail: "routed into wg0" },
        { name: "transport", status: "pass", detail: "198.51.100.50:51820 leaves through eth0" },
        {
          name: "tunnel address",
          status: "pass",
          detail: "10.8.0.10 answered 10.8.0.1 through the tunnel",
        },
        {
          name: "target 192.168.1.20",
          status: "warn",
          detail: "no answer from 192.168.1.20 sourced from 10.8.0.1; ICMP may be filtered there",
        },
      ],
      remoteSteps: ["ping -c 3 10.8.0.1 — this server's tunnel address answers through the tunnel"],
    })
  })
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: "Open Office" }).click()
  const sheet = page.getByRole("dialog")
  await sheet.getByLabel("A host on its network").fill("192.168.1.20")
  await sheet.getByRole("button", { name: "Verify", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "verify")?.body)
    .toEqual({ target: "192.168.1.20" })
  const result = sheet.getByLabel("Office verification")
  await expect(result).toContainText("Partly verified")
  await expect(result).toContainText("ICMP may be filtered there")
  await expect(result).toContainText("ping -c 3 10.8.0.1")
})

test("forgetting a saved copy says it does not revoke the peer", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, mutations)
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: "Open Tablet" }).click()
  await expect(
    page.getByText("Forgetting keeps Tablet connected; removing it revokes its key."),
  ).toBeVisible()
  await page.getByRole("dialog").getByRole("button", { name: "Forget the saved copy" }).click()
  await expect(page.getByText("This does not revoke Tablet")).toBeVisible()
  expect(mutations.filter((m) => m.method !== "GET")).toEqual([])
  await page.getByRole("button", { name: "Forget", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.method === "DELETE")?.path)
    .toBe("/network/vpn/wireguard/wg0/peers/3/config")
})

test("a full-tunnel device offers its Linux kill-switch file as a download", async ({ page }) => {
  const calls: string[] = []
  await open(page, [])
  await page.route("**/api/v1/network/vpn/wireguard/wg0/peers/2/config?*", (route) => {
    calls.push(new URL(route.request().url()).search)
    return json(route, {
      name: "Work laptop",
      config:
        "[Interface]\nPostUp = nft add table inet jd_killswitch\nPreDown = nft delete table inet jd_killswitch\n",
    })
  })
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: "Open Tablet" }).click()
  await expect(page.getByRole("button", { name: "Save Linux kill-switch file" })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Open Work laptop" }).click()
  const download = page.waitForEvent("download")
  await page.getByRole("button", { name: "Save Linux kill-switch file" }).click()
  expect((await download).suggestedFilename()).toBe("Work-laptop-killswitch.conf")
  expect(calls).toEqual(["?variant=linux-killswitch"])
})

test("exits show how many client connections they translated", async ({ page }) => {
  await open(page, [])
  await page.goto("/network/vpn")
  await expect(page.getByLabel("wg0 address family evidence", { exact: true })).toContainText(
    "1,234 client connections translated",
  )
})

test("the tunnel history checks its endpoint and lists its lifecycle with outcomes", async ({
  page,
}) => {
  await open(page, [], {
    "/network/vpn/wireguard/wg0/endpoint": {
      endpoint: "vpn.example.org:51820",
      host: "vpn.example.org",
      port: 51820,
      kind: "hostname",
      resolution: "resolved",
      addresses: [{ address: "198.51.100.99", public: true, onHost: false }],
      uplink: "eth0",
      uplinkPublic: true,
      listening: true,
      verdict: "elsewhere",
      explanation:
        "The endpoint is 198.51.100.99, which this host does not hold, while eth0 has 203.0.113.20; clients may be dialling another machine.",
      reachability: "not_tested",
      checkedAt: now(),
    },
  })
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: "History", exact: true }).click()
  const panel = page.getByRole("dialog", { name: "wg0 history" })
  await panel.getByRole("button", { name: "Check endpoint" }).click()
  const evidence = panel.getByLabel("Endpoint evidence")
  await expect(evidence).toContainText("Not an address of this host")
  await expect(evidence).toContainText("clients may be dialling another machine")
  await expect(evidence).toContainText("not held here")
  const lifecycle = panel.getByLabel("wg0 lifecycle")
  await expect(lifecycle).toContainText("Created")
  await expect(lifecycle).toContainText("rolled back")
  await expect(lifecycle).toContainText("failed")
})

test("an archived tunnel is restored after confirmation and a refused one says why", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await open(page, mutations, {
    "/network/vpn/archive": [
      {
        file: "wg1.conf.1790000000",
        name: "wg1",
        archivedAt: now() - 86400,
        listenPort: 51821,
        addresses: ["10.40.0.1/24"],
        endpoint: "203.0.113.20:51821",
        peers: 2,
        restorable: true,
      },
      {
        file: "wg0.conf.1780000000",
        name: "wg0",
        archivedAt: now() - 86400 * 40,
        listenPort: 51820,
        addresses: ["10.8.0.1/24"],
        endpoint: "203.0.113.20:51820",
        peers: 1,
        restorable: false,
        refusal: "a tunnel called wg0 exists again; remove or rename it first",
      },
    ],
  })
  await page.route("**/api/v1/network/vpn/archive/*/restore", (route) => {
    mutations.push({ method: "POST", path: new URL(route.request().url()).pathname, body: null })
    return json(route, {
      interface: { ...base, name: "wg1" },
      warnings: [
        "Its IPv4 exit was withdrawn when it was removed and is not restored; turn it on again if clients need it.",
      ],
      firewall: { opened: true },
    })
  })
  await page.goto("/network/vpn")
  const archive = page.getByLabel("Archived tunnels", { exact: true })
  await expect(archive).toContainText("udp 51821 · 10.40.0.1/24 · 2 peers")
  await expect(archive).toContainText("exists again")
  await expect(page.getByRole("button", { name: "Restore wg0" })).toBeDisabled()
  await page.getByRole("button", { name: "Restore wg1" }).click()
  expect(mutations.some((m) => m.path.endsWith("/restore"))).toBe(false)
  await page.getByRole("button", { name: "Restore", exact: true }).click()
  await expect
    .poll(() => mutations.find((m) => m.path.endsWith("/restore"))?.path)
    .toBe("/api/v1/network/vpn/archive/wg1.conf.1790000000/restore")
})

test("setup's advanced settings send the MTU and own resolvers it checked", async ({ page }) => {
  const mutations: Mutation[] = []
  await open(page, mutations, {
    "/network/vpn": { ...maturityVPN(), wireguard: { ...vpn.wireguard, interfaces: [] } },
  })
  await page.route("**/api/v1/network/vpn/wireguard", (route) => {
    mutations.push({ method: "POST", path: "create", body: route.request().postDataJSON() })
    return json(route, {
      interface: base,
      warnings: [],
      firewall: { opened: true },
      endpointEvidence: {
        verdict: "on_host_public",
        explanation: "Clients dial a public address this host holds.",
      },
    })
  })
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: /Advanced/ }).click()
  const submit = page.getByRole("button", { name: "Set up WireGuard", exact: true })
  await page.getByLabel("MTU", { exact: true }).fill("900")
  await expect(submit).toBeDisabled()
  await page.getByLabel("MTU", { exact: true }).fill("1380")
  await page.getByLabel("Own resolvers").fill("dns.example")
  await expect(submit).toBeDisabled()
  await page.getByLabel("Own resolvers").fill("10.0.4.53, fd00::53")
  await submit.click()
  await expect
    .poll(() => mutations.find((m) => m.path === "create")?.body)
    .toMatchObject({
      mtu: 1380,
      dns: ["10.0.4.53", "fd00::53"],
    })
})

test("the tailnet says which offers it serves, when this node's key expires and what it reports", async ({
  page,
}) => {
  await open(page, [])
  await page.goto("/network/vpn")
  await expect(
    page.getByText("Awaiting approval on the control server", { exact: true }),
  ).toBeVisible()
  await expect(page.getByText("served", { exact: true })).toBeVisible()
  await expect(page.getByText("not served", { exact: true })).toBeVisible()
  await expect(page.getByText(/^key expires/)).toBeVisible()
  await expect(page.getByText(/This server's Tailscale key expires on/)).toBeVisible()
  await expect(page.getByLabel("Tailscale health")).toContainText("coordination server")
})

test("a Headscale in a container shows its users, expired keys and routes awaiting approval", async ({
  page,
}) => {
  await open(page, [])
  await page.goto("/network/vpn")
  await expect(page.getByText("container headscale · 1 node · 1 user")).toBeVisible()
  await expect(page.getByLabel("Headscale users")).toContainText("bob (1)")
  const nodes = page.getByLabel("Headscale nodes")
  await expect(nodes).toContainText("bob-router")
  await expect(nodes).toContainText("Key expired")
  await expect(nodes).toContainText("routes 192.168.20.0/24")
  await expect(nodes).toContainText("awaiting approval 192.168.30.0/24")
})
