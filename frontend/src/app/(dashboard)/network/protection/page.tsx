"use client"

import { NetworkReadWarning } from "@/components/network/read-warning"
import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { Plus, Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, calendarDate, plural } from "@/lib/format"
import type {
  ProtectionBlocklist,
  ProtectionLimit,
  ProtectionPressure,
  ProtectionView,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { NumberTicker } from "@/components/ui/number-ticker"
import { TileTrend } from "@/components/metrics/sparkline"
import { BlocklistList, BlocklistModal } from "@/components/network/protection/blocklists"
import { ExceptionList, ExceptionModal } from "@/components/network/protection/exceptions"
import { PressurePanel } from "@/components/network/protection/pressure"
import { KernelSettings } from "@/components/network/protection/kernel"
import { LimitList, LimitModal } from "@/components/network/protection/limits"
import { levelTone } from "@/components/network/protection/reading"
import { TrustedList } from "@/components/network/protection/trusted"

type Editor =
  | { kind: "list"; list?: ProtectionBlocklist }
  | { kind: "limit"; limit?: ProtectionLimit }
  | { kind: "exception" }

/**
 * What this server refuses before anything answers.
 *
 * It opens on four readings: what the blocklists have dropped since the table
 * was loaded, what the limits have refused, how many networks are blocked, and
 * how full the connection table is — a meter, because the table filling up is
 * the failure a flood is aiming for, and it goes amber and then red with its
 * level. Then the blocklists as lit cards (countries with their flags, a feed
 * by its name, typed addresses by their count), the rate limits as lit cards
 * with the sentence each one says, the addresses nothing may ever refuse, and
 * the kernel's own defences as staged settings applied together.
 *
 * Every drop here is preceded by the trusted set, so the operator cannot lock
 * themselves out with a list; a list that holds their address says so on its
 * card, and the set is listed so it can be read rather than taken on trust.
 * The connection table's own size is one of the kernel settings, beside the
 * recommendation it is read against.
 */
export default function NetworkProtectionPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const protection = usePoll<ProtectionView>(
    (signal) => get("/network/protection", undefined, signal),
    10_000,
  )
  // The table's breakdown names who connects, so it is an administrator's
  // reading, and slower: it walks the whole connection table.
  const pressure = usePoll<ProtectionPressure>(
    (signal) => get("/network/protection/pressure", undefined, signal),
    30_000,
    [],
    { enabled: admin },
  )
  const [editor, setEditor] = useState<Editor>()

  if (!protection.data) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Network" title="Protection" />
        {protection.error ? (
          <ErrorState error={protection.error} onRetry={protection.refresh} />
        ) : (
          <LoadingPanel />
        )}
      </Page>
    )
  }
  const view = protection.data
  const lists = view.blocklists
  const holdingYou = lists.filter((l) => l.containsYou)
  const listed = lists.filter((l) => l.enabled).reduce((n, l) => n + l.count, 0)
  const byLists = lists.reduce((n, l) => n + l.packets, 0)
  const byListsBytes = lists.reduce((n, l) => n + l.bytes, 0)
  const byLimits = view.limits.reduce((n, l) => n + l.packets, 0)
  const byLimitsBytes = view.limits.reduce((n, l) => n + l.bytes, 0)
  const failing = lists.filter((l) => l.error).length
  const entries = lists.some((l) => l.enabled) || view.limits.some((l) => l.enabled)
  const table = view.conntrack
  const listTotal = lists.reduce((n, l) => n + (l.total?.packets ?? 0), 0)
  const limitTotal = view.limits.reduce((n, l) => n + (l.total?.packets ?? 0), 0)
  const since = [...lists, ...view.limits]
    .map((e) => e.total?.since)
    .filter((t): t is string => Boolean(t))
    .sort()[0]
  const counts = pressure.data?.series["conntrack:count"]?.map((p) => p.value) ?? []
  const exceptions = view.exceptions ?? []

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Protection" />
      {protection.data && (
        <NetworkReadWarning
          error={protection.error}
          refresh={protection.refresh}
          lastSuccess={protection.lastSuccess}
        />
      )}

      {entries && !view.loaded && (
        <Notice tone="warning" icon={Warning} title="Protection is not loaded into the kernel">
          Its lists and limits are saved but not in force: the host was started without the unit
          that loads them, or something deleted the table. Saving any of them loads it again.
        </Notice>
      )}

      <StatGrid columns={4}>
        <StatTile
          label="Dropped by blocklists"
          value={<NumberTicker value={byLists} />}
          trailing="packets"
          hint={
            since
              ? `${bytes(byListsBytes)} since load · ${listTotal.toLocaleString()} since ${calendarDate(since)}`
              : `${bytes(byListsBytes)} since the table was loaded`
          }
        />
        <StatTile
          label="Refused by limits"
          value={<NumberTicker value={byLimits} />}
          trailing="packets"
          hint={
            since
              ? `${bytes(byLimitsBytes)} since load · ${limitTotal.toLocaleString()} since ${calendarDate(since)}`
              : `${bytes(byLimitsBytes)} · ${plural(view.limits.filter((l) => l.enabled).length, "limit")} in force`
          }
        />
        <StatTile
          label="Networks blocked"
          value={<NumberTicker value={listed} />}
          tone={failing > 0 ? "warning" : "default"}
          hint={
            failing > 0
              ? `${plural(failing, "list")} could not be fetched`
              : `across ${plural(lists.filter((l) => l.enabled).length, "list")}`
          }
        />
        <StatTile
          label="Connection table"
          value={
            table.available ? (
              <>
                <NumberTicker value={table.percent} />%
              </>
            ) : (
              "Not tracked"
            )
          }
          tone={table.available ? levelTone(table.level) : "default"}
          meter={table.available ? table.percent : undefined}
          meterLabel="Connection table fullness"
          trend={
            table.available && counts.length > 1 ? (
              <TileTrend
                values={counts}
                max={table.max}
                label="Tracked connections over the last day"
                color="var(--chart-1)"
              />
            ) : undefined
          }
          hint={
            table.available
              ? `${table.count.toLocaleString()} of ${table.max.toLocaleString()} connections`
              : "connection tracking is not loaded"
          }
        />
      </StatGrid>

      <Panel plain>
        <PanelHeader
          title="Blocklists"
          actions={
            <>
              <span className="numeric text-hint text-muted-foreground">{lists.length}</span>
              <Button
                size="xs"
                variant="outline"
                onClick={() => setEditor({ kind: "list" })}
                disabled={!admin}
              >
                <Plus aria-hidden />
                New blocklist
              </Button>
            </>
          }
        />
        <PanelBody>
          <div className="flex min-w-0 flex-col gap-4">
            {holdingYou.length > 0 && (
              <Notice
                tone="warning"
                icon={Warning}
                title={`${holdingYou.map((l) => l.name).join(", ")} ${holdingYou.length === 1 ? "holds" : "hold"} your own address`}
              >
                The trusted set below still lets you in, so nothing is cut off while{" "}
                <span className="font-mono text-foreground">{view.client}</span> stays in it.
              </Notice>
            )}
            {lists.length === 0 ? (
              <EmptyNote>No network is blocked.</EmptyNote>
            ) : (
              <BlocklistList
                lists={lists}
                presets={view.presets}
                onOpen={(list) => setEditor({ kind: "list", list })}
                onChanged={protection.refresh}
              />
            )}
          </div>
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="Rate limits"
          actions={
            <>
              <span className="numeric text-hint text-muted-foreground">{view.limits.length}</span>
              <Button
                size="xs"
                variant="outline"
                onClick={() => setEditor({ kind: "limit" })}
                disabled={!admin}
              >
                <Plus aria-hidden />
                New limit
              </Button>
            </>
          }
        />
        <PanelBody>
          {view.limits.length === 0 ? (
            <EmptyNote>No port is limited.</EmptyNote>
          ) : (
            <LimitList
              limits={view.limits}
              pressure={pressure.data}
              onOpen={(limit) => setEditor({ kind: "limit", limit })}
              onChanged={protection.refresh}
            />
          )}
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="Exceptions"
          actions={
            <>
              <span className="numeric text-hint text-muted-foreground">{exceptions.length}</span>
              <Button
                size="xs"
                variant="outline"
                onClick={() => setEditor({ kind: "exception" })}
                disabled={!admin || !can("destructive")}
              >
                <Plus aria-hidden />
                New exception
              </Button>
            </>
          }
        />
        <PanelBody flush>
          <ExceptionList exceptions={exceptions} onChanged={protection.refresh} />
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="Never refused"
          actions={
            <span className="numeric text-hint text-muted-foreground">{view.trusted.length}</span>
          }
        />
        <PanelBody flush>
          <TrustedList view={view} onChanged={protection.refresh} />
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="Kernel protections"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {plural(view.settings.length, "setting")} · staged here, applied together
            </span>
          }
        />
        <PanelBody>
          <KernelSettings
            settings={view.settings}
            resetNote={view.resetNote}
            profiles={view.kernelProfiles}
            interfaces={view.interfaces}
            onChanged={protection.refresh}
          />
        </PanelBody>
      </Panel>

      {admin && (
        <Panel plain>
          <PanelHeader title="Connection table" />
          <PanelBody>
            {pressure.data ? (
              <PressurePanel pressure={pressure.data} />
            ) : pressure.error ? (
              <ErrorState error={pressure.error} onRetry={pressure.refresh} />
            ) : (
              <LoadingPanel />
            )}
          </PanelBody>
        </Panel>
      )}

      {editor?.kind === "list" && (
        <BlocklistModal
          key={editor.list?.id ?? "new"}
          list={editor.list}
          presets={view.presets}
          exceptions={exceptions.filter((e) => e.scope === `blocklist:${editor.list?.id}`)}
          onOpenChange={(open) => !open && setEditor(undefined)}
          onSaved={protection.refresh}
        />
      )}
      {editor?.kind === "limit" && (
        <LimitModal
          key={editor.limit?.id ?? "new"}
          limit={editor.limit}
          profiles={view.profiles}
          capacity={pressure.data?.limits.find((l) => l.id === editor.limit?.id)}
          onOpenChange={(open) => !open && setEditor(undefined)}
          onSaved={protection.refresh}
        />
      )}
      {editor?.kind === "exception" && (
        <ExceptionModal
          lists={lists}
          onOpenChange={(open) => !open && setEditor(undefined)}
          onSaved={protection.refresh}
        />
      )}
    </Page>
  )
}
