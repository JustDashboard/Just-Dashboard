import type { ServiceLogSource } from "@/components/logs/service-logs"
import type { LogFields } from "@/components/logs/types"
import type { SiteSpec, VHost } from "@/lib/types"
import { dockerSource, fileSource, journalSource } from "@/lib/log-sources"

/** Where nginx writes the errors of every site that names no file of its own. */
export const NGINX_ERROR_LOG = "/var/log/nginx/error.log"

/** The prefix the proxy lists an unmanaged route of a Docker Caddy ingress under. */
const DOCKER_CADDY_HOST = "docker-caddy:"

/**
 * What a site's page reads, and from where.
 *
 * A site's logs are wherever its engine writes them, which is a different
 * place for each kind of site: an nginx site's own two files, or nginx's
 * shared error log when it names none; a route on the shared Docker Caddy
 * ingress, whose requests are the ingress's record of that route and whose
 * failures are the ingress container's output; a site in the host's
 * Caddyfile, whose failures are Caddy's journal.
 */
export type SiteLogPlan = {
  sources: ServiceLogSource[]
  /**
   * The site has a request record of its own, and this is the source its
   * lines are read beside — the Requests view names it in the strip.
   */
  requests?: string
  /**
   * Where the site's failures are written, read through that log's lens and
   * narrowed to the site where the log is shared: the Errors view, and the
   * lines under a failed request, read it.
   */
  errors?: { source: string; lens: string; fields: LogFields }
  /**
   * An nginx site whose requests are written nowhere this page can read as
   * its own: `off` when it logs none, `shared` when they go to nginx's
   * shared log, syslog or a stream.
   */
  unrecorded?: "off" | "shared"
}

/** The names a line's `host` can equal: a wildcard or a regex name matches no one value. */
export function siteHosts(vhost: Pick<VHost, "serverNames">): string[] {
  return vhost.serverNames.filter(
    (name) => name !== "_" && !name.startsWith("*") && !name.startsWith("~"),
  )
}

function hostFields(hosts: string[]): LogFields {
  return hosts.length > 0 ? { host: hosts } : {}
}

/**
 * The plan for one site. `spec` is the nginx file read back, which says where
 * the site logs; `ingress` is the shared Docker Caddy container, when the host
 * has one.
 */
export function siteLogPlan(
  vhost: VHost,
  spec: SiteSpec | undefined,
  ingress: string | undefined,
): SiteLogPlan {
  const hosts = siteHosts(vhost)
  if (vhost.kind === "nginx") {
    const sources: ServiceLogSource[] = []
    const access = spec?.accessLogPath
    if (access) {
      sources.push({
        id: fileSource(access),
        label: "Access log",
        kind: "nginx",
        path: access,
        lens: "http-access",
        product: "nginx-static",
      })
    }
    // A site with no error_log of its own writes to nginx's, beside every
    // other site's; its lines carry the host they were about, so the page
    // reads that log narrowed to this site's names.
    const own = spec?.errorLogPath
    const errors = own ?? NGINX_ERROR_LOG
    sources.push({
      id: fileSource(errors),
      label: own ? "Error log" : "nginx error log",
      kind: "nginx",
      path: errors,
      lens: "nginx-error",
      product: "nginx-static",
      detail: own ? undefined : "nginx's own, shared by every site that names none",
    })
    return {
      sources,
      requests: access ? fileSource(access) : undefined,
      errors: {
        source: fileSource(errors),
        lens: "nginx-error",
        fields: own ? {} : hostFields(hosts),
      },
      unrecorded: access ? undefined : spec && !spec.accessLog ? "off" : "shared",
    }
  }

  // A Caddy site's failures are Caddy's runtime log, which every site on
  // that Caddy shares and whose lines carry the host they were about.
  const container = vhost.name.startsWith(DOCKER_CADDY_HOST)
    ? vhost.name.slice(DOCKER_CADDY_HOST.length).split(":")[0]
    : vhost.path
      ? undefined
      : ingress
  const source: ServiceLogSource | undefined = container
    ? {
        id: dockerSource(container),
        label: container,
        kind: "docker",
        lens: "caddy",
        product: "caddy",
        detail: "The Caddy ingress, shared by every route on it",
      }
    : vhost.path
      ? {
          id: journalSource("caddy.service"),
          label: "caddy.service",
          kind: "journal",
          lens: "caddy",
          product: "caddy",
        }
      : undefined
  if (!source) return { sources: [] }
  // A route the dashboard wrote on the ingress keeps a request record of its
  // own inside the container, which the site's record reads through it.
  const routed = !vhost.path && !vhost.name.startsWith(DOCKER_CADDY_HOST) && Boolean(ingress)
  return {
    sources: [source],
    requests: routed ? source.id : undefined,
    errors: { source: source.id, lens: "caddy", fields: hostFields(hosts) },
  }
}
