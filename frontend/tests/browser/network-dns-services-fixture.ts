import type { Page } from "@playwright/test"
import { createHash } from "node:crypto"
import type {
  DNSChangeRequest,
  DNSConnection,
  DNSRecordInventory,
  DNSServiceChange,
  DNSServiceProvision,
  DNSServiceSnapshot,
  DNSServiceView,
} from "../../src/lib/network-dns-services"
import { admin, mockNetwork, type Mutation } from "./network-fixture"
import { overrides } from "./network-dns-fixture"

const base = "/network/dns/services"
const time = () => new Date().toISOString()
const fingerprint = (value: unknown) =>
  createHash("sha256").update(JSON.stringify(value)).digest("hex")
const reading = (state: string, summary: string) => ({
  state,
  basis: "native fixture inventory",
  summary,
})

export function dnsSnapshot(engine: DNSConnection["engine"] = "adguard"): DNSServiceSnapshot {
  return {
    engine,
    version: engine === "technitium" ? "15.6" : engine === "pihole" ? "v6.7.1" : "v0.107.71",
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
      {
        id: "client-2",
        name: "Empty group client",
        addresses: ["198.51.100.0/24"],
        groups: [],
        inherited: false,
      },
    ],
    clientEvidence: reading(
      "configured",
      "Native client-specific policy is separate from inherited defaults.",
    ),
    zones: [
      { name: "authority.example.test", type: "Primary", disabled: false, dnssec: "Unsigned" },
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
        id: 0,
        name: "Default filtering group",
        enabled: true,
        clientScopes: [],
        listenerScopes: [],
        domains: [],
      },
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

export function dnsConnection(
  management = true,
  engine: DNSConnection["engine"] = "adguard",
): DNSConnection {
  return {
    id: "dns-fixture",
    name: "Fixture DNS",
    engine,
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
    before: { ...dnsSnapshot(connection.engine), queries: [] },
    state: "planned",
    createdAt: time(),
    expiresAt: new Date(Date.now() + 300000).toISOString(),
  }
}

export function dnsRecords(
  patch: Partial<Omit<DNSRecordInventory, "records" | "fingerprint">> = {},
): DNSRecordInventory {
  const inventory = {
    zone: "authority.example.test",
    type: "Primary",
    disabled: false,
    internal: null,
    nativeVersion: "15.6",
    dnssec: "Unsigned",
    evidence: reading(
      "native_authority_configuration",
      "Native zone configuration; publication, delegation and client answers remain unmeasured.",
    ),
    ...patch,
  }
  const editable =
    inventory.type === "Primary" &&
    !inventory.disabled &&
    inventory.dnssec === "Unsigned" &&
    (inventory.internal === false ||
      (inventory.internal === null && ["15.6", "15.6.0"].includes(inventory.nativeVersion)))
  const records = [
    {
      name: `existing.${inventory.zone}`,
      type: "A",
      value: "192.0.2.91",
      ttl: 300,
      disabled: false,
      editable,
      comments: "Retain native record comment",
      fingerprint: fingerprint({ native: "existing-A", zone: inventory.zone }),
    },
    {
      name: `v6.${inventory.zone}`,
      type: "AAAA",
      value: "2001:db8::91",
      ttl: 600,
      disabled: false,
      editable,
      fingerprint: fingerprint({ native: "existing-AAAA", zone: inventory.zone }),
    },
    {
      name: `_policy._tcp.${inventory.zone}`,
      type: "TXT",
      ttl: 300,
      disabled: false,
      editable: false,
      comments: "Unsupported native RR remains visible and unchanged",
      fingerprint: fingerprint({ native: "unrepresented-TXT", zone: inventory.zone }),
    },
  ]
  return { ...inventory, records, fingerprint: fingerprint({ inventory, records }) }
}

function selectedSnapshot(
  native: DNSServiceSnapshot,
  request: DNSChangeRequest,
  records: DNSRecordInventory,
): DNSServiceSnapshot {
  const snapshot = structuredClone(native)
  delete snapshot.records
  delete snapshot.selectedClient
  delete snapshot.selectionFingerprint
  if (request.action === "record_add" || request.action === "record_remove") {
    snapshot.records = structuredClone(records)
    snapshot.selectionFingerprint = records.fingerprint
  } else if (request.action === "client_groups") {
    const client = snapshot.clients.find((item) => item.addresses.includes(request.client.address))
    const comment = "Keep this existing native client comment"
    snapshot.selectedClient = {
      address: request.client.address,
      groups: [...(client?.groups ?? [])],
      comment,
      commentFingerprint: fingerprint(comment),
      otherPolicyFingerprint: fingerprint({ unselectedClients: "unchanged", groups: "unchanged" }),
    }
    snapshot.selectionFingerprint = fingerprint(snapshot.selectedClient)
  } else if (request.action === "override_add" || request.action === "override_remove") {
    snapshot.selectionFingerprint = fingerprint(snapshot.localOverrides)
  }
  if (snapshot.selectionFingerprint)
    snapshot.policyFingerprint = fingerprint({
      native: snapshot.policyFingerprint,
      selected: snapshot.selectionFingerprint,
    })
  return snapshot
}

export function dnsPolicyChange(
  request: DNSChangeRequest,
  connection = dnsConnection(
    true,
    request.action === "record_add" || request.action === "record_remove"
      ? "technitium"
      : request.action === "client_groups"
        ? "pihole"
        : "adguard",
  ),
  snapshot = dnsSnapshot(connection.engine),
  records = dnsRecords(),
): DNSServiceChange {
  return {
    ...dnsChange(connection),
    request: structuredClone(request),
    before: { ...selectedSnapshot(snapshot, request, records), queries: [] },
  }
}

export function dnsAppliedSnapshot(change: DNSServiceChange): DNSServiceSnapshot {
  const after = structuredClone(change.before!)
  const request = change.request
  if (request.action === "protection") after.protection = request.protection
  if (request.action === "upstreams") after.upstreams = [...request.upstreams]
  if (request.action === "access") {
    after.allowedClients = [...request.allowedClients]
    after.deniedClients = [...request.deniedClients]
  }
  if (request.action === "override_add")
    after.localOverrides.push({
      ...request.record,
      ...(after.engine === "adguard" && { enabled: true }),
    })
  if (request.action === "override_remove")
    after.localOverrides = after.localOverrides.filter(
      (row) => row.name !== request.record.name || row.value !== request.record.value,
    )
  if (request.action === "record_add" && after.records)
    after.records.records.push({
      ...request.record,
      disabled: false,
      editable: true,
      fingerprint: fingerprint(request.record),
    })
  if (request.action === "record_remove" && after.records)
    after.records.records = after.records.records.filter(
      (row) => row.name !== request.record.name || row.value !== request.record.value,
    )
  if (request.action === "client_groups" && after.selectedClient) {
    after.selectedClient.groups = [...request.client.groups]
    for (const client of after.clients)
      if (client.addresses.includes(request.client.address))
        client.groups = [...request.client.groups]
  }
  if (after.records) {
    after.records.fingerprint = fingerprint({
      original: after.records.fingerprint,
      records: after.records.records,
    })
    after.selectionFingerprint = after.records.fingerprint
  } else if (after.selectedClient) after.selectionFingerprint = fingerprint(after.selectedClient)
  else if (request.action === "override_add" || request.action === "override_remove")
    after.selectionFingerprint = fingerprint(after.localOverrides)
  after.policyFingerprint = fingerprint({
    original: after.policyFingerprint,
    request,
    selected: after.selectionFingerprint,
  })
  after.observedAt = time()
  return after
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
  options: {
    reader?: boolean
    empty?: boolean
    management?: boolean
    engine?: DNSConnection["engine"]
  } = {},
) {
  const mutations: Mutation[] = []
  const reads: string[] = []
  const connection = dnsConnection(options.management ?? true, options.engine ?? "adguard")
  const control = {
    mutations,
    reads,
    connections: options.empty ? ([] as DNSConnection[]) : [connection],
    view: {
      connection,
      state: "available",
      snapshot: dnsSnapshot(connection.engine),
    } as DNSServiceView,
    records: dnsRecords(),
    changes: [] as DNSServiceChange[],
    provisions: [] as DNSServiceProvision[],
    inspectFailure: false,
    currentFailure: false,
    currentView: undefined as DNSServiceView | undefined,
    currentMissingSelection: false,
    recordsFailure: false,
    stageFailure: false,
    unexpectedReads: [] as string[],
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
      const current = control.changes.find((item) => path === `${base}/changes/${item.id}/current`)
      if (current) {
        if (control.currentFailure)
          return reply(
            { error: { code: "native_unavailable", message: "Fixture owner reading failed." } },
            503,
          )
        if (control.view.connection.generation !== current.generation)
          return reply(
            {
              error: {
                code: "conflict",
                message: "Fixture retained connection generation changed.",
              },
            },
            409,
          )
        if (control.currentView) return reply(control.currentView)
        return reply({
          ...control.view,
          snapshot: control.currentMissingSelection
            ? control.view.snapshot
            : selectedSnapshot(control.view.snapshot!, current.request, control.records),
        })
      }
      const change = control.changes.find((item) => path === `${base}/changes/${item.id}`)
      if (change) return reply(change)
      const setup = control.provisions.find((item) => path === `${base}/provisions/${item.id}`)
      if (setup) return reply(setup)
      if (path === `${base}/${control.view.connection.id}/zones/${control.records.zone}/records`) {
        if (control.recordsFailure)
          return reply(
            {
              error: {
                code: "native_unavailable",
                message: "Fixture authoritative record read failed.",
              },
            },
            503,
          )
        return reply(control.records)
      }
      if (path === `${base}/${control.view.connection.id}`) {
        if (control.inspectFailure)
          return reply(
            { error: { code: "native_unavailable", message: "Fixture owner reading failed." } },
            503,
          )
        return reply(control.view)
      }
      control.unexpectedReads.push(path)
      return reply(
        { error: { code: "fixture_unknown", message: "Unexpected native fixture read." } },
        404,
      )
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
      if (control.stageFailure)
        return reply(
          {
            error: {
              code: "native_refused",
              message: "Fixture selected native policy review refused.",
            },
          },
          409,
        )
      if (
        body.action === "override_remove" &&
        control.view.snapshot?.localOverrides.some(
          (record) =>
            record.name === body.record?.name &&
            record.value === body.record?.value &&
            record.enabled === false,
        )
      )
        return reply(
          {
            error: {
              code: "native_refused",
              message: "Fixture disabled native override cannot be removed.",
            },
          },
          400,
        )
      const next = dnsPolicyChange(
        body,
        control.view.connection,
        control.view.snapshot,
        control.records,
      )
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
      else {
        change.after = dnsAppliedSnapshot(change)
        if (change.after.records) control.records = structuredClone(change.after.records)
        const native = structuredClone(change.after)
        delete native.records
        delete native.selectedClient
        delete native.selectionFingerprint
        native.queries = control.view.snapshot?.queries ?? []
        native.policyFingerprint = fingerprint({
          previous: control.view.snapshot?.policyFingerprint,
          request: change.request,
        })
        control.view = { ...control.view, snapshot: native }
      }
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
