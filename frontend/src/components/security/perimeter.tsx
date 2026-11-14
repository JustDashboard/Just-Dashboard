"use client"

import { useRef } from "react"
import Link from "next/link"
import { Globe, SecureConnection, Shield, ShieldCheck, SignIn, type Icon } from "@/components/icons"
import { networkOf } from "@/lib/clients"
import { plural } from "@/lib/format"
import type { Connections, Exposure, Fail2banJail, FirewallStatus, LoginSession } from "@/lib/types"
import { cn } from "@/lib/utils"
import { InitialsMark } from "@/components/account/user-avatar"
import { NETWORK_GLYPH } from "@/components/client-mark"
import {
  ProductGlyph,
  ProductGlyphs,
  hasProductLogo,
  processProduct,
} from "@/components/product-logo"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { EXPOSURE_GRADE } from "@/components/security/exposure-panel"
import { openings, refusesByDefault } from "@/components/network/firewall-reading"
import type { Tone } from "@/components/tone"

type Fail2ban = { available: boolean; running: boolean; jails: Fail2banJail[] }

/** A read as the picture needs it: what it said, or that it failed. */
export type Read<T> = { data: T | undefined; failed: boolean }

/** sshd in one line (`ssh-reading.ts`), or why there is none to give. */
export type SSHWords = { value: string; tone: Tone; hint: string | undefined }

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
 * shell — the firewall, fail2ban, sshd, and then whoever is signed in — and
 * the way this browser reached the dashboard, through its allowlist.
 *
 * It is the section's page in one picture. A row of five tiles stood over it
 * and said how each part was doing; four of the five said what a node here
 * already said, so each reading is on its node now and the node's mark is the
 * way to its page: the internet to Connections, the firewall to Firewall,
 * fail2ban to Intrusion, sshd to SSH, the people signed in to Logins. What
 * the picture adds to the figures is how they stand in front of each other: a
 * firewall that is off leaves fail2ban as the only thing between the
 * internet and a password prompt, and that is a line drawn red into the next
 * mark rather than a word on a tile.
 *
 * The line is the state, as on every wiring picture: dashed where a layer is
 * missing, red where one is off, amber where it works but should not be
 * relied on, and moving where it carries — the internet lane while peers from
 * the internet are connected, the last hop while somebody holds a shell over
 * ssh, this browser's lane because its own requests are going down it. A
 * layer that has not been read yet is said to be unread and drawn plain, not
 * as a layer that is missing.
 */
export function Perimeter({
  exposure,
  firewall,
  fail2ban,
  connections,
  sessions,
  ssh,
}: {
  exposure: Exposure | undefined
  firewall: Read<FirewallStatus>
  fail2ban: Read<Fail2ban>
  connections: Read<Connections>
  sessions: Read<LoginSession[]>
  /** Undefined while sshd's settings are being read. */
  ssh: SSHWords | undefined
}) {
  const container = useRef<HTMLDivElement>(null)
  const internet = useRef<HTMLDivElement>(null)
  const wall = useRef<HTMLDivElement>(null)
  const jail = useRef<HTMLDivElement>(null)
  const sshd = useRef<HTMLDivElement>(null)
  const shells = useRef<HTMLDivElement>(null)
  const you = useRef<HTMLDivElement>(null)
  const allow = useRef<HTMLDivElement>(null)
  const panel = useRef<HTMLDivElement>(null)

  const peers = connections.data?.peers
  const fromInternet = peers?.filter((peer) => !peer.private).length
  const knocking = (fromInternet ?? 0) > 0
  // What the peers are talking to, as the programs holding the sockets.
  const reached = [
    ...new Set(
      (peers ?? [])
        .flatMap((peer) => peer.processes)
        .map(processProduct)
        .filter((id): id is string => Boolean(id)),
    ),
  ]

  const fw = firewall.data
  const enforcing = Boolean(fw?.available && fw.enabled)
  const inbound = fw?.policy?.incoming
  const open = openings(fw).filter((o) => o.anyone)
  const rules = fw?.rules.filter((rule) => !rule.ipv6).length ?? 0

  const f2b = fail2ban.data
  const banned = f2b?.jails.reduce((n, j) => n + j.currentlyBanned, 0) ?? 0

  const held = sessions.data
  const remote = held?.filter((session) => session.isSsh).length ?? 0
  const signedIn = [...new Set((held ?? []).map((session) => session.user))]
  const heldFrom = [
    ...new Set(
      (held ?? [])
        .map((session) => networkOf(session.from || "127.0.0.1").product)
        .filter((id): id is string => Boolean(id)),
    ),
  ]

  const grade = exposure ? EXPOSURE_GRADE[exposure.grade] : undefined
  const risky = exposure?.grade === "open" || exposure?.grade === "public"
  const network = networkOf(exposure?.client || "")

  const lines: Line[] = [
    {
      from: internet,
      to: wall,
      ...(!fw
        ? { still: true }
        : !fw.available
          ? { dashed: true, still: true, tone: "warning" }
          : !fw.enabled
            ? { still: true, tone: "danger" }
            : { still: !knocking }),
    },
    {
      from: wall,
      to: jail,
      ...(!f2b
        ? { still: true }
        : !f2b.available
          ? { dashed: true, still: true }
          : !f2b.running
            ? { still: true, tone: "warning" }
            : { still: !knocking }),
    },
    { from: jail, to: sshd, still: !knocking },
    {
      from: sshd,
      to: shells,
      still: remote === 0,
      ...(ssh?.tone === "danger"
        ? { tone: "danger" }
        : ssh?.tone === "warning"
          ? { tone: "warning" }
          : {}),
    },
    { from: you, to: allow, ...(risky ? { still: true, tone: "warning" } : {}) },
    { from: allow, to: panel, ...(risky ? { still: true, tone: "warning" } : {}) },
  ]

  return (
    <div className="relative min-w-0 animate-rise py-6">
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
            delay={(index % 4) * 0.45}
          />
        ))}
        <ol
          aria-label="The ways onto this machine"
          className="grid min-w-0 gap-y-7 lg:grid-cols-5 lg:items-center lg:gap-x-4 lg:gap-y-24 lg:pb-16 xl:gap-x-6"
        >
          <li className="min-w-0" data-node="connections">
            <WireNode
              nodeRef={internet}
              align="center"
              mark={
                <Way href="/network/connections" label="Connections">
                  <Mark fallback={Globe} tone="logo" />
                </Way>
              }
              eyebrow="Anyone"
              title="The internet"
              hint={
                <Words>
                  <span className="block">
                    {fromInternet === undefined
                      ? connections.failed
                        ? "unreadable"
                        : "not read yet"
                      : knocking
                        ? `${fromInternet} connected now`
                        : "nobody connected now"}
                  </span>
                  {connections.data && (
                    <span className="numeric flex items-center gap-1.5 lg:justify-center">
                      <span className="truncate">
                        {plural(connections.data.peers.length, "address", "addresses")} ·{" "}
                        {plural(connections.data.total, "socket")}
                      </span>
                      <ProductGlyphs ids={reached} max={2} />
                    </span>
                  )}
                </Words>
              }
            />
          </li>
          <li className="min-w-0" data-node="firewall">
            <WireNode
              nodeRef={wall}
              align="center"
              mark={
                <Way href="/network/firewall" label="Firewall">
                  {fw && !fw.available ? (
                    <WirePlaceholder fallback={Shield} />
                  ) : (
                    <Mark fallback={Shield} tone={!fw || enforcing ? "logo" : "danger"} />
                  )}
                </Way>
              }
              eyebrow="Firewall"
              title={!fw ? <Unread failed={firewall.failed} /> : fw.available ? fw.backend : "None"}
              hint={
                <Words>
                  {!fw ? (
                    firewall.failed ? (
                      "its status could not be read"
                    ) : (
                      "not read yet"
                    )
                  ) : !fw.available ? (
                    <>
                      <span className="block text-warning">nothing filters</span>
                      <span className="block">install ufw or firewalld</span>
                    </>
                  ) : !fw.enabled ? (
                    <>
                      <span className="block text-destructive">not enforcing</span>
                      <span className="numeric block">{plural(rules, "rule")}</span>
                    </>
                  ) : (
                    <>
                      <span
                        className={cn(
                          "numeric block",
                          inbound && !refusesByDefault(inbound) && "text-warning",
                        )}
                      >
                        {inbound ? `${inbound} inbound` : "inbound policy unknown"} ·{" "}
                        {plural(rules, "rule")}
                      </span>
                      <Ports keys={open.map((o) => o.port ?? o.key)} />
                    </>
                  )}
                </Words>
              }
            />
          </li>
          <li className="min-w-0" data-node="intrusion">
            <WireNode
              nodeRef={jail}
              align="center"
              mark={
                <Way href="/security/intrusion" label="Intrusion">
                  {f2b && !f2b.available ? (
                    <WirePlaceholder product="fail2ban" />
                  ) : (
                    <Mark product="fail2ban" tone={!f2b || f2b.running ? "logo" : "warning"} />
                  )}
                </Way>
              }
              eyebrow="Intrusion"
              title="fail2ban"
              hint={
                <Words>
                  {!f2b ? (
                    fail2ban.failed ? (
                      <>
                        <span className="block text-warning">unreadable</span>
                        <span className="block">its status could not be read</span>
                      </>
                    ) : (
                      "not read yet"
                    )
                  ) : !f2b.available ? (
                    "not installed"
                  ) : !f2b.running ? (
                    <span className="text-warning">not running</span>
                  ) : (
                    <>
                      <span className="numeric block">
                        {plural(f2b.jails.length, "jail")} watching
                      </span>
                      <span className={cn("numeric block", banned > 0 && "text-warning")}>
                        {banned} banned now
                      </span>
                    </>
                  )}
                </Words>
              }
            />
          </li>
          <li className="min-w-0" data-node="ssh">
            <WireNode
              nodeRef={sshd}
              align="center"
              mark={
                <Way href="/security/ssh" label="SSH">
                  <Mark
                    fallback={SecureConnection}
                    tone={
                      ssh?.tone === "danger"
                        ? "danger"
                        : ssh?.tone === "warning"
                          ? "warning"
                          : "logo"
                    }
                  />
                </Way>
              }
              eyebrow="SSH"
              title="sshd"
              hint={
                <Words>
                  {ssh ? (
                    <>
                      <span
                        className={cn(
                          "block",
                          ssh.tone === "danger" && "text-destructive",
                          ssh.tone === "warning" && "text-warning",
                        )}
                      >
                        {ssh.value}
                      </span>
                      {ssh.hint && <span className="numeric block">{ssh.hint}</span>}
                    </>
                  ) : (
                    "not read yet"
                  )}
                </Words>
              }
            />
          </li>
          <li className="min-w-0" data-node="logins">
            <WireNode
              nodeRef={shells}
              align="center"
              mark={
                <Way href="/security/logins" label="Logins">
                  {signedIn.length > 0 ? (
                    <span className="flex shrink-0 rounded-xl bg-background">
                      <InitialsMark name={signedIn[0]} className="size-14 rounded-xl" />
                    </span>
                  ) : (
                    <Mark fallback={SignIn} tone="logo" />
                  )}
                </Way>
              }
              eyebrow="Signed in"
              title={
                !held ? (
                  <Unread failed={sessions.failed} />
                ) : held.length === 0 ? (
                  "Nobody"
                ) : (
                  <span className="block truncate">{signedIn.join(", ")}</span>
                )
              }
              hint={
                <Words>
                  {!held ? (
                    sessions.failed ? (
                      "the sessions could not be read"
                    ) : (
                      "not read yet"
                    )
                  ) : held.length === 0 ? (
                    "nobody holds a shell"
                  ) : (
                    <span className="numeric flex items-center gap-1.5 lg:justify-center">
                      <span className="truncate">
                        {plural(held.length, "session")}
                        {remote > 0 && ` · ${remote} over ssh`}
                      </span>
                      <ProductGlyphs ids={heldFrom} max={2} />
                    </span>
                  )}
                </Words>
              }
            />
          </li>

          {/* Stacked on a phone, the second lane starts a step further down so
              the two read as two ways in rather than one path of eight. */}
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
                exposure ? (
                  <span className="block truncate font-mono">
                    {exposure.client || "Address unavailable"}
                  </span>
                ) : (
                  <Unread failed={false} />
                )
              }
              hint={exposure ? network.label : "not read yet"}
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
          <li className="min-w-0 lg:col-start-5">
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

/** A node's mark as the way to its page. */
function Way({
  href,
  label,
  children,
}: {
  href: string
  label: string
  children: React.ReactNode
}) {
  return (
    <Link href={href} aria-label={label} className="flex rounded-xl focus-ring">
      {children}
    </Link>
  )
}

/**
 * A node's words under five marks in a row: held narrower than the caption's
 * own box between `lg` and `xl`, where the columns are, so two neighbours'
 * lines never run into each other.
 */
function Words({ children }: { children: React.ReactNode }) {
  return <span className="block lg:mx-auto lg:max-w-[8.5rem] xl:max-w-none">{children}</span>
}

/** A layer whose read has not landed, or did not: named as that, never as a missing layer. */
function Unread({ failed }: { failed: boolean }) {
  return failed ? (
    <span className="text-warning">Unreadable</span>
  ) : (
    <span className="text-muted-foreground">Reading…</span>
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
