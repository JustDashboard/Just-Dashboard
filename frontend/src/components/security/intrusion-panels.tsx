"use client"

import { useMemo, useState } from "react"
import { Bug, ClockRewind, Slash } from "@/components/icons"
import { get, ApiError } from "@/lib/api"
import { timestamp } from "@/lib/format"
import type { BanEvent, Fail2banJail } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { AreaFindings } from "@/components/security/posture-panel"
import { JailsPanel } from "@/components/security/jail-panel"
import { OffendersPanel } from "@/components/security/offenders-panel"
import { useSecurity } from "@/components/security/security-context"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * fail2ban: the jails, the addresses they are holding, and — under that — what
 * the tool has actually been doing, because a ban expires and the jail is
 * empty again by morning however busy the night was.
 */
export function IntrusionPanels() {
  const { can } = useAuth()
  const { posture, applyFix } = useSecurity()
  const { data, error, loading, refresh } = usePoll(
    (signal) =>
      get<{ available: boolean; running: boolean; jails: Fail2banJail[]; error?: string }>(
        "/fail2ban/",
        undefined,
        signal,
      ),
    20000,
  )

  if (loading) return <LoadingPanel />
  if (error) return <ErrorState error={error} />
  if (!data?.available) {
    return (
      <EmptyState
        icon={Slash}
        title="fail2ban is not installed"
        description="It turns an endless brute-force against a port that has to stay open into a few attempts and a ban, which is the one thing a firewall cannot do for SSH."
      />
    )
  }
  if (!data.running) {
    return (
      <EmptyState
        icon={Slash}
        title="fail2ban is installed but not responding"
        description={
          data.error ?? "Installed and stopped is the state that looks protected and is not."
        }
      />
    )
  }

  const jails = data.jails
  const bannedNow = jails.reduce((n, j) => n + j.currentlyBanned, 0)
  const failingNow = jails.reduce((n, j) => n + j.currentlyFailed, 0)
  const bansTotal = jails.reduce((n, j) => n + j.totalBanned, 0)

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <AreaFindings posture={posture} area="intrusion" onFix={applyFix} />

      {/* The four numbers the rest of the page is an explanation of. They were
          a "·"-joined sentence in each jail's header, which meant comparing two
          jails was reading two sentences. */}
      <StatGrid columns={4}>
        <StatTile label="Jails" value={jails.length} icon={Slash} hint="Configured and running" />
        <StatTile
          label="Banned now"
          value={bannedNow}
          icon={Bug}
          tone={bannedNow > 0 ? "warning" : "default"}
          hint="Held this instant — bans expire"
        />
        <StatTile
          label="Failing now"
          value={failingNow}
          icon={Bug}
          hint="Attempts inside the current window"
        />
        <StatTile
          label="Bans in total"
          value={bansTotal}
          icon={ClockRewind}
          hint="Since fail2ban last started"
        />
      </StatGrid>

      <JailsPanel jails={jails} canManage={can("system.admin")} onChanged={refresh} />

      <OffendersPanel onBlocked={refresh} />
      <BanHistoryPanel />
    </div>
  )
}

/**
 * What fail2ban has done recently, read from its own log — not remembered by
 * the dashboard and not inferred by polling the jail: a ban shorter than the
 * interval would never be seen that way, and the events either side of a
 * restart would be invented.
 */
function BanHistoryPanel() {
  const [query, setQuery] = useState("")
  const [kind, setKind] = useState<"all" | "ban" | "unban">("all")
  const { data, error, loading } = usePoll(
    (signal) => get<BanEvent[]>("/fail2ban/history", { limit: 100 }, signal),
    60000,
  )

  const unavailable = error instanceof ApiError && error.code === "fail2ban_unavailable"

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    return (data ?? []).filter(
      (e) =>
        (kind === "all" || e.action === kind) &&
        (!q || `${e.ip} ${e.jail}`.toLowerCase().includes(q)),
    )
  }, [data, kind, query])

  return (
    <Panel>
      <PanelHeader
        icon={ClockRewind}
        title="Ban activity"
        actions={
          data && data.length > 0 ? (
            <span className="numeric text-xs text-muted-foreground">
              {data.length} recorded events
            </span>
          ) : undefined
        }
      />
      {data && data.length > 0 && (
        <PanelToolbar>
          <ToggleGroup
            type="single"
            value={kind}
            onValueChange={(next) => next && setKind(next as "all" | "ban" | "unban")}
            variant="outline"
            size="sm"
            aria-label="Which events to show"
          >
            <ToggleGroupItem value="all" className="px-2.5 text-hint">
              Everything
            </ToggleGroupItem>
            <ToggleGroupItem value="ban" className="px-2.5 text-hint">
              Bans
            </ToggleGroupItem>
            <ToggleGroupItem value="unban" className="px-2.5 text-hint">
              Releases
            </ToggleGroupItem>
          </ToggleGroup>
          <span className="flex-1" />
          <SearchInput
            dense
            aria-label="Filter ban activity"
            placeholder="Address or jail"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            containerClassName="sm:w-56"
          />
        </PanelToolbar>
      )}
      <PanelBody flush>
        {unavailable ? (
          <div className="p-4">
            <Notice tone="default" title="No fail2ban log on this host">
              fail2ban is not installed, or it logs only to the journal. There is no file to read
              back.
            </Notice>
          </div>
        ) : error ? (
          <div className="p-4">
            <ErrorState error={error} />
          </div>
        ) : loading ? (
          <LoadingPanel />
        ) : shown.length === 0 ? (
          <EmptyState
            icon={ClockRewind}
            title={data?.length ? "Nothing matches" : "No ban activity recorded"}
            className="border-0"
          />
        ) : (
          <Table containerClassName="max-h-[24rem]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead>When</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Address</TableHead>
                <TableHead className="w-full">Jail</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((event, i) => (
                <TableRow key={`${event.at}-${event.ip}-${i}`}>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {timestamp(event.at)}
                  </TableCell>
                  <TableCell>
                    <Status
                      state={event.action === "ban" ? "failed" : "exited"}
                      label={event.action === "ban" ? "banned" : "released"}
                    />
                  </TableCell>
                  <TableCell className="font-mono">{event.ip}</TableCell>
                  <TableCell className="text-body text-muted-foreground">{event.jail}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </PanelBody>
    </Panel>
  )
}
