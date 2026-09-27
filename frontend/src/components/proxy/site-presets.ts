import type { SiteSpec } from "@/lib/types"
import { deriveIdentity, FOLLOW_DOMAINS } from "@/components/proxy/site-identity"

/**
 * Where a new site starts: the form's defaults, one of the applications
 * people most often put behind a domain, or a link from elsewhere in the
 * dashboard that already knows the upstream.
 */

/** The spec a new site starts from. */
export const BLANK: SiteSpec = {
  name: "",
  domains: [],
  kind: "proxy",
  upstream: "http://127.0.0.1:3000",
  tls: false,
  forceHttps: true,
  hsts: true,
  http2: true,
  webSockets: true,
  gzip: true,
  blockExploits: true,
  securityHeaders: true,
  clientMaxBody: "50m",
  proxyTimeout: 60,
  allowFrom: [],
  denyFrom: [],
  accessLog: true,
  locations: [],
}

export type SitePreset = {
  id: string
  label: string
  /** The application's logo, where it has one. */
  product?: string
  /** One line under the name: where it listens and what it changes. */
  detail: string
  /** What the application itself needs set before it works behind a proxy. */
  note?: string
  /**
   * The fields it sets. The backend keeps each as testdata/presets/<id>.json
   * and renders it through a real `nginx -t`; site-presets.test.js holds the
   * two together.
   */
  spec: Partial<SiteSpec>
}

export const PRESETS: SitePreset[] = [
  {
    id: "node",
    label: "Node.js app",
    product: "nodejs",
    detail: "127.0.0.1:3000 · WebSockets",
    spec: {
      kind: "proxy",
      upstream: "http://127.0.0.1:3000",
      webSockets: true,
      clientMaxBody: "50m",
      proxyTimeout: 60,
    },
  },
  {
    id: "spa",
    label: "Single-page app",
    detail: "Files · unknown paths get index.html",
    spec: { kind: "static", spa: true },
  },
  {
    id: "grafana",
    label: "Grafana",
    product: "grafana",
    detail: "127.0.0.1:3000 · live updates",
    note: "Set domain and root_url under [server] in grafana.ini to this address, so the links and sign-in redirects Grafana writes use it.",
    spec: {
      kind: "proxy",
      upstream: "http://127.0.0.1:3000",
      webSockets: true,
      clientMaxBody: "50m",
      proxyTimeout: 300,
    },
  },
  {
    id: "home-assistant",
    label: "Home Assistant",
    product: "home-assistant",
    detail: "127.0.0.1:8123 · WebSockets",
    note: "Home Assistant answers a proxied request with 400 until configuration.yaml trusts this proxy: use_x_forwarded_for: true and trusted_proxies: 127.0.0.1 under http:.",
    spec: {
      kind: "proxy",
      upstream: "http://127.0.0.1:8123",
      webSockets: true,
      clientMaxBody: "50m",
      proxyTimeout: 300,
    },
  },
  {
    id: "registry",
    label: "Docker registry",
    product: "docker",
    detail: "127.0.0.1:5000 · no upload limit",
    note: "Docker refuses a registry over plain HTTP anywhere but localhost, so serve this one over HTTPS.",
    spec: {
      kind: "proxy",
      upstream: "http://127.0.0.1:5000",
      webSockets: false,
      clientMaxBody: "0",
      proxyTimeout: 900,
      custom:
        "# Pushes stream through to the registry as they arrive instead of\n# being spooled to disk first.\nproxy_request_buffering off;",
    },
  },
  {
    id: "minio",
    label: "MinIO",
    product: "minio",
    detail: "127.0.0.1:9000 · no upload limit",
    spec: {
      kind: "proxy",
      upstream: "http://127.0.0.1:9000",
      webSockets: false,
      clientMaxBody: "0",
      proxyTimeout: 300,
      custom:
        "# Uploads stream through to MinIO as they arrive instead of being\n# spooled to disk first.\nproxy_request_buffering off;",
    },
  },
  {
    id: "jellyfin",
    label: "Jellyfin",
    product: "jellyfin",
    detail: "127.0.0.1:8096 · streaming",
    note: "Add 127.0.0.1 to Known proxies under Networking in Jellyfin, so it sees visitors' addresses rather than this proxy's.",
    spec: {
      kind: "proxy",
      upstream: "http://127.0.0.1:8096",
      webSockets: true,
      clientMaxBody: "20m",
      proxyTimeout: 300,
    },
  },
  {
    id: "redirect",
    label: "Redirect",
    detail: "301 to another address",
    spec: { kind: "redirect", permanent: true },
  },
]

export function presetById(id: string | null | undefined): SitePreset | undefined {
  return PRESETS.find((preset) => preset.id === id)
}

/**
 * The fields every preset decides, at BLANK's values: what a preset leaves
 * out goes back to the default, so picking Grafana after the registry does
 * not keep the registry's unlimited uploads.
 */
const DECIDED: Partial<SiteSpec> = {
  kind: BLANK.kind,
  webSockets: BLANK.webSockets,
  clientMaxBody: BLANK.clientMaxBody,
  proxyTimeout: BLANK.proxyTimeout,
  permanent: false,
  spa: false,
}

/**
 * A preset laid over the site so far. The name, domains, certificate and
 * access settings are the operator's and stay. So does an upstream they
 * typed, picked or arrived with from a link — only one that is still a
 * default is replaced by the preset's port. The extra configuration keeps
 * whatever the operator wrote there and swaps only a preset's own lines.
 */
export function applyPreset(spec: SiteSpec, preset: SitePreset): SiteSpec {
  const defaults = new Set([BLANK.upstream, ...PRESETS.map((p) => p.spec.upstream)])
  const typed = spec.upstream && !defaults.has(spec.upstream) ? spec.upstream : undefined
  return {
    ...spec,
    ...DECIDED,
    ...preset.spec,
    upstream: typed ?? preset.spec.upstream ?? BLANK.upstream,
    custom: swapCustom(spec.custom, preset.spec.custom),
  }
}

function swapCustom(current: string | undefined, next: string | undefined) {
  let custom = current ?? ""
  for (const preset of PRESETS) {
    if (preset.spec.custom) custom = custom.replace(preset.spec.custom, "")
  }
  custom = custom.trim()
  if (next) custom = custom ? `${custom}\n${next}` : next
  return custom || undefined
}

/** The query a link into a new site carries, removed once it has been read. */
export const LINK_PARAMS = ["new", "upstream", "domain"] as const

/**
 * What /proxy/sites?new=1&upstream=<url>&domain=<name> opens the form on:
 * the upstream and the domains as given, the name and certificate paths
 * following the first domain as if it had been typed. Both go in as the link
 * spelled them — the form and its preview say what is wrong with either,
 * which beats quietly opening on the default upstream as though the link had
 * asked for it. Nothing without new=1.
 */
export function linkedSite(params: URLSearchParams): { spec: SiteSpec; domains: string } | null {
  if (params.get("new") !== "1") return null
  const upstream = params.get("upstream")?.trim().slice(0, 512)
  const domains = (params.get("domain") ?? "")
    .slice(0, 512)
    .split(/[\s,]+/)
    .filter(Boolean)
  return {
    domains: domains.join(" "),
    spec: {
      ...BLANK,
      ...(upstream ? { kind: "proxy", upstream } : {}),
      domains,
      ...deriveIdentity(domains, BLANK, FOLLOW_DOMAINS),
    },
  }
}
