import type { SiteSpec } from "@/lib/types"
import { sendableSpec } from "@/components/proxy/site-save"
import { SETTINGS_VIEWS, type SettingsView } from "@/components/proxy/site-tabs"

/**
 * Which part of a site's Settings each field of its spec is edited in.
 *
 * The form was one sheet read top to bottom, so an edit was always on screen
 * somewhere above the Save that wrote it. Split across views, an edit made
 * under Access is out of sight under HTTPS: the strip at the head of the
 * column names the parts that hold unsaved changes, each a way back to it,
 * and this is how it knows which.
 *
 * A key left out is General's: a field the form gains later is counted
 * there until it is given a part of its own, rather than not counted at all.
 */
const VIEW_OF: Partial<Record<keyof SiteSpec, SettingsView>> = {
  tls: "https",
  certPath: "https",
  keyPath: "https",
  managedAcme: "https",
  forceHttps: "https",
  hsts: "https",
  hstsMaxAge: "https",
  hstsOwnNameOnly: "https",
  hstsPreload: "https",
  http2: "https",
  tlsProfile: "https",

  allowFrom: "access",
  denyFrom: "access",
  basicAuthFile: "access",
  basicAuthRealm: "access",
  satisfyAny: "access",
  accessList: "access",
  clientCert: "access",
  forwardAuth: "access",
  blockBots: "access",
  hotlink: "access",
  realIp: "access",

  securityHeaders: "hardening",
  blockExploits: "hardening",
  gzip: "hardening",
  accessLog: "hardening",
  accessLogPath: "hardening",
  errorLogPath: "hardening",
  logFormat: "hardening",
  limits: "hardening",
  staticCache: "hardening",
  proxyCache: "hardening",

  headers: "headers",
  maintenance: "headers",
  errorPages: "headers",
  interceptErrors: "headers",
  securityTxt: "headers",
  robotsTxt: "headers",

  locations: "paths",
  custom: "paths",
}

/**
 * A value as the comparison reads it: a key left out, set to nothing, false
 * or empty is the same thing, as `sameSpec` counts it, since the form sets a
 * switch to false where the file it read never mentioned it.
 */
function canonical(value: unknown): string {
  return (
    JSON.stringify(value, (_key, v: unknown) => {
      if (v === null || v === false || v === "" || v === 0) return undefined
      if (Array.isArray(v)) return v.length === 0 ? undefined : v
      if (typeof v === "object") {
        const sorted: Record<string, unknown> = {}
        for (const key of Object.keys(v as object).sort()) {
          sorted[key] = (v as Record<string, unknown>)[key]
        }
        return sorted
      }
      return v
    }) ?? ""
  )
}

/** The fields of a draft that differ from what it started from, as they would be sent. */
export function changedFields(spec: SiteSpec, start: SiteSpec): (keyof SiteSpec)[] {
  const now = sendableSpec(spec) as unknown as Record<string, unknown>
  const then = sendableSpec(start) as unknown as Record<string, unknown>
  const keys = new Set([...Object.keys(now), ...Object.keys(then)])
  return [...keys]
    .filter((key) => canonical(now[key]) !== canonical(then[key]))
    .sort() as (keyof SiteSpec)[]
}

/** The parts of Settings that hold an unsaved change, in the order they are read. */
export function changedViews(spec: SiteSpec, start: SiteSpec): SettingsView[] {
  const views = new Set(changedFields(spec, start).map((key) => VIEW_OF[key] ?? "general"))
  return SETTINGS_VIEWS.filter((view) => views.has(view))
}
