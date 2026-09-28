import type { ProxyTestRecord } from "@/lib/types"
import type { ProxyFinding } from "@/components/proxy/findings/shared"
import { testReason, warningCount } from "@/components/proxy/config-test"

/** What the overview reads to judge the proxy, each from its own endpoint. */
export type ProxySource = "status" | "sites" | "certificates" | "renewal" | "streams" | "ports"

/** A source whose last read failed, with the reason it gave. */
export type UnreadableSource = { source: ProxySource; message: string }

export type EngineFindingInput = {
  unreadable?: UnreadableSource[]
  /** The engine's last config test, for an account that may run one, and the engine's name. */
  lastTest?: { engine: string; record: ProxyTestRecord }
}

/** The id of the last config test's finding, which a page answers by showing that test. */
export const CONFIG_TEST = "engine.config-test"

/** The id prefix of a source that could not be read, which a page answers with a retry. */
export const UNREADABLE = "source.unreadable."

const SOURCE: Record<ProxySource, { title: string; advice: string; href: string }> = {
  // Only once the status has answered before: with no status the page is
  // the error, and with one it goes on drawing the engine from that answer.
  status: {
    title: "The proxy status could not be read",
    advice:
      "Until it can be read, the engine, its version and certbot above are as the last read found them.",
    href: "/proxy",
  },
  sites: {
    title: "Sites could not be read",
    advice:
      "Until it can be read, this list cannot show a site that is disabled or serves plain HTTP.",
    href: "/proxy/sites",
  },
  certificates: {
    title: "Certificates could not be read",
    advice:
      "Until it can be read, this list cannot show a certificate that has expired or is about to.",
    href: "/proxy/certificates",
  },
  renewal: {
    title: "Certificate renewal could not be read",
    advice: "Until it can be read, this list cannot show renewal that nothing runs.",
    href: "/proxy/certificates",
  },
  streams: {
    title: "Streams could not be read",
    advice:
      "Until it can be read, this list cannot show a stream nginx is not reading, or one open to anyone.",
    href: "/proxy/streams",
  },
  ports: {
    title: "Listening ports could not be read",
    advice:
      "Until it can be read, this list cannot show a database or control port open on every interface.",
    href: "/proxy/ports",
  },
}

/**
 * What is wrong with the engine itself rather than with something it serves,
 * and what the overview could not see. A source that failed its read is a
 * finding of its own: without one, the list folded from what did answer came
 * up empty and read "all within limits" about a host it could not see.
 */
export function engineFindings({ unreadable, lastTest }: EngineFindingInput): ProxyFinding[] {
  const findings = (unreadable ?? []).map(({ source, message }): ProxyFinding => ({
    id: `${UNREADABLE}${source}`,
    level: "warning",
    title: SOURCE[source].title,
    detail: message,
    advice: SOURCE[source].advice,
    meta: source,
    href: SOURCE[source].href,
  }))
  const tested = lastTest && testFinding(lastTest.engine, lastTest.record)
  return tested ? [...findings, tested] : findings
}

/**
 * The last config test, while it says something is wrong. It stays until a
 * test comes back clean, whichever command runs it: a warning was a toast
 * that said "valid", and a failure one that was gone before it was read.
 * A failure is critical even while the engine serves, since what it serves
 * is what it loaded before, and the next restart or reboot is refused.
 */
function testFinding(engine: string, record: ProxyTestRecord): ProxyFinding | undefined {
  const { validation } = record
  const warnings = warningCount(validation)
  if (validation.valid && warnings === 0) return undefined
  return validation.valid
    ? {
        id: CONFIG_TEST,
        level: "warning",
        title: `${engine}'s config test has ${warnings === 1 ? "a warning" : `${warnings} warnings`}`,
        detail: testReason(validation),
        advice: `${engine} accepts its configuration, but a warning can mean part of it is ignored, such as a site whose name another site already holds. This stays until a test comes back clean.`,
        meta: "config test",
        href: "/proxy",
      }
    : {
        id: CONFIG_TEST,
        level: "critical",
        title: `${engine}'s configuration fails its test`,
        detail: testReason(validation),
        advice: `A reload, start or restart is refused until it is fixed, and a running ${engine} goes on serving what it loaded last. This stays until a test comes back clean.`,
        meta: "config test",
        href: "/proxy",
      }
}

/** The source behind an unreadable-source finding, or undefined for any other. */
export function unreadableSource(finding: ProxyFinding): ProxySource | undefined {
  return finding.id.startsWith(UNREADABLE)
    ? (finding.id.slice(UNREADABLE.length) as ProxySource)
    : undefined
}
