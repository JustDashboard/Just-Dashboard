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

/** Who else answers each name, in the words the conflict refusal uses. */
export function conflictsText(conflicts: ServerNameConflict[]): string {
  const parts = conflicts.map(
    (c) => `${c.domain} is also served by ${c.site ?? "another server block"} on ${c.listen}`,
  )
  return `${parts.join("; ")}. nginx answers each name from one of them.`
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
    return {
      tone: "warning",
      title: `${name} saved and tested; reload failed`,
      description: `nginx is still serving what it served before. ${res.reloadError}`,
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
      description: conflictsText(res.conflicts),
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
