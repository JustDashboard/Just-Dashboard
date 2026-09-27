"use client"

import { Slash } from "@/components/icons"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { ProductGlyph } from "@/components/product-logo"
import { Address, jailProduct } from "@/components/security/marks"
import { get } from "@/lib/api"
import { lensFor } from "@/lib/log-lenses"
import type { Fail2banJail } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { PageContext } from "@/components/page"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { ReadingTile, useLensReadings } from "@/components/logs/lens-readings"
import { FAIL2BAN_LOG } from "@/components/security/host-logs"
import { HostLogSection, useAddressLineVerbs, useHostLog } from "@/components/security/log-section"
import { AreaFindings } from "@/components/security/posture-panel"
import { JailsPanel } from "@/components/security/jail-panel"
import { OffendersPanel } from "@/components/security/offenders-panel"
import { useSecurity } from "@/components/security/security-context"

/**
 * fail2ban: the tool itself in one identity line, drawn as its own mark with
 * whether it is running at the right end; the jails, the addresses they are
 * holding, and — under that — what the tool has actually been doing, because
 * a ban expires and the jail is empty again by morning however busy the night
 * was.
 *
 * What it has been doing is its own log, read through the fail2ban lens: every
 * strike a jail counted as well as every ban, so an address three strikes
 * from a ban is on the page before it is banned, and Insights ranks the
 * strikes and bans by jail and the addresses banned more than once. The day's
 * counts are the second row of the readings. It used to be a table of bans
 * and unbans parsed from the file alone, which on a host where fail2ban
 * writes to the journal said there was nothing to read; the journal is read
 * now instead.
 */
export function IntrusionPanels() {
  const { can } = useAuth()
  const { posture, exposure, applyFix } = useSecurity()
  const { data, error, loading, refresh } = usePoll(
    (signal) =>
      get<{ available: boolean; running: boolean; jails: Fail2banJail[]; error?: string }>(
        "/fail2ban/",
        undefined,
        signal,
      ),
    20000,
  )

  const jails = data?.jails ?? []
  const bannedNow = jails.reduce((n, j) => n + j.currentlyBanned, 0)
  const failingNow = jails.reduce((n, j) => n + j.currentlyFailed, 0)
  const bansTotal = jails.reduce((n, j) => n + j.totalBanned, 0)
  const watched = new Set(jails.flatMap((j) => j.fileList)).size
  const activity = useHostLog(FAIL2BAN_LOG, Boolean(data?.available))
  const readings = useLensReadings(activity.data?.id ?? "", FAIL2BAN_LENS, {
    forcedLens: "fail2ban",
    enabled: Boolean(activity.data) && Boolean(data?.running),
  })
  const lineVerbs = useAddressLineVerbs({
    comment: "blocked from fail2ban's log",
    onBlocked: refresh,
    blockOn: OFFENCES,
  })
  const activitySection = (
    <HostLogSection
      title="Activity"
      log={activity}
      storageKey="security.intrusion.log"
      lineVerbs={lineVerbs}
    />
  )

  const header = <PageContext eyebrow="Security" title="Intrusion prevention" />

  if (loading && !data) {
    return (
      <>
        {header}
        <LoadingPanel />
      </>
    )
  }
  if (error && !data) {
    return (
      <>
        {header}
        <ErrorState error={error} />
      </>
    )
  }
  if (!data?.available) {
    return (
      <>
        {header}
        <AreaFindings posture={posture} area="intrusion" onFix={applyFix} />
        <EmptyState
          icon={Slash}
          title="fail2ban is not installed"
          description="It turns an endless brute-force against a port that has to stay open into a few attempts and a ban, which is the one thing a firewall cannot do for SSH."
        />
      </>
    )
  }
  if (!data.running) {
    return (
      <>
        {header}
        <AreaFindings posture={posture} area="intrusion" onFix={applyFix} />
        <EmptyState
          icon={Slash}
          title="fail2ban is installed but not responding"
          description={
            data.error ?? "Installed and stopped is the state that looks protected and is not."
          }
        />
        {/* Its last lines are usually why. */}
        {activitySection}
      </>
    )
  }

  return (
    <>
      {header}

      <HostIdentity
        mark="fail2ban"
        title="fail2ban"
        facts={
          <>
            <span className="numeric">
              {jails.length === 1 ? "1 jail" : `${jails.length} jails`}
            </span>
            <FactDot />
            <span className="numeric">
              {watched === 1 ? "1 log watched" : `${watched} logs watched`}
            </span>
            {exposure?.client && (
              <>
                <FactDot />
                <span className="inline-flex items-center gap-1">
                  <span>you</span>
                  <Address ip={exposure.client} className="text-foreground" />
                </span>
              </>
            )}
          </>
        }
        aside={<Status tone="running" live label="running" className="text-body" />}
      />

      {/* The four numbers the rest of the page is an explanation of. They were
          a "·"-joined sentence in each jail's header, which meant comparing two
          jails was reading two sentences. Under them, from the log, the last
          day's bans, strikes, unbans and errors — the jails' counters start
          again with every restart, the log does not. Two-up on a phone: eight
          short figures one-up are a screen and a half before the jails. */}
      <StatGrid columns={4} dense>
        <StatTile label="Jails" value={jails.length} hint="configured and running" />
        <StatTile
          label="Banned now"
          value={bannedNow}
          tone={bannedNow > 0 ? "warning" : "default"}
          hint="held this instant — bans expire"
        />
        <StatTile
          label="Failing now"
          value={failingNow}
          hint="attempts inside the current window"
        />
        <StatTile label="Bans in total" value={bansTotal} hint="since fail2ban last started" />
        {readings.tiles.map((tile) => (
          <ReadingTile key={tile.reading.id} tile={tile} window={readings.window} />
        ))}
      </StatGrid>

      <AreaFindings posture={posture} area="intrusion" onFix={applyFix} />

      <JailsPanel
        jails={jails}
        canManage={can("system.admin")}
        clientIp={exposure?.client}
        onChanged={refresh}
      />

      <OffendersPanel onBlocked={refresh} journal={activity.data?.kind === "journal"} />
      {activitySection}
    </>
  )
}

const FAIL2BAN_LENS = lensFor("fail2ban")

/**
 * The lines whose address a deny answers: every strike and ban, and the ones
 * fail2ban let go. An address it was told to ignore is one the operator
 * trusts, and is never offered.
 */
const OFFENCES = ["found", "ban", "increase", "restore_ban", "already_banned", "unban"]

/** A jail's name with the mark of the service it watches, where that is one. */
export function JailName({ name }: { name: string }) {
  const product = jailProduct(name)
  return (
    <span className="inline-flex items-center gap-1.5">
      {product && <ProductGlyph id={product} />}
      <span>{name}</span>
    </span>
  )
}
