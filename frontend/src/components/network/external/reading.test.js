import { expect, test } from "bun:test"
import { approvedServices, checkOutcome, newestFirst, reachVerdict, sourceState } from "./reading"

const source = (id, extra = {}) => ({
  id,
  name: id,
  location: "",
  placement: "external_host",
  scopes: [
    {
      id: "service",
      target: "service.example",
      addresses: ["192.0.2.8"],
      ports: [443],
      families: ["inet"],
    },
  ],
  createdAt: "2026-10-08T10:00:00Z",
  enrolledAt: "2026-10-08T10:01:00Z",
  ...extra,
})
const check = (id, vantageId, status, stages, at = "2026-10-08T10:03:00Z") => ({
  id,
  vantageId,
  request: { vantageId, scopeId: "service", family: "inet", port: 443, tls: true },
  status,
  createdAt: at,
  expiresAt: at,
  startedBy: "operator",
  result: stages && {
    target: "service.example",
    endedAt: at,
    stages: stages.map(([name, basis, state]) => ({ name, basis, state })),
  },
})
const good = [
  ["dns", "measured", "resolved"],
  ["tcp", "measured", "connected"],
  ["tls", "measured", "verified"],
]

test("a check that measured nothing is no measurement, never a failure", () => {
  for (const status of ["expired", "cancelled", "failed", "completed"]) {
    const outcome = checkOutcome(check("a", "s", status))
    expect(outcome.kind).toBe("none")
    expect(outcome.tone).toBe("unknown")
    expect(outcome.line).toEqual({ state: "idle" })
  }
  expect(checkOutcome(undefined).word).toBe("not checked yet")
})

test("a check in flight pulses on a dashed line", () => {
  expect(checkOutcome(check("a", "s", "queued")).line).toEqual({
    state: "up",
    moving: true,
    dashed: true,
  })
  expect(checkOutcome(check("a", "s", "running")).word).toBe("measuring")
})

test("only a measured stage can break a path, and it is named", () => {
  expect(checkOutcome(check("a", "s", "completed", good)).kind).toBe("reached")
  const refused = checkOutcome(
    check("a", "s", "completed", [
      ["dns", "measured", "resolved"],
      ["tcp", "measured", "refused"],
      ["tls", "unknown", "skipped"],
    ]),
  )
  expect(refused.kind).toBe("failed")
  expect(refused.word).toBe("TCP refused")
  expect(refused.line.state).toBe("danger")
})

test("a stage that is not measured leaves the path partly unknown", () => {
  const outcome = checkOutcome(
    check("a", "s", "completed", [
      ["dns", "observed", "resolved"],
      ["tcp", "measured", "connected"],
      ["tls", "unknown", "failed"],
    ]),
  )
  expect(outcome.kind).toBe("unclear")
  expect(outcome.tone).toBe("warning")
})

test("a literal target and an unrequested TLS check still count as reached", () => {
  const outcome = checkOutcome(
    check("a", "s", "completed", [
      ["dns", "measured", "not_applicable"],
      ["tcp", "measured", "connected"],
      ["tls", "measured", "not_requested"],
    ]),
  )
  expect(outcome.kind).toBe("reached")
})

test("the verdict counts sources by their last settled check", () => {
  const sources = [source("a"), source("b"), source("gone", { revokedAt: "2026-10-08T11:00:00Z" })]
  expect(reachVerdict([], []).word).toBe("No sources enrolled")
  expect(reachVerdict(sources, []).word).toBe("No measurement yet")
  const checks = [
    check("1", "a", "completed", good, "2026-10-08T10:03:00Z"),
    check("2", "a", "queued", undefined, "2026-10-08T10:09:00Z"),
    check("3", "b", "expired", undefined, "2026-10-08T10:05:00Z"),
  ]
  expect(reachVerdict(sources, checks)).toEqual({
    word: "Reached from 1 of 2 sources",
    tone: "warning",
  })
  expect(reachVerdict([source("a")], checks)).toEqual({
    word: "Reached from 1 of 1 source",
    tone: "running",
  })
})

test("checks sort newest first by when they last changed", () => {
  const rows = [
    check("old", "a", "expired", undefined, "2026-10-08T09:00:00Z"),
    check("new", "a", "completed", good, "2026-10-08T10:00:00Z"),
  ]
  expect(newestFirst(rows).map((row) => row.id)).toEqual(["new", "old"])
})

test("approved services are joined across live sources", () => {
  const services = approvedServices([
    source("a"),
    source("b", {
      scopes: [
        {
          id: "service",
          target: "service.example",
          addresses: ["2001:db8::8"],
          ports: [8443, 443],
          families: ["inet6"],
        },
      ],
    }),
    source("gone", {
      revokedAt: "2026-10-08T11:00:00Z",
      scopes: [{ id: "x", target: "old.example", addresses: [], ports: [25], families: ["inet"] }],
    }),
  ])
  expect(services).toEqual([
    {
      target: "service.example",
      ports: [443, 8443],
      families: ["inet", "inet6"],
      addresses: ["192.0.2.8", "2001:db8::8"],
    },
  ])
})

test("a source says whether it can be asked", () => {
  expect(sourceState(source("a")).word).toBe("Enrolled")
  expect(sourceState(source("a", { enrolledAt: undefined })).word).toBe("Awaiting enrollment")
  expect(sourceState(source("a", { revokedAt: "2026-10-08T11:00:00Z" })).word).toBe("Revoked")
})
