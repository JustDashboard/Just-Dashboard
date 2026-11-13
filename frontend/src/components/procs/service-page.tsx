"use client"

import { useCallback, useMemo, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowLeft, ArrowUpRight, ChevronDown, ChevronUp, Copy, Servers } from "@/components/icons"
import { ApiError, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { duration, plural, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  ProcessList,
  ProcessRow,
  SystemdList,
  SystemdRelated,
  SystemdSecurity,
  SystemdUnit,
  SystemdUnitDetail,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/components/deploy/vocabulary"
import { rowReveal, IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { PageContext, Page } from "@/components/page"
import { ProductLogo, unitProduct } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { VerbBar } from "@/components/verbs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useInspectionOrder } from "@/components/procs/inspection-order"
import { ServiceOverview } from "@/components/procs/service-overview"
import { ServiceRail } from "@/components/procs/service-rail"
import {
  ServiceControl,
  ServiceDependencies,
  ServiceEnvironment,
  ServiceFile,
  ServiceJournal,
  ServiceProcesses,
  ServiceSecurity,
} from "@/components/procs/service-views"
import {
  BUCKETS,
  SERVICE_FILTER_KEYS,
  STARTUPS,
  exposureTone,
  filterUnits,
  relations,
  serviceHref,
  splitEnvironment,
  startupSummary,
  words,
  type StartupFilter,
} from "@/components/procs/service-facts"
import { useUnitControl, useUnitVerbs } from "@/components/procs/unit-actions"
import {
  unitBucket,
  unitShortName,
  unitStateWord,
  unitTone,
  type UnitBucket,
} from "@/components/procs/units"

const TABS = [
  "overview",
  "processes",
  "journal",
  "dependencies",
  "file",
  "environment",
  "security",
  "control",
] as const
type Tab = (typeof TABS)[number]

/** An open unit is read this often; the read also measures it, so its trend fills in. */
const DETAIL_POLL = 2000

const BACK = (
  <Link
    href="/processes/services"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Services
  </Link>
)

/**
 * One systemd unit, as a place of its own.
 *
 * It was a `SidePanel` over the Services table until 0.8.6: a unit's
 * readings, command and settings in a drawer's width, and its journal in a
 * second tab of the same drawer. Everything else systemd keeps about a unit
 * — what starts it and what it needs, the file it was read from, the
 * environment that file gives it, how much of the host it can reach, and the
 * verbs only a full read can offer — had nowhere to go. So a unit is a
 * destination with a way back, the way a process is (`process-page`), and
 * its views are the underlined strip under its identity line.
 *
 * The table stays beside it from `xl` (`ServiceRail`): its filters, search
 * and order, so checking the next failed unit is one press, Alt+↑ and Alt+↓
 * walk the list, and the view on screen goes with you. On a phone the page
 * is the one pane, with the way back and the same two arrows.
 */
export function ServicePage() {
  const params = useParams<{ unit: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const name = decodeName(params.unit)
  const requested = search.get("tab")
  const [remembered, remember] = useViewState<string>("processes.services.page.tab", "overview")
  const [chosen, setChosen] = useState<string>(requested ?? remembered)
  const tab: Tab = (TABS as readonly string[]).includes(chosen) ? (chosen as Tab) : "overview"
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      remember(next)
      // The address says which view, so a link to a unit's journal opens on
      // it; replaced rather than pushed, because the view is not a place Back
      // should step through.
      const url = new URL(window.location.href)
      if (next === "overview") url.searchParams.delete("tab")
      else url.searchParams.set("tab", next)
      window.history.replaceState(null, "", `${url.pathname}${url.search}`)
    },
    [remember],
  )
  const open = useCallback((next: string) => router.push(serviceHref(next, tab)), [router, tab])

  const rail = useRail()
  const at = rail.units.findIndex((unit) => unit.name === name)
  const adjacent = (direction: number) => {
    const next = at >= 0 ? rail.units[at + direction] : rail.units[direction > 0 ? 0 : -1]
    if (next) open(next.name)
  }
  const detail = usePoll(
    (signal) => get<SystemdUnitDetail>(`/systemd/${encodeURIComponent(name)}`, undefined, signal),
    DETAIL_POLL,
    [name],
    { enabled: name !== "" },
  )
  const previous = { disabled: at <= 0, run: () => adjacent(-1) }
  const next = {
    disabled: rail.units.length === 0 || at >= rail.units.length - 1,
    run: () => adjacent(1),
  }

  return (
    <Workspace
      name="Service"
      stateKey="service.page"
      refresh={detail.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous service",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next service",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <ServiceRail
            className="hidden w-72 shrink-0 xl:flex"
            units={rail.units}
            current={name}
            currentUnit={detail.data?.unit}
            loading={rail.loading}
            query={rail.query}
            onQuery={rail.setQuery}
            filtered={rail.filtered}
            onClearFilters={rail.clearFilters}
            caption={rail.caption}
            now={rail.now}
            onOpen={open}
            bindings={rail.bindings}
          />
          <ServiceMain
            key={name}
            name={name}
            detail={detail}
            onChanged={rail.refresh}
            tab={tab}
            setTab={setTab}
            previous={previous}
            next={next}
          />
        </div>
      </Page>
    </Workspace>
  )
}

/** The route's segment as a unit name; a name cannot hold `%`, so decoding twice is harmless. */
function decodeName(segment: string | undefined): string {
  if (!segment) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}

/**
 * The table's list, read for the rail: the same filters and search the table
 * was left on (they are its session's), at the table's cadence and in its
 * order, failed first.
 */
function useRail() {
  const [query, setQuery] = useSessionState(SERVICE_FILTER_KEYS.query, "")
  const [rememberedState, setState] = useSessionState(SERVICE_FILTER_KEYS.state, "")
  const [rememberedStartup, setStartup] = useSessionState(SERVICE_FILTER_KEYS.startup, "")
  const state = (BUCKETS.has(rememberedState) ? rememberedState : "") as UnitBucket | ""
  const startup = (STARTUPS.has(rememberedStartup) ? rememberedStartup : "") as StartupFilter
  const list = usePoll((signal) => get<SystemdList>("/systemd/", undefined, signal), 5000)
  const now = useNow(30_000)
  const matching = useMemo(
    () => filterUnits(list.data?.units ?? [], { query, state, startup }),
    [list.data, query, state, startup],
  )
  const inspection = useInspectionOrder(
    matching,
    (unit) => unit.name,
    JSON.stringify([query, state, startup]),
  )
  const installed = (list.data?.units ?? []).filter((u) => u.loadState !== "not-found").length
  const filtered = Boolean(state || startup)
  const narrowed = [
    state ? (state === "changing" ? "starting" : state) : "",
    startup ? startup : "",
  ]
    .filter(Boolean)
    .join(", ")
  const caption = list.data
    ? `${filtered || query ? `${matching.length} of ${installed}` : plural(installed, "service")}${narrowed ? ` · ${narrowed}` : " · failed first"}`
    : "Reading the services…"
  return {
    units: inspection.rows,
    bindings: inspection.bindings,
    loading: list.loading,
    refresh: list.refresh,
    query,
    setQuery,
    filtered,
    clearFilters: () => {
      setState("")
      setStartup("")
    },
    caption,
    now,
  }
}

type Step = { disabled: boolean; run: () => void }

function ServiceMain({
  name,
  detail,
  onChanged,
  tab,
  setTab,
  previous,
  next,
}: {
  name: string
  detail: ReturnType<typeof usePoll<SystemdUnitDetail>>
  onChanged: () => void
  tab: Tab
  setTab: (tab: string) => void
  previous: Step
  next: Step
}) {
  const { confirm, dialog } = useConfirm()
  const { can } = useAuth()
  const service = detail.data?.unit
  const properties = detail.data?.properties
  const missing = service?.loadState === "not-found" && !service.fragmentPath
  const bucket = service ? unitBucket(service) : "inactive"
  const running = bucket === "active" || bucket === "changing"

  const related = usePoll(
    (signal) =>
      get<SystemdRelated>(`/systemd/${encodeURIComponent(name)}/related`, undefined, signal),
    15_000,
    [name],
    { enabled: Boolean(service) && !missing },
  )
  const processes = usePoll(
    (signal) =>
      get<ProcessList>(
        "/processes/inventory",
        { group: `systemd:${name}`, sort: "cpu", limit: 200 },
        signal,
      ),
    4000,
    [name],
    { enabled: running && Boolean(service?.mainPid || service?.tasks) },
  )
  // The main process's own reading carries what it listens on and the
  // connections it holds, which the inventory's rows do not.
  const mainPid = service?.mainPid
  const main = usePoll(
    (signal) => get<ProcessRow>(`/processes/${mainPid}`, undefined, signal),
    6000,
    [mainPid],
    { enabled: Boolean(mainPid) },
  )
  const security = usePoll(
    (signal) =>
      get<SystemdSecurity>(`/systemd/${encodeURIComponent(name)}/security`, undefined, signal),
    60_000,
    [name],
    { enabled: Boolean(service) && !missing && name.endsWith(".service") },
  )

  const refreshDetail = detail.refresh
  const refreshRelated = related.refresh
  const changed = useCallback(() => {
    refreshDetail()
    refreshRelated()
    onChanged()
  }, [refreshDetail, refreshRelated, onChanged])
  const control = useUnitControl(changed)
  const daemonReload = useCallback(async () => {
    try {
      await post("/systemd/daemon-reload")
      notify.success("systemd re-read its unit files")
      changed()
    } catch (err) {
      notify.error("Could not reload unit files", err)
    }
  }, [changed])
  const busy = service ? control.pending[service.name] : undefined

  if (!service || missing) {
    if (detail.error || missing) {
      const notFound =
        missing || (detail.error instanceof ApiError && [400, 404].includes(detail.error.status))
      return (
        <div className="flex min-w-0 flex-1 flex-col gap-5">
          <PageContext eyebrow={BACK} title="Service" />
          {notFound ? (
            <EmptyState
              icon={Servers}
              title={`No unit named ${name || "that"} on this host`}
              description="It may have been removed, or the name in the link is misspelt. Pick another from the list."
              action={
                <Link
                  href="/processes/services"
                  className="text-body font-medium text-brand hover:underline"
                >
                  Back to the services
                </Link>
              }
            />
          ) : (
            detail.error && <ErrorState error={detail.error} />
          )}
        </div>
      )
    }
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        <PageContext eyebrow={BACK} title={unitShortName(name)} />
        <HostIdentity
          className="border-b-0 pb-0"
          title={<TextShimmer>{`Reading ${name}…`}</TextShimmer>}
          facts={<span>&nbsp;</span>}
        />
        <LoadingRows />
      </div>
    )
  }

  const p = properties ?? {}
  const relationCount = new Set(
    Object.entries(relations(p))
      .filter(([key]) => key !== "After" && key !== "Before")
      .flatMap(([, names]) => names),
  ).size
  const views: { key: Tab; label: string; count?: number }[] = [
    { key: "overview", label: "Overview" },
    { key: "processes", label: "Processes", count: running ? processes.data?.total : undefined },
    { key: "journal", label: "Journal" },
    { key: "dependencies", label: "Dependencies", count: relationCount },
    {
      key: "file",
      label: "Unit file",
      count: service.fragmentPath ? 1 + words(p.DropInPaths).length : undefined,
    },
    { key: "environment", label: "Environment", count: splitEnvironment(p.Environment).length },
    { key: "security", label: "Limits and security" },
    { key: "control", label: "Control" },
  ]
  const shown = views.some((view) => view.key === tab) ? tab : "overview"

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        <PageContext
          eyebrow={BACK}
          title={service.name}
          actions={
            <>
              <HeaderVerbs
                unit={service}
                properties={p}
                confirm={confirm}
                act={control.act}
                onDaemonReload={can("system.admin") ? () => void daemonReload() : undefined}
              />
              <span className="flex items-center">
                <IconAction
                  label="Previous service"
                  disabled={previous.disabled}
                  onClick={previous.run}
                >
                  <ChevronUp />
                </IconAction>
                <IconAction label="Next service" disabled={next.disabled} onClick={next.run}>
                  <ChevronDown />
                </IconAction>
                <span className="max-sm:hidden">
                  <WorkspaceHelp compact />
                </span>
              </span>
            </>
          }
        />
        <ServiceIdentity
          unit={service}
          properties={p}
          busy={busy}
          security={security.data}
          onTab={setTab}
        />
      </div>

      <Tabs value={shown} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
        <TabsPrimitive.List
          aria-label={`Views of ${service.name}`}
          className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
        >
          {views.map((view) => (
            <TabsPrimitive.Trigger
              key={view.key}
              value={view.key}
              className={tabClasses(shown === view.key, "h-10")}
            >
              {view.label}
              {view.count !== undefined && view.count > 0 && <ChipCount>{view.count}</ChipCount>}
            </TabsPrimitive.Trigger>
          ))}
        </TabsPrimitive.List>

        <TabsContent value="overview" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          <ServiceOverview
            unit={service}
            properties={p}
            processes={processes.data}
            main={main.data}
            related={related.data}
            onTab={setTab}
            onDaemonReload={can("system.admin") ? () => void daemonReload() : undefined}
          />
        </TabsContent>
        <TabsContent value="processes" className="min-h-0 flex-1 pt-4 xl:overflow-y-auto">
          {shown === "processes" && (
            <ServiceProcesses
              unit={service}
              list={processes.data}
              loading={processes.loading}
              error={processes.error}
            />
          )}
        </TabsContent>
        <TabsContent
          value="journal"
          className="flex min-h-0 flex-1 flex-col pt-4 xl:overflow-y-auto"
        >
          {/* Keyed on the unit so another unit starts a clean buffer. */}
          {shown === "journal" && <ServiceJournal key={service.name} unit={service.name} />}
        </TabsContent>
        <TabsContent value="dependencies" className="min-h-0 flex-1 pt-4 xl:overflow-y-auto">
          {shown === "dependencies" && (
            <ServiceDependencies properties={p} related={related.data} />
          )}
        </TabsContent>
        <TabsContent value="file" className="min-h-0 flex-1 pt-4 xl:overflow-y-auto">
          {shown === "file" && (
            <ServiceFile
              unit={service}
              onDaemonReload={can("system.admin") ? () => void daemonReload() : undefined}
            />
          )}
        </TabsContent>
        <TabsContent
          value="environment"
          className="flex min-h-0 flex-1 flex-col pt-4 xl:overflow-y-auto"
        >
          {shown === "environment" && <ServiceEnvironment unit={service} properties={p} />}
        </TabsContent>
        <TabsContent value="security" className="min-h-0 flex-1 pt-4 xl:overflow-y-auto">
          {shown === "security" && (
            <ServiceSecurity
              unit={service}
              properties={p}
              security={security.data}
              loading={security.loading}
              onTab={setTab}
            />
          )}
        </TabsContent>
        <TabsContent value="control" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {shown === "control" && (
            <ServiceControl
              unit={service}
              properties={p}
              confirm={confirm}
              act={control.act}
              pending={busy}
              onChanged={changed}
              onDaemonReload={can("system.admin") ? () => void daemonReload() : undefined}
            />
          )}
        </TabsContent>
      </Tabs>
      {dialog}
    </div>
  )
}

function HeaderVerbs({
  unit,
  properties,
  confirm,
  act,
  onDaemonReload,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
  confirm: Parameters<typeof useUnitVerbs>[0]["confirm"]
  act: Parameters<typeof useUnitVerbs>[0]["act"]
  onDaemonReload?: () => void
}) {
  const verbs = useUnitVerbs({
    unit,
    confirm,
    act,
    canReload: properties.CanReload === "yes",
    properties,
    onDaemonReload,
  })
  return <VerbBar verbs={verbs} />
}

/**
 * The line the page opens on (`HostIdentity`): the unit as the product it
 * runs with its state in the tile's corner, its name, and after it the facts
 * a unit is opened to check — what it is, who it runs as, whether it starts
 * on boot, how long it has been up (ticking), its main process as a link to
 * that process's page, and how many tasks it holds. Its state stands at the
 * line's end, and under it how exposed it is, in its tone.
 */
function ServiceIdentity({
  unit,
  properties: p,
  busy,
  security,
  onTab,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
  busy?: string
  security?: SystemdSecurity
  onTab: (tab: string) => void
}) {
  const bucket = unitBucket(unit)
  const up = bucket === "active" && unit.subState === "running"
  const now = useNow(up ? 1000 : 30_000)
  const startup = startupSummary(unit.unitFileState)
  const word = unitStateWord(unit)
  const tone = unitTone(unit)
  const score = security?.available ? security.score : undefined
  return (
    <HostIdentity
      className="animate-rise border-b-0 pb-0"
      logo={
        <span className="relative flex shrink-0">
          <ProductLogo
            id={unitProduct(unit.name)}
            fallback={Servers}
            className="size-12 rounded-xl [&_img]:size-7"
          />
          <span
            aria-hidden
            className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-full bg-background"
          >
            <StatusDot tone={tone} live={up} />
          </span>
        </span>
      }
      title={
        <span className="inline-flex min-w-0 items-baseline gap-2">
          <span className="truncate">{unitShortName(unit.name)}</span>
          <button
            type="button"
            onClick={() => void copyText(unit.name, "Unit name copied")}
            aria-label={`Copy ${unit.name}`}
            className="group inline-flex shrink-0 items-center gap-1 rounded-sm font-mono text-body font-normal text-muted-foreground focus-ring hover:text-foreground"
          >
            {unit.name.slice(unitShortName(unit.name).length) || unit.name}
            <Copy aria-hidden className={cn("size-3", rowReveal())} />
          </button>
        </span>
      }
      facts={
        <>
          {unit.description && unit.description !== unit.name && (
            <>
              <span className="text-foreground">{unit.description}</span>
              <FactDot />
            </>
          )}
          <span>
            runs as{" "}
            <span className="font-mono">
              {p.User || (p.DynamicUser === "yes" ? "a dynamic user" : "root")}
            </span>
          </span>
          <FactDot />
          <span
            title={startup.hint}
            className={cn(unit.unitFileState === "masked" && "text-warning")}
          >
            {startup.fact}
          </span>
          {up && unit.activeSince ? (
            <>
              <FactDot />
              <span
                className="numeric text-foreground"
                title={timestamp(new Date(unit.activeSince * 1000).toISOString())}
              >
                up {duration(Math.max(0, now / 1000 - unit.activeSince))}
              </span>
            </>
          ) : null}
          {unit.mainPid ? (
            <>
              <FactDot />
              <Link
                href={`/processes/${unit.mainPid}`}
                className="numeric inline-flex items-center gap-1 rounded-sm text-foreground focus-ring hover:underline"
              >
                main PID {unit.mainPid}
                <ArrowUpRight aria-hidden className="size-3" />
              </Link>
            </>
          ) : null}
          {(unit.tasks ?? 0) > 0 && (
            <>
              <FactDot />
              <span className="numeric">{plural(unit.tasks ?? 0, "task")}</span>
            </>
          )}
        </>
      }
      aside={
        <span className="flex flex-col items-end gap-0.5 max-sm:items-start">
          {busy ? (
            <span className="inline-flex items-center gap-1.5 text-body font-medium">
              <StatusDot tone="notice" />
              <TextShimmer>{`${busy}…`}</TextShimmer>
            </span>
          ) : (
            <span title={`${unit.activeState} (${unit.subState})`}>
              <Status
                tone={tone}
                live={up}
                label={bucket === "changing" ? <TextShimmer>{word}</TextShimmer> : word}
                className="text-body"
              />
            </span>
          )}
          {score !== undefined && (
            <button
              type="button"
              onClick={() => onTab("security")}
              className={cn(
                "numeric rounded-sm text-hint focus-ring hover:underline",
                {
                  running: "text-success",
                  notice: "text-muted-foreground",
                  warning: "text-warning",
                  danger: "text-destructive",
                }[exposureTone(score)],
              )}
            >
              exposure {score.toFixed(1)} · {security?.verdict?.toLowerCase()}
            </button>
          )}
        </span>
      }
    />
  )
}
