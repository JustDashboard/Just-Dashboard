import { ShieldCheck } from "@/components/icons"
import type { VHost } from "@/lib/types"
import { ProductGlyph } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { certPathProduct } from "@/components/proxy/marks"

/**
 * A site's two states, as the overview's list and the Sites cards both draw
 * them: whether it is on TLS and by whose certificate, and whether it is
 * serving. Declared once so the two pages cannot disagree about a site.
 */

/** On TLS, with Let's Encrypt drawn as itself where the site points at certbot's live directory. */
export function SiteTLS({ vhost }: { vhost: VHost }) {
  if (!vhost.tls)
    return vhost.enabled && vhost.upstreams.length > 0 ? (
      <Status verdict="warning" label="plain HTTP" />
    ) : (
      <span className="text-xs text-muted-foreground">plain HTTP</span>
    )
  const product = certPathProduct(vhost.certPath)
  if (!product) return <Status state="active" label="TLS" icon={ShieldCheck} />
  return (
    <span className="inline-flex items-center gap-1.5 text-xs font-medium whitespace-nowrap">
      <ProductGlyph id={product} />
      <span>TLS</span>
    </span>
  )
}

/** Whether the site is serving, said as a state rather than a switch. */
export function ServingStatus({ vhost, busy }: { vhost: VHost; busy?: string }) {
  if (busy) return <Status state="activating" label={`${busy}…`} />
  if (vhost.broken === "dangling") return <Status verdict="critical" label="broken link" />
  if (vhost.broken === "stale") return <Status verdict="warning" label="stale link" />
  if (vhost.kind === "nginx" && !vhost.enabledPath && vhost.enabled) {
    // conf.d: every present .conf file is active and there is nothing to
    // toggle, which "always on" says without offering a control.
    return <Status state="active" label="always on" />
  }
  return vhost.enabled ? (
    <Status state="active" label="serving" />
  ) : (
    <Status state="inactive" label="disabled" />
  )
}

/** Where an nginx site lives, where that is not the ordinary sites-available. */
export function siteKind(vhost: VHost): string {
  if (vhost.kind !== "nginx") return vhost.path ? "Caddyfile" : "Docker Caddy ingress"
  if (vhost.layout === "conf.d") return "nginx site in conf.d"
  if (vhost.layout === "sites-enabled") return "nginx site in sites-enabled"
  return "nginx site"
}

/** Why a broken site's name in sites-enabled does not serve it, in one line. */
export function SiteLinkNote({ vhost }: { vhost: VHost }) {
  const link = <span className="font-mono">sites-enabled/{vhost.name}</span>
  const target = <span className="font-mono break-all">{vhost.linkTarget}</span>
  if (vhost.broken === "dangling") {
    return (
      <p className="text-hint text-muted-foreground">
        {link} points at {target}, which is missing, so nginx refuses every reload until the link is
        {vhost.layout === "sites-available"
          ? " pointed back at this file or removed."
          : " removed."}
      </p>
    )
  }
  if (vhost.broken === "stale") {
    return (
      <p className="text-hint text-muted-foreground">
        {vhost.linkTarget ? (
          <>
            {link} points at {target}, so nginx serves that file instead of this one.
          </>
        ) : (
          <>{link} is a separate file rather than a link, so nginx serves that copy instead.</>
        )}
      </p>
    )
  }
  return null
}
