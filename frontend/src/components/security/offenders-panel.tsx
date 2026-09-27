"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Crosshair } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, ApiError } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { BanSummary } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Meter } from "@/components/meter"
import { Sparkline } from "@/components/metrics/sparkline"
import { VerbActions } from "@/components/verbs"
import { addressVerbs, blockAddress } from "@/components/security/address-verbs"
import { JailName } from "@/components/security/intrusion-panels"
import { PeerIdentity } from "@/components/security/marks"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * Who keeps coming back.
 *
 * A jail lists the bans in force this instant, and a ban expires — so the
 * address banned eleven times this week is invisible the moment its eleventh
 * ban lapses, and the page reads as a quiet night. Folding the log the other
 * way round turns it into the one question worth asking of it: this is not a
 * passing scanner, it is somebody working through your host, and a permanent
 * firewall rule is a better answer than another ten-minute ban.
 *
 * The fold reads fail2ban.log. Where the page's Activity reads fail2ban's
 * journal instead (`journal`) — no file, or none the dashboard may read —
 * an empty fold is not "nothing was banned", and Activity's Insights, which
 * rank the addresses banned more than once, is where the same answer is.
 */
export function OffendersPanel({
  onBlocked,
  journal,
}: {
  onBlocked?: () => void
  journal?: boolean
}) {
  const { can } = useAuth()
  const router = useRouter()
  const [blocking, setBlocking] = useState<string | null>(null)
  const { data, error, loading, refresh } = usePoll<BanSummary>(
    (signal) => get("/fail2ban/offenders", { top: 15 }, signal),
    120000,
  )
  const unavailable = error instanceof ApiError && error.code === "fail2ban_unavailable"

  const block = async (ip: string) => {
    setBlocking(ip)
    try {
      await blockAddress(ip, "repeat offender")
      notify.success(`${ip} blocked at the firewall`, {
        description: "A firewall rule outlives a ban, which expires.",
      })
      onBlocked?.()
      refresh()
    } catch (err) {
      notify.error("Could not add the rule", err)
    } finally {
      setBlocking(null)
    }
  }

  // Framed only around its table (§2): on a journal host the fold is a
  // sentence pointing at Activity, drawn on the page like any other.
  const pointer = journal && !data?.offenders.length
  const mostBans = Math.max(1, ...(data?.offenders ?? []).map((offender) => offender.bans))

  return (
    <Panel plain={pointer}>
      <PanelHeader title="Repeat offenders" />
      {data && data.offenders.length > 0 && (
        <PanelToolbar className="gap-x-6">
          <span className="numeric text-hint text-muted-foreground">
            {data.bans} bans · {data.unbans} releases · since{" "}
            {data.since ? relativeTime(data.since) : "—"}
          </span>
          {data.perDay.length > 1 && (
            <>
              <span className="flex-1" />
              <span className="flex items-center gap-2">
                <span className="eyebrow">bans per day</span>
                <Sparkline
                  values={data.perDay.map((d) => d.count)}
                  color="var(--destructive)"
                  label={`${data.bans} bans over ${data.perDay.length} days`}
                />
              </span>
            </>
          )}
        </PanelToolbar>
      )}
      <PanelBody flush>
        {pointer && (unavailable || data) ? (
          <Notice tone="default" title="No bans in fail2ban.log" className="mt-3">
            Activity below reads fail2ban&rsquo;s journal instead, and its Insights rank the
            addresses banned more than once.
          </Notice>
        ) : unavailable ? (
          <Notice tone="default" title="No fail2ban log on this host" className="mt-3">
            fail2ban is not installed, or it logs only to the journal. There is no file to fold.
          </Notice>
        ) : error ? (
          <ErrorState error={error} className="mt-3" />
        ) : loading ? (
          <LoadingPanel rows={4} className="mt-3" />
        ) : !data?.offenders.length ? (
          <EmptyState icon={Crosshair} title="Nothing has been banned yet" className="mt-3" />
        ) : (
          <div className="min-w-0 group-data-[plain]/panel:-mx-4">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Address</TableHead>
                  <TableHead className="w-full">Bans</TableHead>
                  <TableHead className="w-px">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.offenders.map((offender) => (
                  <TableRow key={offender.ip} className="group">
                    <TableCell className="py-4">
                      <PeerIdentity ip={offender.ip} />
                      <span className="mt-2 flex flex-wrap gap-x-2 text-hint text-muted-foreground">
                        {offender.jails.map((jail) => (
                          <JailName key={jail} name={jail} />
                        ))}
                      </span>
                    </TableCell>
                    <TableCell>
                      <div className="min-w-24 space-y-2">
                        <div className="flex items-center justify-between gap-4">
                          <span
                            className={cn(
                              "numeric text-body font-medium",
                              offender.bans >= 5 && "text-destructive",
                            )}
                          >
                            {offender.bans}
                          </span>
                          <span className="text-hint text-muted-foreground">
                            {relativeTime(offender.last)}
                          </span>
                        </div>
                        <Meter
                          value={(offender.bans / mostBans) * 100}
                          tone={offender.bans >= 5 ? "danger" : "default"}
                          size="thin"
                          label={`${offender.bans} bans`}
                        />
                        <span className="block text-hint text-muted-foreground">
                          Since {relativeTime(offender.first)}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell>
                      <VerbActions
                        dim
                        className="justify-end"
                        verbs={addressVerbs({
                          ip: offender.ip,
                          block: can("system.admin") ? () => void block(offender.ip) : undefined,
                          blocking: blocking === offender.ip,
                          navigate: (href) => router.push(href),
                        })}
                      />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}
