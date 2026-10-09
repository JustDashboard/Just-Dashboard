import { expect, test } from "bun:test"
import {
  dnsAttemptKey,
  dnsReviewProblem,
  readDNSChange,
  readDNSConnection,
  readDNSList,
  readDNSProvision,
  readDNSSnapshot,
  readDNSView,
} from "./network-dns-services"

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
