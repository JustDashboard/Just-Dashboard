"use client"

import { useRef } from "react"
import { Globe, SecureConnection, Shield, ShieldCheck, type Icon } from "@/components/icons"
import { networkOf } from "@/lib/clients"
import type { Exposure, Fail2banJail, FirewallStatus, Posture } from "@/lib/types"
import { cn } from "@/lib/utils"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { EXPOSURE_GRADE } from "@/components/security/exposure-panel"
import { openings, refusesByDefault } from "@/components/network/firewall-reading"
import { worstLevel } from "@/components/security/posture-panel"

type Fail2ban = { available: boolean; running: boolean; jails: Fail2banJail[] }

type Line = {
  from: React.RefObject<HTMLDivElement | null>
  to: React.RefObject<HTMLDivElement | null>
  still?: boolean
  dashed?: boolean
  tone?: "default" | "success" | "warning" | "danger"
}

/**
 * The two ways onto this machine, drawn in the wiring vocabulary the
 * Configuration page's request path and the deployment maps speak
 * (`deploy/wire`): what a connection from the internet meets on its way to a
 * shell — the firewall, fail2ban, sshd — and the way this browser reached the
 * dashboard, through its allowlist.
 *
 * It replaced a framed picture of the second lane alone. The area tiles above
 * say how each part is doing; this says how they stand in front of each other,
 * which a row of figures cannot: a firewall that is off leaves fail2ban as the
 * only thing between the internet and a password prompt, and that is a line
 * drawn red into the next mark rather than a word on a tile.
 *
 * The line is the state, as on every wiring picture: dashed where a layer is
 * missing, red where one is off, amber where it works but should not be
 * relied on, and moving where it carries — the internet lane while peers from
 * the internet are connected, this browser's lane because its own requests
 * are going down it.
 */
export function Perimeter({
  exposure,
  firewall,
  fail2ban,
  posture,
  fromInternet,
}: {
  exposure: Exposure | undefined
  firewall: FirewallStatus | undefined
  fail2ban: Fail2ban | undefined
  posture: Posture | undefined
  /** Peers from the internet connected now; undefined while unread. */
  fromInternet: number | undefined
}) {
  const container = useRef<HTMLDivElement>(null)
  const internet = useRef<HTMLDivElement>(null)
  const wall = useRef<HTMLDivElement>(null)
  const jail = useRef<HTMLDivElement>(null)
  const sshd = useRef<HTMLDivElement>(null)
  const you = useRef<HTMLDivElement>(null)
  const allow = useRef<HTMLDivElement>(null)
  const panel = useRef<HTMLDivElement>(null)

  const knocking = (fromInternet ?? 0) > 0
  const sshFindings = posture?.findings.filter((f) => f.area === "ssh") ?? []
  const sshLevel = worstLevel(sshFindings)
  const noSshd = posture?.skipped.includes("ssh") ?? false

  const enforcing = Boolean(firewall?.available && firewall.enabled)
  const inbound = firewall?.policy?.incoming
  const open = openings(firewall).filter((o) => o.anyone)
  const banned = fail2ban?.jails.reduce((n, j) => n + j.currentlyBanned, 0) ?? 0

  const grade = exposure ? EXPOSURE_GRADE[exposure.grade] : undefined
  const risky = exposure?.grade === "open" || exposure?.grade === "public"
  const network = networkOf(exposure?.client || "")

  const lines: Line[] = [
    {
      from: internet,
      to: wall,
      ...(!firewall?.available
        ? { dashed: true, still: true, tone: "warning" }
        : !firewall.enabled
          ? { still: true, tone: "danger" }
          : { still: !knocking }),
    },
    {
      from: wall,
      to: jail,
      ...(!fail2ban?.available
        ? { dashed: true, still: true }
        : !fail2ban.running
          ? { still: true, tone: "warning" }
          : { still: !knocking }),
    },
    {
      from: jail,
      to: sshd,
      ...(noSshd
        ? { dashed: true, still: true }
        : sshLevel === "critical"
          ? { still: true, tone: "danger" }
          : sshLevel === "warning"
            ? { still: true, tone: "warning" }
            : { still: !knocking }),
    },
    { from: you, to: allow, ...(risky ? { still: true, tone: "warning" } : {}) },
    { from: allow, to: panel, ...(risky ? { still: true, tone: "warning" } : {}) },
  ]

  return (
    <div className="relative min-w-0 animate-rise py-6">
      <div aria-hidden className="wire-grid pointer-events-none absolute inset-0" />
      <div ref={container} className="relative min-w-0">
        {lines.map((line, index) => (
          <AnimatedBeam
            key={index}
            containerRef={container}
            fromRef={line.from}
            toRef={line.to}
            still={line.still}
            dashed={line.dashed}
            tone={line.tone ?? "default"}
            duration={2.4}
            delay={(index % 3) * 0.45}
          />
        ))}
        <ol
          aria-label="The ways onto this machine"
          className="grid min-w-0 gap-y-7 lg:grid-cols-4 lg:items-center lg:gap-x-6 lg:gap-y-24 lg:pb-16"
        >
          <li className="min-w-0">
            <WireNode
              nodeRef={internet}
              align="center"
              mark={<Mark fallback={Globe} tone="logo" />}
              eyebrow="Anyone"
              title="The internet"
              hint={
                fromInternet === undefined
                  ? "not read yet"
                  : knocking
                    ? `${fromInternet} connected now`
                    : "nobody connected now"
              }
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={wall}
              align="center"
              mark={
                !firewall?.available ? (
                  <WirePlaceholder fallback={Shield} />
                ) : (
                  <Mark fallback={Shield} tone={enforcing ? "logo" : "danger"} />
                )
              }
              eyebrow="Firewall"
              title={firewall?.available ? firewall.backend : "None"}
              hint={
                !firewall?.available ? (
                  <span className="text-warning">nothing filters</span>
                ) : !firewall.enabled ? (
                  <span className="text-destructive">not enforcing</span>
                ) : (
                  <>
                    <span
                      className={cn(
                        "block",
                        inbound && !refusesByDefault(inbound) && "text-warning",
                      )}
                    >
                      {inbound ?? "—"} inbound
                    </span>
                    <Ports keys={open.map((o) => o.port ?? o.key)} />
                  </>
                )
              }
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={jail}
              align="center"
              mark={
                !fail2ban?.available ? (
                  <WirePlaceholder product="fail2ban" />
                ) : (
                  <Mark product="fail2ban" tone={fail2ban.running ? "logo" : "warning"} />
                )
              }
              eyebrow="Intrusion"
              title="fail2ban"
              hint={
                !fail2ban ? (
                  "not read yet"
                ) : !fail2ban.available ? (
                  "not installed"
                ) : !fail2ban.running ? (
                  <span className="text-warning">not running</span>
                ) : (
                  <>
                    <span className="block">
                      {fail2ban.jails.length} jail{fail2ban.jails.length === 1 ? "" : "s"} watching
                    </span>
                    <span className={cn("numeric block", banned > 0 && "text-warning")}>
                      {banned} banned now
                    </span>
                  </>
                )
              }
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={sshd}
              align="center"
              mark={
                noSshd ? (
                  <WirePlaceholder fallback={SecureConnection} />
                ) : (
                  <Mark
                    fallback={SecureConnection}
                    tone={
                      sshLevel === "critical"
                        ? "danger"
                        : sshLevel === "warning"
                          ? "warning"
                          : "logo"
                    }
                  />
                )
              }
              eyebrow="Shell"
              title="sshd"
              hint={
                noSshd ? (
                  "none on this host"
                ) : sshFindings.length > 0 ? (
                  <span
                    className={cn(
                      "line-clamp-2",
                      sshLevel === "critical" && "text-destructive",
                      sshLevel === "warning" && "text-warning",
                    )}
                  >
                    {sshFindings[0].title}
                  </span>
                ) : posture ? (
                  <span className="text-success">at recommendation</span>
                ) : undefined
              }
            />
          </li>

          {/* Stacked on a phone, the second lane starts a step further down so
              the two read as two ways in rather than one path of seven. */}
          <li className="min-w-0 max-lg:mt-6">
            <WireNode
              nodeRef={you}
              align="center"
              mark={
                <Mark
                  product={network.product}
                  fallback={NETWORK_GLYPH[network.kind]}
                  tone="logo"
                />
              }
              eyebrow="You"
              title={
                <span className="block truncate font-mono">
                  {exposure?.client || "Address unavailable"}
                </span>
              }
              hint={network.label}
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={allow}
              align="center"
              mark={
                <Mark
                  product={exposure?.grade === "tailscale" ? "tailscale" : undefined}
                  fallback={risky ? Globe : ShieldCheck}
                  tone={risky ? "warning" : "logo"}
                />
              }
              eyebrow="Allowlist"
              title={
                <span className={cn(risky && "text-warning")}>{grade?.label ?? "Reading…"}</span>
              }
              hint={
                exposure?.allowlist.length
                  ? `${exposure.allowlist.length} allowed range${exposure.allowlist.length === 1 ? "" : "s"}`
                  : exposure
                    ? "no network restriction"
                    : undefined
              }
            />
          </li>
          <li className="min-w-0 lg:col-start-4">
            <WireNode
              nodeRef={panel}
              align="center"
              mark={<WireHost />}
              eyebrow="This dashboard"
              title={
                <span className="block truncate font-mono">
                  {exposure?.tailscaleIp || "This server"}
                </span>
              }
              hint={exposure?.interfaces.join(", ") || "protected by sign-in"}
            />
          </li>
        </ol>
      </div>
    </div>
  )
}

/** A product on its tile, or the glyph for a layer no product names, in its state's tint. */
function Mark({
  product,
  fallback: Fallback,
  tone,
}: {
  product?: string
  fallback?: Icon
  tone: "logo" | "warning" | "danger"
}) {
  return (
    <WireMark tone={tone} shape="square">
      {hasProductLogo(product) ? (
        <ProductGlyph id={product} />
      ) : Fallback ? (
        <Fallback aria-hidden />
      ) : null}
    </WireMark>
  )
}

/** The ports open to anyone, each in the port hue the Configuration page gives one. */
function Ports({ keys }: { keys: string[] }) {
  if (keys.length === 0) return <span className="block">nothing open to anyone</span>
  return (
    <span className="numeric block truncate font-mono">
      {keys.slice(0, 4).map((key) => (
        <span key={key} className="mr-1.5">
          <span className="text-muted-foreground">:</span>
          <span className="text-[var(--tag-pink)]">{key}</span>
        </span>
      ))}
      {keys.length > 4 && <span>+{keys.length - 4}</span>}
      <span className="font-sans"> open</span>
    </span>
  )
}
