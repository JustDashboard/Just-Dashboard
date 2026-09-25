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
  if (!vhost.tls) return <span className="text-xs text-muted-foreground">plain HTTP</span>
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
