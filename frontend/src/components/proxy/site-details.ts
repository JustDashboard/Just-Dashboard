import type { SiteFeature, SitePool, VHost, VHostOwner } from "@/lib/types"

/** The word each feature is drawn as on a site's card. */
export const FEATURE_LABEL: Record<SiteFeature, string> = {
  auth: "password",
  sso: "SSO",
  allow: "IP-restricted",
  ratelimit: "rate-limited",
  cache: "cached",
  ws: "WebSockets",
  h2: "HTTP/2",
  h3: "HTTP/3",
  maintenance: "maintenance",
}

/**
 * The deployment that writes the site and will write it again. An archived
 * one deploys nothing any more, so its route is the operator's to edit or
 * delete like any other.
 */
export function activeOwner(v: VHost): VHostOwner | undefined {
  return v.owner && !v.owner.archived ? v.owner : undefined
}

/** The upstream block a proxy_pass names: a scheme and a bare name, with no port. */
function poolOf(upstream: string, pools: SitePool[] | undefined): SitePool | undefined {
  const host = /^[a-z][a-z0-9+.-]*:\/\/([^/:?#]+)(?:[/?#]|$)/i.exec(upstream)?.[1]
  return host ? pools?.find((pool) => pool.name === host) : undefined
}

/**
 * Where the site sends its requests. An upstream block is written out as the
 * servers in it: `http://app_pool` names nothing a reader can check.
 */
export function upstreamTargets(v: VHost): string[] {
  return v.upstreams.map((upstream) => {
    const pool = poolOf(upstream, v.pools)
    return pool && pool.servers.length > 0 ? `${upstream} (${pool.servers.join(", ")})` : upstream
  })
}

/** Whether a search finds the site: by its name, a domain, an upstream or a server in a pool. */
export function matchesSearch(v: VHost, needle: string): boolean {
  if (!needle) return true
  return [
    v.name,
    ...v.serverNames,
    ...v.upstreams,
    ...(v.pools ?? []).flatMap((pool) => pool.servers),
  ].some((text) => text.toLowerCase().includes(needle))
}
