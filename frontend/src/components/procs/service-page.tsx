"use client"

import { useCallback, useMemo } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { ArrowUpRight, Copy, Servers } from "@/components/icons"
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
import { useSessionState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { useNow } from "@/hooks/use-now"
import { rowReveal } from "@/components/icon-action"
import { DetailPage, useDetailTab, useSiblingSteps } from "@/components/detail-page"
import { Fact } from "@/components/fact-line"
import { IdentityHeader, IdentityVerbs } from "@/components/identity-header"
import { missingFromError } from "@/components/missing-thing-model"
import { PageContext, Page } from "@/components/page"
import { ProductLogo, unitProduct } from "@/components/product-logo"
import { EmptyState, ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Status, StatusDot } from "@/components/status-dot"
import { ViewTabList, type TabView } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useInspectionOrder } from "@/components/procs/inspection-order"
import { NoSystemd } from "@/components/procs/no-systemd"
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
import { decodeSegment } from "@/lib/route-params"

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

const BACK = { href: "/processes/services", label: "Services" }

/**
 * One systemd unit, as a place of its own.
 *
 * It was a `SidePanel` over the Services table until 0.8.6: a unit's
 * readings, command and settings in a drawer's width, and its journal in a
 * second tab of the same drawer. Everything else systemd keeps about a unit
 * — what starts it and what it needs, the file it was read from, the
 * environment that file gives it, how much of the host it can reach, and the
 * verbs only a full read can offer — had nowhere to go. So a unit is a
 * destination with a way back, the way a process is (`process-page`), on the
 * shared detail frame (`DetailPage`), and its views are the underlined strip
 * under its identity line.
 *
 * The table stays beside it from `xl` (`ServiceRail`): its filters, search
 * and order, so checking the next failed unit is one press, Alt+↑ and Alt+↓
 * walk the list, and the view on screen goes with you. On a phone the page
 * is the one pane, with the way back and the same two arrows.
 */
export function ServicePage() {
  const params = useParams<{ unit: string }>()
  const router = useRouter()
  const name = decodeSegment(params.unit)
  const [tab, setTab] = useDetailTab(TABS, { remember: "processes.services.page.tab" })
  const open = useCallback((next: string) => router.push(serviceHref(next, tab)), [router, tab])

  const rail = useRail()
  const steps = useSiblingSteps(
    rail.units.map((unit) => unit.name),
    name,
    open,
  )
  const detail = usePoll(
    (signal) => get<SystemdUnitDetail>(`/systemd/${encodeURIComponent(name)}`, undefined, signal),
    DETAIL_POLL,
    [name],
    { enabled: name !== "" },
  )
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
  const refreshRail = rail.refresh
  const changed = useCallback(() => {
    refreshDetail()
    refreshRelated()
    refreshRail()
  }, [refreshDetail, refreshRelated, refreshRail])
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
  const onDaemonReload = can("system.admin") ? () => void daemonReload() : undefined
  const busy = service ? control.pending[service.name] : undefined

  // No systemd is the page's answer, not one unit's failure: the rail would
  // read "No service matches" beside a red box with systemctl's refusal.
  const noSystemd = missingFromError(detail.error, { systemd: "service" })
  if (rail.absent || noSystemd?.reason === "no-systemd") {
    return (
      <Page>
        <PageContext back={BACK} title="Service" />
        <NoSystemd reader="service" raw={noSystemd?.raw} />
      </Page>
    )
  }

  const failed = !service && (detail.error || missing)
  const notFound =
    missing || (detail.error instanceof ApiError && [400, 404].includes(detail.error.status))
  const p = properties ?? {}
  const relationCount = new Set(
    Object.entries(relations(p))
      .filter(([key]) => key !== "After" && key !== "Before")
      .flatMap(([, names]) => names),
  ).size
  const views: TabView<Tab>[] = service
    ? [
        { key: "overview", label: "Overview" },
        {
          key: "processes",
          label: "Processes",
          count: running ? processes.data?.total : undefined,
        },
        { key: "journal", label: "Journal" },
        { key: "dependencies", label: "Dependencies", count: relationCount },
        {
          key: "file",
          label: "Unit file",
          count: service.fragmentPath ? 1 + words(p.DropInPaths).length : undefined,
        },
        {
          key: "environment",
          label: "Environment",
          count: splitEnvironment(p.Environment).length,
        },
        { key: "security", label: "Limits and security" },
        { key: "control", label: "Control" },
      ]
    : []
  const shown = views.some((view) => view.key === tab) ? tab : "overview"

  return (
    <DetailPage
      name="Service"
      what="service"
      stateKey="service.page"
      refresh={detail.refresh}
      steps={steps}
      back={BACK}
      title={service && !missing ? service.name : failed ? "Service" : unitShortName(name)}
      rail={
        <ServiceRail
          units={rail.units}
          current={name}
          currentUnit={service}
          loading={rail.loading}
          error={rail.error}
          onRetry={rail.refresh}
          query={rail.query}
          onQuery={rail.setQuery}
          filtered={rail.filtered}
          onClearFilters={rail.clearFilters}
          caption={rail.caption}
          now={rail.now}
          onOpen={open}
          bindings={rail.bindings}
        />
      }
      identity={
        failed ? undefined : service && !missing ? (
          <ServiceIdentity
            unit={service}
            properties={p}
            busy={busy}
            security={security.data}
            confirm={confirm}
            act={control.act}
            onDaemonReload={onDaemonReload}
            onTab={setTab}
          />
        ) : (
          // The line's final shape while the unit is read, its verbs' place
          // held, so nothing under it moves when it lands (D11).
          <IdentityHeader
            loading
            className="border-b-0 pb-0"
            title={<TextShimmer>{`Reading ${name}…`}</TextShimmer>}
            state
            command
            menu={false}
          />
        )
      }
    >
      {failed ? (
        notFound ? (
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
        )
      ) : !service || missing ? (
        <LoadingRows />
      ) : (
        <Tabs key={name} value={shown} onValueChange={setTab} className="gap-0">
          <ViewTabList label={`Views of ${service.name}`} views={views} value={shown} sticky />

          <TabsContent value="overview" className="pt-6">
            <ServiceOverview
              unit={service}
              properties={p}
              processes={processes.data}
              main={main.data}
              related={related.data}
              onTab={setTab}
              onDaemonReload={onDaemonReload}
            />
          </TabsContent>
          <TabsContent value="processes" className="pt-4">
            {shown === "processes" && (
              <ServiceProcesses
                unit={service}
                list={processes.data}
                loading={processes.loading}
                error={processes.error}
              />
            )}
          </TabsContent>
          <TabsContent value="journal" className="flex flex-col pt-4">
            {/* Keyed on the unit so another unit starts a clean buffer. */}
            {shown === "journal" && <ServiceJournal key={service.name} unit={service.name} />}
          </TabsContent>
          <TabsContent value="dependencies" className="pt-4">
            {shown === "dependencies" && (
              <ServiceDependencies properties={p} related={related.data} />
            )}
          </TabsContent>
          <TabsContent value="file" className="pt-4">
            {shown === "file" && <ServiceFile unit={service} onDaemonReload={onDaemonReload} />}
          </TabsContent>
          <TabsContent value="environment" className="flex flex-col pt-4">
            {shown === "environment" && <ServiceEnvironment unit={service} properties={p} />}
          </TabsContent>
          <TabsContent value="security" className="pt-4">
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
          <TabsContent value="control" className="pt-6">
            {shown === "control" && (
              <ServiceControl
                unit={service}
                properties={p}
                confirm={confirm}
                act={control.act}
                pending={busy}
                onChanged={changed}
                onDaemonReload={onDaemonReload}
              />
            )}
          </TabsContent>
        </Tabs>
      )}
      {dialog}
    </DetailPage>
  )
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
    // The list answers `available: false` where there is no systemd to ask.
    absent: list.data ? !list.data.available : false,
    loading: list.loading,
    error: list.data ? undefined : list.error,
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

/**
 * The line the page opens on (`IdentityHeader`): the unit as the product it
 * runs with its state in the tile's corner, its name, its state beside it,
 * and after them the facts a unit is opened to check. How exposed it is leads
 * them where that is a warning or worse, and so does a masked unit; then what
 * it is, who it runs as, whether it starts on boot, how long it has been up
 * (ticking), its main process as a link to that process's page, and how many
 * tasks it holds. Its verbs are the line's own: one command, the rest behind
 * `⋯`.
 */
function ServiceIdentity({
  unit,
  properties: p,
  busy,
  security,
  confirm,
  act,
  onDaemonReload,
  onTab,
}: {
  unit: SystemdUnit
  properties: Record<string, string>
  busy?: string
  security?: SystemdSecurity
  confirm: Parameters<typeof useUnitVerbs>[0]["confirm"]
  act: Parameters<typeof useUnitVerbs>[0]["act"]
  onDaemonReload?: () => void
  onTab: (tab: string) => void
}) {
  const verbs = useUnitVerbs({
    unit,
    confirm,
    act,
    canReload: p.CanReload === "yes",
    properties: p,
    onDaemonReload,
  })
  const bucket = unitBucket(unit)
  const up = bucket === "active" && unit.subState === "running"
  const now = useNow(up ? 1000 : 30_000)
  const startup = startupSummary(unit.unitFileState)
  const word = unitStateWord(unit)
  const tone = unitTone(unit)
  const score = security?.available ? security.score : undefined
  const exposure = score !== undefined ? exposureTone(score) : undefined
  const exposed = (
    <Fact>
      <button
        type="button"
        onClick={() => onTab("security")}
        className={cn(
          "numeric rounded-sm focus-ring hover:underline",
          exposure &&
            {
              running: "text-success",
              notice: "text-muted-foreground",
              warning: "text-warning",
              danger: "text-destructive",
            }[exposure],
        )}
      >
        exposure {score?.toFixed(1)} · {security?.verdict?.toLowerCase()}
      </button>
    </Fact>
  )
  const urgent = exposure === "warning" || exposure === "danger"
  const masked = unit.unitFileState === "masked"
  const startsOnBoot = (
    <Fact title={startup.hint} className={cn(masked && "text-warning")}>
      {startup.fact}
    </Fact>
  )
  return (
    <IdentityHeader
      className="animate-rise border-b-0 pb-0"
      logo={
        <span className="relative flex shrink-0">
          <ProductLogo
            id={unitProduct(unit.name)}
            fallback={Servers}
            className="size-10 rounded-lg [&_img]:size-6"
          />
          <StatusDot corner="lg" tone={tone} live={up} />
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
      state={
        busy ? (
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
            />
          </span>
        )
      }
      facts={
        <>
          {score !== undefined && urgent && exposed}
          {masked && startsOnBoot}
          {unit.description && unit.description !== unit.name && (
            <Fact className="text-foreground">{unit.description}</Fact>
          )}
          <Fact>
            runs as{" "}
            <span className="font-mono">
              {p.User || (p.DynamicUser === "yes" ? "a dynamic user" : "root")}
            </span>
          </Fact>
          {!masked && startsOnBoot}
          {up && unit.activeSince ? (
            <Fact
              className="numeric text-foreground"
              title={timestamp(new Date(unit.activeSince * 1000).toISOString())}
            >
              up {duration(Math.max(0, now / 1000 - unit.activeSince))}
            </Fact>
          ) : null}
          {unit.mainPid ? (
            <Fact>
              <Link
                href={`/processes/${unit.mainPid}`}
                className="numeric inline-flex items-center gap-1 rounded-sm text-foreground focus-ring hover:underline"
              >
                main PID {unit.mainPid}
                <ArrowUpRight aria-hidden className="size-3" />
              </Link>
            </Fact>
          ) : null}
          {(unit.tasks ?? 0) > 0 && (
            <Fact className="numeric">{plural(unit.tasks ?? 0, "task")}</Fact>
          )}
          {score !== undefined && !urgent && exposed}
        </>
      }
      {...IdentityVerbs({ verbs, name: unit.name })}
    />
  )
}
