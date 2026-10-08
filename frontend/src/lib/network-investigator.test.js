import { describe, expect, it } from "bun:test"
import { evidenceCounts, newPathDraft, pathRequest } from "./network-investigator"

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
