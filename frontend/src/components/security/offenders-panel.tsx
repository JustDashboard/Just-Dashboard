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
import { Metric, MetricStrip } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Sparkline } from "@/components/metrics/sparkline"
import { Tag } from "@/components/tag"
import { VerbActions } from "@/components/verbs"
import { addressVerbs, blockAddress } from "@/components/security/address-verbs"
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
 */
export function OffendersPanel({ onBlocked }: { onBlocked?: () => void }) {
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

  return (
    <Panel plain>
      <PanelHeader title="Repeat offenders" />
      {data && data.offenders.length > 0 && (
        <PanelToolbar className="gap-x-6">
          <MetricStrip>
            <Metric label="bans" value={data.bans} />
            <Metric label="releases" value={data.unbans} />
            <Metric label="addresses" value={data.offenders.length} />
            <Metric
              label="since"
              value={data.since ? relativeTime(data.since) : "—"}
              hint="as far back as the log goes"
            />
          </MetricStrip>
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
        {unavailable ? (
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
          <div className="-mx-4 min-w-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Address</TableHead>
                  <TableHead>Bans</TableHead>
                  <TableHead className="hidden md:table-cell">First seen</TableHead>
                  <TableHead className="hidden sm:table-cell">Last seen</TableHead>
                  <TableHead className="w-full">Jails</TableHead>
                  <TableHead className="w-px" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.offenders.map((offender) => (
                  <TableRow key={offender.ip} className="group">
                    <TableCell className="font-mono">{offender.ip}</TableCell>
                    <TableCell>
                      <span
                        className={cn(
                          "numeric text-xs font-medium",
                          offender.bans >= 5 ? "text-destructive" : "text-muted-foreground",
                        )}
                      >
                        {offender.bans}
                      </span>
                    </TableCell>
                    <TableCell className="hidden whitespace-nowrap text-muted-foreground md:table-cell">
                      {relativeTime(offender.first)}
                    </TableCell>
                    <TableCell className="hidden whitespace-nowrap text-muted-foreground sm:table-cell">
                      {relativeTime(offender.last)}
                    </TableCell>
                    <TableCell>
                      <span className="flex flex-wrap gap-1">
                        {offender.jails.map((jail) => (
                          <Tag key={jail}>{jail}</Tag>
                        ))}
                      </span>
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
