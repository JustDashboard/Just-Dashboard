"use client"

import { useMemo, useState } from "react"
import { useViewState } from "@/lib/view-state"
import Link from "next/link"
import type { SystemdUnit, SystemdUnitDetail } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { journalSource } from "@/lib/log-sources"
import { useConfirm } from "@/components/confirm-dialog"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Detail, DetailList } from "@/components/page"
import { Servers } from "@/components/icons"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ProductLogo, unitProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbBar } from "@/components/verbs"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useUnitControl, useUnitVerbs } from "@/components/procs/unit-actions"
import { unitRunsView } from "@/components/procs/unit-runs"
import { authUnit } from "@/components/procs/shared"

/**
 * One unit, opened: its state, how it runs, and its journal. The verbs sit in
 * the header, and here — where `systemctl show` has said whether the unit
 * takes a reload — the reload verb is offered too.
 */
export function UnitJournalSheet(props: {
  unit: string | null
  initialTab?: string
  onOpenChange: (open: boolean) => void
  onChanged?: () => void
}) {
  // Remounted per unit and per requested tab, so "Journal" from a row lands
  // on the journal even when the sheet was already open on the overview.
  return <UnitSheet key={`${props.unit ?? "none"}:${props.initialTab ?? ""}`} {...props} />
}

function UnitSheet({
  unit,
  initialTab,
  onOpenChange,
  onChanged,
}: {
  unit: string | null
  initialTab?: string
  onOpenChange: (open: boolean) => void
  onChanged?: () => void
}) {
  const { confirm, dialog } = useConfirm()
  // Which tab a unit opens on, remembered the way a container's is; a caller
  // asking for a specific tab ("open its journal") is answered first.
  const [remembered, remember] = useViewState("processes.services.detail.tab", "overview")
  const [tab, setTabState] = useState(initialTab ?? remembered)
  const setTab = (next: string) => {
    setTabState(next)
    remember(next)
  }
  const detail = usePoll(
    (signal) =>
      get<SystemdUnitDetail>(`/systemd/${encodeURIComponent(unit ?? "")}`, undefined, signal),
    5000,
    [unit],
    { enabled: unit !== null },
  )
  const { pending, act } = useUnitControl(() => {
    detail.refresh()
    onChanged?.()
  })
  const service = detail.data?.unit
  const busy = service ? pending[service.name] : undefined

  return (
    <SidePanel
      open={unit !== null}
      onOpenChange={onOpenChange}
      // The sheet opens on the unit as the product it runs, then its name.
      title={
        <>
          <ProductLogo id={unit ? unitProduct(unit) : undefined} size="sm" fallback={Servers} />
          <span className="min-w-0 truncate">{unit ?? "Unit"}</span>
        </>
      }
      description="Service state, configuration, journal and runs"
      actions={
        service && (
          <UnitSheetActions
            unit={service}
            canReload={detail.data?.properties.CanReload === "yes"}
            busy={busy}
            confirm={confirm}
            act={act}
          />
        )
      }
      bodyClassName="flex min-h-0 flex-1 flex-col p-4"
    >
      {unit && (
        <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-4">
          <TabsList className="w-fit shrink-0">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="journal">Journal</TabsTrigger>
          </TabsList>
          <TabsContent value="overview" className="min-h-0 flex-1 overflow-y-auto">
            {detail.loading && !detail.data && <LoadingRows rows={6} />}
            {detail.error && !detail.data && <ErrorState error={detail.error} />}
            {detail.data && <UnitOverview detail={detail.data} />}
          </TabsContent>
          <TabsContent value="journal" className="flex min-h-0 flex-1 flex-col">
            {/* Keyed on the unit so switching units starts a clean buffer. */}
            <UnitLogs key={unit} unit={unit} />
          </TabsContent>
        </Tabs>
      )}
      {dialog}
    </SidePanel>
  )
}

function UnitSheetActions({
  unit,
  canReload,
  busy,
  confirm,
  act,
}: {
  unit: SystemdUnit
  canReload: boolean
  busy?: string
  confirm: Parameters<typeof useUnitVerbs>[0]["confirm"]
  act: Parameters<typeof useUnitVerbs>[0]["act"]
}) {
  const verbs = useUnitVerbs({ unit, confirm, act, canReload })
  return (
    <>
      <Status
        state={busy ? "activating" : unit.activeState}
        label={busy ? `${busy}…` : `${unit.activeState} (${unit.subState})`}
      />
      <VerbBar verbs={verbs} />
    </>
  )
}

function UnitOverview({ detail }: { detail: SystemdUnitDetail }) {
  const { unit: service, properties } = detail
  const started = service.activeSince
    ? new Date(service.activeSince * 1000).toISOString()
    : undefined
  const startup = startupSummary(service.unitFileState)
  const restart = restartSummary(properties.Restart)
  return (
    <div className="flex animate-rise flex-col gap-6">
      {service.description && (
        <p className="text-body text-muted-foreground">{service.description}</p>
      )}

      <Panel plain>
        <PanelHeader title="State" />
        <PanelBody>
          <DetailList>
            <Detail label="Startup">
              <Tag>{startup.label}</Tag>
              <span className="block text-hint text-muted-foreground">{startup.hint}</span>
            </Detail>
            <Detail label="Active since">
              {started ? (
                <>
                  {relativeTime(started)}
                  <span className="block text-hint text-muted-foreground">
                    {timestamp(started)}
                  </span>
                </>
              ) : (
                "—"
              )}
            </Detail>
            <Detail label="Last result">{service.result || "—"}</Detail>
            <Detail label="Restarts" className="numeric">
              {service.restarts ?? 0}
            </Detail>
            <Detail label="Main PID" className="font-mono">
              {service.mainPid ? (
                <Link href={`/processes?pid=${service.mainPid}`} className="hover:underline">
                  {service.mainPid}
                </Link>
              ) : (
                "—"
              )}
            </Detail>
            <Detail label="Memory">{bytes(service.memoryBytes)}</Detail>
            <Detail label="Tasks" className="numeric">
              {service.tasks ?? "—"}
            </Detail>
          </DetailList>
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader title="How it runs" />
        <PanelBody className="space-y-4">
          <DetailList>
            <Detail label="Runs as">
              <span className="font-mono">{properties.User || "root"}</span>
              <span className="text-muted-foreground"> : {properties.Group || "default"}</span>
            </Detail>
            <Detail label="Working directory" className="font-mono break-all">
              {properties.WorkingDirectory || "Not set"}
            </Detail>
            <Detail label="Restart policy">
              {restart.label}
              <span className="block text-hint text-muted-foreground">{restart.hint}</span>
            </Detail>
            <Detail label="Reload">
              {properties.CanReload === "yes"
                ? "Re-reads its configuration without stopping"
                : "Not supported — a restart is the only way to apply changes"}
            </Detail>
            <Detail label="Memory limit">
              {properties.MemoryMax && properties.MemoryMax !== "infinity"
                ? bytes(Number(properties.MemoryMax))
                : "No limit"}
            </Detail>
            <Detail label="Task limit">{properties.TasksMax || "No limit"}</Detail>
            <Detail label="Unit file" className="font-mono break-all">
              {service.fragmentPath ? (
                <Link
                  href={`/files?path=${encodeURIComponent(service.fragmentPath)}`}
                  className="hover:underline"
                >
                  {service.fragmentPath}
                </Link>
              ) : (
                "—"
              )}
            </Detail>
          </DetailList>
          {properties.ExecStart && (
            <div className="space-y-1.5">
              <p className="eyebrow">Command</p>
              <Well className="max-h-36 whitespace-pre-wrap">{properties.ExecStart}</Well>
            </div>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}

/** What the startup state means for the next boot, in words. */
function startupSummary(state: string | undefined): { label: string; hint: string } {
  switch (state) {
    case "enabled":
    case "enabled-runtime":
      return { label: state, hint: "Starts automatically on boot" }
    case "disabled":
      return { label: state, hint: "Only runs when started by hand" }
    case "static":
      return { label: state, hint: "Started by another unit, not on its own" }
    case "masked":
      return { label: state, hint: "Blocked from starting at all" }
    default:
      return { label: state || "unknown", hint: "Startup state was not reported" }
  }
}

/** What the restart policy does the next time the process exits. */
function restartSummary(policy: string | undefined): { label: string; hint: string } {
  switch ((policy || "no").toLowerCase()) {
    case "always":
      return { label: policy!, hint: "Restarts after every exit" }
    case "on-failure":
      return { label: policy!, hint: "Restarts after crashes, not clean exits" }
    case "on-abnormal":
    case "on-abort":
    case "on-watchdog":
      return { label: policy!, hint: "Restarts after abnormal exits only" }
    default:
      return { label: policy || "no", hint: "Stays stopped until started again" }
  }
}

/**
 * The unit's journal, read the way the logs page reads it — live, searched,
 * added up through the unit's lens — and its runs beside that: when systemd
 * started it, how long each lasted and how it ended. One unit only; the
 * whole journal is the logs page's.
 *
 * sshd's journal is login records, which only an administrator reads. For
 * anyone else it opens nothing: the server refuses the read before the
 * socket upgrades, and a pane retrying a refusal while it says the tunnel
 * dropped is wrong twice.
 */
function UnitLogs({ unit }: { unit: string }) {
  const { can } = useAuth()
  const sources = useMemo<ServiceLogSource[]>(
    () => [{ id: journalSource(unit), label: unit, kind: "journal", product: unitProduct(unit) }],
    [unit],
  )
  const views = useMemo(() => [unitRunsView(unit)], [unit])
  if (authUnit(unit) && !can("system.admin")) {
    return (
      <Notice title="Login records need an administrator">
        {unit}&apos;s journal holds every sign-in attempt, and a failed one can hold a password
        typed into the username prompt, so only an administrator can read it.
      </Notice>
    )
  }
  return (
    <ServiceLogs
      sources={sources}
      views={views}
      layout="sheet"
      className="min-h-0 flex-1"
      paneClassName="min-h-80"
    />
  )
}
