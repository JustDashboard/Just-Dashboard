import { expect, test } from "bun:test"
import {
  externalFor,
  externalWords,
  pendingCheck,
  scopesFor,
  unreadSources,
} from "./ports-reachability"

const evidence = (over) => ({
  checkId: "c",
  vantageId: "a",
  source: "Probe A",
  placement: "external_host",
  port: 5432,
  family: "inet",
  tls: false,
  local: false,
  status: "completed",
  state: "connected",
  basis: "measured",
  at: "2026-10-09T12:00:00Z",
  ...over,
})

const external = {
  checkedAt: "",
  evidence: [
    evidence({ checkId: "old", at: "2026-10-09T10:00:00Z", state: "failed" }),
    evidence({ checkId: "new", address: "203.0.113.9" }),
    evidence({
      checkId: "six",
      family: "inet6",
      vantageId: "b",
      status: "expired",
      basis: "unknown",
      state: "unknown",
    }),
    evidence({ checkId: "other", port: 443 }),
  ],
  scopes: [
    {
      vantageId: "a",
      source: "Probe A",
      placement: "external_host",
      scopeId: "service",
      target: "shop",
      addresses: [],
      ports: [443, 5432],
      families: ["inet"],
    },
    {
      vantageId: "b",
      source: "Probe B",
      placement: "controlled_fixture",
      scopeId: "lab",
      target: "lab",
      addresses: [],
      ports: [22],
      families: ["inet6"],
    },
  ],
}

test("a socket's external evidence is its port's newest per source and family", () => {
  const socket = { port: 5432, protocol: "tcp", family: "ipv4", twin: { family: "ipv6" } }
  expect(externalFor(external, socket).map((e) => e.checkId)).toEqual(["new", "six"])
  expect(externalFor(external, { ...socket, protocol: "udp" })).toEqual([])
  expect(
    externalFor(external, { port: 5432, protocol: "tcp", family: "ipv6" }).map((e) => e.checkId),
  ).toEqual(["six"])
})

test("only scopes naming the port and the socket's family can be asked", () => {
  expect(
    scopesFor(external, { port: 5432, protocol: "tcp", family: "ipv4" }).map(
      (s) => s.scope.vantageId,
    ),
  ).toEqual(["a"])
  expect(scopesFor(external, { port: 22, protocol: "tcp", family: "ipv4" })).toEqual([])
})

test("words never claim more than the source measured", () => {
  expect(externalWords(evidence({ address: "203.0.113.9" }))).toBe(
    "Connected to 203.0.113.9, not on this host",
  )
  expect(externalWords(evidence({ local: true, address: "198.51.100.4", state: "refused" }))).toBe(
    "Refused at this host's 198.51.100.4",
  )
  expect(externalWords(evidence({ status: "expired", basis: "unknown", state: "unknown" }))).toBe(
    "Expired unmeasured — reachability unknown",
  )
  expect(pendingCheck([evidence({ status: "queued" })])).toBe(true)
})

test("sources that were not read are listed, provider reservations included", () => {
  expect(
    unreadSources([
      { key: "sockets", label: "Listening sockets", state: "checked" },
      { key: "provider", label: "Provider reservations", state: "not_supplied" },
    ]).map((s) => s.key),
  ).toEqual(["provider"])
})

test("a socket bound to one address takes only measurements of that address", () => {
  const bound = { port: 5432, protocol: "tcp", family: "ipv4", address: "198.51.100.4" }
  expect(externalFor(external, bound)).toEqual([])
  expect(
    externalFor(
      {
        ...external,
        evidence: [evidence({ checkId: "here", address: "198.51.100.4", local: true })],
      },
      bound,
    ).map((e) => e.checkId),
  ).toEqual(["here"])
  expect(
    externalFor(external, { port: 5432, protocol: "tcp", family: "ipv4", address: "0.0.0.0" }).map(
      (e) => e.checkId,
    ),
  ).toEqual(["new"])
})
