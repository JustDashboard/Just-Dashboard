import { describe, expect, it } from "bun:test"
import {
  evidenceCounts,
  investigateHref,
  newPathDraft,
  pathDraftFromQuery,
  pathRequest,
} from "./network-investigator"

describe("connection path requests", () => {
  it("retains a container, chosen DNS address, source, family and port", () => {
    expect(
      pathRequest({
        ...newPathDraft(),
        source: "a".repeat(64),
        target: "private.corp",
        sourceAddress: "2001:db8::2",
        address: "2001:db8::9",
        family: "inet6",
        port: "8443",
        measure: true,
      }),
    ).toEqual({
      sourceKind: "container",
      containerId: "a".repeat(64),
      target: "private.corp",
      sourceAddress: "2001:db8::2",
      address: "2001:db8::9",
      family: "inet6",
      protocol: "tcp",
      port: 8443,
      measure: true,
    })
  })
  it("requires an explicit supported measurement opt-in", () => {
    expect(pathRequest({ ...newPathDraft(), target: "192.0.2.8" })?.measure).toBe(false)
    expect(
      pathRequest({ ...newPathDraft(), target: "192.0.2.8", protocol: "udp", measure: true })
        ?.measure,
    ).toBe(false)
    expect(
      pathRequest({ ...newPathDraft(), target: "192.0.2.8", mark: "0x1", measure: true })?.measure,
    ).toBe(false)
  })
  it("refuses malformed drafts before sending", () => {
    for (const change of [
      { target: "$(id)" },
      { target: "--help" },
      { port: "65536" },
      { port: "443junk" },
      { source: "short-id" },
      { mark: "0x1/0xff" },
    ]) {
      expect(pathRequest({ ...newPathDraft(), target: "private.corp", ...change })).toBeUndefined()
    }
  })
  it("keeps observed, modeled, measured and unknown counts separate", () => {
    const evidence = ["observed", "modeled", "unknown", "measured", "unknown"].map((basis) => ({
      basis,
    }))
    expect(evidenceCounts(evidence)).toEqual({ observed: 1, modeled: 1, measured: 1, unknown: 2 })
  })
})

describe("links into the investigator", () => {
  it("carries a stream's tuple and opens the form on it", () => {
    const href = investigateHref({ target: "127.0.0.1", port: 6432, measure: true })
    expect(href).toBe(
      "/network/investigate?target=127.0.0.1&port=6432&protocol=tcp&family=inet&measure=1",
    )
    const params = new URL(href, "http://x").searchParams
    expect(pathDraftFromQuery(params)).toEqual({
      ...newPathDraft(),
      target: "127.0.0.1",
      port: "6432",
      measure: true,
    })
    expect(investigateHref({ target: "::1", port: 53, protocol: "udp" })).toBe(
      "/network/investigate?target=%3A%3A1&port=53&protocol=udp&family=inet6",
    )
  })
  it("keeps the blank form for anything the form could not have produced", () => {
    for (const query of [
      "target=$(id)&port=1",
      "target=a.b&port=70000",
      "target=--help&port=22",
      "port=22",
    ]) {
      expect(pathDraftFromQuery(new URLSearchParams(query))).toEqual(newPathDraft())
    }
    const udp = pathDraftFromQuery(new URLSearchParams("target=a.b&port=53&protocol=udp&measure=1"))
    expect(udp.measure).toBe(false)
    expect(pathDraftFromQuery(new URLSearchParams("target=a.b&port=x")).port).toBe("443")
  })
})
