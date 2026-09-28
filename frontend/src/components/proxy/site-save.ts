import type {
  ProxyDiagnostic,
  ServerNameConflict,
  SiteLocation,
  SiteResult,
  SiteSpec,
} from "@/lib/types"

/**
 * The spec as the server should read it. HSTS goes only with TLS: the switch
 * is drawn only under "Serve over HTTPS", and the default it keeps for when
 * TLS is turned on reached the server on every plain-HTTP site as a warning
 * about a control nobody could see. The single-page-app fallback goes only
 * with files and a permanent redirect only with a redirect, for the same
 * reason: a switch left on under another kind is not drawn and means nothing.
 */
export function sendableSpec(spec: SiteSpec): SiteSpec {
  const { spa, permanent, limits, hostHeaderValue, upstreamCa, upstreamTlsName, ...rest } = spec
  const locations = spec.locations.map(sendableLocation)
  return {
    ...rest,
    locations,
    ...sendableLimits({ ...spec, locations }, limits),
    ...(spec.hostHeader === "custom" ? { hostHeaderValue } : {}),
    ...(spec.upstreamVerify ? { upstreamCa } : {}),
    ...(spec.upstreamSni || spec.upstreamVerify ? { upstreamTlsName } : {}),
    hsts: spec.hsts && spec.tls,
    ...(spec.kind === "static" && spa !== undefined ? { spa } : {}),
    ...(spec.kind === "redirect" && permanent !== undefined ? { permanent } : {}),
  }
}

/**
 * A path sends only what its kind uses: a folder does not forward, so it has
 * no prefix to strip, upload limit, timeout or buffering, and a forwarding
 * path has no index.html to fall back to. An exact or regex path has no
 * prefix to strip either. The server refuses each of these rather than
 * ignoring it, and each is a switch the form no longer draws.
 */
function sendableLocation(loc: SiteLocation): SiteLocation {
  const prefix = !loc.match || loc.match === "^~"
  if (!loc.upstream && loc.root) {
    return {
      ...loc,
      stripPrefix: undefined,
      bodyLimit: undefined,
      timeout: undefined,
      buffering: undefined,
      requestBuffering: undefined,
      spa: Boolean(loc.spa) && prefix,
    }
  }
  return {
    ...loc,
    spa: undefined,
    root: undefined,
    rootMode: undefined,
    stripPrefix: Boolean(loc.stripPrefix) && prefix,
  }
}

/**
 * A redirect is answered before nginx applies any limit, so it sends none,
 * and exemptions or log-only mode left over once the last limit is turned
 * off go with it: there is nothing left for them to apply to.
 */
function sendableLimits(spec: SiteSpec, limits: SiteSpec["limits"]): Partial<SiteSpec> {
  if (spec.kind === "redirect") {
    return { locations: spec.locations.map((loc) => ({ ...loc, rateLimit: undefined })) }
  }
  const pathLimited =
    spec.kind === "proxy" && spec.locations.some((loc) => loc.rateLimit !== undefined)
  if (!limits || (!limits.request && !limits.connPerIp && !pathLimited)) return {}
  return { limits }
}

/**
 * The request that saves a site. An existing site keeps whatever state its
 * link is in unless the operator chose "Save and enable" — the form used to
 * enable every site it saved, so fixing one field of a disabled site put it
 * back on the internet — and a new one is enabled.
 */
export function saveRequest(
  spec: SiteSpec,
  {
    existing,
    reload,
    enable,
    allowConflict,
    baseDigest,
  }: {
    existing: boolean
    reload: boolean
    enable?: boolean
    allowConflict?: boolean
    /** The version of the file the form read: the save is refused if it changed since. */
    baseDigest?: string
  },
) {
  return {
    spec: sendableSpec(spec),
    enable: existing && !enable ? "keep" : "enable",
    reload,
    overwrite: existing,
    ...(allowConflict ? { allowConflict: true } : {}),
    ...(existing && baseDigest ? { baseDigest } : {}),
  }
}

export type SaveOutcome = { tone: "success" | "warning"; title: string; description?: string }

/**
 * Which site nginx answers each contested name from after the save. nginx
 * keeps the first server block it reads, so "is also served by" was false
 * one way round or the other: a site saved anyway that sorts first has taken
 * the name, and one that sorts after it is the site being ignored.
 */
export function conflictsText(
  conflicts: ServerNameConflict[],
  { name, reloaded, enabled = true }: { name: string; reloaded: boolean; enabled?: boolean },
): string {
  return conflicts
    .map((c) => {
      const at = `${c.domain} on ${c.listen}`
      const other = c.site ?? "another server block"
      // A disabled site answers nothing yet: what its test found is what
      // enabling it would do.
      if (!enabled) {
        if (c.effect === "ignored")
          return `Enabled, its claim to ${at} would be ignored: nginx answers it from ${other}.`
        if (c.effect === "takes") return `Enabled, it would take ${at} from ${other}.`
        return `Enabled, it would share ${at} with ${other}, and nginx would answer it from only one of the two.`
      }
      switch (c.effect) {
        case "ignored":
          return `nginx answers ${at} from ${other}, not ${name}.`
        case "takes":
          return reloaded
            ? `nginx now answers ${at} from ${name}, not ${other}.`
            : `After a reload nginx answers ${at} from ${name}, not ${other}.`
        case "keeps":
          return `nginx goes on answering ${at} from ${name} and ignores ${other}'s claim.`
        default:
          return `${at} is also claimed by ${other}; nginx answers it from only one of the two.`
      }
    })
    .join(" ")
}

function warningsText(warnings: ProxyDiagnostic[]): string {
  return warnings.map((w) => (w.line ? `Line ${w.line}: ${w.message}` : w.message)).join("; ")
}

function warningsTitle(warnings: ProxyDiagnostic[]): string {
  return warnings.length === 1 ? "a warning" : `${warnings.length} warnings`
}

/**
 * nginx's first objection, placed: by line in the site's own file, by path
 * and line in another — a test run with this site enabled also fails over a
 * broken neighbour, and saying "line 3" would point into the wrong file.
 */
function refusalText(res: SiteResult): string {
  const validation = res.validation
  const error = validation?.diagnostics?.find((d) => d.level !== "warn")
  if (!error) {
    const lines = (validation?.output ?? "").split("\n").filter((l) => l.trim() !== "")
    return lines.at(-1) ?? "nginx -t failed"
  }
  if (error.file === res.path && error.line) return `Line ${error.line}: ${error.message}`
  if (error.file) return `${error.file}${error.line ? `:${error.line}` : ""}: ${error.message}`
  return error.message
}

const STAYS_DISABLED = "It stays disabled until it is enabled."

/**
 * A site saved disabled: tested with its link in place for the test, the
 * result is what enabling it would meet — or, where its name's link is
 * another site's, the reason it could not be tested at all.
 */
function disabledOutcome(res: SiteResult): SaveOutcome {
  const name = res.name
  if (!res.testedAsEnabled) {
    return {
      tone: "warning",
      title: `${name} saved`,
      description: `${res.validation?.note ?? "nginx could not test it as enabled."} It stays disabled.`,
    }
  }
  if (res.validation && !res.validation.valid) {
    return {
      tone: "warning",
      title: `${name} saved; enabling it would fail nginx's test`,
      description: `${refusalText(res)}. ${STAYS_DISABLED}`,
    }
  }
  const conflicts = res.conflicts ?? []
  const warnings = res.testWarnings ?? []
  if (conflicts.length > 0 || warnings.length > 0) {
    const found = [
      conflicts.length > 0
        ? conflictsText(conflicts, { name, reloaded: false, enabled: false })
        : "",
      warnings.length > 0 ? `nginx warns: ${warningsText(warnings)}.` : "",
    ]
    return {
      tone: "warning",
      title:
        conflicts.length > 0
          ? `${name} saved with a name conflict once enabled`
          : `${name} saved with ${warningsTitle(warnings)}`,
      description: `${found.filter(Boolean).join(" ")} ${STAYS_DISABLED}`,
    }
  }
  return {
    tone: "success",
    title: `${name} saved`,
    description: `nginx tested it as if enabled. ${STAYS_DISABLED}`,
  }
}

/**
 * What a save did, said as it happened, and where a hand-written file it
 * replaced was kept.
 */
export function saveOutcome(res: SiteResult, { existing }: { existing: boolean }): SaveOutcome {
  const outcome = savedOutcome(res, { existing })
  if (!res.backup) return outcome
  const kept = `The hand-written version is kept as ${res.backup}.`
  return {
    ...outcome,
    description: outcome.description ? `${outcome.description} ${kept}` : kept,
  }
}

/**
 * "Is live" only when nginx reloaded an enabled site and had nothing to say
 * about it; everything short of that is its own sentence.
 */
function savedOutcome(res: SiteResult, { existing }: { existing: boolean }): SaveOutcome {
  const name = res.name
  if (res.servedCopy) {
    // Not "stays disabled": nginx answers the name, from the other file.
    return {
      tone: "warning",
      title: `${name} saved`,
      description: `nginx serves sites-enabled/${name}, a file of its own, not this one, so this save changes nothing it serves.`,
    }
  }
  if (res.reloadError) {
    // Nothing about what nginx is serving: the usual cause is an nginx that
    // is not running, which serves nothing at all.
    return {
      tone: "warning",
      title: `${name} saved and tested; reload failed`,
      description: `nginx did not pick it up; start or reload nginx to apply it. ${res.reloadError}`,
    }
  }
  if (!res.enabled) return disabledOutcome(res)
  const state = res.reloaded ? "is live" : "saved"
  const warnings = res.testWarnings ?? []
  if (res.conflicts && res.conflicts.length > 0) {
    // The warnings go with it rather than being dropped behind it.
    const also = warnings.length > 0 ? ` nginx also warns: ${warningsText(warnings)}.` : ""
    return {
      tone: "warning",
      title: `${name} ${state} with a name conflict`,
      description: conflictsText(res.conflicts, { name, reloaded: res.reloaded }) + also,
    }
  }
  if (warnings.length > 0) {
    return {
      tone: "warning",
      title: `${name} ${state} with ${warningsTitle(warnings)}`,
      description: warningsText(warnings),
    }
  }
  if (res.reloaded) return { tone: "success", title: `${name} is live` }
  return {
    tone: "success",
    title: `${name} saved`,
    description: existing
      ? "nginx serves the previous version until it reloads."
      : "nginx has not reloaded yet, so the site is on disk but not serving.",
  }
}
