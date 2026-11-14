"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import {
  ClockRewind,
  Play,
  RefreshClockwise,
  SettingsSliders,
  Stop,
  Trash,
} from "@/components/icons"
import { Page, PageContext } from "@/components/page"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Status } from "@/components/status-dot"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs } from "@/components/ui/tabs"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import { IdentityFacts, IdentityStateLoading } from "@/components/network/identity-facts"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { KernelObserverPanel } from "@/components/network/kernel-observer"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { del, errorMessage, get, post, put } from "@/lib/api"
import { plural } from "@/lib/format"
import {
  flowDateRange,
  flowReading,
  yesterdayUTC,
  type FlowReport,
  type FlowSettings,
} from "@/lib/network-flows"
import { FlowsPicture } from "@/components/network/flows/flows-picture"
import {
  FlowRowReading,
  FlowsTable,
  flowRowKey,
  type FlowFilters,
} from "@/components/network/flows/flows-table"
import { FlowsHours } from "@/components/network/flows/flows-hours"
import { FlowsBoundaries, FlowsSample } from "@/components/network/flows/flows-sample"
import { FlowPolicySheet } from "@/components/network/flows/flows-policy"
import { UTCTime } from "@/components/network/flows/marks"
import {
  FLOW_ANCHOR_VIEW,
  FLOW_VIEWS,
  FLOW_VIEW_NAME,
  flowViewOf,
  type FlowAnchor,
  type FlowViewKey,
} from "@/components/network/flows/views"

/**
 * Reading register: the socket recorder, the hours it has kept and what
 * they measured, with its exact owner evidence and its gaps.
 *
 * The page was a status paragraph, a filter form, a caveat notice, four
 * tiles that read "Unknown" whenever the recorder was off, and seven
 * sections of sentences and five-line table cells. It opens on the recorder
 * itself now — how it stands, since when, how often and for how long — and
 * the picture of what it samples and keeps. With the recorder off and
 * nothing kept that picture, one sentence and Start recording are the page.
 * With a record, the day and its two filters are the table's toolbar, a
 * socket hour is one line with the chosen one read out under the table, the
 * totals of the rows shown are the table's foot, and the day's recorded
 * hours are a strip. Nothing a reading did not measure is given a figure.
 *
 * Under the picture it is three views named in the address (`?tab=`,
 * `flows/views.ts`) rather than five blocks down one column: the day's
 * socket hours, the kernel observer, and the latest sample with what a
 * reading can say. A node of the picture opens the view it stands for.
 */
const VIEW = "flex min-w-0 flex-col gap-8 pt-6 focus-ring"

export function SocketHistoryPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const destructive = can("destructive")
  const view = flowViewOf(useSearchParams().get("tab"))
  // Replaced rather than pushed: a view is not a place Back should step through.
  const show = useCallback((next: FlowViewKey) => {
    const url = new URL(window.location.href)
    if (next === "hours") url.searchParams.delete("tab")
    else url.searchParams.set("tab", next)
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [])
  // A node of the picture opens its view and brings its block up: the block
  // is in the document a frame or two after its view is chosen.
  const jump = useCallback(
    (anchor: FlowAnchor) => {
      show(FLOW_ANCHOR_VIEW[anchor])
      const reach = (tries: number) => {
        const target = document.getElementById(anchor)
        if (target) target.scrollIntoView({ block: "start", behavior: "smooth" })
        else if (tries > 0) requestAnimationFrame(() => reach(tries - 1))
      }
      requestAnimationFrame(() => reach(10))
    },
    [show],
  )
  const [draft, setDraft] = useState<FlowFilters>({
    day: yesterdayUTC(),
    address: "",
    containerId: "",
  })
  const [filters, setFilters] = useState(draft)
  const range = flowDateRange(filters.day)
  const query = { ...range, address: filters.address, containerId: filters.containerId }
  const read = usePoll<FlowReport>(
    (signal) => get("/network/flows/", query, signal),
    30_000,
    [admin, filters.day, filters.address, filters.containerId],
    { enabled: admin && Boolean(range) },
  )
  const [action, setAction] = useState<"recording" | "export" | "observer" | null>(null)
  const busy = action !== null
  const [actionError, setActionError] = useState("")
  const [policyOpen, setPolicyOpen] = useState(false)
  const [selectedKey, setSelectedKey] = useState("")
  const { confirm, dialog } = useConfirm()
  const data = read.data
  const loading = admin && !data && !read.error
  const disabled = busy || Boolean(read.error) || !data
  const observerRetained = Boolean(
    data?.settings.kernelObserverEnabled || data?.kernelObserver.attachmentsRetained,
  )
  const reading = flowReading(data?.status ?? "unavailable")
  // Nothing recorded and nothing kept: the page is what recording would do.
  const untouched = Boolean(
    data &&
    !data.settings.enabled &&
    data.retainedRows === 0 &&
    data.rows.length === 0 &&
    data.coverageHours.length === 0 &&
    !data.recordingSince,
  )
  const rows = data?.rows ?? []
  const selected = rows.find((row) => flowRowKey(row) === selectedKey) ?? rows[0]

  // A review that is open reads the state as it is when it is confirmed, not
  // as it was when it was opened.
  const mutationState = useRef({ data, disabled, destructive })
  useEffect(() => {
    mutationState.current = { data, disabled, destructive }
  }, [data, disabled, destructive])

  const reviewObserver = (enabled: boolean) => {
    if (!data || disabled || !destructive) return
    confirm({
      title: enabled ? "Activate kernel packet observer" : "Stop kernel packet observer",
      description: enabled
        ? `Observe TCP and UDP transport payload metadata at the declared cgroup hooks. Peer and owner evidence is retained for up to ${data.settings.retentionDays} days with bounded event and storage budgets. Missing events, bytes and owners remain unknown. Restarting the dashboard will leave the observer off.`
        : "Drain retained observations and detach this session's owned programs. Failed cleanup retains the actual attachment state for inspection and retry. The final shutdown tail can remain unknown.",
      confirmLabel: enabled ? "Activate observer" : "Stop observer",
      action: async () => {
        const latest = mutationState.current
        if (!latest.data || latest.disabled || !latest.destructive)
          throw new Error("Inspect a fresh recorder state before changing the observer.")
        if (enabled && !latest.data.settings.enabled)
          throw new Error("Start history recording before activating the observer.")
        setAction("observer")
        setActionError("")
        try {
          await post("/network/flows/observer", { enabled })
          return "reported"
        } catch (err) {
          setActionError(`${errorMessage(err)} Inspect observer state before trying again.`)
          throw err
        } finally {
          read.refresh()
          setAction(null)
        }
      },
    })
  }
  const changeRecording = async () => {
    if (!data || disabled || (data.settings.enabled && observerRetained)) return
    setAction("recording")
    setActionError("")
    try {
      await post<FlowSettings>("/network/flows/recording", { enabled: !data.settings.enabled })
      read.refresh()
    } catch (err) {
      setActionError(`${errorMessage(err)} Inspect recorder state before trying again.`)
      read.refresh()
    } finally {
      setAction(null)
    }
  }
  const exportRows = async () => {
    setAction("export")
    setActionError("")
    try {
      const artifact = await get<FlowReport>("/network/flows/export", query)
      const url = URL.createObjectURL(
        new Blob([JSON.stringify(artifact)], { type: "application/json" }),
      )
      const link = document.createElement("a")
      link.href = url
      link.download = `socket-observations-${filters.day}.json`
      link.click()
      URL.revokeObjectURL(url)
    } catch (err) {
      setActionError(errorMessage(err))
    } finally {
      setAction(null)
    }
  }
  const reviewPolicy = (intervalSeconds: number, retentionDays: number) => {
    // One dialog at a time: the sheet keeps what was typed and gives way to the review.
    setPolicyOpen(false)
    confirm({
      title: "Change socket recording policy",
      description: `Sample every ${intervalSeconds} seconds and retain up to ${retentionDays} days. Expired and over-cap observations are erased immediately. Changing the interval resets the byte baseline; shorter intervals still miss short-lived sockets.`,
      confirmLabel: "Save policy",
      action: async () => {
        await put("/network/flows/policy", { intervalSeconds, retentionDays })
      },
      onDone: read.refresh,
    })
  }
  const erase = () =>
    confirm({
      title: "Erase socket history",
      description:
        "Stop any attached kernel observer, then delete all retained socket observations and coverage hours. Ordinary history keeps its current on/off setting and starts a fresh byte baseline.",
      confirmLabel: "Erase history",
      action: async () => {
        await del("/network/flows/history")
      },
      onDone: read.refresh,
    })

  const recordingVerb = data && (
    <Button
      size="sm"
      variant={data.settings.enabled ? "outline" : "default"}
      pending={action === "recording"}
      disabled={disabled || (data.settings.enabled && observerRetained)}
      onClick={changeRecording}
    >
      {data.settings.enabled ? <Stop className="size-3.5" /> : <Play className="size-3.5" />}
      {data.settings.enabled ? "Stop recording" : "Start recording"}
    </Button>
  )
  const menu: Verb[] = [
    { key: "inspect", label: "Inspect again", icon: RefreshClockwise, run: read.refresh },
    ...(destructive
      ? [
          {
            key: "policy",
            label: "Sample interval and retention",
            icon: SettingsSliders,
            disabled: !data,
            run: () => setPolicyOpen(true),
          },
          {
            key: "erase",
            label: "Erase retained history",
            icon: Trash,
            danger: true,
            disabled,
            run: erase,
          },
        ]
      : []),
  ]

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Socket history" />
      <HostIdentity
        fallback={ClockRewind}
        title="Socket history"
        label="About socket history"
        state={
          !admin ? undefined : loading ? (
            <IdentityStateLoading />
          ) : !data ? undefined : (
            <Status
              tone={read.error ? "warning" : reading.tone}
              label={read.error ? "Last known recorder state" : reading.label}
            />
          )
        }
        facts={
          <IdentityFacts loading={loading}>
            {!admin ? (
              <span>needs the admin capability</span>
            ) : !data ? (
              <span>the recorder could not be read</span>
            ) : (
              <>
                <span>
                  {data.recordingSince ? (
                    <>
                      recording since <UTCTime value={data.recordingSince} form="short" />
                    </>
                  ) : (
                    "recording has not been enabled"
                  )}
                </span>
                <FactDot />
                <span className="numeric">every {data.settings.intervalSeconds}s</span>
                <FactDot />
                <span className="numeric">keeps {plural(data.settings.retentionDays, "day")}</span>
                <FactDot />
                <span className="numeric">{plural(data.retainedRows, "row")} kept</span>
                <FactDot />
                <span>
                  read <UTCTime value={data.checkedAt} form="clock" /> UTC
                </span>
              </>
            )}
          </IdentityFacts>
        }
        command={admin && data && !untouched ? recordingVerb : undefined}
        menu={admin ? <VerbMenu verbs={menu} label="More recorder actions" /> : undefined}
      />
      {!admin ? (
        <EmptyState
          icon={ClockRewind}
          title="Socket history needs the admin capability"
          description="Peer and descriptor-owner history can expose private workload connections."
        />
      ) : !data && read.error ? (
        <ErrorState error={read.error} onRetry={read.refresh} retryable />
      ) : (
        <>
          <NetworkReadWarning
            error={read.error}
            refresh={read.refresh}
            lastSuccess={read.lastSuccess}
            reading="socket history"
          />
          {actionError && (
            <Notice tone="warning" title="Recorder action needs a fresh reading">
              <p>{actionError}</p>
            </Notice>
          )}
          {data?.error && (
            <Notice tone="warning" title="The recorder reported a problem">
              <p>{data.error}</p>
            </Notice>
          )}
          <FlowsPicture
            data={data}
            day={filters.day}
            loading={loading}
            stale={Boolean(read.error)}
            words={untouched}
            onJump={jump}
          />
          <Tabs
            value={view}
            onValueChange={(next) => show(next as FlowViewKey)}
            className="min-w-0 gap-0"
          >
            <TabsPrimitive.List
              aria-label="Views of socket history"
              className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
            >
              {FLOW_VIEWS.map((key) => (
                <TabsPrimitive.Trigger
                  key={key}
                  value={key}
                  className={tabClasses(view === key, "h-10")}
                >
                  {FLOW_VIEW_NAME[key]}
                </TabsPrimitive.Trigger>
              ))}
            </TabsPrimitive.List>
            <TabsPrimitive.Content value="hours" className={VIEW}>
              {untouched ? (
                <div
                  data-slot="flow-off"
                  className="mx-auto flex max-w-xl flex-col items-center gap-4 pb-2 text-center"
                >
                  <p className="text-body text-muted-foreground">
                    Recording has not been enabled, so no contact or bandwidth conclusion can be
                    drawn.
                  </p>
                  {recordingVerb}
                </div>
              ) : (
                <>
                  <FlowsTable
                    rows={rows}
                    loading={loading}
                    truncated={Boolean(data?.truncated)}
                    draft={draft}
                    onDraft={setDraft}
                    onRead={() =>
                      setFilters({
                        ...draft,
                        address: draft.address.trim(),
                        containerId: draft.containerId.trim(),
                      })
                    }
                    onExport={exportRows}
                    exporting={action === "export"}
                    disabled={disabled}
                    selected={selected}
                    onSelect={setSelectedKey}
                  />
                  {selected && <FlowRowReading row={selected} />}
                  <FlowsHours
                    day={filters.day}
                    hours={data?.coverageHours ?? []}
                    loading={loading}
                  />
                </>
              )}
            </TabsPrimitive.Content>
            <TabsPrimitive.Content value="observer" className={VIEW}>
              {data ? (
                <KernelObserverPanel
                  observer={data.kernelObserver}
                  settings={data.settings}
                  disabled={disabled}
                  pending={action === "observer"}
                  stale={Boolean(read.error)}
                  canControl={destructive}
                  onReview={reviewObserver}
                />
              ) : (
                <LoadingRows rows={4} />
              )}
            </TabsPrimitive.Content>
            <TabsPrimitive.Content value="sample" className={VIEW}>
              {data ? (
                <>
                  <FlowsSample cycle={data.lastCycle} />
                  <FlowsBoundaries coverage={data.coverage} />
                </>
              ) : (
                <LoadingRows rows={4} />
              )}
            </TabsPrimitive.Content>
          </Tabs>
        </>
      )}
      {data && (
        <FlowPolicySheet
          key={`${data.settings.intervalSeconds}:${data.settings.retentionDays}`}
          open={policyOpen}
          onOpenChange={setPolicyOpen}
          settings={data.settings}
          disabled={disabled}
          onReview={reviewPolicy}
        />
      )}
      {dialog}
    </Page>
  )
}
