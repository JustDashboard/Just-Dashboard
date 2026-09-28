import { duration, plural } from "@/lib/format"
import type { ProxyDiagnostic, ProxyValidation } from "@/lib/types"
import type { ProxyStatus } from "@/components/proxy/proxy-context"

/**
 * What the engine's config test said, in the words the test panel and the
 * overview's finding use for it.
 *
 * Test config answered with a toast: "valid" whatever nginx warned, and a
 * failure cut to four hundred characters, gone in twelve seconds. nginx
 * exits 0 through a warning that matters — a second site claiming a server
 * name another holds is "ignored", and never answers for it — so the verdict
 * has three words, not two.
 */

/** How the panel came to show a test: run from it, a reload's own, or the one the server kept. */
export type TestOrigin = "test" | "reload" | "last"

export type TestVerdict = { label: string; tone: "success" | "warning" | "danger" }

/** The count of warnings, which older answers may carry only as diagnostics. */
export function warningCount(validation: ProxyValidation): number {
  return (
    validation.warnings ?? (validation.diagnostics ?? []).filter((d) => d.level === "warn").length
  )
}

export function testVerdict(validation: ProxyValidation): TestVerdict {
  if (!validation.valid) return { label: "Fails", tone: "danger" }
  const warnings = warningCount(validation)
  return warnings > 0
    ? { label: `Valid with ${plural(warnings, "warning")}`, tone: "warning" }
    : { label: "Valid", tone: "success" }
}

/** What the verdict means for the engine, said after it. */
export function testMeaning(
  engine: string,
  validation: ProxyValidation,
  origin: TestOrigin,
): string {
  if (!validation.valid) {
    return origin === "reload"
      ? `The reload was refused, so ${engine} goes on serving what it loaded last. Fix what the test found, then reload again.`
      : `${engine} refuses this configuration, so a reload, start or restart is refused until it is fixed. A running ${engine} goes on serving what it loaded last.`
  }
  const lead = origin === "reload" ? `${engine} reloaded.` : "A reload would succeed."
  return warningCount(validation) > 0
    ? `${lead} ${engine} accepts this configuration, but a warning can mean part of it is ignored.`
    : lead
}

/**
 * "tested 14s ago", in its largest unit: the first seconds read as now rather
 * than as a counter starting at 0s, and "3h 7s" as the 3h it is.
 */
export function testedLabel(checkedAt: number, now: number): string {
  const age = Math.max(0, (now - checkedAt) / 1000)
  return age < 5 ? "tested just now" : `tested ${duration(age).split(" ")[0]} ago`
}

/** A diagnostic as one line of prose: its words, and where, when it says. */
export function diagnosticLine(diagnostic: ProxyDiagnostic): string {
  if (!diagnostic.file) return diagnostic.message
  return `${diagnostic.message} in ${diagnostic.file}${diagnostic.line ? `:${diagnostic.line}` : ""}`
}

/** Levels that pass a test: anything else is a reason it failed. */
const PASSING = new Set<ProxyDiagnostic["level"]>(["warn", "notice", "info"])

/** The line a failed test failed on, or its warnings, as the finding names them. */
export function testReason(validation: ProxyValidation): string {
  const diagnostics = validation.diagnostics ?? []
  if (!validation.valid) {
    const reason = diagnostics.find((d) => !PASSING.has(d.level)) ?? diagnostics[0]
    return reason ? diagnosticLine(reason) : validation.output.split("\n", 1)[0]
  }
  const warnings = diagnostics.filter((d) => d.level === "warn")
  if (warnings.length === 0) return ""
  const first = diagnosticLine(warnings[0])
  return warnings.length > 1 ? `${first}, and ${warnings.length - 1} more` : first
}

/**
 * The directories a diagnostic's file must be under for the config editor to
 * open it: the ones `/proxy/config` reads. The Docker ingress has none — the
 * files its test names are the container's, and the host's Caddyfile path is
 * a different file.
 */
export function engineRoots(status: ProxyStatus | undefined): string[] {
  if (!status) return []
  if (status.nginx) return [status.nginxDir]
  if (status.ingressContainer) return []
  return [status.caddyFile.replace(/\/[^/]*$/, "")]
}
