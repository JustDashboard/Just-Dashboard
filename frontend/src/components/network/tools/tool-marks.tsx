import {
  ArrowUpRight,
  Calculator,
  Connection,
  Cpu,
  Crosshair,
  Envelope,
  FileText,
  Gauge,
  Globe,
  GridSquare,
  Hub,
  Inbox,
  Inspect,
  Layers,
  Lightning,
  ListOrdered,
  LockClosed,
  LockOpen,
  NetworkDevice,
  Route,
  Router,
  SecureConnection,
  Shield,
  ShieldCheck,
  ShieldOff,
  Terminal,
  Topology,
  type Icon,
} from "@/components/icons"
import { cn } from "@/lib/utils"

/**
 * What each diagnostic is drawn as, so the rail is scanned by its marks before
 * its words: the glyph the rest of the product already gives the thing (DNS is
 * the globe the DNS page wears, a certificate the lock Certificates wears, SSH
 * the secure connection the SSH page wears, a port the router Ports wears),
 * on the hue of its group. Every card in a group had one grey glyph, so the
 * rail was a column of words.
 */
export const TOOL_GLYPH: Record<string, Icon> = {
  dns: Globe,
  dnsauth: ListOrdered,
  ping: Connection,
  traceroute: Topology,
  route: Route,
  mtu: Gauge,
  path: Inspect,
  port: Router,
  scan: GridSquare,
  banner: Terminal,
  ssh: SecureConnection,
  http: Globe,
  httpsec: Shield,
  tls: LockClosed,
  tlssurvey: Layers,
  siteaudit: ShieldCheck,
  mx: Envelope,
  starttls: LockOpen,
  dnsbl: ShieldOff,
  asn: Hub,
  whois: FileText,
  capabilities: Cpu,
  capture: Crosshair,
  listeners: Inbox,
  egress: ArrowUpRight,
  neigh: NetworkDevice,
  wol: Lightning,
  subnet: Calculator,
}

/** One hue per group, in the order of `TOOL_GROUPS`; the chart ramp, so it holds in both themes. */
export const GROUP_HUE = [
  "var(--chart-2)",
  "var(--chart-5)",
  "var(--chart-4)",
  "var(--chart-3)",
  "var(--chart-1)",
] as const

/**
 * A diagnostic's mark: its glyph on a tint of its group's hue. `sm` is the
 * rail's, `lg` the empty pane's. The address planner's calculator has no
 * group and stands on the neutral ground.
 */
export function ToolMark({
  tool,
  hue,
  size = "sm",
  className,
}: {
  tool: string
  hue?: string
  size?: "sm" | "md" | "lg"
  className?: string
}) {
  const Glyph = TOOL_GLYPH[tool] ?? Globe
  return (
    <span
      aria-hidden
      data-slot="tool-mark"
      className={cn(
        "flex shrink-0 items-center justify-center",
        size === "sm" && "size-6 rounded-md [&>svg]:size-3.5",
        size === "md" && "size-8 rounded-lg [&>svg]:size-4",
        size === "lg" && "size-14 rounded-xl [&>svg]:size-6",
        !hue && "border border-hairline bg-control text-foreground",
        className,
      )}
      style={
        hue ? { color: hue, background: `color-mix(in oklab, ${hue} 14%, transparent)` } : undefined
      }
    >
      <Glyph />
    </span>
  )
}
