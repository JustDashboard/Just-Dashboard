import { expect, test } from "bun:test"
import { admissionRepairable, admissionState, entryState, flowOf, gatewayState } from "./evidence"

const ready = (fields) => ({
  policy: "installed",
  rules: 4,
  expected: 4,
  forwarding: true,
  admission: "present",
  ready: true,
  reachability: "unverified",
  reason: "",
  ...fields,
})

test("an entry switched off says only that", () => {
  expect(entryState(false, ready({ policy: "missing" }), { verdict: "blocked" })).toEqual({
    label: "Switched off",
    tone: "stopped",
  })
})

test("what is installed is read before what is modelled, and both before what was measured", () => {
  expect(entryState(true, ready({ ready: false }), { verdict: "blocked" })).toEqual({
    label: "Admission missing",
    tone: "warning",
  })
  expect(entryState(true, ready({ reachability: "verified" }), { verdict: "unknown" })).toEqual({
    label: "May be dropped",
    tone: "warning",
  })
  expect(entryState(true, ready({ reachability: "failed" }), { verdict: "clear" })).toEqual({
    label: "Failed from outside",
    tone: "danger",
  })
  expect(entryState(true, ready({}), { verdict: "blocked" })).toEqual({
    label: "Dropped by a layer",
    tone: "danger",
  })
})

test("a sound entry says the best thing known about it", () => {
  expect(entryState(true, ready({ reachability: "verified" }), { verdict: "clear" }).label).toBe(
    "Verified from outside",
  )
  expect(entryState(true, ready({}), { verdict: "clear" })).toEqual({
    label: "Passes every layer",
    tone: "running",
  })
  expect(entryState(true, ready({}), undefined)).toEqual({ label: "Installed", tone: "running" })
  expect(entryState(true, undefined, undefined)).toEqual({ label: "Unknown", tone: "unknown" })
})

test("a flow is found by its entry's kind and id", () => {
  const flows = [
    {
      entry: "forward:3",
      name: "Game server",
      direction: "inbound",
      verdict: "unknown",
      layers: [],
    },
    { entry: "nat:3", name: "Lab", direction: "outbound", verdict: "clear", layers: [] },
  ]
  expect(flowOf(flows, "forward", 3)?.name).toBe("Game server")
  expect(flowOf(flows, "nat", 3)?.name).toBe("Lab")
  expect(flowOf(flows, "forward", 1)).toBeUndefined()
  expect(flowOf(undefined, "forward", 1)).toBeUndefined()
})

test("admission is a word, and repair is offered only for an absent owned rule", () => {
  expect(admissionState({ needed: false, present: false }).label).toBe("Nothing to admit")
  expect(admissionState({ needed: true, present: true })).toEqual({
    label: "Admission present",
    tone: "running",
  })
  const absent = {
    needed: true,
    present: false,
    chains: [
      { family: "inet", tool: "iptables", chain: "DOCKER-USER", needed: true, status: "absent" },
    ],
  }
  expect(admissionState(absent).label).toBe("Admission missing")
  expect(admissionRepairable(absent)).toBe(true)
  const unreadable = {
    needed: true,
    present: false,
    chains: [
      { family: "inet", tool: "iptables", chain: "FORWARD", needed: true, status: "unreadable" },
    ],
  }
  expect(admissionState(unreadable).label).toBe("Admission unreadable")
  expect(admissionRepairable(unreadable)).toBe(false)
  expect(admissionRepairable({ needed: true, present: true })).toBe(false)
})

test("the gateway is in force only when something enabled is in the kernel", () => {
  const entry = (enabled) => ({ enabled })
  expect(gatewayState({ loaded: true, forwards: [], nat: [] }).label).toBe("Nothing passed on")
  expect(gatewayState({ loaded: true, forwards: [entry(false)], nat: [] }).label).toBe(
    "All switched off",
  )
  expect(gatewayState({ loaded: false, forwards: [entry(true)], nat: [] })).toEqual({
    label: "Not loaded",
    tone: "warning",
  })
  expect(gatewayState({ loaded: true, forwards: [], nat: [entry(true)] }).label).toBe("In force")
})
