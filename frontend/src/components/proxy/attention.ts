import type { Certificate, CertbotState, Listener, StreamStatus, VHost } from "@/lib/types"
import type { Finding } from "@/components/finding-list"

/**
 * What is wrong with the proxy, as one list.
 *
 * The overview used to answer "needs attention" with every certificate past
 * its warning window and every socket bound to a wildcard address — the
 * second of which is sshd and nginx on every server there is, so the list
 * was never empty and stopped meaning anything. These are the conditions an
 * operator would actually act on, each with what was measured, what it
 * means and what to do, in the shape the host overview and the security
 * pages already render.
 */
export type ProxyFinding = Finding & { href: string }

/**
 * The ports the security catalogue treats as a database or a control plane —
 * the same judgement the stream form applies before forwarding one.
 */
export const DANGEROUS_PORTS: Record<number, string> = {
  5432: "PostgreSQL",
  3306: "MySQL",
  6379: "Redis",
  27017: "MongoDB",
  11211: "memcached",
  9200: "Elasticsearch",
  2375: "the Docker API",
}

const RANK: Record<Finding["level"], number> = { critical: 3, warning: 2, notice: 1 }

export function foldProxyFindings({
  certs,
  certbot,
  vhosts,
  streams,
  ports,
}: {
  certs?: Certificate[]
  /** `null` when certbot is not installed, which is not a finding. */
  certbot?: CertbotState | null
  vhosts?: VHost[]
  streams?: StreamStatus
  ports?: Listener[]
}): ProxyFinding[] {
  const out: ProxyFinding[] = []

  for (const cert of certs ?? []) {
    const usedBy = cert.usedBy.length ? ` Used by ${cert.usedBy.join(", ")}.` : ""
    if (cert.error) {
      out.push({
        id: `cert.error.${cert.path || cert.name}`,
        level: "warning",
        title: `${cert.name} could not be read`,
        detail: cert.error,
        advice:
          "A site pointing at a certificate nginx cannot read fails its next reload. Fix or replace the file, or point the site elsewhere.",
        meta: "certificate",
        href: "/proxy/certificates",
      })
    } else if (cert.expired) {
      out.push({
        id: `cert.expired.${cert.path}`,
        level: "critical",
        title: `${cert.name} has expired`,
        detail: `Expired ${-cert.daysLeft} day${cert.daysLeft === -1 ? "" : "s"} ago; every browser refuses it now.${usedBy}`,
        advice: "Renew it, then find out why the renewal did not run on its own.",
        meta: "certificate",
        href: "/proxy/certificates",
      })
    } else if (cert.expiring) {
      out.push({
        id: `cert.expiring.${cert.path}`,
        level: cert.daysLeft <= 7 ? "critical" : "warning",
        title: `${cert.name} expires in ${cert.daysLeft} day${cert.daysLeft === 1 ? "" : "s"}`,
        detail: `Inside Let's Encrypt's renewal window and still not renewed.${usedBy}`,
        advice:
          "certbot renews at thirty days. A certificate still here a week later means the timer is not running.",
        meta: "certificate",
        href: "/proxy/certificates",
      })
    }
  }

  if (certbot && certbot.available && !certbot.autoRenew && certbot.certs.length > 0) {
    out.push({
      id: "certbot.no-timer",
      level: "critical",
      title: "Nothing is scheduled to renew certbot's certificates",
      detail: `No certbot timer and no cron entry was found for ${certbot.certs.length} certificate${certbot.certs.length === 1 ? "" : "s"}.`,
      advice: certbot.renewUnit
        ? `${certbot.renewUnit} is installed but not running. Turn it on from the Certificates page.`
        : "Install certbot's timer or a cron entry; without one every certificate here expires in ninety days.",
      meta: "renewal",
      href: "/proxy/certificates",
    })
  }

  for (const vhost of vhosts ?? []) {
    if (vhost.kind !== "nginx") continue
    if (!vhost.enabled && vhost.enabledPath) {
      out.push({
        id: `site.disabled.${vhost.name}`,
        level: "notice",
        title: `${vhost.name} is on disk but not serving`,
        detail: "The file is in sites-available with no link in sites-enabled.",
        advice: "Enable it from Sites if it is meant to serve, or delete it if it is not.",
        meta: "site",
        href: `/proxy/sites?site=${encodeURIComponent(vhost.name)}`,
      })
      continue
    }
    if (vhost.enabled && !vhost.tls && vhost.upstreams.length > 0) {
      out.push({
        id: `site.plain.${vhost.name}`,
        level: "warning",
        title: `${vhost.name} serves an application in plain text`,
        detail: `${vhost.serverNames.join(", ") || vhost.name} proxies to ${vhost.upstreams[0]} with no TLS, so anything typed into it crosses the network readable.`,
        advice:
          "Issue a certificate from the Certificates page, then turn TLS on in the site's form.",
        meta: "site",
        href: `/proxy/sites?site=${encodeURIComponent(vhost.name)}`,
      })
    }
  }

  if (streams && streams.streams.length > 0 && !streams.included) {
    out.push({
      id: "streams.not-included",
      level: "warning",
      title: `${streams.streams.length} stream${streams.streams.length === 1 ? " is" : "s are"} written but nginx is not reading ${streams.streams.length === 1 ? "it" : "them"}`,
      detail: `nginx.conf has no stream block including ${streams.dir}.`,
      advice:
        "Add the include the Streams page prints, at the top level of nginx.conf beside the http block.",
      meta: "streams",
      href: "/proxy/streams",
    })
  }
  for (const stream of streams?.streams ?? []) {
    if (stream.allowFrom.length > 0) continue
    const service = DANGEROUS_PORTS[stream.listen]
    out.push({
      id: `stream.open.${stream.name}`,
      level: service ? "warning" : "notice",
      title: `Stream ${stream.name} forwards port ${stream.listen} to anyone`,
      detail: `${stream.protocol.toUpperCase()} ${stream.listen} → ${stream.upstream} with no allow list${service ? `, and ${stream.listen} is ${service}` : ""}.`,
      advice:
        "A stream has no authentication of its own. Restrict the source unless the service behind it authenticates for itself.",
      meta: "stream",
      href: "/proxy/streams",
    })
  }

  const exposed = (ports ?? []).filter((l) => l.exposed)
  const dangerous = exposed.filter((l) => DANGEROUS_PORTS[l.port])
  if (dangerous.length > 0) {
    out.push({
      id: "ports.dangerous",
      level: "warning",
      title:
        dangerous.length === 1
          ? `${DANGEROUS_PORTS[dangerous[0].port]} answers on every interface`
          : `${dangerous.length} database or control ports answer on every interface`,
      detail: dangerous.map((l) => `${l.port}/${l.protocol} ${l.process || "unknown"}`).join(", "),
      advice:
        "Bind these to loopback or a private address, or close them in the firewall. A database port on the internet is the commonest way a server is emptied.",
      meta: "ports",
      href: "/proxy/ports",
    })
  }

  return out.sort((a, b) => RANK[b.level] - RANK[a.level])
}
