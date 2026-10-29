import { expect, test } from "@playwright/test"
import { json, mockProxy } from "./proxy-fixtures"
import { mockProject } from "./deploy-fixture"
import type { TunnelStatus } from "../../src/lib/proxy/types-tunnel"

/**
 * Cloudflare Tunnel as an ingress (`backend/internal/cftunnel`): connecting
 * one under Proxy › Tunnel — a token read out of the command Cloudflare
 * shows it in, or a tunnel created with the saved API token — the page
 * reading cloudflared's own failure, the routes left to add by hand without
 * credentials, and a deployment domain turned to the tunnel from its row.
 */

const token =
  "eyJhIjoiYWNjMTIzIiwidCI6IjExMTExMTExLTIyMjItNDMzMy04NDQ0LTU1NTU1NTU1NTU1NSIsInMiOiJjMlZqY21WMCJ9"
const tunnelId = "11111111-2222-4333-8444-555555555555"
const at = new Date().toISOString()

const disconnected: TunnelStatus = {
  connected: false,
  connector: { state: "missing", version: "2025.10.0" },
  ready: false,
  readyConnections: 0,
  api: {
    available: false,
    reason:
      "no Cloudflare credentials are saved: save an API token for the Cloudflare DNS provider under Certificates, with Zone › DNS › Edit and Account › Cloudflare Tunnel › Edit",
  },
  origin: {
    service: "http://just-dashboard-ingress:80",
    network: "just-dashboard-tunnel",
    proxy: "the Caddy ingress",
  },
  hostnames: [],
}

const complaint = `Register tunnel error from server side error="Unauthorized: Invalid tunnel secret" connIndex=0`

test("a pasted install command connects the tunnel, and the page reads cloudflared's own failure", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  let status = disconnected
  const sent: unknown[] = []
  await page.route("**/api/v1/proxy/tunnel", async (route) => {
    if (route.request().method() === "PUT") {
      sent.push(route.request().postDataJSON())
      status = {
        ...disconnected,
        connected: true,
        connection: {
          mode: "token",
          tunnelId,
          accountId: "acc123",
          createdBy: "operator",
          createdAt: at,
          updatedAt: at,
        },
        connector: {
          state: "running",
          version: "2025.10.0",
          address: "172.20.0.2",
          startedAt: at,
          origin: "http://just-dashboard-ingress:80",
        },
        checkedAt: at,
        error: complaint,
        hostnames: [
          {
            hostname: "shop.example.com",
            projectId: 7,
            environmentId: 12,
            project: "shop",
            routed: false,
            dns: "manual",
            error: `Without saved Cloudflare credentials the record is yours to create: a proxied CNAME to ${tunnelId}.cfargotunnel.com.`,
          },
        ],
        routes: [
          { hostname: "shop.example.com", service: "http://just-dashboard-ingress:80" },
          { service: "http_status:404" },
        ],
        syncedAt: at,
      }
    }
    await json(route, status)
  })

  await page.goto("/proxy/tunnel")
  await expect(page.getByRole("heading", { name: "Paste a tunnel token" })).toBeVisible()
  // Without credentials, creating one is not offered, and the page says why.
  await expect(page.getByRole("button", { name: "Create tunnel" })).toBeDisabled()
  await expect(page.getByText(/No Cloudflare credentials are saved/).first()).toBeVisible()

  const field = page.getByLabel("Tunnel token")
  await field.fill("not a token")
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await expect(page.getByText(/one long word starting eyJ/)).toBeVisible()
  expect(sent).toHaveLength(0)

  await field.fill(`sudo cloudflared service install ${token}`)
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await expect.poll(() => sent).toEqual([{ token }])

  await expect(page.getByText("No edge connection").first()).toBeVisible()
  await expect(page.getByText(complaint)).toBeVisible()
  await expect(page.getByText("Record to add in Cloudflare")).toBeVisible()
  await expect(page.getByRole("heading", { name: "Routes to add in Cloudflare" })).toBeVisible()
  await expect(
    page.getByText(/shop\.example\.com {2}→ {2}http:\/\/just-dashboard-ingress:80/),
  ).toBeVisible()
  await expect(page.getByText(`${tunnelId}.cfargotunnel.com`).first()).toBeVisible()
  await expect(page.getByText("Published ports")).toBeVisible()
  // The token never comes back to the page.
  await expect(page.locator("body")).not.toContainText(token)
})

test("a tunnel created with the saved API token lists its names and their records, and disconnects", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  let status: TunnelStatus = { ...disconnected, api: { available: true } }
  const calls: string[] = []
  const connected: TunnelStatus = {
    ...status,
    connected: true,
    connection: {
      mode: "api",
      tunnelId,
      tunnelName: "production",
      accountId: "acc123",
      createdAt: at,
      updatedAt: at,
    },
    connector: { state: "running", version: "2025.10.0", address: "172.20.0.2", startedAt: at },
    ready: true,
    readyConnections: 4,
    checkedAt: at,
    hostnames: [
      {
        hostname: "shop.example.com",
        projectId: 7,
        environmentId: 12,
        project: "shop",
        routed: true,
        dns: "created",
        checkedAt: at,
      },
      {
        hostname: "www.example.com",
        projectId: 7,
        environmentId: 12,
        project: "shop",
        routed: true,
        dns: "error",
        error:
          "www.example.com already has a A record to 203.0.113.7, which this dashboard did not make; remove it in Cloudflare to route the name through the tunnel",
        checkedAt: at,
      },
    ],
    routes: [
      { hostname: "shop.example.com", service: "http://just-dashboard-ingress:80" },
      { hostname: "www.example.com", service: "http://just-dashboard-ingress:80" },
      { service: "http_status:404" },
    ],
    syncedAt: at,
  }
  await page.route("**/api/v1/proxy/tunnel", async (route) => {
    calls.push(route.request().method())
    if (route.request().method() === "DELETE")
      status = { ...disconnected, api: { available: true } }
    await json(route, status)
  })
  await page.route("**/api/v1/proxy/tunnel/create", async (route) => {
    calls.push(`create ${JSON.stringify(route.request().postDataJSON())}`)
    status = connected
    await json(route, status)
  })

  await page.goto("/proxy/tunnel")
  await page.getByLabel("Tunnel name").fill("production")
  await page.getByRole("button", { name: "Create tunnel" }).click()
  await expect(page.getByText("Tunnel production created")).toBeVisible()
  expect(calls).toContain(`create {"name":"production"}`)

  await expect(page.getByText("Connected", { exact: true })).toBeVisible()
  await expect(page.getByText("Record created")).toBeVisible()
  await expect(page.getByText("Record refused")).toBeVisible()
  await expect(page.getByText(/already has a A record to 203\.0\.113\.7/)).toBeVisible()
  // With credentials the routes are set here, so none are listed to add by hand.
  await expect(page.getByRole("heading", { name: "Routes to add in Cloudflare" })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "shop", exact: true }).first()).toHaveAttribute(
    "href",
    "/deploy/7/settings/domains",
  )

  await page.getByRole("button", { name: "Disconnect" }).click()
  const dialog = page.getByRole("dialog", { name: "Disconnect the tunnel" })
  await expect(dialog).toContainText("stay at Cloudflare")
  await dialog.getByRole("button", { name: "Disconnect" }).click()
  await expect.poll(() => calls).toContain("DELETE")
  await expect(page.getByRole("heading", { name: "Paste a tunnel token" })).toBeVisible()
})

test("a deployment domain is turned to the tunnel from its row, and a new one checked against it", async ({
  page,
}) => {
  await mockProject(page)
  const puts: Array<{ domains: unknown[] }> = []
  page.on("request", (request) => {
    if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
      puts.push(request.postDataJSON())
    }
  })
  const asked: string[] = []
  await page.route("**/api/v1/deploy/hostname**", (route) => {
    const url = new URL(route.request().url())
    asked.push(url.search)
    const tunnel = url.searchParams.get("tunnel") === "1"
    return json(route, {
      hostname: url.searchParams.get("hostname"),
      covered: true,
      method: tunnel ? "tunnel" : "custom",
      certificateName: tunnel ? "Cloudflare" : "next.example.test",
      detail: tunnel
        ? "Served through the Cloudflare Tunnel production: Cloudflare terminates HTTPS, nothing is issued here, and the name's CNAME to the tunnel is created as the release goes live."
        : "Already covered.",
    })
  })
  await page.goto("/deploy/7/settings/domains")

  const tunnel = page.getByRole("button", { name: "Cloudflare Tunnel on api.example.test" })
  await expect(tunnel).toHaveAttribute("aria-pressed", "false")
  await tunnel.click()
  await expect(tunnel).toHaveAttribute("aria-pressed", "true")
  // HTTPS is Cloudflare's edge's now, and no certificate here is the row's answer.
  await expect(page.getByRole("button", { name: "HTTPS on api.example.test" })).toBeDisabled()
  await expect(
    page.getByText("Through Cloudflare Tunnel · HTTPS at Cloudflare's edge"),
  ).toBeVisible()
  await expect(page.getByText("Certificate valid", { exact: true })).toHaveCount(0)

  await page.getByRole("button", { name: "Add domain", exact: true }).click()
  const sheet = page.getByRole("dialog", { name: "Add domain" })
  await sheet.getByRole("textbox", { name: "Hostname" }).fill("next.example.test")
  await sheet.getByRole("switch", { name: "Serve through Cloudflare Tunnel" }).click()
  await expect(sheet.getByText("Through the tunnel", { exact: true })).toBeVisible()
  await expect(sheet.getByText(/Served through the Cloudflare Tunnel production/)).toBeVisible()
  // No A record to point at this server, and no HTTPS switch to turn off.
  await expect(sheet.getByText("DNS record", { exact: true })).toHaveCount(0)
  await expect(sheet.getByRole("button", { name: "Serve this hostname over HTTPS" })).toHaveCount(0)
  expect(asked.some((search) => search.includes("tunnel=1"))).toBe(true)
  await sheet.getByRole("button", { name: "Add domain", exact: true }).click()

  await expect(page.getByText("Domains saved", { exact: true })).toBeVisible()
  await expect.poll(() => puts.length).toBe(1)
  expect(puts[0].domains).toEqual([
    { hostname: "api.example.test", https: true, ownership: "managed", tunnel: true },
    { hostname: "next.example.test", https: true, ownership: "managed", tunnel: true },
  ])
})
