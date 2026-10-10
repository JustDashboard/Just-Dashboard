import { expect, test } from "bun:test"
import {
  dnsAttemptKey,
  dnsChangeOwnerProblem,
  dnsChangeName,
  dnsRetainedReview,
  dnsReviewProblem,
  dnsSameProvisionResources,
  dnsSameReviewedIntent,
  readDNSAttempts,
  readDNSChange,
  readDNSChangeRequest,
  readDNSCurrentChange,
  readDNSConnection,
  readDNSList,
  readDNSProvision,
  readDNSRecords,
  readDNSSelectedFilter,
  readDNSSnapshot,
  readDNSView,
} from "./network-dns-services"

test("retained history readers do not mistake the row index for an expected identity", () => {
  const reviews = [change(), { ...change(), id: "change-two" }]
  expect(readDNSList(reviews, readDNSChange).map((row) => row.id)).toEqual([
    "change-one",
    "change-two",
  ])
  const setups = [provision(), { ...provision(), id: "setup-two" }]
  expect(readDNSList(setups, readDNSProvision).map((row) => row.id)).toEqual([
    "setup-one",
    "setup-two",
  ])
})

test("native owner refresh holds a stale or unavailable baseline before confirmation", () => {
  const review = change()
  const view = { connection: connection(), state: "available", snapshot: snapshot() }
  expect(dnsChangeOwnerProblem(review, view)).toBeUndefined()
  for (const next of [
    undefined,
    { ...view, state: "unavailable" },
    { ...view, error: "native timeout" },
    { ...view, snapshot: undefined },
  ])
    expect(dnsChangeOwnerProblem(review, next)).toContain("could not be read")
  for (const next of [
    { ...view, snapshot: { ...view.snapshot, policyFingerprint: "foreign-change" } },
    { ...view, connection: { ...view.connection, generation: 4 } },
    { ...view, connection: { ...view.connection, management: false } },
  ])
    expect(dnsChangeOwnerProblem(review, next)).toContain("changed")
})

test("owned removal retains exact resource identities while its confirmation is open", () => {
  const setup = provision()
  expect(dnsSameProvisionResources(setup, structuredClone(setup))).toBe(true)
  for (const next of [
    { ...setup, state: "verified" },
    { ...setup, owner: "other-install" },
    { ...setup, imageId: "other-image" },
    { ...setup, resources: { ...setup.resources, volumes: ["foreign-data"] } },
    { ...setup, resources: { ...setup.resources, containerId: "replaced" } },
  ])
    expect(dnsSameProvisionResources(setup, next)).toBe(false)
})

test("only bounded review identities survive the browser attempt record", () => {
  expect([...readDNSAttempts(null)]).toEqual([])
  expect([...readDNSAttempts('["change:change-one","provision:setup-one"]')]).toEqual([
    "change:change-one",
    "provision:setup-one",
  ])
  for (const value of [
    '["password:secret"]',
    '["change:"]',
    "{}",
    "broken",
    JSON.stringify(Array(257).fill("change:x")),
  ])
    expect(() => readDNSAttempts(value)).toThrow()
})

test("late reads preserve terminal native responses while newer cleanup results remain visible", () => {
  const done = { ...change(), state: "verified", endedAt: "2026-10-09T12:01:00Z" }
  expect(dnsRetainedReview(done, change())).toBe(done)
  expect(dnsRetainedReview(done, { ...change(), state: "applying" })).toBe(done)
  const removed = { ...provision(), state: "removed", endedAt: "2026-10-09T12:02:00Z" }
  expect(dnsRetainedReview(removed, { ...provision(), state: "verified" })).toBe(removed)
  const removing = { ...provision(), state: "removing" }
  expect(
    dnsRetainedReview({ ...provision(), state: "verified", endedAt: done.endedAt }, removing),
  ).toBe(removing)
  const newer = { ...done, state: "needs_review", endedAt: "2026-10-09T12:03:00Z" }
  expect(dnsRetainedReview(done, newer)).toBe(newer)
})

test("an open confirmation and its result retain the exact native owner and reviewed intent", () => {
  const native = change()
  expect(dnsSameReviewedIntent(native, { ...native, state: "verified" })).toBe(true)
  for (const next of [
    { ...native, generation: 4 },
    { ...native, connectionId: "replacement-owner" },
    { ...native, before: { ...native.before, policyFingerprint: "other-policy" } },
    { ...native, request: { action: "protection", protection: true } },
    { ...native, expiresAt: "2026-10-09T13:05:00Z" },
  ])
    expect(dnsSameReviewedIntent(native, next)).toBe(false)
  const setup = provision()
  expect(
    dnsSameReviewedIntent(setup, {
      ...setup,
      state: "verified",
      resources: { ...setup.resources, phase: "verified", containerId: "new-owned-id" },
    }),
  ).toBe(true)
  for (const next of [
    { ...setup, owner: "other-owner" },
    { ...setup, imageId: "unreviewed-image" },
    { ...setup, request: { ...setup.request, dnsPort: 5353 } },
    { ...setup, resources: { ...setup.resources, volumes: ["different-data"] } },
  ])
    expect(dnsSameReviewedIntent(setup, next)).toBe(false)
  expect(dnsSameReviewedIntent(native, setup)).toBe(false)
})

const at = "2026-10-09T12:00:00Z"
const connection = () => ({
  id: "connection-one",
  name: "Office DNS",
  engine: "adguard",
  endpoint: "https://192.0.2.2:443",
  customCA: false,
  management: true,
  hasCredential: true,
  generation: 3,
  ownership: "connected",
  createdAt: at,
  updatedAt: at,
})
const snapshot = () => {
  const evidence = {
    state: "unknown",
    basis: "native_config",
    summary: "Native capability is unknown.",
  }
  return {
    policyFingerprint: "opaque-native-policy",
    observedAt: at,
    engine: "adguard",
    version: "0.107.71",
    roles: ["recursive", "filtering"],
    transport: { ...evidence },
    runtime: { ...evidence },
    listeners: [],
    access: { ...evidence },
    allowedClients: [],
    deniedClients: [],
    protection: true,
    protectionTemporary: false,
    upstreams: ["192.0.2.53:53"],
    upstreamProtocol: "classic",
    clients: [],
    clientEvidence: { ...evidence },
    zones: [],
    zoneEvidence: { ...evidence },
    views: { ...evidence },
    viewGroups: [],
    namedNetworks: {},
    filterGroups: [],
    appClientEvidence: { ...evidence },
    localOverrides: [],
    overrideEvidence: { ...evidence },
    queries: [],
    queryEvidence: { ...evidence },
    limitations: ["Configuration is not packet-level proof."],
  }
}
const change = () => ({
  id: "change-one",
  connectionId: "connection-one",
  generation: 3,
  request: { action: "protection", protection: false },
  before: snapshot(),
  state: "planned",
  createdAt: at,
  expiresAt: "2026-10-09T12:05:00Z",
})
const provision = () => ({
  id: "setup-one",
  request: {
    name: "Office DNS",
    engine: "adguard",
    managementPort: 18443,
    dnsPort: 18553,
    memoryMiB: 256,
    cpus: 0.5,
    upstreams: ["192.0.2.53:53"],
    management: false,
    username: "operator",
  },
  image: "reviewed@sha256:fixture",
  imageId: "sha256:fixture",
  owner: "install-one",
  resources: {
    networkName: "jd-dns-one",
    volumes: ["jd-dns-config", "jd-dns-data"],
    containerName: "jd-dns-one",
    phase: "planned",
  },
  state: "planned",
  createdAt: at,
  expiresAt: "2026-10-09T12:05:00Z",
  limitations: [],
})

test("private connection reads retain permissions and discard unexpected credentials", () => {
  const value = { ...connection(), credential: { password: "must-not-retain" }, ca: "private-ca" }
  const read = readDNSConnection(value)
  expect(read.management).toBe(true)
  expect(read).not.toHaveProperty("credential")
  expect(read).not.toHaveProperty("ca")
})

test("native unknown and unsupported evidence survives a successful HTTP reading", () => {
  const value = snapshot()
  value.zoneEvidence.state = "unsupported"
  const read = readDNSView({ connection: connection(), state: "available", snapshot: value })
  expect(read.snapshot.runtime.state).toBe("unknown")
  expect(read.snapshot.zoneEvidence.state).toBe("unsupported")
  expect(read.snapshot.zones).toEqual([])
})

for (const [name, mutate] of [
  ["missing query inventory", (v) => delete v.queries],
  [
    "unknown listener port",
    (v) =>
      v.listeners.push({ address: "127.0.0.1", port: "53", protocol: "udp", scope: "configured" }),
  ],
  [
    "invalid client groups",
    (v) =>
      v.clients.push({ id: "one", name: "one", addresses: [], groups: ["1"], inherited: false }),
  ],
  ["missing zone evidence", (v) => delete v.zoneEvidence],
  ["unknown protection value", (v) => (v.protection = "false")],
  ["a malformed query", (v) => v.queries.push(null)],
  [
    "invalid native group",
    (v) =>
      v.filterGroups.push({ name: "one", clientScopes: [null], listenerScopes: [], domains: [] }),
  ],
  ["missing named networks", (v) => delete v.namedNetworks],
  ["an unknown engine", (v) => (v.engine = "new-engine")],
])
  test(`native DNS refuses ${name} before the inventory can render it`, () => {
    const value = snapshot()
    mutate(value)
    expect(() => readDNSSnapshot(value)).toThrow()
  })

test("a reading for another connection or engine cannot replace the selected owner", () => {
  expect(() =>
    readDNSView({ connection: connection(), state: "available", snapshot: snapshot() }, "other"),
  ).toThrow("changed")
  const native = snapshot()
  native.engine = "technitium"
  expect(() =>
    readDNSView({ connection: connection(), state: "available", snapshot: native }),
  ).toThrow("owner changed")
})

test("retained history metadata is readable without decrypted snapshots or query rows", () => {
  const value = change()
  delete value.before
  expect(readDNSList([value], readDNSChange)[0].before).toBeUndefined()
  expect(
    dnsReviewProblem(readDNSChange(value), "change", new Set(), Date.parse(at), connection()),
  ).toContain("baseline")
})

test("unknown retained intent and mismatched review identity refuse apply preparation", () => {
  const value = change()
  value.request = { action: "arbitrary_native_payload", body: {} }
  expect(() => readDNSChange(value)).toThrow("unsupported")
  expect(() => readDNSChange(change(), "other")).toThrow("changed")
})

test("only a planned fresh review on the same managed generation can apply", () => {
  const review = readDNSChange(change())
  const owner = readDNSConnection(connection())
  expect(dnsReviewProblem(review, "change", new Set(), Date.parse(at), owner)).toBeUndefined()
  for (const altered of [
    { ...owner, management: false },
    { ...owner, generation: 4 },
    { ...owner, id: "other" },
  ]) {
    expect(dnsReviewProblem(review, "change", new Set(), Date.parse(at), altered)).toContain(
      "baseline",
    )
  }
})

for (const state of [
  "verified",
  "applying",
  "refused",
  "needs_review",
  "interrupted",
  "future_state",
]) {
  test(`HTTP success with ${state} never authorizes another native apply`, () => {
    const review = readDNSChange({ ...change(), state })
    expect(dnsReviewProblem(review, "change", new Set(), Date.parse(at), connection())).toContain(
      "consumed",
    )
  })
}

test("expiry and invalid clocks refuse both native mutation and owned setup", () => {
  const native = readDNSChange(change())
  const setup = readDNSProvision(provision())
  for (const now of [Date.parse(native.expiresAt), NaN]) {
    expect(dnsReviewProblem(native, "change", new Set(), now, connection())).toContain("expired")
    expect(dnsReviewProblem(setup, "provision", new Set(), now)).toContain("expired")
  }
  setup.expiresAt = "unreadable"
  expect(dnsReviewProblem(setup, "provision", new Set(), Date.parse(at))).toContain("expired")
})

test("an uncertain first apply remains consumed when the retained server state is still planned", () => {
  const review = readDNSChange(change())
  const attempted = new Set([dnsAttemptKey("change", review.id)])
  expect(dnsReviewProblem(review, "change", attempted, Date.parse(at), connection())).toContain(
    "attempted apply",
  )
})

test("bootstrap passwords are request-only and never survive a retained setup read", () => {
  const value = provision()
  value.request.password = "explicit-bootstrap-secret"
  const read = readDNSProvision(value)
  expect(read.request).not.toHaveProperty("password")
  expect(dnsReviewProblem(read, "provision", new Set(), Date.parse(at))).toBeUndefined()
  read.resources.phase = "native_bootstrap"
  expect(dnsReviewProblem(read, "provision", new Set(), Date.parse(at))).toContain(
    "already started",
  )
})

test("missing inventory is a read failure rather than a healthy empty list", () => {
  expect(() => readDNSList(null, readDNSConnection)).toThrow("incomplete")
  expect(() => readDNSList(Array(65).fill(change()), readDNSChange)).toThrow("retained limit")
})

const hash = (digit = "a") => digit.repeat(64)
const records = () => ({
  zone: "owned.example",
  type: "Primary",
  disabled: false,
  internal: null,
  nativeVersion: "15.6",
  dnssec: "Unsigned",
  fingerprint: hash(),
  evidence: {
    state: "native_authority_configuration",
    basis: "native_api",
    summary: "Configured authority; publication is unmeasured.",
  },
  records: [
    {
      name: "host.owned.example",
      type: "A",
      value: "198.51.100.9",
      ttl: 60,
      disabled: false,
      editable: true,
      fingerprint: hash("b"),
    },
    {
      name: "_service.owned.example",
      type: "TXT",
      ttl: 60,
      disabled: false,
      editable: false,
      fingerprint: hash("c"),
    },
  ],
})
const policyChange = (action = "record_add") => {
  const row = change()
  row.before.engine = "technitium"
  row.before.version = "15.6"
  row.before.records = records()
  row.before.selectionFingerprint = hash()
  row.request = {
    action,
    zone: "owned.example",
    record: { name: "new.owned.example", type: "AAAA", value: "2001:db8::9", ttl: 60 },
  }
  if (action === "client_groups") {
    row.before.engine = "pihole"
    row.before.version = "v6.7.1"
    delete row.before.records
    row.before.selectedClient = {
      address: "198.51.100.9",
      groups: [0],
      comment: null,
      commentFingerprint: hash("b"),
      otherPolicyFingerprint: hash("c"),
    }
    row.request = { action, client: { address: "198.51.100.9", groups: [] } }
  }
  if (action === "override_add" || action === "override_remove") {
    row.before.engine = "adguard"
    row.before.version = "v0.107.71"
    delete row.before.records
    row.request = { action, record: { name: "local.example", type: "A", value: "198.51.100.9" } }
  }
  return row
}

test("closed reviewed actions preserve exact record TTL and explicit empty native groups", () => {
  for (const action of [
    "override_add",
    "override_remove",
    "record_add",
    "record_remove",
    "client_groups",
  ]) {
    const row = policyChange(action)
    const read = readDNSChange(row)
    expect(read.request).toEqual(row.request)
    expect(read.before.selectionFingerprint).toBe(hash())
    expect(dnsChangeName(read.request)).toBeTruthy()
  }
  const read = readDNSChange(policyChange("client_groups"))
  expect(read.request.client.groups).toEqual([])
  expect(read.before.selectedClient.comment).toBeNull()
  const metadata = policyChange()
  delete metadata.before
  expect(readDNSChange(metadata).before).toBeUndefined()
})

test("every retained action rejects irrelevant or arbitrary native payload fields", () => {
  const intents = [
    { action: "protection", protection: false },
    { action: "upstreams", upstreams: ["192.0.2.53:53"] },
    { action: "access", allowedClients: ["192.0.2.0/24"] },
    { action: "zone_create", zone: "owned.example" },
    ...["override_add", "override_remove", "record_add", "record_remove", "client_groups"].map(
      (action) => policyChange(action).request,
    ),
  ]
  for (const request of intents) {
    expect(readDNSChangeRequest(request)).toBeTruthy()
    for (const field of ["nativeBody", "token", "command", "proxy"])
      expect(() => readDNSChangeRequest({ ...request, [field]: {} })).toThrow("unsupported")
    const irrelevant =
      request.action === "client_groups"
        ? { record: { name: "local.example", type: "A", value: "198.51.100.9" } }
        : { client: { address: "198.51.100.9", groups: [] } }
    expect(() => readDNSChangeRequest({ ...request, ...irrelevant })).toThrow("unsupported")
  }
})

test("record and client reviewed requests enforce canonical grammar, bounds and native engine", () => {
  for (const delta of [
    { name: "UPPER.owned.example" },
    { name: "*.owned.example" },
    { name: "badowned.example" },
    { type: "TXT" },
    { value: "::ffff:198.51.100.9" },
    { ttl: "60" },
    { ttl: 0 },
    { ttl: 86401 },
    { ttl: 1.5 },
    { unknown: true },
  ]) {
    const request = policyChange().request
    expect(() =>
      readDNSChangeRequest({ ...request, record: { ...request.record, ...delta } }),
    ).toThrow("unsupported")
  }
  for (const groups of [
    undefined,
    null,
    ["0"],
    [0, 0],
    [-1],
    [2147483648],
    Array.from({ length: 65 }, (_, i) => i),
  ])
    expect(() =>
      readDNSChangeRequest({
        action: "client_groups",
        client: { address: "198.51.100.9", groups },
      }),
    ).toThrow("unsupported")
  expect(() => readDNSChangeRequest(policyChange().request, "pihole")).toThrow()
  expect(() => readDNSChangeRequest(policyChange("client_groups").request, "adguard")).toThrow()
  expect(() => readDNSChangeRequest(policyChange("override_add").request, "technitium")).toThrow()
  expect(() =>
    readDNSChangeRequest({ ...policyChange("override_add").request, zone: "owned.example" }),
  ).toThrow()
  expect(() =>
    readDNSChangeRequest({
      ...policyChange("override_add").request,
      record: { ...policyChange("override_add").request.record, ttl: 0 },
    }),
  ).toThrow()
  expect(() =>
    readDNSChangeRequest({ action: "upstreams", upstreams: ["dns.example:53"] }),
  ).toThrow()
  expect(() =>
    readDNSChangeRequest({ action: "access", allowedClients: ["192.0.2.9/24"] }),
  ).toThrow()
  expect(() =>
    readDNSChangeRequest({
      action: "access",
      allowedClients: ["192.0.2.0/24"],
      deniedClients: ["192.0.2.0/24"],
    }),
  ).toThrow()
})

test("record inventory retains nullable classification and unsupported rows without editable claims", () => {
  const read = readDNSRecords(records(), "owned.example")
  expect(read.internal).toBeNull()
  expect(read.nativeVersion).toBe("15.6")
  expect(read.records[1].type).toBe("TXT")
  expect(read.records[1].editable).toBe(false)
  expect(read.records[1].value).toBeUndefined()
  for (const mutate of [
    (v) => delete v.internal,
    (v) => (v.internal = "false"),
    (v) => (v.nativeVersion = "15.7"),
    (v) => (v.records[0].ttl = "60"),
    (v) => (v.records[0].fingerprint = "missing"),
    (v) => (v.records[0].value = "x".repeat(513)),
    (v) => (v.records[0].comments = "x".repeat(513)),
    (v) => (v.records[1].editable = true),
    (v) => (v.records[0].disabled = true),
    (v) => (v.records[0].name = "foreign.example"),
    (v) => (v.records = Array(257).fill(v.records[0])),
  ]) {
    const value = records()
    mutate(value)
    expect(() => readDNSRecords(value)).toThrow()
  }
  expect(() => readDNSRecords(records(), "foreign.example")).toThrow("changed")
})

test("selected snapshots refuse missing fingerprints, owner/type inconsistencies and malformed client state", () => {
  for (const mutate of [
    (v) => delete v.selectionFingerprint,
    (v) => (v.selectionFingerprint = hash("d")),
    (v) => (v.engine = "adguard"),
    (v) => (v.version = "15.7"),
    (v) => (v.records.internal = true),
  ]) {
    const value = policyChange().before
    mutate(value)
    expect(() => readDNSSnapshot(value)).toThrow()
  }
  for (const mutate of [
    (v) => delete v.selectedClient.comment,
    (v) => (v.selectedClient.commentFingerprint = "unknown"),
    (v) => (v.selectedClient.groups = ["0"]),
    (v) => (v.selectedClient.address = "hostname.example"),
  ]) {
    const value = policyChange("client_groups").before
    mutate(value)
    expect(() => readDNSSnapshot(value)).toThrow()
  }
  const value = snapshot()
  value.localOverrides = [
    { name: "local.example", value: "198.51.100.9", type: "native_rewrite", enabled: false },
  ]
  expect(readDNSSnapshot(value).localOverrides[0].enabled).toBe(false)
  value.localOverrides[0].enabled = "false"
  expect(() => readDNSSnapshot(value)).toThrow()
})

test("plain owner inventory cannot replace an exact selected current-review read", () => {
  for (const action of ["record_add", "client_groups", "override_add"]) {
    const review = readDNSChange(policyChange(action))
    const owner = { ...connection(), engine: review.before.engine }
    const view = { connection: owner, state: "available", snapshot: structuredClone(review.before) }
    expect(readDNSCurrentChange(view, review).snapshot.selectionFingerprint).toBe(hash())
    expect(dnsChangeOwnerProblem(review, view)).toBeUndefined()
    const plain = structuredClone(view)
    delete plain.snapshot.selectionFingerprint
    delete plain.snapshot.records
    delete plain.snapshot.selectedClient
    expect(() => readDNSCurrentChange(plain, review)).toThrow("exact reviewed")
    expect(dnsChangeOwnerProblem(review, plain)).toBeTruthy()
    expect(() =>
      readDNSCurrentChange({ ...view, connection: { ...owner, generation: 4 } }, review),
    ).toThrow("generation")
    const drift = { ...view, snapshot: { ...view.snapshot, policyFingerprint: "changed-policy" } }
    expect(readDNSCurrentChange(drift, review).state).toBe("available")
    expect(dnsChangeOwnerProblem(review, drift)).toContain("changed")
    expect(
      readDNSCurrentChange(
        { connection: owner, state: "unavailable", error: "native timeout" },
        review,
      ).state,
    ).toBe("unavailable")
  }
})

test("selected metadata remains immutable across confirmation even when a top-level digest is reused", () => {
  const review = readDNSChange(policyChange())
  for (const mutate of [
    (v) => (v.before.records.records[0].ttl = 120),
    (v) => (v.before.records.records[0].fingerprint = hash("d")),
    (v) => (v.before.records.internal = false),
    (v) => (v.before.records.nativeVersion = "15.6.0"),
    (v) => (v.before.selectionFingerprint = hash("d")),
  ]) {
    const next = structuredClone(review)
    mutate(next)
    expect(dnsSameReviewedIntent(review, next)).toBe(false)
  }
  const client = readDNSChange(policyChange("client_groups"))
  const next = structuredClone(client)
  next.before.selectedClient.comment = "other comment"
  expect(dnsSameReviewedIntent(client, next)).toBe(false)
  expect(
    dnsReviewProblem(
      client,
      "change",
      new Set([dnsAttemptKey("change", client.id)]),
      Date.parse(at),
      { ...connection(), engine: "pihole" },
    ),
  ).toContain("attempted apply")
})

const domainFilterChange = (engine = "adguard", action = "filter_add") => {
  const row = change()
  const removing = action === "filter_remove"
  row.before.engine = engine
  row.before.version = engine === "adguard" ? "v0.107.71" : "v6.7.1"
  row.before.selectionFingerprint = hash()
  row.request = {
    action,
    filter: {
      domain: "selected.example",
      disposition: "deny",
      match: engine === "adguard" ? "suffix" : "exact",
      ...(engine === "pihole" && !removing ? { groups: [] } : {}),
    },
  }
  row.before.selectedFilter = {
    domain: row.request.filter.domain,
    disposition: row.request.filter.disposition,
    match: row.request.filter.match,
    present: removing,
    comment: null,
    commentReported: engine === "pihole" && removing,
    ...(removing ? { ruleFingerprint: hash("b") } : {}),
    ...(engine === "pihole" && removing ? { enabled: true, groups: [0] } : {}),
    otherPolicyFingerprint: hash("c"),
    evidence: {
      state: "configured",
      basis: "native_configuration",
      summary: "Native configuration; client filtering decisions remain unmeasured.",
    },
    owners: removing ? 1 : 0,
    exact: removing ? 1 : 0,
    inventoryCount: removing ? 3 : 2,
  }
  return row
}

test("domain reviews preserve engine-specific match and explicit membership instead of native DSL", () => {
  for (const engine of ["adguard", "pihole"])
    for (const action of ["filter_add", "filter_remove"]) {
      const value = domainFilterChange(engine, action)
      const read = readDNSChange(value)
      expect(read.request).toEqual(value.request)
      expect(read.before.selectedFilter).toEqual(value.before.selectedFilter)
      expect(dnsChangeName(read.request)).toBe(
        `${action === "filter_add" ? "Add" : "Remove"} deny ${engine === "adguard" ? "domain-suffix" : "exact-domain"} filter selected.example`,
      )
      for (const unwanted of ["record", "client", "protection", "zone", "nativeBody", "token"])
        expect(() => readDNSChangeRequest({ ...value.request, [unwanted]: {} })).toThrow()
      const metadata = { ...value, before: undefined }
      expect(readDNSChange(metadata).request).toEqual(value.request)
    }
  const value = domainFilterChange("pihole")
  value.request.filter.groups = [0]
  const read = readDNSChange(value)
  expect(read.request.filter.groups).toEqual([0])
  expect(read.request.filter.groups).not.toBe(value.request.filter.groups)
})

test("native domain readback supports the opposite presence after each consumed add or removal", () => {
  for (const engine of ["adguard", "pihole"])
    for (const action of ["filter_add", "filter_remove"]) {
      const row = domainFilterChange(engine, action)
      row.after = domainFilterChange(
        engine,
        action === "filter_add" ? "filter_remove" : "filter_add",
      ).before
      row.after.selectionFingerprint = hash("d")
      if (engine === "pihole" && action === "filter_add") row.after.selectedFilter.groups = []
      row.state = "verified"
      row.endedAt = "2026-10-09T12:01:00Z"
      const read = readDNSChange(row)
      expect(read.after.selectedFilter.present).toBe(action === "filter_add")
      expect(read.after.selectionFingerprint).toBe(hash("d"))
      expect(read.after.selectedFilter.otherPolicyFingerprint).toBe(
        read.before.selectedFilter.otherPolicyFingerprint,
      )
      expect(
        dnsReviewProblem(read, "change", new Set(), Date.parse(at), { ...connection(), engine }),
      ).toContain("consumed")
    }
})

test("closed domain intents reject wrong engines, missing selections and malformed group elements", () => {
  for (const engine of ["adguard", "pihole"])
    for (const action of ["filter_add", "filter_remove"]) {
      const request = domainFilterChange(engine, action).request
      for (const delta of [
        { domain: "*.selected.example" },
        { domain: "SELECTED.example" },
        { domain: "selected.example." },
        { domain: "||selected.example^" },
        { domain: "single" },
        { domain: `${"a".repeat(64)}.example` },
        { disposition: "block" },
        { match: "regex" },
        { comment: "not a writable field" },
        { enabled: true },
        { nativeBody: {} },
      ])
        expect(() =>
          readDNSChangeRequest({ ...request, filter: { ...request.filter, ...delta } }),
        ).toThrow()
      expect(() => readDNSChangeRequest(request, "technitium")).toThrow()
      expect(() =>
        readDNSChangeRequest(request, engine === "adguard" ? "pihole" : "adguard"),
      ).toThrow()
      expect(() => readDNSChangeRequest({ action, filter: null })).toThrow()
      expect(() => readDNSChangeRequest({ action })).toThrow()
      if (engine !== "pihole" || action !== "filter_add")
        for (const groups of [[], [0], null])
          expect(() =>
            readDNSChangeRequest({ ...request, filter: { ...request.filter, groups } }),
          ).toThrow()
    }
  const request = domainFilterChange("pihole").request
  for (const groups of [
    undefined,
    null,
    [null],
    ["0"],
    [false],
    [0, 0],
    [-1],
    [0.5],
    [2147483648],
    Array(1),
    Array.from({ length: 65 }, (_, id) => id),
  ])
    expect(() =>
      readDNSChangeRequest({ ...request, filter: { ...request.filter, groups } }),
    ).toThrow()
})

test("selected filter counts, digests and configured provenance must agree before rendering", () => {
  for (const engine of ["adguard", "pihole"])
    for (const action of ["filter_add", "filter_remove"]) {
      const row = domainFilterChange(engine, action).before.selectedFilter
      for (const mutate of [
        (v) => (v.domain = "*.selected.example"),
        (v) => (v.disposition = "regex"),
        (v) => (v.match = "native"),
        (v) => (v.inventoryCount = 257),
        (v) => (v.inventoryCount = -1),
        (v) => (v.inventoryCount = null),
        (v) => (v.owners = v.inventoryCount + 1),
        (v) => (v.exact = v.owners + 1),
        (v) => (v.exact = "1"),
        (v) => (v.present = !v.present),
        (v) => (v.otherPolicyFingerprint = "unreported"),
        (v) => (v.evidence.state = "unknown"),
        (v) => (v.evidence.basis = "runtime"),
        (v) => (v.evidence.summary = ""),
        (v) => (v.evidence.nativeBody = "secret"),
        (v) => (v.comment = undefined),
        (v) => (v.commentReported = undefined),
        (v) => (v.nativeBody = {}),
        (v) => (v.ruleFingerprint = v.exact ? undefined : hash("d")),
      ]) {
        const changed = structuredClone(row)
        mutate(changed)
        expect(() => readDNSSelectedFilter(changed, engine)).toThrow()
      }
    }
  const ambiguous = domainFilterChange("adguard", "filter_remove").before.selectedFilter
  ambiguous.owners = ambiguous.exact = 2
  ambiguous.present = false
  expect(readDNSSelectedFilter(ambiguous).owners).toBe(2)
  const opposite = domainFilterChange().before.selectedFilter
  opposite.owners = 1
  expect(readDNSSelectedFilter(opposite).present).toBe(false)
})

test("Pi-hole reported false, empty membership and null versus empty comment remain distinct", () => {
  const selected = domainFilterChange("pihole", "filter_remove").before.selectedFilter
  for (const comment of [null, "", "native comment"]) {
    const value = { ...selected, enabled: false, groups: [], comment }
    const read = readDNSSelectedFilter(value, "pihole")
    expect(read.enabled).toBe(false)
    expect(read.groups).toEqual([])
    expect(read.groups).not.toBe(value.groups)
    expect(read.comment).toBe(comment)
    expect(read.commentReported).toBe(true)
  }
  for (const delta of [
    { enabled: undefined },
    { enabled: null },
    { groups: undefined },
    { groups: [null] },
    { groups: [0, 0] },
    { groups: [-1] },
    { groups: Array.from({ length: 65 }, (_, id) => id) },
    { comment: "x".repeat(513) },
    { commentReported: false },
  ])
    expect(() => readDNSSelectedFilter({ ...selected, ...delta }, "pihole")).toThrow()
  for (const row of [
    domainFilterChange("adguard", "filter_remove").before.selectedFilter,
    domainFilterChange("pihole").before.selectedFilter,
  ])
    for (const delta of [
      { enabled: false },
      { groups: [] },
      { comment: "" },
      { commentReported: true },
    ])
      expect(() => readDNSSelectedFilter({ ...row, ...delta })).toThrow()
})

test("selected domain snapshots and retained reviews cannot borrow another selection or native version", () => {
  for (const engine of ["adguard", "pihole"]) {
    const review = domainFilterChange(engine)
    for (const mutate of [
      (v) => delete v.selectionFingerprint,
      (v) => (v.selectionFingerprint = "not-raw"),
      (v) => (v.engine = "technitium"),
      (v) => (v.version = "future-version"),
      (v) => (v.records = records()),
      (v) => (v.selectedClient = policyChange("client_groups").before.selectedClient),
    ]) {
      const snapshot = structuredClone(review.before)
      mutate(snapshot)
      expect(() => readDNSSnapshot(snapshot)).toThrow()
    }
    for (const delta of [
      { domain: "foreign.example" },
      { disposition: "allow" },
      { match: engine === "adguard" ? "exact" : "suffix" },
    ]) {
      const row = structuredClone(review)
      Object.assign(row.before.selectedFilter, delta)
      expect(() => readDNSChange(row)).toThrow()
    }
  }
  const legacy = policyChange("override_add")
  legacy.before.selectedFilter = domainFilterChange().before.selectedFilter
  expect(() => readDNSChange(legacy)).toThrow("selection")
})

test("domain current reads retain changed evidence but hold apply for raw or selected metadata drift", () => {
  for (const engine of ["adguard", "pihole"])
    for (const action of ["filter_add", "filter_remove"]) {
      const review = readDNSChange(domainFilterChange(engine, action))
      const owner = { ...connection(), engine }
      const view = {
        connection: owner,
        state: "available",
        snapshot: structuredClone(review.before),
      }
      expect(readDNSCurrentChange(view, review).state).toBe("available")
      expect(dnsChangeOwnerProblem(review, view)).toBeUndefined()
      const ordinary = { ...view, snapshot: snapshot() }
      ordinary.snapshot.engine = engine
      ordinary.snapshot.version = review.before.version
      expect(() => readDNSCurrentChange(ordinary, review)).toThrow("exact reviewed")
      expect(dnsChangeOwnerProblem(review, ordinary)).toBeTruthy()
      for (const mutate of [
        (v) => (v.selectionFingerprint = hash("d")),
        (v) => (v.selectedFilter.otherPolicyFingerprint = hash("d")),
        (v) => v.selectedFilter.inventoryCount++,
      ]) {
        const fresh = structuredClone(view)
        mutate(fresh.snapshot)
        expect(readDNSCurrentChange(fresh, review).state).toBe("available")
        expect(dnsChangeOwnerProblem(review, fresh)).toContain("changed")
      }
      const different = structuredClone(view)
      different.snapshot.selectedFilter.domain = "other.example"
      expect(() => readDNSCurrentChange(different, review)).toThrow("exact reviewed")
      expect(() =>
        readDNSCurrentChange({ ...view, connection: { ...owner, generation: 4 } }, review),
      ).toThrow()
      const unavailable = { connection: owner, state: "unavailable", error: "native timeout" }
      expect(readDNSCurrentChange(unavailable, review).state).toBe("unavailable")
      expect(dnsChangeOwnerProblem(review, unavailable)).toContain("could not be read")
    }
})

test("domain apply requires an editable retained baseline while uncertainty and expiry stay single-use", () => {
  for (const engine of ["adguard", "pihole"])
    for (const action of ["filter_add", "filter_remove"]) {
      const review = readDNSChange(domainFilterChange(engine, action))
      const owner = { ...connection(), engine }
      expect(dnsReviewProblem(review, "change", new Set(), Date.parse(at), owner)).toBeUndefined()
      const changed = structuredClone(review)
      if (action === "filter_add") changed.before.selectedFilter.owners = 1
      else changed.before.selectedFilter.owners = 2
      expect(dnsReviewProblem(changed, "change", new Set(), Date.parse(at), owner)).toContain(
        "baseline",
      )
      expect(
        dnsReviewProblem(review, "change", new Set(), Date.parse(review.expiresAt), owner),
      ).toContain("expired")
      expect(
        dnsReviewProblem(
          { ...review, state: "needs_review" },
          "change",
          new Set(),
          Date.parse(at),
          owner,
        ),
      ).toContain("consumed")
      expect(
        dnsReviewProblem(
          review,
          "change",
          new Set([dnsAttemptKey("change", review.id)]),
          Date.parse(at),
          owner,
        ),
      ).toContain("attempted")
    }
  const full = readDNSChange(domainFilterChange())
  full.before.selectedFilter.inventoryCount = 256
  expect(dnsReviewProblem(full, "change", new Set(), Date.parse(at), connection())).toContain(
    "baseline",
  )
  const disabled = readDNSChange(domainFilterChange("pihole", "filter_remove"))
  disabled.before.selectedFilter.enabled = false
  expect(
    dnsReviewProblem(disabled, "change", new Set(), Date.parse(at), {
      ...connection(),
      engine: "pihole",
    }),
  ).toContain("baseline")
})

test("held domain intent includes selected raw metadata even when another digest is reused", () => {
  const review = readDNSChange(domainFilterChange("pihole", "filter_remove"))
  expect(dnsSameReviewedIntent(review, { ...review, state: "verified" })).toBe(true)
  for (const mutate of [
    (v) => (v.request.filter.domain = "other.example"),
    (v) => (v.request.filter.disposition = "allow"),
    (v) => (v.request.filter.match = "suffix"),
    (v) => (v.before.selectedFilter.comment = ""),
    (v) => (v.before.selectedFilter.groups = []),
    (v) => (v.before.selectedFilter.enabled = false),
    (v) => (v.before.selectedFilter.ruleFingerprint = hash("d")),
    (v) => (v.before.selectedFilter.otherPolicyFingerprint = hash("d")),
    (v) => (v.before.selectionFingerprint = hash("d")),
  ]) {
    const next = structuredClone(review)
    mutate(next)
    expect(dnsSameReviewedIntent(review, next)).toBe(false)
  }
  const add = readDNSChange(domainFilterChange("pihole"))
  const next = structuredClone(add)
  next.request.filter.groups = [0]
  expect(dnsSameReviewedIntent(add, next)).toBe(false)
})
