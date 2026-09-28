import { ShieldCheck } from "@/components/icons"
import type { VHost } from "@/lib/types"
import { ProductGlyph } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { certPathProduct } from "@/components/proxy/marks"
import { FEATURE_LABEL, activeOwner } from "@/components/proxy/site-details"

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

/**
 * Whether the site is serving, said as a state rather than a switch.
 * `unread` is a site a verb changed whose list could not be read again: what
 * `vhost` says is from before the change, so it is not drawn.
 *
 * "serving" is a claim about the engine as well as the file, and is not made
 * where the page knows better. `stopped` is a site whose engine is not
 * running, which serves nothing whatever its file says; `unloaded` is a site
 * a verb changed that nginx did not reload, so a running nginx still has it
 * as it was; and `notLive` is what nginx itself shows of that — the file
 * changed since the configuration it is running was loaded — in words such
 * as "saved, not live".
 */
export function ServingStatus({
  vhost,
  busy,
  unread,
  stopped,
  unloaded,
  notLive,
}: {
  vhost: VHost
  busy?: string
  unread?: boolean
  stopped?: boolean
  unloaded?: boolean
  notLive?: string
}) {
  if (busy) return <Status state="activating" label={`${busy}…`} />
  if (unread) return <Status tone="unknown" label="not read back" />
  if (vhost.broken === "dangling") return <Status verdict="critical" label="broken link" />
  if (vhost.broken === "stale") return <Status verdict="warning" label="stale link" />
  if (stopped) {
    return vhost.enabled ? (
      <Status tone="stopped" label="enabled" />
    ) : (
      <Status state="inactive" label="disabled" />
    )
  }
  if (unloaded) {
    return (
      <Status
        verdict="warning"
        label={vhost.enabled ? "enabled, not reloaded" : "disabled, not reloaded"}
      />
    )
  }
  if (notLive) return <Status verdict="warning" label={notLive} />
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

/**
 * Who writes the site, where that is a deployment; otherwise where an nginx
 * site lives, where that is not the ordinary sites-available.
 */
export function siteKind(vhost: VHost): string {
  const owner = activeOwner(vhost)
  if (owner) return `Managed by ${owner.project} · ${owner.environment}`
  if (vhost.kind !== "nginx") return vhost.path ? "Caddyfile" : "Docker Caddy ingress"
  if (vhost.layout === "conf.d") return "nginx site in conf.d"
  if (vhost.layout === "sites-enabled") return "nginx site in sites-enabled"
  return "nginx site"
}

/**
 * What the rest of the card cannot say about where the site comes from and
 * how nginx reaches it: a name in sites-enabled that does not serve its file,
 * the other names that do, a file the editor will not open because it lives
 * outside the nginx directory, a deployment that wrote the route and is
 * archived, and a file still as its package installed it.
 */
export function SiteNotes({ vhost }: { vhost: VHost }) {
  const link = <span className="font-mono">sites-enabled/{vhost.name}</span>
  const target = <span className="font-mono break-all">{vhost.linkTarget}</span>
  const lines: { key: string; text: React.ReactNode }[] = []
  if (vhost.broken === "dangling") {
    lines.push({
      key: "dangling",
      text: (
        <>
          {link} points at {target}, which is missing, so nginx refuses every reload until the link
          is
          {vhost.layout === "sites-available"
            ? " pointed back at this file or removed."
            : " removed."}
        </>
      ),
    })
  } else if (vhost.broken === "stale") {
    lines.push({
      key: "stale",
      text: vhost.linkTarget ? (
        <>
          {link} points at {target}, so nginx serves that file instead of this one.
        </>
      ) : (
        <>{link} is a separate file rather than a link, so nginx serves that copy instead.</>
      ),
    })
  }
  if (vhost.linkedAs?.length) {
    lines.push({
      key: "linked",
      text: (
        <>
          nginx serves this file through{" "}
          {vhost.linkedAs.map((alias, i) => (
            <span key={alias}>
              {i > 0 && " and "}
              <span className="font-mono break-all">sites-enabled/{alias}</span>
            </span>
          ))}
          .
        </>
      ),
    })
  }
  if (vhost.resolvesTo) {
    lines.push({
      key: "outside",
      text: (
        <>
          <span className="font-mono">
            {vhost.layout}/{vhost.name}
          </span>{" "}
          links to <span className="font-mono break-all">{vhost.resolvesTo}</span>, outside the
          nginx directory, so this page does not open it.
        </>
      ),
    })
  }
  const owner = vhost.owner
  if (activeOwner(vhost) && owner) {
    lines.push({
      key: "owner",
      text: (
        <>
          The next deploy of <b className="font-medium text-foreground">{owner.project}</b> writes
          this route again, and an edit made here is lost then.
        </>
      ),
    })
  } else if (owner?.archived) {
    lines.push({
      key: "owner",
      text: (
        <>
          Written by <b className="font-medium text-foreground">{owner.project}</b> ·{" "}
          {owner.environment}, which is archived: nothing deploys it any more.
        </>
      ),
    })
  }
  if (vhost.package) {
    lines.push({
      key: "package",
      text: (
        <>
          As the <span className="font-mono">{vhost.package}</span> package installed it, unchanged
          since.
        </>
      ),
    })
  }
  return lines.map((line) => (
    <p key={line.key} className="text-hint text-muted-foreground">
      {line.text}
    </p>
  ))
}

/** What the site does besides naming and listening, one word each. */
export function SiteFeatures({ vhost }: { vhost: VHost }) {
  if (!vhost.features?.length) return null
  return vhost.features.map((feature) => (
    <Tag key={feature} tone={feature === "maintenance" ? "warning" : "default"}>
      {FEATURE_LABEL[feature]}
    </Tag>
  ))
}
