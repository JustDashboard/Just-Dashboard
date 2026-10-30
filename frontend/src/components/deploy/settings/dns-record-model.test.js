import { describe, expect, test } from "bun:test"
import {
  byHand,
  changeVerb,
  currentLine,
  managedLine,
  opensCertificates,
  recordButton,
  recordState,
  refusalTitle,
  setLine,
  ttlLabel,
  whereLine,
} from "./dns-record-model"

function plan(overrides) {
  return {
    hostname: "app.example.com",
    environmentId: 2,
    environment: "production",
    tunnel: false,
    want: [{ name: "app.example.com", type: "A", values: ["203.0.113.7"], ttl: 300 }],
    candidates: [],
    problems: [],
    changes: [],
    writable: true,
    ready: false,
    foreign: false,
    summary: "",
    managed: [],
    provider: "Cloudflare",
    zone: "example.com",
    ...overrides,
  }
}

function change(overrides) {
  return {
    type: "A",
    name: "app.example.com",
    action: "create",
    current: [],
    want: ["203.0.113.7"],
    detail: "",
    ours: false,
    ...overrides,
  }
}

describe("where the record stands", () => {
  test("missing, elsewhere, here, at the tunnel, or not writable from here", () => {
    expect(recordState(plan({ changes: [change()] }))).toEqual({ tone: "notice", label: "Missing" })
    expect(
      recordState(plan({ changes: [change({ action: "update", current: ["198.51.100.9"] })] }))
        .label,
    ).toBe("Names another server")
    expect(recordState(plan({ ready: true, writable: false })).label).toBe("Points here")
    expect(recordState(plan({ ready: true, tunnel: true })).label).toBe("Points at the tunnel")
    expect(recordState(plan({ refusalCode: "conflict", writable: false })).label).toBe(
      "Another record is in the way",
    )
    expect(recordState(plan({ refusalCode: "no_credentials", writable: false }))).toEqual({
      tone: "warning",
      label: "Cannot be written from here",
    })
  })

  test("the press says what it does, and a record not written here is replaced", () => {
    expect(recordButton(plan({ changes: [change()] }))).toBe("Create the record")
    expect(recordButton(plan({ changes: [change({ action: "update", ours: true })] }))).toBe(
      "Correct the record",
    )
    expect(recordButton(plan({ foreign: true, changes: [change({ action: "update" })] }))).toBe(
      "Replace the record",
    )
  })
})

describe("each set's line", () => {
  test("the record, the word beside it and what the zone holds now", () => {
    expect(setLine({ type: "AAAA", name: "app.example.com", values: ["2001:db8::7"] })).toBe(
      "AAAA  app.example.com  →  2001:db8::7",
    )
    expect(changeVerb(change())).toBe("To create")
    expect(changeVerb(change({ action: "update", ours: true }))).toBe("To correct")
    expect(changeVerb(change({ action: "update" }))).toBe("To replace")
    expect(changeVerb(change({ action: "keep", ours: true }))).toBe("Managed here")
    expect(changeVerb(change({ action: "keep" }))).toBe("Already right")
    expect(changeVerb(change({ action: "stale" }))).toBe("Left as it is")
    expect(currentLine(change())).toBe("Nothing at the name yet")
    expect(currentLine(change({ action: "update", current: ["198.51.100.9"] }))).toBe(
      "Now 198.51.100.9",
    )
    expect(currentLine(change({ action: "keep", current: ["203.0.113.7"] }))).toBe(null)
  })

  test("a TTL as it is read", () => {
    expect(ttlLabel(1)).toBe("TTL auto")
    expect(ttlLabel(300)).toBe("TTL 5 min")
    expect(ttlLabel(3600)).toBe("TTL 1 h")
    expect(ttlLabel(45)).toBe("TTL 45 s")
    expect(ttlLabel(undefined)).toBe("")
  })
})

describe("who manages it", () => {
  const now = new Date()
  const hoursAgo = (hours) => new Date(now.getTime() - hours * 3600_000).toISOString()

  test("created by whom and when, and changed since", () => {
    const row = {
      type: "A",
      values: ["203.0.113.7"],
      ttl: 300,
      provider: "cloudflare",
      zone: "example.com",
      createdBy: "alice",
      createdAt: hoursAgo(3),
      updatedBy: "alice",
      updatedAt: hoursAgo(3),
      current: true,
    }
    expect(managedLine([row])).toBe("Record managed here · created 3h ago by alice")
    expect(
      managedLine([row, { ...row, type: "AAAA", updatedBy: "bob", updatedAt: hoursAgo(1) }]),
    ).toBe("Record managed here · created 3h ago by alice · changed 1h ago by bob")
    expect(managedLine([{ ...row, current: false }])).toBe(
      "Written here before, and changed since outside this dashboard",
    )
    expect(managedLine([])).toBe(null)
  })

  test("where it is written, and when Certificates is the answer", () => {
    expect(whereLine(plan({ account: "Agency" }))).toBe(
      "Zone example.com at Cloudflare (Agency), with the credentials saved under Certificates",
    )
    expect(whereLine(plan({ zone: undefined }))).toBe(null)
    expect(opensCertificates(plan({ refusalCode: "no_zone" }))).toBe(true)
    expect(opensCertificates(plan({ refusalCode: "tunnel_zone" }))).toBe(false)
  })
})

describe("what cannot be written from here", () => {
  test("the notice names what stands in the way, and the records to make by hand", () => {
    const refused = plan({ refusalCode: "no_zone", writable: false, changes: [] })
    expect(refusalTitle(refused)).toBe("No saved credential reaches this zone")
    expect(refusalTitle(plan({ refusalCode: "tunnel_zone" }))).toBe("The zone is not on Cloudflare")
    expect(refusalTitle(plan({ refusalCode: "conflict" }))).toBe("Nothing here deletes a record")
    expect(byHand(refused)).toEqual(refused.want)
    // A conflict draws the zone's own lines instead, and a writable plan has none to make.
    expect(
      byHand(plan({ refusalCode: "conflict", changes: [change({ action: "conflict" })] })),
    ).toEqual([])
    expect(byHand(plan())).toEqual([])
  })
})
