import type { Page } from "@playwright/test"
import type {
  DNSConnection,
  DNSServiceChange,
  DNSServiceProvision,
  DNSServiceSnapshot,
  DNSServiceView,
} from "../../src/lib/network-dns-services"
import { admin, mockNetwork, type Mutation } from "./network-fixture"
import { overrides } from "./network-dns-fixture"

const base = "/network/dns/services"
const time = () => new Date().toISOString()
const reading = (state: string, summary: string) => ({
  state,
  basis: "native fixture inventory",
  summary,
})

export function dnsSnapshot(engine: DNSConnection["engine"] = "adguard"): DNSServiceSnapshot {
  return {
    engine,
    version: "fixture-native-version",
    policyFingerprint: "fixture-policy-1",
    observedAt: time(),
    roles: ["recursive", "authoritative"],
    transport: reading(
      "configured",
      "Verified HTTPS management identity; client DNS transport remains unmeasured.",
    ),
    runtime: reading("unknown", "Foreign process identity is unavailable."),
    listeners: [
      { address: "::1", port: 53, protocol: "UDP/TCP", scope: "configured listener scope" },
    ],
    protection: true,
    protectionTemporary: false,
    upstreams: ["192.0.2.53:53"],
    upstreamProtocol: "Udp",
    access: reading(
      "partial",
      "Named client policy is configured; packet enforcement remains unmeasured.",
    ),
    allowedClients: ["192.0.2.0/24"],
    deniedClients: ["198.51.100.0/24"],
    clients: [
      {
        id: "client-1",
        name: "Office client",
        addresses: ["192.0.2.10"],
        groups: [7],
        filtering: false,
        inherited: false,
      },
    ],
    clientEvidence: reading(
      "configured",
      "Native client-specific policy is separate from inherited defaults.",
    ),
    zones: [
      { name: "authority.example.test", type: "Primary", disabled: false, dnssec: "unsigned" },
    ],
    zoneEvidence: reading("configured", "Authoritative zones are separate from local overrides."),
    localOverrides: [{ name: "printer.home.test", value: "192.0.2.90", type: "A" }],
    overrideEvidence: reading("configured", "A local override is not an authoritative zone."),
    views: reading(
      "configured",
      "Configured native views; packet-level selection remains unknown.",
    ),
    viewGroups: [
      {
        name: "Office view",
        enabled: true,
        clientScopes: ["192.0.2.0/24"],
        listenerScopes: ["::1"],
        domains: ["view.example.test"],
        translations: { "inside.example.test": "outside.example.test" },
      },
    ],
    namedNetworks: { Office: ["192.0.2.0/24"] },
    translationEnabled: false,
    filterGroups: [
      {
        id: 7,
        name: "No filtering group",
        enabled: false,
        clientScopes: ["192.0.2.10"],
        listenerScopes: [],
        domains: ["filter.example.test"],
      },
    ],
    appProtection: false,
    appClientEvidence: reading(
      "unsupported",
      "Installed-app client matching is not readable on this owner.",
    ),
    queries: [
      {
        at: "native-time-reading",
        client: "192.0.2.10",
        name: "query.example.test",
        type: "A",
        status: "allowed",
        protocol: "UDP",
      },
    ],
    queryEvidence: reading(
      "partial",
      "Native history is bounded; this is not an independent client test.",
    ),
    limitations: ["Configured native views are not measured client reachability."],
  }
}

export function dnsConnection(management = true): DNSConnection {
  return {
    id: "dns-fixture",
    name: "Fixture DNS",
    engine: "adguard",
    endpoint: "https://192.0.2.10:8443",
    serverName: "dns.example.test",
    customCA: true,
    management,
    hasCredential: true,
    generation: 1,
    ownership: "connected",
    createdAt: time(),
    updatedAt: time(),
  }
}

export function dnsChange(connection = dnsConnection()): DNSServiceChange {
  return {
    id: "change-fixture",
    connectionId: connection.id,
    generation: connection.generation,
    request: { action: "protection", protection: false },
    before: dnsSnapshot(connection.engine),
    state: "planned",
    createdAt: time(),
    expiresAt: new Date(Date.now() + 300000).toISOString(),
  }
}

export function dnsProvision(): DNSServiceProvision {
  return {
    id: "setup-fixture",
    request: {
      name: "Owned fixture DNS",
      engine: "pihole",
      managementPort: 18080,
      dnsPort: 1053,
      memoryMiB: 256,
      cpus: 0.5,
      upstreams: ["192.0.2.53:53"],
      management: false,
      username: "admin",
    },
    image: "pihole/pihole:pinned-fixture",
    imageId: "sha256:fixture-image",
    owner: "fixture-install",
    resources: {
      containerName: "jd-dns-fixture",
      networkName: "jd-dns-fixture-net",
      volumes: ["jd-dns-fixture-config", "jd-dns-fixture-data"],
      phase: "planned",
    },
    state: "planned",
    createdAt: time(),
    expiresAt: new Date(Date.now() + 300000).toISOString(),
    limitations: ["Loopback publication only; both owned volumes are removed together."],
  }
}

export async function mockDNSServicePage(
  page: Page,
  options: { reader?: boolean; empty?: boolean; management?: boolean } = {},
) {
  const mutations: Mutation[] = []
  const reads: string[] = []
  const connection = dnsConnection(options.management ?? true)
  const control = {
    mutations,
    reads,
    connections: options.empty ? ([] as DNSConnection[]) : [connection],
    view: { connection, state: "available", snapshot: dnsSnapshot() } as DNSServiceView,
    changes: [] as DNSServiceChange[],
    provisions: [] as DNSServiceProvision[],
    inspectFailure: false,
    connectFailure: false,
    apply: "verified" as "verified" | "needs_review" | "lost",
  }
  await mockNetwork(page, mutations, {
    session: options.reader
      ? { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
      : admin,
    overrides,
  })
  await page.route(/\/api\/v1\/network\/dns\/services(?:[/?]|$)/, async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "").replace(/\/$/, "")
    const method = request.method()
    const reply = (value: unknown, status = 200) =>
      route.fulfill({ status, contentType: "application/json", body: JSON.stringify(value) })
    if (method === "GET") {
      reads.push(path)
      if (path === base) return reply(control.connections)
      if (path === `${base}/provisions`) return reply(control.provisions)
      if (path === `${base}/${control.view.connection.id}/changes`)
        return reply(
          control.changes.map((change) => ({ ...change, before: undefined, after: undefined })),
        )
      const change = control.changes.find((item) => path === `${base}/changes/${item.id}`)
      if (change) return reply(change)
      const setup = control.provisions.find((item) => path === `${base}/provisions/${item.id}`)
      if (setup) return reply(setup)
      if (control.inspectFailure)
        return reply(
          { error: { code: "native_unavailable", message: "Fixture owner reading failed." } },
          503,
        )
      return reply(control.view)
    }
    const body = request.postData() ? request.postDataJSON() : undefined
    mutations.push({ method, path, body })
    if (
      (method === "POST" && path === base) ||
      (method === "PUT" && path === `${base}/${control.view.connection.id}`)
    ) {
      if (control.connectFailure)
        return reply(
          { error: { code: "native_unavailable", message: "Fixture credential read failed." } },
          500,
        )
      const next = {
        ...control.view.connection,
        name: body.name,
        engine: body.engine,
        endpoint: body.endpoint,
        serverName: body.serverName,
        customCA: Boolean(body.ca),
        management: body.management,
        generation: control.view.connection.generation + (method === "PUT" ? 1 : 0),
      }
      control.view = { connection: next, state: "available", snapshot: dnsSnapshot(next.engine) }
      control.connections = [next]
      return reply(control.view, method === "POST" ? 201 : 200)
    }
    if (method === "POST" && path === `${base}/${control.view.connection.id}/changes`) {
      const next = { ...dnsChange(control.view.connection), request: body }
      control.changes = [next]
      return reply(next, 201)
    }
    if (method === "POST" && path === `${base}/provisions`) {
      const next = { ...dnsProvision(), request: { ...body, password: undefined } }
      control.provisions = [next]
      return reply(next, 201)
    }
    const change = control.changes.find((item) => path === `${base}/changes/${item.id}/apply`)
    if (change) {
      change.state = control.apply === "verified" ? "verified" : "needs_review"
      change.endedAt = time()
      if (control.apply !== "verified") change.error = "Fixture native outcome needs review."
      if (control.apply === "lost") return route.abort("failed")
      return reply(change)
    }
    const setup = control.provisions.find((item) => path === `${base}/provisions/${item.id}`)
    if (method === "DELETE" && setup) {
      setup.state = "removed"
      setup.resources.phase = "removed"
      setup.endedAt = time()
      return reply(setup)
    }
    return reply(
      { error: { code: "fixture_unknown", message: "Unexpected native fixture mutation." } },
      400,
    )
  })
  await page.goto("/network/dns")
  return control
}
