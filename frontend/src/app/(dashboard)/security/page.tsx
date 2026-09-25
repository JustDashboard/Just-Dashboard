"use client"

import { useMemo } from "react"
import { Bug, FirewallCheck, NetworkDevice, SecureConnection, SignIn } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Connections, Fail2banJail, LoginSession, SecurityFinding } from "@/lib/types"
import type { Tone } from "@/components/tone"
import { usePoll } from "@/hooks/use-poll"
import { Metric, MetricStrip, Page, PageContext } from "@/components/page"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { Notice } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { ExposureFacts } from "@/components/security/exposure-panel"
import { PosturePanel, worstLevel } from "@/components/security/posture-panel"
import { useSecurity } from "@/components/security/security-context"

type Fail2banReply = { available: boolean; running: boolean; jails: Fail2banJail[] }

/**
 * Is this machine in reasonable shape, and where is it not?
 *
 * The page answers in the order the host Overview does: what the machine is
 * (how this browser reaches it, and from where), five readings — one per area
 * that has a figure — each a way into its page, and the findings, worst first,
 * with the remedy on the ones the dashboard can carry out itself. The section
 * strip above already names every page, so there is no second list of them
 * here: the tiles carry the verdicts that used to sit beside seven links.
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

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Protection" title="Security" />

      {/* What this machine is, from a security point of view: how the panel
          is reachable, and from where this reader is reaching it. This was a
          framed panel with a sentence, a labelled list and a recommendation;
          the grade is a reading and its inputs are facts, so they sit in the
          row the host Overview keeps its platform and kernel in. */}
      <ExposureFacts exposure={exposure} />

      {posture && (
        <MetricStrip>
          <Metric label="Checks" value={posture.checks} />
          <Metric
            label="Not checked"
            value={posture.skipped.length}
            hint={posture.skipped.length > 0 ? posture.skipped.join(", ") : undefined}
          />
          <Metric label="Checked" value={relativeTime(posture.checkedAt)} />
        </MetricStrip>
      )}

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
 * sidebar entry carries, so the eye finds "Firewall" without reading. The
 * figure rises once when its poll lands, so a row of five fills in rather than
 * flickering from bone to number.
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
        hint={hint}
      />
    </StatLink>
  )
}
