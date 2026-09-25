"use client"

import { useMemo } from "react"
import { Bug, FirewallCheck, NetworkDevice, SecureConnection, SignIn } from "@/components/icons"
import { get } from "@/lib/api"
import { networkOf } from "@/lib/clients"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Connections, Fail2banJail, LoginSession, SecurityFinding } from "@/lib/types"
import type { Tone } from "@/components/tone"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext } from "@/components/page"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { Notice } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { ProductGlyphs, processProduct } from "@/components/product-logo"
import { ExposureIdentity } from "@/components/security/exposure-panel"
import { PostureBadge, PosturePanel, worstLevel } from "@/components/security/posture-panel"
import { useSecurity } from "@/components/security/security-context"

type Fail2banReply = { available: boolean; running: boolean; jails: Fail2banJail[] }

/**
 * Is this machine in reasonable shape, and where is it not?
 *
 * The page answers in the order the host Overview does: what the machine is
 * (how this browser reaches it, drawn as the way in, with the posture's
 * verdict at the line's right end), five readings — one per area that has a
 * figure — each a way into its page and each naming what it counts with the
 * products themselves, and the findings, worst first, with the remedy on the
 * ones the dashboard can carry out itself. The rail already names every page,
 * so there is no second list of them here: the tiles carry the verdicts.
 *
 * The three figures that stood in a strip under the exposure line — checks,
 * not checked, checked at — are the verdict's own facts and sit beside it now;
 * the split by severity is the Findings header's.
 */
export default function SecurityOverviewPage() {
  const { posture, postureLoading, firewall, exposure, applyFix } = useSecurity()

  const fail2ban = usePoll<Fail2banReply>((signal) => get("/fail2ban/", undefined, signal), 20_000)
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
  const fromInternet = connections.data?.peers.filter((p) => !p.private).length ?? 0
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
            <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <PostureBadge status={posture.status} className="text-body" />
              <span className="numeric text-hint text-muted-foreground">
                {plural(posture.checks, "check")}
                {posture.skipped.length > 0 && (
                  <span title={posture.skipped.join(", ")}>
                    {" "}
                    · {posture.skipped.length} not run
                  </span>
                )}{" "}
                · {relativeTime(posture.checkedAt)}
              </span>
            </span>
          ) : postureLoading ? (
            <Skeleton className="h-4 w-40" />
          ) : undefined
        }
      />

      {/* The recommendation only where no finding already carries it: a
          public or open allowlist is a finding below, and saying it twice on
          one screen is what taught people to skip both. */}
      {exposure?.recommendation && grade === "ok" && (
        <Notice title="A quieter arrangement is available">{exposure.recommendation}</Notice>
      )}

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
            !posture
              ? undefined
              : posture.skipped.includes("ssh")
                ? "Not checked"
                : ssh.length === 0
                  ? "Hardened"
                  : `${ssh.length} finding${ssh.length === 1 ? "" : "s"}`
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
            !fail2ban.data
              ? undefined
              : !fail2ban.data.available
                ? "No fail2ban"
                : !fail2ban.data.running
                  ? "Not running"
                  : `${bannedNow} banned now`
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
          value={connections.data ? fromInternet : connections.error ? "Unreadable" : undefined}
          trailing={connections.data ? "from the internet" : undefined}
          tone={fromInternet > 0 ? "warning" : "default"}
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
            sessions.data
              ? sessions.data.length === 0
                ? "Nobody in"
                : `${sessions.data.length} session${sessions.data.length === 1 ? "" : "s"}`
              : sessions.error
                ? "Unreadable"
                : undefined
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

      <PosturePanel posture={posture} loading={postureLoading} onFix={applyFix} />
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
 * One area, its headline figure, and the way to its page — a `StatTile`
 * behind a `StatLink`, exactly as the host Overview draws its Services row.
 * The glyph before the name is wayfinding, not decoration: it is the mark the
 * sidebar entry carries, so the eye finds "Firewall" without reading. What
 * the figure counts is said with the products themselves after the hint —
 * the programs the peers reach, the network the shells are held from, the
 * tool doing the banning. The figure rises once when its poll lands, so a
 * row of five fills in rather than flickering from bone to number.
 */
function AreaTile({
  icon: Icon,
  title,
  href,
  value,
  hint,
  trailing,
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
