/**
 * The deep scan's readings, worked out apart from the page so each can be
 * tested: which suites a filter keeps, which findings the quick report has
 * not already shown, and what each connection feature's answer reads as.
 */

import type { DotTone, Verdict } from "@/components/status-dot"
import type { ScanFinding, TLSScan } from "./proxy/types-tls"
import type {
  ALPNResult,
  DeepScan,
  GroupResult,
  HTTP3Result,
  ResumptionResult,
  SNIProbe,
  SuiteResult,
  VersionSuites,
} from "./proxy/types-tls-deep"

export type Rating = SuiteResult["rating"]

/** A reading as `Status` draws it: a verdict, or a tone for no verdict at all. */
export type Reading = { verdict?: Verdict; tone?: DotTone; label: string; detail?: string }

export const RATINGS: Rating[] = ["strong", "weak", "insecure"]

export const RATING_VERDICT: Record<Rating, Verdict> = {
  strong: "ok",
  weak: "notice",
  insecure: "critical",
}

/** Which suites the list shows: one version or all, one rating or any. */
export type SuiteFilter = { version: string; rating: Rating | "any" }

/** Accepted suites per version, for the version chips. */
export function versionCounts(deep: DeepScan): { name: string; count: number }[] {
  return deep.versions
    .filter((v) => v.suites.length > 0)
    .map((v) => ({ name: v.name, count: v.suites.length }))
}

/** Suites per rating within the chosen version, for the rating chips. */
export function ratingCounts(deep: DeepScan, version: string): Record<Rating, number> {
  const counts: Record<Rating, number> = { strong: 0, weak: 0, insecure: 0 }
  for (const v of deep.versions) {
    if (version !== "all" && v.name !== version) continue
    for (const s of v.suites) counts[s.rating]++
  }
  return counts
}

/**
 * The filter as it can apply to this scan. A choice remembered from another
 * scan that matches nothing here falls back to showing everything, rather
 * than an empty list with its chip hidden.
 */
export function effectiveFilter(deep: DeepScan, filter: SuiteFilter): SuiteFilter {
  const version =
    filter.version !== "all" && !versionCounts(deep).some((v) => v.name === filter.version)
      ? "all"
      : filter.version
  const rating =
    filter.rating !== "any" && ratingCounts(deep, version)[filter.rating] === 0
      ? "any"
      : filter.rating
  return { version, rating }
}

/**
 * The versions to draw, each with the suites the filter keeps. A version
 * that accepted nothing is drawn only when nothing is filtered: its refusal
 * is part of the whole picture, not of a list of weak suites.
 */
export function suiteGroups(deep: DeepScan, given: SuiteFilter): VersionSuites[] {
  const filter = effectiveFilter(deep, given)
  const everything = filter.version === "all" && filter.rating === "any"
  return deep.versions
    .filter((v) => filter.version === "all" || v.name === filter.version)
    .map((v) => ({
      ...v,
      suites:
        filter.rating === "any" ? v.suites : v.suites.filter((s) => s.rating === filter.rating),
    }))
    .filter((v) => v.suites.length > 0 || (everything && v.status !== "accepted"))
}

/**
 * The deep findings the quick report does not already list. The deep scan
 * reports a version under the quick scan's own finding id, so the same id is
 * the same finding.
 */
export function newDeepFindings(deep: DeepScan, scan: TLSScan | null | undefined): ScanFinding[] {
  const shown = new Set((scan?.findings ?? []).map((f) => f.id))
  return deep.findings.filter((f) => !shown.has(f.id))
}

/** This page's address with the deep scan asked for, or not, keeping the rest. */
export function deepHref(params: { toString(): string }, deep: boolean): string {
  const next = new URLSearchParams(params.toString())
  if (deep) next.set("deep", "1")
  else next.delete("deep")
  const query = next.toString()
  return query ? `/proxy/tls?${query}` : "/proxy/tls"
}

const RETIRED = new Set(["SSL 3.0", "TLS 1.0", "TLS 1.1"])

/**
 * A version's heading reading: how many suites, and whose order picks. A
 * retired version refused is right; TLS 1.3 refused is the quick scan's own
 * finding; TLS 1.2 refused is neither.
 */
export function versionReading(v: VersionSuites): Reading {
  if (v.status === "refused")
    return {
      ...(RETIRED.has(v.name)
        ? { verdict: "ok" as const }
        : v.name === "TLS 1.3"
          ? { verdict: "notice" as const }
          : { tone: "stopped" as const }),
      label: "refused",
      detail: v.detail,
    }
  if (v.status === "unknown") return { tone: "unknown", label: "unknown", detail: v.detail }
  const count = `${v.suites.length} ${v.suites.length === 1 ? "suite" : "suites"}`
  const order =
    v.order === "server"
      ? " · server's order"
      : v.order === "client"
        ? " · client's order"
        : v.order === "unclear"
          ? " · order unclear"
          : ""
  return {
    verdict: RETIRED.has(v.name) ? "critical" : "ok",
    label: count + order,
    detail: v.complete ? undefined : v.detail,
  }
}

/** Whether a suite's key exchange is its own or TLS 1.3's group. */
export function suiteFacts(s: SuiteResult): string {
  const kex = s.kex === "any" ? "the group decides the key exchange" : `${s.kex} key exchange`
  const auth =
    s.auth === "anon" ? ", no certificate" : s.auth === "any" ? "" : `, ${s.auth} certificate`
  return `${s.cipher}, ${kex}${auth}`
}

/**
 * A group's answer. A refusal's alert is the same handshake failure for every
 * group a server lacks, so it is left out rather than said eleven times.
 */
export function groupReading(g: GroupResult): Reading {
  if (g.status === "accepted") return { verdict: "ok", label: "accepted", detail: g.detail }
  if (g.status === "refused") return { tone: "stopped", label: "refused" }
  return { tone: "unknown", label: "unknown", detail: g.detail }
}

const POST_QUANTUM = new Set(["X25519MLKEM768", "SecP256r1MLKEM768", "SecP384r1MLKEM1024"])

/** The key exchange a browser's offer gets: post-quantum or not. */
export function browserGroupReading(deep: DeepScan): Reading {
  if (!deep.browserGroup)
    return {
      tone: "unknown",
      label: "not read",
      detail: "The server did not name a group to a browser's offer.",
    }
  const pq = POST_QUANTUM.has(deep.browserGroup)
  return {
    verdict: pq ? "ok" : "notice",
    label: deep.browserGroup,
    detail:
      deep.browserGroupVersion === "TLS 1.2"
        ? "The curve a browser's TLS 1.2 offer gets; without TLS 1.3 there is no post-quantum exchange."
        : pq
          ? "Post-quantum, for the X25519MLKEM768 and X25519 a browser offers."
          : "Not post-quantum, for the X25519MLKEM768 and X25519 a browser offers.",
  }
}

export function dhReading(bits: number): Reading {
  return {
    verdict: bits >= 2048 ? "ok" : "warning",
    label: `${bits} bits`,
    detail: "The finite-field group a DHE suite uses.",
  }
}

export function alpnReading(alpn: ALPNResult): Reading {
  const h2 = alpn.negotiated === "h2"
  const form = alpn.site
    ? ` ${alpn.site.name}'s site form has HTTP/2 ${alpn.site.http2 ? "on" : "off"}.`
    : ""
  return {
    verdict: h2 ? "ok" : alpn.site?.http2 ? "warning" : "notice",
    label: alpn.negotiated || "none chosen",
    detail: `Offered ${alpn.offered.join(" and ")}.${form}`,
  }
}

/** HTTP/3 as advertised, and whether QUIC answered where it points. */
export function http3Reading(h: HTTP3Result): Reading {
  if (!h.answered)
    return {
      tone: "unknown",
      label: "not checked",
      detail: `HTTPS gave no HTTP answer${h.error ? `: ${h.error}` : ""}.`,
    }
  const port = h.quic?.port ?? h.port
  const versions = h.quic?.versions?.length ? ` (${h.quic.versions.join(", ")})` : ""
  if (h.advertised && h.host)
    return {
      tone: "unknown",
      label: "advertised elsewhere",
      detail: `Alt-Svc points at ${h.host}:${h.port}, another host, which is not sent anything.`,
    }
  if (h.advertised && h.quic?.answered)
    return {
      verdict: "ok",
      label: "answers",
      detail: `Alt-Svc offers h3 on UDP ${port}, and QUIC answered there${versions}.`,
    }
  if (h.advertised)
    return {
      verdict: "warning",
      label: "no QUIC answer",
      detail: `Alt-Svc offers h3 on UDP ${port}. ${h.quic?.detail ?? ""}`.trim(),
    }
  if (h.quic?.answered)
    return {
      verdict: "notice",
      label: "not advertised",
      detail: `QUIC answers on UDP ${port}${versions}, and no Alt-Svc header says so.`,
    }
  return { tone: "unknown", label: "not offered", detail: "No Alt-Svc header advertises it." }
}

export function resumptionReading(r: ResumptionResult): Reading {
  switch (r.status) {
    case "resumed":
      return { verdict: "ok", label: "resumed", detail: r.detail }
    case "not-resumed":
      return { verdict: "notice", label: "not resumed", detail: r.detail }
    case "no-ticket":
      return r.version === "TLS 1.3"
        ? { verdict: "notice", label: "no ticket", detail: r.detail }
        : { tone: "unknown", label: "no ticket", detail: r.detail }
  }
  return { tone: "unknown", label: "unknown", detail: r.detail }
}

/** A client naming no site, or an unknown one: refused, or handed a certificate. */
export function sniReading(p: SNIProbe): Reading & { title: string } {
  const title = p.kind === "none" ? "No name" : "Unknown name"
  if (p.status === "refused") return { title, verdict: "ok", label: "refused", detail: p.detail }
  if (p.status === "certificate") {
    const names = (p.names?.length ? p.names : [p.subject ?? ""]).join(", ")
    return {
      title,
      verdict: "notice",
      label: "gets a certificate",
      detail: p.sameAsNamed
        ? `The scanned name's own certificate, for ${names}.`
        : `${p.subject}'s certificate, for ${names}.`,
    }
  }
  return { title, tone: "unknown", label: "unknown", detail: p.detail }
}
