"use client"

import { useMemo, useState } from "react"
import { Bug, FirewallCheck, NetworkDevice, SecureConnection, SignIn } from "@/components/icons"
import { get } from "@/lib/api"
import { networkOf } from "@/lib/clients"
import { cn } from "@/lib/utils"
import type {
  BanSummary,
  Connections,
  Fail2banJail,
  LoginSession,
  SecurityFinding,
} from "@/lib/types"
import type { Tone } from "@/components/tone"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { Notice } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { NumberTicker } from "@/components/ui/number-ticker"
import { TileTrend } from "@/components/metrics/sparkline"
import { ProductGlyphs, processProduct } from "@/components/product-logo"
import { ExposureIdentity } from "@/components/security/exposure-panel"
import { Perimeter } from "@/components/security/perimeter"
import { PostureBadge, PosturePanel, worstLevel } from "@/components/security/posture-panel"
import { PostureStrip } from "@/components/security/posture-strip"
import { useSecurity } from "@/components/security/security-context"

type Fail2banReply = { available: boolean; running: boolean; jails: Fail2banJail[] }

/**
 * Is this machine in reasonable shape, and where is it not?
 *
 * The page answers in the order the host Overview does: what the machine is
 * (how this browser reaches it, with the posture's verdict at the line's
 * right end); the seven checks the verdict is made of, one coloured segment
 * each, which narrow the findings at the foot of the page; five readings —
 * one per area that has a figure — each a way into its page, counting up as
 * they land; the ways onto the machine drawn as wires, the internet's
 * through the firewall, fail2ban and sshd and this browser's through the
 * allowlist; and the findings, worst first, with the remedy on the ones the
 * dashboard can carry out itself. The rail already names every page, so
 * there is no second list of them here: the tiles carry the verdicts.
 *
 * The checks' count and when they ran were facts beside the verdict and are
 * the strip's head now; the split by severity is the Findings header's, and
 * which checks did not run is the strip's dashed segments.
 */
export default function SecurityOverviewPage() {
  const { posture, postureLoading, firewall, exposure, applyFix } = useSecurity()
  const [area, setArea] = useState<SecurityFinding["area"]>()

  const fail2ban = usePoll<Fail2banReply>((signal) => get("/fail2ban/", undefined, signal), 20_000)
  const bans = usePoll<BanSummary>(
    (signal) => get("/fail2ban/offenders", { top: 1 }, signal),
    120_000,
  )
  const connections = usePoll<Connections>(
    (signal) => get("/connections", undefined, signal),
    10_000,
  )
  const sessions = usePoll<LoginSession[]>(
    (signal) => get("/ssh-sessions", undefined, signal),
    10_000,
  )

  const ssh = useMemo(() => areaFindings(posture?.findings, "ssh"), [posture])
  const grade = exposure ? worstLevel(areaFindings(posture?.findings, "exposure")) : "ok"

  const bannedNow = fail2ban.data?.jails.reduce((n, j) => n + j.currentlyBanned, 0) ?? 0
  const fromInternet = connections.data?.peers.filter((p) => !p.private).length
  const remote = sessions.data?.filter((s) => s.isSsh).length ?? 0
  // What the peers are talking to, as the programs holding the sockets; and
  // where the shells are held from, as the networks that have a mark.
  const reached = useMemo(
    () => [
      ...new Set(
        (connections.data?.peers ?? [])
          .flatMap((p) => p.processes)
          .map(processProduct)
          .filter((id): id is string => Boolean(id)),
      ),
    ],
    [connections.data],
  )
  const loginNetworks = useMemo(
    () => [
      ...new Set(
        (sessions.data ?? [])
          .map((s) => networkOf(s.from || "127.0.0.1").product)
          .filter((id): id is string => Boolean(id)),
      ),
    ],
    [sessions.data],
  )

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Protection" title="Security" />

      <ExposureIdentity
        exposure={exposure}
        aside={
          posture ? (
            <PostureBadge status={posture.status} className="text-body" />
          ) : postureLoading ? (
            <Skeleton className="h-4 w-28" />
          ) : undefined
        }
      />

      {/* The recommendation only where no finding already carries it: a
          public or open allowlist is a finding below, and saying it twice on
          one screen is what taught people to skip both. */}
      {exposure?.recommendation && grade === "ok" && (
        <Notice title="A quieter arrangement is available">{exposure.recommendation}</Notice>
      )}

      <PostureStrip posture={posture} loading={postureLoading} area={area} onArea={setArea} />

      <StatGrid columns={5}>
        <AreaTile
          icon={FirewallCheck}
          title="Firewall"
          href="/security/firewall"
          loading={!firewall}
          value={
            !firewall
              ? undefined
              : !firewall.available
                ? "None"
                : firewall.enabled
                  ? firewall.backend
                  : "Not enabled"
          }
          tone={
            !firewall || !firewall.available ? "warning" : firewall.enabled ? "default" : "danger"
          }
          hint={
            !firewall || !firewall.available
              ? "install ufw or firewalld"
              : `${firewall.rules.filter((r) => !r.ipv6).length} rules · inbound ${firewall.policy.incoming ?? "—"}`
          }
        />
        <AreaTile
          icon={SecureConnection}
          title="SSH"
          href="/security/ssh"
          loading={postureLoading && !posture}
          value={
            !posture ? undefined : posture.skipped.includes("ssh") ? (
              "Not checked"
            ) : ssh.length === 0 ? (
              "Hardened"
            ) : (
              <Count value={ssh.length} unit={ssh.length === 1 ? " finding" : " findings"} />
            )
          }
          tone={toneFor(worstLevel(ssh))}
          hint={
            !posture || posture.skipped.includes("ssh")
              ? "no sshd on this host"
              : (ssh[0]?.title ?? "keys, root login and attempts in order")
          }
        />
        <AreaTile
          icon={Bug}
          title="Intrusion"
          href="/security/intrusion"
          loading={fail2ban.loading && !fail2ban.data}
          products={fail2ban.data?.running ? ["fail2ban"] : []}
          value={
            !fail2ban.data ? undefined : !fail2ban.data.available ? (
              "No fail2ban"
            ) : !fail2ban.data.running ? (
              "Not running"
            ) : (
              <Count value={bannedNow} unit=" banned now" />
            )
          }
          tone={
            !fail2ban.data
              ? "default"
              : !fail2ban.data.available || !fail2ban.data.running
                ? "warning"
                : bannedNow > 0
                  ? "warning"
                  : "default"
          }
          // What the jails did over the days fail2ban.log covers: a reading
          // that moves, where the figure is only this moment of it.
          trend={
            fail2ban.data?.running && bans.data ? (
              <TileTrend
                values={bans.data.perDay.map((d) => d.count)}
                color="var(--chart-3)"
                label={`Bans per day over the last ${bans.data.perDay.length} days`}
              />
            ) : undefined
          }
          hint={
            fail2ban.data?.running
              ? `${fail2ban.data.jails.length} jail${fail2ban.data.jails.length === 1 ? "" : "s"} watching`
              : "nothing is blocking repeated failures"
          }
        />
        <AreaTile
          icon={NetworkDevice}
          title="Connections"
          href="/security/connections"
          loading={connections.loading && !connections.data}
          products={reached}
          value={
            fromInternet !== undefined ? (
              <Count value={fromInternet} />
            ) : connections.error ? (
              "Unreadable"
            ) : undefined
          }
          trailing={connections.data ? "from the internet" : undefined}
          tone={(fromInternet ?? 0) > 0 ? "warning" : "default"}
          hint={
            connections.data
              ? `${connections.data.peers.length} remote address${connections.data.peers.length === 1 ? "" : "es"} · ${connections.data.total} sockets`
              : undefined
          }
        />
        <AreaTile
          icon={SignIn}
          title="Logins"
          href="/security/logins"
          loading={sessions.loading && !sessions.data}
          products={loginNetworks}
          value={
            sessions.data ? (
              sessions.data.length === 0 ? (
                "Nobody in"
              ) : (
                <Count
                  value={sessions.data.length}
                  unit={sessions.data.length === 1 ? " session" : " sessions"}
                />
              )
            ) : sessions.error ? (
              "Unreadable"
            ) : undefined
          }
          hint={
            sessions.data
              ? remote > 0
                ? `${remote} over ssh`
                : "nobody holds a shell right now"
              : undefined
          }
        />
      </StatGrid>

      <Panel plain>
        <PanelHeader title="Ways in" />
        <PanelBody>
          <Perimeter
            exposure={exposure}
            firewall={firewall}
            fail2ban={fail2ban.data}
            posture={posture}
            fromInternet={fromInternet}
          />
        </PanelBody>
      </Panel>

      <PosturePanel
        posture={posture}
        loading={postureLoading}
        onFix={applyFix}
        area={area}
        onClearArea={() => setArea(undefined)}
      />
    </Page>
  )
}

function areaFindings(findings: SecurityFinding[] | undefined, area: SecurityFinding["area"]) {
  return findings?.filter((f) => f.area === area) ?? []
}

function toneFor(level: ReturnType<typeof worstLevel>): Tone {
  if (level === "critical") return "danger"
  if (level === "warning") return "warning"
  return "default"
}

/**
 * A count that counts up when it lands and glides to each poll's value after,
 * as the host Overview's readings do; the unit is set after it as written.
 */
function Count({ value, unit }: { value: number; unit?: string }) {
  return (
    <>
      <NumberTicker value={value} />
      {unit}
    </>
  )
}

/**
 * One area, its headline figure, and the way to its page — a `StatTile`
 * behind a `StatLink`, exactly as the host Overview draws its Services row.
 * The glyph before the name is wayfinding, not decoration: it is the mark the
 * sidebar entry carries, so the eye finds "Firewall" without reading. What
 * the figure counts is said with the products themselves after the hint —
 * the programs the peers reach, the network the shells are held from, the
 * tool doing the banning. The figure rises once when its poll lands and a
 * count ticks up to its value, so a row of five fills in rather than
 * flickering from bone to number.
 */
function AreaTile({
  icon: Icon,
  title,
  href,
  value,
  hint,
  trailing,
  trend,
  tone = "default",
  loading,
  products = [],
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  href: string
  value?: React.ReactNode
  hint?: React.ReactNode
  /** The figure's unit, set beside it rather than folded into it. */
  trailing?: React.ReactNode
  /** The figure's recent shape, where the area keeps one. */
  trend?: React.ReactNode
  tone?: Tone
  loading?: boolean
  /** What the figure counts, as `product-logo` ids. */
  products?: string[]
}) {
  const settled = !loading
  return (
    <StatLink href={href} label={title}>
      <StatTile
        className="h-full transition-colors group-hover:bg-row-hover"
        label={
          <>
            <Icon aria-hidden className="mr-1.5 inline-block size-3 align-[-1.5px] text-brand" />
            {title}
          </>
        }
        value={
          <span
            key={settled ? "figure" : "skeleton"}
            className={cn(
              "inline-block max-w-full truncate align-bottom",
              settled && "animate-rise",
            )}
          >
            {loading ? <Skeleton className="my-1.5 h-5 w-24" /> : (value ?? "—")}
          </span>
        }
        tone={value == null ? "default" : tone}
        trailing={loading ? undefined : trailing}
        trend={settled ? trend : undefined}
        hint={
          <span className="inline-flex max-w-full min-w-0 items-center gap-2">
            {hint && <span className="truncate">{hint}</span>}
            {settled && <ProductGlyphs ids={products} />}
          </span>
        }
      />
    </StatLink>
  )
}
