import { describe, expect, test } from "bun:test"
import {
  cleanTitle,
  findingProduct,
  groupFindings,
  nearestFix,
  scanLead,
  ageWords,
  atLeast,
  badgeOf,
  countAtLeast,
  countsLine,
  floorWords,
  inFlight,
  scanVerdict,
  shortDigest,
  vulnerable,
} from "./vulnerabilities-model"

const zero = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 }

function scan(overrides = {}) {
  return {
    releaseId: 9,
    releaseNumber: 4,
    environmentId: 12,
    state: "scanned",
    floor: "HIGH",
    summary: {
      counts: { ...zero, critical: 1, high: 2, medium: 3 },
      fixableCounts: { ...zero, critical: 1, high: 1, medium: 3 },
      fixable: [
        {
          id: "CVE-1",
          package: "zlib",
          installed: "1",
          fixed: "2",
          severity: "CRITICAL",
          target: "alpine 3.12.12",
        },
        {
          id: "CVE-2",
          package: "openssl",
          installed: "1",
          fixed: "2",
          severity: "HIGH",
          target: "alpine 3.12.12",
        },
      ],
      unfixed: [
        {
          id: "CVE-3",
          package: "bash",
          installed: "1",
          severity: "HIGH",
          target: "alpine 3.12.12",
        },
      ],
    },
    targets: [],
    baseUpdates: [{ reference: "node:22-alpine", state: "outdated" }],
    newerBase: true,
    rebuildable: true,
    finishedAt: "2026-10-29T09:00:00Z",
    ...overrides,
  }
}

function reading(overrides = {}) {
  return {
    policy: { enabled: true, severityFloor: "HIGH" },
    available: true,
    scanner: "Trivy 0.76.1",
    database: { installed: true },
    releaseId: 9,
    scan: scan(),
    releases: [],
    ...overrides,
  }
}

describe("vulnerabilities", () => {
  test("a floor counts its own severity and every worse one, never an unrated finding", () => {
    expect(atLeast("CRITICAL", "HIGH")).toBe(true)
    expect(atLeast("MEDIUM", "HIGH")).toBe(false)
    expect(atLeast("UNKNOWN", "LOW")).toBe(false)
    const counts = { ...zero, critical: 1, high: 2, medium: 3, low: 4, unknown: 5 }
    expect(countAtLeast(counts, "HIGH")).toBe(3)
    expect(countAtLeast(counts, "LOW")).toBe(10)
    expect(countsLine(counts, "HIGH")).toBe("1 critical and 2 high")
    expect(countsLine(counts)).toBe("1 critical, 2 high, 3 medium and 4 low")
    expect(countsLine(zero)).toBe("")
    expect(floorWords("HIGH")).toBe("high or worse")
    expect(floorWords("CRITICAL")).toBe("critical")
  })

  test("the alert's condition needs a fixable finding at the floor and a newer base", () => {
    expect(vulnerable(scan())).toBe(true)
    expect(vulnerable(scan({ newerBase: false }))).toBe(false)
    expect(vulnerable(scan({ state: "scanning" }))).toBe(false)
    expect(
      vulnerable(
        scan({
          floor: "CRITICAL",
          summary: { ...scan().summary, fixable: [scan().summary.fixable[1]] },
        }),
      ),
    ).toBe(false)
    expect(vulnerable(null)).toBe(false)
    expect(inFlight(scan({ state: "queued" }))).toBe(true)
    expect(inFlight(scan())).toBe(false)
  })

  test("the header says the verdict a reader asks first", () => {
    expect(scanVerdict(reading())).toEqual({ tone: "danger", line: "2 fixable by a newer base" })
    expect(scanVerdict(reading({ scan: scan({ newerBase: false }) }))).toEqual({
      tone: "danger",
      line: "2 have a fix",
    })
    const unfixedOnly = scan({
      newerBase: false,
      summary: { counts: { ...zero, high: 1 }, fixableCounts: zero, fixable: [], unfixed: [] },
    })
    expect(scanVerdict(reading({ scan: unfixedOnly }))).toEqual({
      tone: "warning",
      line: "1 with no fix yet",
    })
    const clean = scan({
      summary: { counts: { ...zero, low: 2 }, fixableCounts: zero, fixable: [], unfixed: [] },
    })
    expect(scanVerdict(reading({ scan: clean }))).toEqual({
      tone: "running",
      line: "No high or worse vulnerabilities",
    })
    expect(scanVerdict(reading({ policy: { enabled: false, severityFloor: "HIGH" } })).line).toBe(
      "Scanning is off for this environment",
    )
    expect(scanVerdict(reading({ available: false, reason: "Docker is unavailable" })).line).toBe(
      "Docker is unavailable",
    )
    expect(scanVerdict(reading({ scan: null })).line).toBe("Not scanned yet")
    expect(
      scanVerdict(reading({ scan: scan({ state: "scanning", finishedAt: undefined }) })).line,
    ).toBe("Scanning")
    // A rescan in flight keeps the last verdict on screen.
    expect(scanVerdict(reading({ scan: scan({ state: "scanning" }) })).tone).toBe("danger")
  })

  test("a release's row is marked only when its scan found something at the floor", () => {
    const badge = {
      releaseId: 9,
      state: "scanned",
      floor: "HIGH",
      counts: { ...zero, critical: 1, high: 2 },
      atFloor: 3,
      fixable: 2,
      newerBase: true,
    }
    expect(badgeOf(badge)).toEqual({
      label: "2 fixable",
      tone: "danger",
      title:
        "1 critical and 2 high at high or worse, 2 with a published fix; a newer base image is out, so a rebuild may fix them.",
    })
    expect(badgeOf({ ...badge, fixable: 0, newerBase: false })).toEqual({
      label: "3 unfixed",
      tone: "warning",
      title: "1 critical and 2 high at high or worse, none with a published fix yet.",
    })
    expect(badgeOf({ ...badge, atFloor: 0 })).toBeUndefined()
    expect(badgeOf(undefined)).toBeUndefined()
  })

  test("ages and digests read short", () => {
    expect(ageWords(0.2)).toBe("under an hour old")
    expect(ageWords(1)).toBe("1 hour old")
    expect(ageWords(9.4)).toBe("9 hours old")
    expect(ageWords(72)).toBe("3 days old")
    expect(ageWords(undefined)).toBe("")
    expect(shortDigest("sha256:0123456789abcdef0123")).toBe("0123456789ab")
    expect(shortDigest(undefined)).toBe("")
  })
})

describe("the findings as upgrades", () => {
  const finding = (overrides) => ({
    id: "CVE-1",
    package: "undici",
    installed: "6.28.0",
    fixed: "6.29.1, 7.30.1, 8.12.2",
    severity: "HIGH",
    target: "Node.js",
    class: "lang-pkgs",
    ...overrides,
  })

  test("a title loses the package Trivy repeats in front of it", () => {
    expect(cleanTitle("undici: undici: Denial of Service", "undici")).toBe("Denial of Service")
    expect(cleanTitle("a flaw in zlib", "zlib")).toBe("A flaw in zlib")
    expect(cleanTitle(undefined, "zlib")).toBeUndefined()
    expect(cleanTitle("undici:", "undici")).toBeUndefined()
  })

  test("the fix is the one on the line already installed", () => {
    expect(nearestFix("6.28.0", "6.29.1, 7.30.1, 8.12.2")).toBe("6.29.1")
    expect(nearestFix("5.0.9", "5.0.10, 3.0.7, 2.1.5, 1.1.19")).toBe("5.0.10")
    expect(nearestFix("2.0.0", "3.1.0, 4.0.1")).toBe("3.1.0")
    expect(nearestFix("1.2.13-r0", "1.2.13-r1")).toBe("1.2.13-r1")
    expect(nearestFix("1.0.0", undefined)).toBeUndefined()
  })

  test("one package is one upgrade, to the version that fixes all of it, worst first", () => {
    const groups = groupFindings([
      finding({
        id: "CVE-MED",
        package: "tar",
        installed: "6.0.0",
        fixed: "6.2.1",
        severity: "MEDIUM",
      }),
      finding({ id: "CVE-A", fixed: "6.28.1, 7.29.1" }),
      finding({ id: "CVE-B" }),
      finding({ id: "CVE-C", severity: "CRITICAL", fromBase: true }),
    ])
    expect(groups.map((group) => group.package)).toEqual(["undici", "tar"])
    const [undici] = groups
    expect(undici.fix).toBe("6.29.1")
    expect(undici.worst).toBe("CRITICAL")
    expect(undici.fromBase).toBe(true)
    expect(undici.counts).toMatchObject({ critical: 1, high: 2 })
    expect(undici.findings.map((item) => item.id)).toEqual(["CVE-A", "CVE-B", "CVE-C"])
  })

  test("a version-major compare is numeric, not alphabetical", () => {
    const [group] = groupFindings([
      finding({ installed: "5.0.9", fixed: "5.0.9" }),
      finding({ id: "CVE-2", installed: "5.0.9", fixed: "5.0.10" }),
    ])
    expect(group.fix).toBe("5.0.10")
  })

  test("the mark is the distribution or the ecosystem", () => {
    expect(findingProduct({ class: "os-pkgs", target: "alpine 3.19.1" })).toBe("alpine")
    expect(findingProduct({ class: "os-pkgs", target: "something 1" })).toBe("linux")
    expect(findingProduct({ class: "lang-pkgs", target: "web · Node.js" })).toBe("npm")
    expect(findingProduct({ class: "lang-pkgs", target: "app/go.mod" })).toBe("go")
    expect(findingProduct({ class: "lang-pkgs", target: "app/requirements.txt" })).toBe("python")
    expect(findingProduct({ class: "lang-pkgs", target: "app/unknown" })).toBeUndefined()
  })

  test("the sentence says how many and how many can be fixed", () => {
    const scan = (counts, fixableCounts) => ({
      releaseNumber: 19,
      summary: { counts, fixableCounts, fixable: [], unfixed: [] },
    })
    const zero = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 }
    const some = { ...zero, high: 6, medium: 14, low: 1 }
    expect(scanLead(scan(some, some))).toEqual({
      title: "21 known vulnerabilities in release 19",
      fix: "All 21 have a fix",
    })
    expect(scanLead(scan(some, { ...zero, high: 6 })).fix).toBe("6 of 21 have a fix")
    expect(scanLead(scan(some, { ...zero, high: 1 })).fix).toBe("1 of 21 has a fix")
    expect(scanLead(scan({ ...zero, low: 1 }, zero))).toEqual({
      title: "1 known vulnerability in release 19",
      fix: "None has a fix yet",
    })
    expect(scanLead(scan(zero, zero))).toEqual({
      title: "No known vulnerabilities in release 19",
      fix: undefined,
    })
  })
})
