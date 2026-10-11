import { expect, test } from "bun:test"
import { dnsLogSources } from "./log-sources"

const view = (over = {}) => ({
  resolved: { installed: false },
  listeners: [],
  adblock: [],
  ...over,
})

const listener = (over) => ({
  address: "0.0.0.0",
  protocol: "udp",
  loopback: false,
  kind: "other",
  ...over,
})

const setup = (over = {}) => ({
  id: "a1b2",
  request: { name: "Home DNS", engine: "adguard" },
  resources: { containerName: "jd-dns-a1b2", containerId: "f".repeat(64) },
  state: "verified",
  ...over,
})

test("systemd-resolved is read when it is installed, running or not", () => {
  expect(dnsLogSources(view({ resolved: { installed: true } }))).toEqual([
    { id: "journal:systemd-resolved.service", label: "systemd-resolved.service", kind: "journal" },
  ])
  expect(dnsLogSources(view())).toEqual([])
})

test("a resolver program on the host is read by its unit, once, and only for the kinds with one", () => {
  const sources = dnsLogSources(
    view({
      listeners: [
        listener({ kind: "dnsmasq" }),
        listener({ kind: "dnsmasq", protocol: "tcp" }),
        listener({ kind: "unbound" }),
        listener({ kind: "named" }),
        listener({ kind: "resolved-stub" }),
        listener({ kind: "other" }),
      ],
    }),
  )
  expect(sources.map((s) => [s.id, s.product])).toEqual([
    ["journal:dnsmasq.service", undefined],
    ["journal:unbound.service", "unbound"],
    ["journal:named.service", undefined],
  ])
})

test("a container is read once, by the most particular name the page has for it", () => {
  const sources = dnsLogSources(
    view({
      listeners: [
        listener({ kind: "docker-proxy", container: "adguard" }),
        listener({ kind: "docker-proxy", container: "adguard", protocol: "tcp" }),
        listener({ kind: "unbound", container: "resolver" }),
        listener({ kind: "docker-proxy", container: "jd-dns-a1b2" }),
      ],
      adblock: [
        { kind: "adguardhome", name: "AdGuard Home", runsAs: "container", container: "adguard" },
        { kind: "pihole", name: "Pi-hole", runsAs: "process" },
      ],
    }),
    [setup()],
  )
  expect(sources.map((s) => [s.id, s.label, s.product])).toEqual([
    ["docker:jd-dns-a1b2", "Home DNS", "adguard"],
    ["docker:adguard", "AdGuard Home", "adguard"],
    ["docker:resolver", "resolver", "unbound"],
  ])
  expect(sources.every((s) => s.kind === "docker")).toBe(true)
})

test("a setup with no container yet, or none any more, has no log; an engine without a mark is Docker's", () => {
  const sources = dnsLogSources(view(), [
    setup({ state: "planned", resources: { containerName: "jd-dns-x", containerId: undefined } }),
    setup({ state: "removed" }),
    setup({ request: { name: "Lab", engine: "technitium" } }),
  ])
  expect(sources.map((s) => [s.label, s.product])).toEqual([["Lab", "docker"]])
})

test("the host's unit, the programs and the containers are listed in that order, no lens named", () => {
  const sources = dnsLogSources(
    view({
      resolved: { installed: true },
      listeners: [
        listener({ kind: "dnsmasq" }),
        listener({ kind: "docker-proxy", container: "adguard" }),
      ],
    }),
  )
  expect(sources.map((s) => s.id)).toEqual([
    "journal:systemd-resolved.service",
    "journal:dnsmasq.service",
    "docker:adguard",
  ])
  expect(sources.some((s) => s.lens)).toBe(false)
})
