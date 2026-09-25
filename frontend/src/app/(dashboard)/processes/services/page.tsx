"use client"

import { Suspense, useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { ListOrdered, RefreshClockwise, Servers } from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { SystemdUnit } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useConfirm } from "@/components/confirm-dialog"
import { cn } from "@/lib/utils"
import { Page, PageContext, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { ProductGlyphs, ProductLogo, unitProduct } from "@/components/product-logo"
import { ROW_BLEED } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { UnitJournalSheet } from "@/components/procs/unit-journal"
import {
  useUnitControl,
  useUnitVerbs,
  type ConfirmFn,
  type PendingMap,
} from "@/components/procs/unit-actions"

type StateFilter = "all" | "active" | "failed" | "inactive"

const STATE_LABEL: Record<StateFilter, string> = {
  all: "All",
  active: "Active",
  failed: "Failed",
  inactive: "Inactive",
}

export default function ServicesPage() {
  return (
    <Suspense>
      <Services />
    </Suspense>
  )
}

/**
 * Every systemd service, failed ones first.
 *
 * The four figures answer the question the page is opened for — is anything
 * failed — before the table does, and "enabled on boot" is there because the
 * other morning-after question is whether the thing that is running now
 * would come back. Each unit is drawn as the product it runs (§14):
 * `postgresql.service` is Postgres, `pm2-deploy.service` is PM2, and the
 * Active and Failed tiles carry the marks of what they count after their
 * figures, so "2 failed" says *what* failed before the table is read.
 */
function Services() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [filter, setFilter] = useSessionState("processes.services.query", "")
  const [state, setState] = useSessionState<StateFilter>("processes.services.state", "all")
  const [startup, setStartup] = useSessionState("processes.services.startup", "all")
  const [selected, select] = useQuerySelection("unit")
  const [focusTab, setFocusTab] = useState<string>()
  const [reloading, setReloading] = useState(false)
  const units = usePoll(
    (signal) => get<{ available: boolean; units: SystemdUnit[] }>("/systemd/", undefined, signal),
    10000,
  )
  const { pending, act } = useUnitControl(units.refresh)

  const all = useMemo(() => units.data?.units ?? [], [units.data])
  const counts = useMemo(
    () => ({
      all: all.length,
      active: all.filter((u) => u.activeState === "active").length,
      failed: all.filter((u) => u.activeState === "failed").length,
      inactive: all.filter((u) => u.activeState === "inactive").length,
      enabled: all.filter(
        (u) => u.unitFileState === "enabled" || u.unitFileState === "enabled-runtime",
      ).length,
      disabled: all.filter((u) => u.unitFileState === "disabled").length,
      static: all.filter((u) => u.unitFileState === "static").length,
    }),
    [all],
  )
  // What runs on this host, as the products the units are — the active ones
  // and the failed ones, each once, most-numerous first.
  const products = useMemo(() => {
    const of = (state: string) => {
      const counts = new Map<string, number>()
      for (const u of all) {
        if (u.activeState !== state) continue
        const id = unitProduct(u.name)
        if (id) counts.set(id, (counts.get(id) ?? 0) + 1)
      }
      return [...counts.entries()].sort((a, b) => b[1] - a[1]).map(([id]) => id)
    }
    return { active: of("active"), failed: of("failed") }
  }, [all])
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return all.filter((u) => {
      if (state !== "all" && u.activeState !== state) return false
      if (startup !== "all" && (u.unitFileState || "unknown") !== startup) return false
      if (!needle) return true
      return u.name.toLowerCase().includes(needle) || u.description.toLowerCase().includes(needle)
    })
  }, [all, filter, state, startup])

  const open = (unit: SystemdUnit, tab?: string) => {
    setFocusTab(tab)
    select(unit.name)
  }

  const daemonReload = async () => {
    setReloading(true)
    try {
      await post("/systemd/daemon-reload")
      notify.success("systemd re-read its unit files")
      units.refresh()
    } catch (err) {
      notify.error("Could not reload unit files", err)
    } finally {
      setReloading(false)
    }
  }

  const header = <PageContext eyebrow="Processes" title="Services" />

  if (units.loading && !units.data) {
    return (
      <Page>
        {header}
        <LoadingPanel />
      </Page>
    )
  }
  if (units.error && !units.data) {
    return (
      <Page>
        {header}
        <ErrorState error={units.error} />
      </Page>
    )
  }
  if (!units.data?.available) {
    return (
      <Page>
        {header}
        <EmptyState icon={ListOrdered} title="systemd is not available on this host" />
      </Page>
    )
  }

  return (
    <Page className="animate-rise">
      {header}

      <StatGrid columns={4}>
        <StatTile
          label="Active"
          value={counts.active}
          hint={<TileHint products={products.active}>of {counts.all} services</TileHint>}
        />
        <StatTile
          label="Failed"
          value={counts.failed}
          tone={counts.failed > 0 ? "danger" : "default"}
          hint={
            <TileHint products={products.failed}>
              {counts.failed > 0 ? "listed first below" : "nothing has failed"}
            </TileHint>
          }
        />
        <StatTile label="Inactive" value={counts.inactive} hint="installed, not running" />
        <StatTile
          label="Enabled on boot"
          value={counts.enabled}
          hint={`${counts.disabled} disabled · ${counts.static} static`}
        />
      </StatGrid>

      <Panel>
        <PanelHeader
          title="Units"
          advanced
          actions={
            can("system.admin") && (
              <Button
                size="sm"
                variant="outline"
                disabled={reloading}
                onClick={() => void daemonReload()}
              >
                <RefreshClockwise className="size-3.5" />
                {reloading ? "Reloading…" : "Reload unit files"}
              </Button>
            )
          }
        />
        <PanelToolbar>
          <SearchInput
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Unit or description"
            containerClassName="sm:w-64"
          />
          <div className="flex min-w-0 flex-wrap items-center gap-1">
            {(Object.keys(STATE_LABEL) as StateFilter[]).map((key) => (
              <FilterChip key={key} selected={state === key} onClick={() => setState(key)}>
                {STATE_LABEL[key]} <ChipCount>{counts[key]}</ChipCount>
              </FilterChip>
            ))}
          </div>
          <Select value={startup} onValueChange={setStartup}>
            <SelectTrigger size="sm" className="ml-auto w-40" aria-label="Startup">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">Any startup</SelectItem>
              <SelectItem value="enabled">Enabled</SelectItem>
              <SelectItem value="disabled">Disabled</SelectItem>
              <SelectItem value="static">Static</SelectItem>
              <SelectItem value="masked">Masked</SelectItem>
            </SelectContent>
          </Select>
        </PanelToolbar>
        <PanelBody flush>
          {visible.length === 0 ? (
            <EmptyState icon={ListOrdered} title="No units match" className="mt-4" />
          ) : (
            <>
              <div className="hidden min-w-0 group-data-[plain]/panel:-mx-4 lg:block">
                <Table containerClassName="max-h-[calc(100svh-24rem)]">
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead className="w-full">Unit</TableHead>
                      <TableHead>State</TableHead>
                      <TableHead>Startup</TableHead>
                      <TableHead className="w-px" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {visible.slice(0, 400).map((unit) => (
                      <UnitRow
                        key={unit.name}
                        unit={unit}
                        pending={pending}
                        confirm={confirm}
                        act={act}
                        onOpen={open}
                      />
                    ))}
                  </TableBody>
                </Table>
              </div>
              <ul className="divide-y divide-hairline lg:hidden">
                {visible.slice(0, 400).map((unit) => (
                  <UnitNarrowRow
                    key={unit.name}
                    unit={unit}
                    pending={pending}
                    confirm={confirm}
                    act={act}
                    onOpen={open}
                  />
                ))}
              </ul>
            </>
          )}
        </PanelBody>
      </Panel>

      <UnitJournalSheet
        unit={selected}
        initialTab={focusTab}
        onOpenChange={(o) => !o && select(null)}
        onChanged={units.refresh}
      />
      {dialog}
    </Page>
  )
}

/** The unit as the product it runs; one this cannot name keeps the page's glyph. */
function UnitMark({ unit }: { unit: SystemdUnit }) {
  return <ProductLogo id={unitProduct(unit.name)} size="sm" fallback={Servers} />
}

/** A tile's hint with the products it counts drawn bare after the words. */
function TileHint({ products, children }: { products: string[]; children: React.ReactNode }) {
  return (
    <span className="inline-flex max-w-full min-w-0 items-center gap-2">
      <span className="truncate">{children}</span>
      <ProductGlyphs ids={products} />
    </span>
  )
}

type RowProps = {
  unit: SystemdUnit
  pending: PendingMap
  confirm: ConfirmFn
  act: Parameters<typeof useUnitVerbs>[0]["act"]
  onOpen: (unit: SystemdUnit, tab?: string) => void
}

function UnitRow({ unit, pending, confirm, act, onOpen }: RowProps) {
  const verbs = useUnitVerbs({ unit, confirm, act, onOpenTab: (tab) => onOpen(unit, tab) })
  const busy = pending[unit.name]
  return (
    <TableRow className="group" onActivate={() => onOpen(unit)}>
      <TableCell>
        <div className="flex max-w-[30rem] min-w-0 items-center gap-3">
          <UnitMark unit={unit} />
          <div className="min-w-0">
            <RowLink onClick={() => onOpen(unit)}>{unit.name}</RowLink>
            <p className="truncate text-hint text-muted-foreground">{unit.description}</p>
          </div>
        </div>
      </TableCell>
      <TableCell>
        <Status
          state={busy ? "activating" : unit.activeState}
          label={busy ? `${busy}…` : `${unit.activeState} (${unit.subState})`}
        />
      </TableCell>
      <TableCell>
        <Tag tone={unit.unitFileState === "masked" ? "warning" : "default"}>
          {unit.unitFileState || "unknown"}
        </Tag>
      </TableCell>
      <TableCell>
        {/* Always drawn, quiet until the row is hovered: these own their
            column, and a reserved column left empty reads as a layout bug. */}
        <VerbActions dim verbs={verbs} />
      </TableCell>
    </TableRow>
  )
}

function UnitNarrowRow({ unit, pending, confirm, act, onOpen }: RowProps) {
  const verbs = useUnitVerbs({ unit, confirm, act, onOpenTab: (tab) => onOpen(unit, tab) })
  const busy = pending[unit.name]
  return (
    <li
      className={cn(
        "group flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
        ROW_BLEED,
      )}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("a, button, [role='menuitem']")) return
        onOpen(unit)
      }}
    >
      <UnitMark unit={unit} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-baseline gap-2">
          <RowLink onClick={() => onOpen(unit)}>{unit.name}</RowLink>
          <Tag>{unit.unitFileState || "unknown"}</Tag>
        </div>
        <p className="truncate text-hint text-muted-foreground">{unit.description}</p>
        <div className="mt-1.5">
          <Status
            state={busy ? "activating" : unit.activeState}
            label={busy ? `${busy}…` : `${unit.activeState} (${unit.subState})`}
          />
        </div>
      </div>
      <VerbActions verbs={verbs} className="shrink-0" />
    </li>
  )
}
