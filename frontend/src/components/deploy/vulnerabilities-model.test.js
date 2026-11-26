import { describe, expect, test } from "bun:test"
import {
  cleanTitle,
  dependencyWords,
  findingProduct,
  groupFindings,
  nearestFix,
  originOf,
  overviewLead,
  scanActions,
  scanLead,
  upgradeTitle,
  ageWords,
  atLeast,
  badgeOf,
  countAtLeast,
  countsLine,
  floorWords,
  inFlight,
  reportReachable,
  scanVerdict,
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
          class: "os-pkgs",
          fromBase: true,
          origin: "base",
          action: "rebuild",
        },
        {
          id: "CVE-2",
          package: "openssl",
          installed: "1",
          fixed: "2",
          severity: "HIGH",
          target: "alpine 3.12.12",
          class: "os-pkgs",
          fromBase: true,
          origin: "base",
          action: "rebuild",
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

  test("the alert's condition needs a fixable finding the base carries and a newer base", () => {
    expect(vulnerable(scan())).toBe(true)
    expect(vulnerable(scan({ newerBase: false }))).toBe(false)
    // esbuild's Go is not the base's to fix, however new the base.
    const tooling = {
      id: "CVE-2024-24790",
      package: "stdlib",
      installed: "v1.20.7",
      fixed: "1.21.11",
      severity: "CRITICAL",
      target: "app/node_modules/@esbuild/linux-x64/bin/esbuild",
      class: "lang-pkgs",
      dependency: "development",
    }
    expect(vulnerable(scan({ summary: { ...scan().summary, fixable: [tooling] } }))).toBe(false)
    expect(vulnerable(scan({ state: "scanning" }))).toBe(false)
    // Another base moved, not the one that carries these: the server gives
    // them no rebuild, and a newer Bun is no fix for what Node carries.
    const otherBaseMoved = scan({
      summary: {
        ...scan().summary,
        fixable: scan().summary.fixable.map((finding) => ({ ...finding, action: undefined })),
      },
    })
    expect(vulnerable(otherBaseMoved)).toBe(false)
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

  test("the report is a link away as soon as the live release can be scanned", () => {
    // Scan now is the report's: the first scan is asked for there.
    expect(reportReachable(reading({ scan: null }))).toBe(true)
    expect(
      reportReachable(reading({ scan: scan({ state: "queued", finishedAt: undefined }) })),
    ).toBe(true)
    expect(reportReachable(reading({ scan: null, releaseId: undefined }))).toBe(false)
    expect(reportReachable(reading({ scan: null, available: false }))).toBe(false)
    expect(
      reportReachable(reading({ scan: null, policy: { enabled: false, severityFloor: "HIGH" } })),
    ).toBe(false)
    // A scan already read keeps its report, whatever the terms say now.
    expect(reportReachable(reading({ policy: { enabled: false, severityFloor: "HIGH" } }))).toBe(
      true,
    )
    expect(reportReachable(undefined)).toBe(false)
  })

  test("the header says what there is to do, and is quiet when there is nothing", () => {
    expect(scanVerdict(reading())).toEqual({ tone: "danger", line: "2 to fix" })
    // With no newer base, the base's fixes are not the reader's to take.
    const waiting = scan({
      newerBase: false,
      summary: {
        ...scan().summary,
        fixable: scan().summary.fixable.map((finding) => ({ ...finding, action: undefined })),
      },
    })
    expect(scanVerdict(reading({ scan: waiting }))).toEqual({
      tone: "running",
      line: "Nothing high or worse to fix",
    })
    const clean = scan({
      summary: { counts: { ...zero, low: 2 }, fixableCounts: zero, fixable: [], unfixed: [] },
    })
    expect(scanVerdict(reading({ scan: clean })).tone).toBe("running")
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

  test("a release's row is marked only when something on it can be fixed", () => {
    const badge = {
      releaseId: 9,
      state: "scanned",
      floor: "HIGH",
      counts: { ...zero, critical: 1, high: 2 },
      atFloor: 3,
      fixable: 3,
      actionable: 2,
      newerBase: true,
    }
    expect(badgeOf(badge)).toEqual({
      label: "2 to fix",
      tone: "danger",
      title: "2 high or worse that an upgrade, or a rebuild on the newer base image, fixes.",
    })
    expect(badgeOf({ ...badge, newerBase: false }).title).toBe(
      "2 high or worse that an upgrade or a rebuild fixes.",
    )
    // 67 "fixable" in esbuild's Go and the newest base: nothing on the row.
    expect(badgeOf({ ...badge, actionable: 0 })).toBeUndefined()
    expect(badgeOf({ ...badge, actionable: undefined })).toBeUndefined()
    expect(badgeOf(undefined)).toBeUndefined()
  })

  test("ages and digests read short", () => {
    expect(ageWords(0.2)).toBe("under an hour old")
    expect(ageWords(1)).toBe("1 hour old")
    expect(ageWords(9.4)).toBe("9 hours old")
    expect(ageWords(72)).toBe("3 days old")
    expect(ageWords(undefined)).toBe("")
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
    expect(
      findingProduct({
        class: "lang-pkgs",
        target: "app/node_modules/@esbuild/linux-x64/bin/esbuild",
        package: "stdlib",
      }),
    ).toBe("go")
  })

  test("the report's sentence says how many and whose they are", () => {
    const zero = { critical: 0, high: 0, medium: 0, low: 0, unknown: 0 }
    const scan = (counts, origins) => ({
      releaseNumber: 19,
      floor: "HIGH",
      targets: [{ kind: "base", reference: "node:22-alpine", state: "scanned", counts: zero }],
      summary: { counts, fixableCounts: counts, fixable: [], unfixed: [] },
      origins,
    })
    const some = { ...zero, high: 6, medium: 14, low: 1 }
    expect(
      scanLead(
        scan(some, { app: zero, development: { ...zero, high: 20 }, base: { ...zero, low: 1 } }),
      ),
    ).toEqual({
      title: "21 known vulnerabilities in release 19",
      where: "20 in development tools shipped with it and 1 in the node:22-alpine base image",
    })
    expect(scanLead(scan({ ...zero, low: 1 }, undefined))).toEqual({
      title: "1 known vulnerability in release 19",
      where: undefined,
    })
    expect(scanLead(scan(zero, undefined)).title).toBe("No known vulnerabilities in release 19")
  })
})

/** The release the operator saw: 183 findings, none theirs to fix. */
describe("what the overview says", () => {
  const counts = (overrides) => ({
    critical: 0,
    high: 0,
    medium: 0,
    low: 0,
    unknown: 0,
    ...overrides,
  })
  const esbuild = {
    id: "CVE-2024-24790",
    package: "stdlib",
    installed: "v1.20.7",
    fixed: "1.21.11, 1.22.4",
    severity: "CRITICAL",
    target: "app/node_modules/@esbuild/linux-x64/bin/esbuild",
    path: "app/node_modules/@esbuild/linux-x64/bin/esbuild",
    class: "lang-pkgs",
    dependency: "development",
    via: "drizzle-kit",
    origin: "development",
  }
  const npmGlob = {
    id: "CVE-2025-1",
    package: "brace-expansion",
    installed: "2.0.2",
    fixed: "2.0.3",
    severity: "HIGH",
    target: "Node.js",
    path: "usr/local/lib/node_modules/npm/node_modules/brace-expansion/package.json",
    class: "lang-pkgs",
    dependency: "indirect",
    via: "npm",
    fromBase: true,
    origin: "base",
  }
  const quiet = {
    releaseNumber: 1,
    state: "scanned",
    floor: "HIGH",
    newerBase: false,
    rebuildable: true,
    targets: [{ kind: "base", reference: "node:22-alpine", state: "scanned", counts: counts() }],
    baseUpdates: [{ reference: "node:22-alpine", state: "current" }],
    summary: {
      counts: counts({ critical: 3, high: 64, medium: 104, low: 12 }),
      fixableCounts: counts({ critical: 3, high: 64, medium: 104, low: 12 }),
      fixable: [esbuild, npmGlob],
      unfixed: [],
    },
    origins: {
      app: counts({ medium: 1 }),
      development: counts({ critical: 3, high: 54, medium: 84, low: 7 }),
      base: counts({ high: 10, medium: 19, low: 5 }),
    },
    actionable: counts(),
  }

  test("nothing to fix says where the findings are, not that there are 183 of them", () => {
    expect(overviewLead(quiet)).toEqual({
      title: "Nothing high or worse to fix in release 1",
      where:
        "183 known: 1 in your app's packages, 148 in development tools shipped with it and 34 in the node:22-alpine base image.",
    })
    expect(scanActions(quiet)).toEqual([])
    expect(originOf(esbuild)).toBe("development")
    expect(originOf({ ...esbuild, origin: undefined, dependency: undefined })).toBe("app")
    expect(originOf({ ...npmGlob, origin: undefined })).toBe("base")
  })

  test("an own package with no fix yet is said, not counted as something to do", () => {
    const unfixed = { ...npmGlob, fromBase: false, origin: "app", fixed: undefined, path: "app/x" }
    expect(
      overviewLead({ ...quiet, summary: { ...quiet.summary, unfixed: [unfixed] } }).where,
    ).toContain("1 high or worse in your app's packages has no fix yet.")
  })

  test("what can be done is a rebuild first, then one upgrade per package", () => {
    const next = {
      id: "CVE-2026-9",
      package: "next",
      installed: "16.4.6",
      fixed: "16.4.7",
      severity: "HIGH",
      target: "Node.js",
      path: "app/node_modules/next/package.json",
      class: "lang-pkgs",
      dependency: "direct",
      via: "next",
      origin: "app",
      action: "upgrade",
    }
    const zlib = {
      id: "CVE-2022-37434",
      package: "zlib",
      installed: "1.2.13-r0",
      fixed: "1.2.13-r1",
      severity: "CRITICAL",
      target: "alpine 3.19.1",
      class: "os-pkgs",
      fromBase: true,
      origin: "base",
      action: "rebuild",
    }
    const scan = {
      ...quiet,
      newerBase: true,
      summary: { ...quiet.summary, fixable: [zlib, esbuild, next, npmGlob] },
      actionable: counts({ critical: 1, high: 1 }),
    }
    const actions = scanActions(scan)
    expect(actions.map((action) => action.kind)).toEqual(["rebuild", "upgrade"])
    expect(actions[0]).toMatchObject({ packages: ["zlib"], newerBase: true, worst: "CRITICAL" })
    expect(upgradeTitle(actions[1].group)).toBe("Upgrade next to 16.4.7")
    expect(dependencyWords(actions[1].group)).toBe("in package.json")
    expect(overviewLead(scan).title).toBe("2 high or worse to fix in release 1")
  })

  test("a list filled by a development tool's findings still says what there is to fix", () => {
    // The server cut the list at 200 criticals from esbuild's Go; the count of
    // what can be done was read before the cut.
    const crowded = {
      ...quiet,
      newerBase: true,
      summary: { ...quiet.summary, fixable: Array.from({ length: 200 }, () => esbuild) },
      actionable: counts({ high: 1 }),
    }
    expect(overviewLead(crowded).title).toBe("1 high or worse to fix in release 1")
    expect(
      scanVerdict({
        policy: { enabled: true, severityFloor: "HIGH" },
        available: true,
        scan: { ...crowded, finishedAt: "2026-10-29T09:00:00Z" },
      }),
    ).toEqual({ tone: "danger", line: "1 to fix" })
    // A medium the reader can act on is not counted at a high floor.
    expect(overviewLead({ ...crowded, actionable: counts({ medium: 2 }) }).title).toBe(
      "Nothing high or worse to fix in release 1",
    )
  })

  test("Go's standard library is the Go a binary was built with", () => {
    const [group] = groupFindings([{ ...esbuild, origin: "app", dependency: undefined }])
    expect(upgradeTitle(group)).toBe("Build with Go 1.21.11")
    expect(dependencyWords({ package: "postcss", dependency: "indirect", via: "next" })).toBe(
      "comes with next",
    )
    expect(dependencyWords({ package: "x", dependency: undefined })).toBe("")
  })
})
