import type { ProxyDiagnostic, ServerNameConflict, SiteResult, SiteSpec } from "@/lib/types"

/**
 * The spec as the server should read it. HSTS goes only with TLS: the switch
 * is drawn only under "Serve over HTTPS", and the default it keeps for when
 * TLS is turned on reached the server on every plain-HTTP site as a warning
 * about a control nobody could see.
 */
export function sendableSpec(spec: SiteSpec): SiteSpec {
  return spec.hsts && !spec.tls ? { ...spec, hsts: false } : spec
}

/**
 * The request that saves a site. An existing site keeps whatever state its
 * link is in — the form used to enable every site it saved, so fixing one
 * field of a disabled site put it back on the internet — and a new one is
 * enabled.
 */
export function saveRequest(
  spec: SiteSpec,
  {
    existing,
    reload,
    allowConflict,
  }: { existing: boolean; reload: boolean; allowConflict?: boolean },
) {
  return {
    spec: sendableSpec(spec),
    enable: existing ? "keep" : "enable",
    reload,
    overwrite: existing,
    ...(allowConflict ? { allowConflict: true } : {}),
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
  { name, reloaded }: { name: string; reloaded: boolean },
): string {
  return conflicts
    .map((c) => {
      const at = `${c.domain} on ${c.listen}`
      const other = c.site ?? "another server block"
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

/**
 * What a save did, said as it happened. "Is live" only when nginx reloaded
 * an enabled site and had nothing to say about it; everything short of that
 * is its own sentence.
 */
export function saveOutcome(res: SiteResult, { existing }: { existing: boolean }): SaveOutcome {
  const name = res.name
  if (res.reloadError) {
    // Nothing about what nginx is serving: the usual cause is an nginx that
    // is not running, which serves nothing at all.
    return {
      tone: "warning",
      title: `${name} saved and tested; reload failed`,
      description: `nginx did not pick it up; start or reload nginx to apply it. ${res.reloadError}`,
    }
  }
  if (!res.enabled) {
    return {
      tone: "success",
      title: `${name} saved`,
      description: "It is disabled and stays that way until it is enabled from the list.",
    }
  }
  const state = res.reloaded ? "is live" : "saved"
  if (res.conflicts && res.conflicts.length > 0) {
    return {
      tone: "warning",
      title: `${name} ${state} with a name conflict`,
      description: conflictsText(res.conflicts, { name, reloaded: res.reloaded }),
    }
  }
  const warnings = res.testWarnings ?? []
  if (warnings.length > 0) {
    return {
      tone: "warning",
      title: `${name} ${state} with ${warnings.length === 1 ? "a warning" : `${warnings.length} warnings`}`,
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
