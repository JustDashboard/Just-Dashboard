"use client"

import { useState } from "react"
import Link from "next/link"
import {
  Crosshair,
  LockClosed,
  LockOpen,
  Play,
  RefreshClockwise,
  Slash,
  Warning,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { lensFor } from "@/lib/log-lenses"
import { notify } from "@/lib/toast"
import type { SystemdUnitDetail } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useLensReadings } from "@/components/logs/lens-readings"
import { FactDot } from "@/components/metrics/host-identity"
import { InstallHandoff } from "@/components/network/install"
import { ProductLogos } from "@/components/product-logo"
import { ReadingLines } from "@/components/reading-line"
import { EngineHead, EngineWaiting } from "@/components/security/engine-head"
import { FAIL2BAN_LOG } from "@/components/security/host-logs"
import { fail2banFault, type Fail2banStatus } from "@/components/security/intrusion-model"
import { JailsPanel } from "@/components/security/jail-panel"
import { LensReadingLine, type ReadingLook } from "@/components/security/lens-reading-line"
import {
  HostLogSection,
  useAddressLineVerbs,
  useHostLog,
  useReadingPress,
} from "@/components/security/log-section"
import { OffendersPanel } from "@/components/security/offenders-panel"
import { useSecurity } from "@/components/security/security-context"
import { EmptyState, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"

const FAIL2BAN_UNIT = "fail2ban.service"
const FAIL2BAN_LENS = lensFor("fail2ban")

/**
 * The lines whose address a deny answers: every strike and ban, and the ones
 * fail2ban let go. An address it was told to ignore is one the operator
 * trusts, and is never offered.
 */
const OFFENCES = ["found", "ban", "increase", "restore_ban", "already_banned", "unban"]

/** Each of the log's readings as its own glyph on its own hue. */
const LOOKS: Record<string, ReadingLook> = {
  bans: { icon: LockClosed, hue: "var(--tag-amber)" },
  strikes: { icon: Crosshair, hue: "var(--tag-blue)" },
  unbans: { icon: LockOpen, hue: "var(--tag-green)" },
  errors: { icon: Warning, hue: "var(--tag-red)" },
}

/**
 * fail2ban: the tool itself in one line, drawn as its own mark with whether
 * it is running beside its name; what it has been doing; the jails; the
 * addresses that keep coming back; and its log.
 *
 * What it has been doing is its own log, read through the fail2ban lens:
 * every strike a jail counted as well as every ban, so an address three
 * strikes from a ban is on the page before it is banned. The last day's
 * bans, strikes, unbans and errors are four lines, each with the day's shape
 * and each a press away from the lines it counts: a ban expires and the jail
 * is empty again by morning however busy the night was, and the jails'
 * own counters start again with every restart. Four tiles above them used to
 * add the jails up (how many, banned now, failing now, bans in total), which
 * on a host with one jail said what its card said; those figures are on the
 * jails' rows and the page's picture.
 *
 * A fail2ban that is installed and silent is a state of its own: its unit is
 * asked after, a stopped one is offered Start, and what its client said is
 * evidence under a fold rather than the headline.
 */
export function Fail2banPanel({
  view,
  onChanged,
}: {
  view: PollState<Fail2banStatus>
  /** Something that holds an address changed: the merged list is stale too. */
  onChanged: () => void
}) {
  const { can } = useAuth()
  const { exposure } = useSecurity()
  const { data, error, loading } = view

  const activity = useHostLog(FAIL2BAN_LOG, Boolean(data?.available))
  const readings = useLensReadings(activity.data?.id ?? "", FAIL2BAN_LENS, {
    forcedLens: "fail2ban",
    enabled: Boolean(activity.data) && Boolean(data?.running),
  })
  const lineVerbs = useAddressLineVerbs({
    comment: "blocked from fail2ban's log",
    onBlocked: onChanged,
    blockOn: OFFENCES,
  })
  const [ask, press] = useReadingPress()
  const activitySection = (
    <HostLogSection
      title="Activity"
      log={activity}
      storageKey="security.intrusion.log"
      lineVerbs={lineVerbs}
      ask={ask}
      readings={readings}
    />
  )

  if (loading && !data) return <EngineWaiting id="fail2ban" name="fail2ban" lines={4} />
  if (error && !data) return <ErrorState error={error} onRetry={view.refresh} />
  if (!data) return null

  if (!data.available) {
    return (
      <InstallHandoff
        pkg="fail2ban"
        products={["fail2ban"]}
        icon={Slash}
        title="fail2ban is not installed"
        description="It turns an endless brute-force against a port that has to stay open into a few attempts and a ban, which a firewall cannot do for SSH."
        onInstalled={onChanged}
        phases={false}
      />
    )
  }

  if (!data.running) {
    return (
      <>
        <EngineHead
          id="fail2ban"
          name="fail2ban"
          state={<Status tone="warning" label="not running" />}
          facts={<span>installed · no jail is watching</span>}
        />
        <Fail2banFault said={data.error} onChanged={onChanged} />
        {/* Its last lines are usually why. */}
        {activitySection}
      </>
    )
  }

  const jails = data.jails
  const watched = new Set(jails.flatMap((j) => j.fileList)).size
  const bansTotal = jails.reduce((n, j) => n + j.totalBanned, 0)

  return (
    <>
      <EngineHead
        id="fail2ban"
        name="fail2ban"
        state={<Status tone="running" label="running" />}
        facts={
          <>
            <span className="numeric">
              {jails.length === 1 ? "1 jail" : `${jails.length} jails`}
            </span>
            <FactDot />
            <span className="numeric">
              {watched === 1 ? "1 log watched" : `${watched} logs watched`}
            </span>
            <FactDot />
            <span className="numeric">
              {bansTotal.toLocaleString()} {bansTotal === 1 ? "ban" : "bans"} since it last started
            </span>
          </>
        }
      />

      <ReadingLines>
        {readings.tiles.map((tile) => (
          <LensReadingLine
            key={tile.reading.id}
            tile={tile}
            window={readings.window}
            look={LOOKS[tile.reading.id] ?? LOOKS.strikes}
            onPick={() => press(tile.reading)}
          />
        ))}
      </ReadingLines>

      <JailsPanel
        jails={jails}
        canManage={can("system.admin")}
        clientIp={exposure?.client}
        onChanged={onChanged}
      />

      <OffendersPanel onBlocked={onChanged} journal={activity.data?.kind === "journal"} />
      {activitySection}
    </>
  )
}

/**
 * fail2ban is installed and its status could not be read. systemd says which
 * of the two that is: stopped, which Start fixes, or running and silent,
 * which the log under this explains.
 */
function Fail2banFault({ said, onChanged }: { said?: string; onChanged: () => void }) {
  const { can } = useAuth()
  const unit = usePoll(
    (signal) =>
      get<SystemdUnitDetail>(`/systemd/${encodeURIComponent(FAIL2BAN_UNIT)}`, undefined, signal),
    15_000,
  )
  const [starting, setStarting] = useState(false)
  // A server with no systemd, or an account that may not read units, leaves
  // the unit unread: the state then claims neither.
  const fault = fail2banFault(unit.error ? undefined : unit.data?.unit)
  const asking = unit.loading && !unit.data && !unit.error

  const start = async () => {
    setStarting(true)
    try {
      await post(`/systemd/${encodeURIComponent(FAIL2BAN_UNIT)}/start`)
      notify.success("fail2ban started")
      unit.refresh()
      onChanged()
    } catch (err) {
      notify.error("Could not start fail2ban", err)
    } finally {
      setStarting(false)
    }
  }

  return (
    <EmptyState
      icon={Slash}
      mark={<ProductLogos ids={["fail2ban"]} />}
      title={asking ? "fail2ban is not answering" : fault.title}
      description={asking ? "Asking systemd whether it is stopped…" : fault.description}
      action={
        <div className="flex max-w-full min-w-0 flex-col items-center gap-3">
          <div className="flex flex-wrap items-center justify-center gap-2">
            {fault.startable && !asking && can("service.control") && (
              <Button size="sm" onClick={() => void start()} pending={starting} disabled={starting}>
                <Play className="size-4" />
                Start fail2ban
              </Button>
            )}
            <Button size="sm" variant="outline" onClick={onChanged}>
              <RefreshClockwise className="size-4" />
              Check again
            </Button>
            <Button size="sm" variant="ghost" asChild>
              <Link href={`/processes/services/${encodeURIComponent(FAIL2BAN_UNIT)}`}>
                Service details
              </Link>
            </Button>
          </div>
          {said && (
            <details className="w-full max-w-md min-w-0 text-left text-xs">
              <summary className="cursor-pointer text-center text-muted-foreground hover:text-foreground">
                What fail2ban&rsquo;s client said
              </summary>
              <pre className="mt-1 overflow-x-auto rounded-sm border border-hairline bg-surface-header/40 p-2 font-mono text-micro break-words whitespace-pre-wrap">
                {said}
              </pre>
            </details>
          )}
        </div>
      }
    />
  )
}
