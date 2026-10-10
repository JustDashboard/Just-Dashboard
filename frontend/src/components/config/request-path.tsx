"use client"

import { useRef } from "react"
import { DesktopDevice, Globe, Terminal, type Icon } from "@/components/icons"
import { describeClient, parseAgent } from "@/lib/clients"
import type { DashboardConfigReport } from "@/lib/types"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { cn } from "@/lib/utils"

/**
 * How this tab reaches the dashboard, drawn as the things a request passes
 * through: this browser, the network it arrives on, Caddy, and the two
 * services Caddy hands it to — Next.js for the pages, Go for `/api`.
 *
 * It replaced a list of the three services, each with its address beside it.
 * The list said what the stack is made of but not how it fits, and the one
 * fact a reader comes to this page with — *why can I (or can't I) reach it
 * from here* — is a path, so it is drawn as one in the wiring vocabulary the
 * deployment and database maps already speak (`deploy/wire`). Every mark is
 * the product itself: the browser this tab is, Tailscale when the address is
 * a tailnet name, the certificate's issuer on the proxy.
 *
 * The wires pulse because this tab's own requests are going down them; while
 * a restart is in flight the containers are being replaced, so they are drawn
 * still and the services carry an amber dot until it is over.
 */
export function RequestPath({
  report,
  restarting,
}: {
  report: DashboardConfigReport
  restarting: boolean
}) {
  const container = useRef<HTMLDivElement>(null)
  const browserNode = useRef<HTMLDivElement>(null)
  const networkNode = useRef<HTMLDivElement>(null)
  const proxyNode = useRef<HTMLDivElement>(null)
  const frontendNode = useRef<HTMLDivElement>(null)
  const backendNode = useRef<HTMLDivElement>(null)

  const s = report.settings
  const network = networkReading(report)
  const agent = parseAgent(navigator.userAgent)
  const here = window.location.hostname
  const reached =
    here === s.site
      ? "on the configured address"
      : here === "localhost" || here === "127.0.0.1"
        ? "through an SSH tunnel"
        : `at ${here}`
  const allowed = s.allowedCidrs.split(",").filter((entry) => entry.trim()).length
  const certificate = certificateReading(report)

  return (
    <div ref={container} className="relative min-w-0">
      {[
        [browserNode, networkNode],
        [networkNode, proxyNode],
        [proxyNode, frontendNode],
        [proxyNode, backendNode],
      ].map(([from, to], index) => (
        <AnimatedBeam
          key={index}
          containerRef={container}
          fromRef={from}
          toRef={to}
          shape={index > 1 ? "s" : "arc"}
          still={restarting}
          tone={restarting ? "warning" : "default"}
          duration={2.4}
          delay={index * 0.45}
        />
      ))}

      <ol
        aria-label="How a request reaches the dashboard"
        className="grid min-w-0 gap-y-7 lg:grid-cols-[repeat(3,minmax(0,1fr))_minmax(0,1.15fr)] lg:items-center lg:gap-x-6 lg:pb-16"
      >
        <li className="min-w-0">
          <WireNode
            nodeRef={browserNode}
            align="center"
            mark={<Mark product={agent.product} fallback={DesktopDevice} />}
            eyebrow="You"
            title={describeClient(navigator.userAgent)}
            hint={reached}
          />
        </li>
        <li className="min-w-0">
          <WireNode
            nodeRef={networkNode}
            align="center"
            mark={<Mark product={network.product} fallback={network.glyph} />}
            eyebrow={network.eyebrow}
            title={<span className="block truncate font-mono">{network.title}</span>}
            hint={
              <>
                {network.hint && (
                  <span className={cn("block truncate", network.address && "font-mono")}>
                    {network.hint}
                  </span>
                )}
                <span className="block">
                  {allowed} {allowed === 1 ? "network" : "networks"} allowed
                </span>
              </>
            }
          />
        </li>
        <li className="min-w-0">
          <WireNode
            nodeRef={proxyNode}
            align="center"
            mark={<Mark product="caddy" />}
            eyebrow="Proxy"
            title={
              <>
                Caddy <span className="numeric font-mono text-[var(--tag-pink)]">:{s.port}</span>
              </>
            }
            hint={
              <span className="inline-flex items-center gap-1.5">
                {certificate.product && <ProductGlyph id={certificate.product} />}
                {certificate.label}
              </span>
            }
          />
        </li>
        {/* Two to a row on a phone, so both wires leave the proxy's mark
            rather than one of them running through the other service. */}
        <li className="grid min-w-0 grid-cols-2 gap-4 lg:flex lg:flex-col lg:gap-6">
          <WireNode
            nodeRef={frontendNode}
            mark={<Mark product="nextjs" restarting={restarting} />}
            eyebrow="Frontend"
            title={<Loopback port={s.frontendPort} />}
            hint="Next.js · every page"
          />
          <WireNode
            nodeRef={backendNode}
            mark={<Mark product="go" restarting={restarting} />}
            eyebrow="Backend"
            title={<Loopback port={s.backendPort} />}
            hint="Go · /api and /healthz"
          />
        </li>
      </ol>
    </div>
  )
}

/**
 * A product on its tile. A service of the stack carries its state in the
 * corner, the way the project map's runtime does: green while it serves, amber
 * while a restart is replacing it.
 */
function Mark({
  product,
  fallback: Fallback = Globe,
  restarting,
}: {
  product?: string
  fallback?: Icon
  restarting?: boolean
}) {
  const tile = (
    <WireMark tone="logo">
      {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Fallback aria-hidden />}
    </WireMark>
  )
  if (restarting === undefined) return tile
  return (
    <span className="relative flex">
      {tile}
      <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
        <StatusDot tone={restarting ? "warning" : "running"} />
      </span>
    </span>
  )
}

function Loopback({ port }: { port: number }) {
  return (
    <span className="numeric block truncate font-mono">
      {/* Two to a row on a phone leaves no room for the address every
          service shares; the port is the part that differs. */}
      <span className="text-muted-foreground max-sm:hidden">127.0.0.1</span>
      <span className="text-[var(--tag-pink)]">:{port}</span>
    </span>
  )
}

/** The network a request arrives on, as the settings say it is reached. */
function networkReading(report: DashboardConfigReport): {
  eyebrow: string
  title: string
  hint?: string
  /** The hint is an address rather than words. */
  address?: boolean
  product?: string
  glyph: Icon
} {
  const s = report.settings
  if (s.tls === "tailscale") {
    return {
      eyebrow: "Tailnet",
      title: report.tailscale.hostname ?? s.site,
      hint: s.bind || report.tailscale.ip4,
      address: true,
      product: "tailscale",
      glyph: Globe,
    }
  }
  if (s.site === "localhost" || s.tls === "off") {
    return { eyebrow: "Loopback", title: "localhost", hint: "an SSH tunnel's end", glyph: Terminal }
  }
  return {
    eyebrow: "Network",
    title: s.site,
    hint: s.bind || undefined,
    address: true,
    glyph: Globe,
  }
}

/**
 * The certificate on the proxy as its issuer: what is on disk, not what the
 * mode promises, so a Tailscale install still waiting for its certificate is
 * drawn on Caddy's own CA.
 */
export function certificateReading(report: DashboardConfigReport): {
  label: string
  product?: string
  trusted: boolean
} {
  const { tls } = report.settings
  if (tls === "off") return { label: "plain HTTP", trusted: false }
  if (tls === "tailscale" && report.certificate.issued) {
    return { label: "Let's Encrypt", product: "lets-encrypt", trusted: true }
  }
  return { label: "Caddy's own CA", product: "caddy", trusted: false }
}

/** The address as a browser would be given it, each part in its own hue. */
export function EndpointText({ url, className }: { url: string; className?: string }) {
  const match = /^(https?:\/\/)([^:/]+)(:\d+)?(.*)$/.exec(url)
  if (!match) return <span className={cn("font-mono", className)}>{url}</span>
  const [, scheme, host, port, rest] = match
  return (
    <span className={cn("min-w-0 truncate font-mono", className)}>
      <span className="text-muted-foreground">{scheme}</span>
      <span className="text-foreground">{host}</span>
      {port && <span className="numeric text-[var(--tag-pink)]">{port}</span>}
      {rest && <span className="text-muted-foreground">{rest}</span>}
    </span>
  )
}
