import type { SiteFeature, VHost, VHostOwner, VHostPool } from "@/lib/types"

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

/**
 * The name deploy gives every route it writes, as the server checks it
 * (`deploymentRouteRe`): the one thing that says a deployment made a route
 * once its project is gone.
 */
const DEPLOYMENT_ROUTE = /^just-dashboard-env-[1-9][0-9]{0,17}\.conf$/

export function isDeploymentRoute(v: Pick<VHost, "name">): boolean {
  return DEPLOYMENT_ROUTE.test(v.name)
}

/**
 * A deployment's route that nothing deploys any more: its project deleted
 * (no owner at all) or archived. Deleting a project keeps its route, so the
 * route goes on answering its domain with nothing behind it that an operator
 * can find from the deployment's side.
 */
export function isLeftover(v: VHost): boolean {
  return isDeploymentRoute(v) && !activeOwner(v)
}

/**
 * What a site is called wherever the operator reads it: the address its
 * visitors type. The file name was the title, and every deployment's route
 * read `just-dashboard-env-450.conf`, which names nothing anyone remembers.
 * nginx's catch-all `_` is not a name, so a site with only that keeps its
 * file's. So does a host's Caddyfile, which is one entry holding every
 * address the file serves (`:80`, `https://blog.example.com`): the first of
 * them is not what the entry is.
 */
export function siteTitle(v: Pick<VHost, "name" | "kind" | "path" | "serverNames">): string {
  if (v.kind === "caddy" && v.path) return v.name
  return v.serverNames.find((name) => name !== "_" && name.trim() !== "") ?? v.name
}

/**
 * Who writes the site, in the words under its title on Sites, the Overview
 * and its own page: the deployment, by project and environment; that it is a
 * leftover and why; otherwise where its configuration lives. The engine is
 * not said: it is the mark in the tile's corner.
 */
export function siteKind(v: VHost): string {
  const owner = activeOwner(v)
  if (owner) return `${owner.project} · ${owner.environment}`
  if (isLeftover(v)) {
    return v.owner
      ? `Leftover · ${v.owner.project} is archived`
      : "Leftover · its deployment was deleted"
  }
  if (v.kind !== "nginx") {
    if (v.path) return "Caddyfile"
    return v.name.startsWith("docker-caddy:") ? "Ingress Caddyfile" : "Caddy route"
  }
  if (v.layout === "conf.d") return "nginx site in conf.d"
  if (v.layout === "sites-enabled") return "nginx site in sites-enabled"
  return "nginx site"
}

/**
 * Why a site is not removed from here and where it is instead, or for a
 * leftover what it is: one line, with the place to go where there is one.
 * "I can't remove sites" was a list whose rows offered no Delete and never
 * said why.
 */
export type SiteOrigin = {
  kind: "deployment" | "leftover" | "caddyfile" | "ingress"
  text: string
  /** The place the removal is done, as a link's words and address. */
  link?: { label: string; href: string }
}

export function siteOrigin(v: VHost): SiteOrigin | undefined {
  const owner = activeOwner(v)
  if (owner) {
    return {
      kind: "deployment",
      text: `Written by ${owner.project} on every deploy.`,
      link: {
        label: `Remove the domain in ${owner.project} · Domains`,
        href: `/deploy/${owner.projectId}/settings/domains`,
      },
    }
  }
  if (isLeftover(v)) {
    return {
      kind: "leftover",
      text: v.owner
        ? `Left by ${v.owner.project}, which is archived`
        : "Left by a deleted deployment",
    }
  }
  if (v.kind !== "caddy") return undefined
  if (v.path) return { kind: "caddyfile", text: "Edit the Caddyfile to remove it" }
  if (v.name.startsWith("docker-caddy:")) {
    return { kind: "ingress", text: "Defined in the ingress container's own Caddyfile" }
  }
  return undefined
}

/** The upstream block a proxy_pass names: a scheme and a bare name, with no port. */
function poolOf(upstream: string, pools: VHostPool[] | undefined): VHostPool | undefined {
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

/**
 * Whether a search finds the site: by name, domain, the project and
 * environment that deploy it, upstream or pool server,
 * and by the files, listeners, certificates, roots and redirect targets its
 * configuration names — "443", "/var/www" and "letsencrypt/live/app" are all
 * ways an operator remembers a site.
 */
export function matchesSearch(v: VHost, needle: string): boolean {
  if (!needle) return true
  return [
    v.name,
    v.owner?.project ?? "",
    v.owner?.environment ?? "",
    v.path,
    v.enabledPath ?? "",
    v.linkTarget ?? "",
    ...v.serverNames,
    ...v.upstreams,
    ...(v.pools ?? []).flatMap((pool) => pool.servers),
    ...v.listen,
    ...(v.certPaths ?? []),
    ...(v.roots ?? []),
    ...(v.redirects ?? []),
  ].some((text) => text.toLowerCase().includes(needle))
}
