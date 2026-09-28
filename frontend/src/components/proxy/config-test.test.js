import { describe, expect, test } from "bun:test"
import {
  diagnosticLine,
  engineRoots,
  testedLabel,
  testMeaning,
  testReason,
  testVerdict,
  warningCount,
} from "./config-test"
import { CONFIG_TEST, engineFindings } from "./findings/engine"
import { foldProxyFindings, findingAction } from "./attention"

const conflict = {
  level: "warn",
  message: 'conflicting server name "a.test" on 0.0.0.0:80, ignored',
}
const deprecated = {
  level: "warn",
  message: 'the "listen ... http2" directive is deprecated, use the "http2" directive instead',
  file: "/etc/nginx/sites-available/app",
  line: 4,
}
const unknown = {
  level: "emerg",
  message: 'unknown directive "frobnicate"',
  file: "/etc/nginx/sites-available/app",
  line: 3,
}
const passed = (diagnostics = []) => ({
  valid: true,
  output: "nginx: configuration file /etc/nginx/nginx.conf test is successful",
  command: "nginx -t",
  diagnostics,
  warnings: diagnostics.filter((d) => d.level === "warn").length,
})
const failed = (diagnostics) => ({
  valid: false,
  output: "nginx: configuration file /etc/nginx/nginx.conf test failed",
  command: "nginx -t",
  diagnostics,
  warnings: 0,
})
const record = (validation) => ({
  kind: "nginx",
  checkedAt: "2026-09-28T02:00:00Z",
  validation,
})

describe("the verdict", () => {
  // nginx exits 0 through a warning, so a pass is two verdicts, not one.
  test("is valid, valid with warnings, or fails", () => {
    expect(testVerdict(passed())).toEqual({ label: "Valid", tone: "success" })
    expect(testVerdict(passed([conflict]))).toEqual({
      label: "Valid with 1 warning",
      tone: "warning",
    })
    expect(testVerdict(passed([conflict, deprecated]))).toEqual({
      label: "Valid with 2 warnings",
      tone: "warning",
    })
    expect(testVerdict(failed([unknown]))).toEqual({ label: "Fails", tone: "danger" })
  })

  test("counts warnings from the diagnostics where the answer has no count", () => {
    const uncounted = { ...passed([conflict, deprecated]), warnings: undefined }
    expect(warningCount(uncounted)).toBe(2)
    expect(warningCount({ valid: true, output: "", command: "nginx -t" })).toBe(0)
  })

  test("says what it means for the engine, and after a reload what the reload did", () => {
    expect(testMeaning("nginx", passed(), "test")).toBe("A reload would succeed.")
    expect(testMeaning("nginx", passed([conflict]), "last")).toBe(
      "A reload would succeed. nginx accepts this configuration, but a warning can mean part of it is ignored.",
    )
    expect(testMeaning("nginx", passed([conflict]), "reload")).toBe(
      "nginx reloaded. nginx accepts this configuration, but a warning can mean part of it is ignored.",
    )
    expect(testMeaning("Caddy", failed([unknown]), "test")).toBe(
      "Caddy refuses this configuration, so a reload, start or restart is refused until it is fixed. A running Caddy goes on serving what it loaded last.",
    )
    expect(testMeaning("nginx", failed([unknown]), "reload")).toBe(
      "The reload was refused, so nginx goes on serving what it loaded last. Fix what the test found, then reload again.",
    )
  })

  test("says how long ago the test ran, the first seconds as now", () => {
    const at = Date.parse("2026-09-28T02:00:00Z")
    expect(testedLabel(at, at + 3_000)).toBe("tested just now")
    expect(testedLabel(at, at + 14_000)).toBe("tested 14s ago")
    expect(testedLabel(at, at + 3 * 3_600_000 + 7_000)).toBe("tested 3h ago")
    expect(testedLabel(at, at + 150_000)).toBe("tested 2m ago")
    // A clock a little behind the server's is not a test from the future.
    expect(testedLabel(at, at - 2_000)).toBe("tested just now")
  })
})

describe("the reason a finding gives", () => {
  test("is the line a failed test stopped on, with its place", () => {
    expect(testReason(failed([conflict, unknown]))).toBe(
      'unknown directive "frobnicate" in /etc/nginx/sites-available/app:3',
    )
    // Output nothing could be read from is still a reason.
    expect(testReason(failed([]))).toBe(
      "nginx: configuration file /etc/nginx/nginx.conf test failed",
    )
  })

  test("is the first warning, and how many more there are", () => {
    expect(testReason(passed([conflict]))).toBe(conflict.message)
    expect(testReason(passed([conflict, deprecated]))).toBe(`${conflict.message}, and 1 more`)
    expect(diagnosticLine(deprecated)).toBe(
      `${deprecated.message} in /etc/nginx/sites-available/app:4`,
    )
  })
})

describe("the last config test in Needs attention", () => {
  test("a clean test is no finding, and neither is none at all", () => {
    expect(engineFindings({ lastTest: { engine: "nginx", record: record(passed()) } })).toEqual([])
    expect(engineFindings({})).toEqual([])
  })

  test("a warning stays as a warning finding that names it", () => {
    const [finding] = engineFindings({
      lastTest: { engine: "nginx", record: record(passed([conflict])) },
    })
    expect(finding).toMatchObject({
      id: CONFIG_TEST,
      level: "warning",
      title: "nginx's config test has a warning",
      detail: conflict.message,
      meta: "config test",
      href: "/proxy",
    })
    expect(finding.advice).toContain("This stays until a test comes back clean.")
    const [two] = engineFindings({
      lastTest: { engine: "nginx", record: record(passed([conflict, deprecated])) },
    })
    expect(two.title).toBe("nginx's config test has 2 warnings")
  })

  // What runs was loaded before, and the next restart or reboot is refused.
  test("a failed test is critical, even while the engine serves", () => {
    const [finding] = engineFindings({
      lastTest: { engine: "Caddy", record: record(failed([unknown])) },
    })
    expect(finding).toMatchObject({
      id: CONFIG_TEST,
      level: "critical",
      title: "Caddy's configuration fails its test",
      detail: 'unknown directive "frobnicate" in /etc/nginx/sites-available/app:3',
    })
  })

  test("is folded with the rest, worst first", () => {
    const folded = foldProxyFindings({
      lastTest: { engine: "nginx", record: record(failed([unknown])) },
      unreadable: [{ source: "ports", message: "gopsutil failed" }],
    })
    expect(folded.map((f) => f.id)).toEqual([CONFIG_TEST, "source.unreadable.ports"])
    // The overview answers it by showing the test; its href alone opens the overview.
    expect(findingAction(folded[0])).toBe("Open")
  })
})

describe("which files a diagnostic may open", () => {
  const status = {
    nginx: true,
    caddy: false,
    nginxDir: "/etc/nginx",
    caddyFile: "/etc/caddy/Caddyfile",
    certbot: false,
  }
  test("nginx's directory, a host Caddy's, and nothing inside the Docker ingress", () => {
    expect(engineRoots(status)).toEqual(["/etc/nginx"])
    expect(engineRoots({ ...status, nginx: false, caddy: true })).toEqual(["/etc/caddy"])
    // The ingress's test names the container's Caddyfile, which the host's
    // /etc/caddy/Caddyfile is not.
    expect(engineRoots({ ...status, nginx: false, caddy: true, ingressContainer: "edge" })).toEqual(
      [],
    )
    expect(engineRoots(undefined)).toEqual([])
  })
})
