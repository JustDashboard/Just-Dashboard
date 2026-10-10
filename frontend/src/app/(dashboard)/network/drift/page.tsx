"use client"

import { useState } from "react"
import { get, post } from "@/lib/api"
import { useAuth } from "@/hooks/use-auth"
import { notify } from "@/lib/toast"
import type { NetworkChangeStatus } from "@/lib/types"
import {
  driftRepairOutcome,
  driftReviewKey,
  driftRows,
  driftVerdict,
  selectedDriftRepairRequest,
  type DriftDomain,
  type DriftReport,
} from "@/lib/network-drift"
import { duration, plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useNow } from "@/components/deploy/vocabulary"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { LogoGlyph } from "@/components/logo"
import { Page, PageContext } from "@/components/page"
import { StatusDot } from "@/components/status-dot"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Button } from "@/components/ui/button"
import { RefreshClockwise } from "@/components/icons"
import { DriftPicture } from "@/components/network/drift/drift-picture"
import { DriftRhythm, useDriftHistory } from "@/components/network/drift/drift-rhythm"
import { DriftTable, type DriftScope } from "@/components/network/drift/drift-table"
import { DriftBoot } from "@/components/network/drift/drift-boot"
import { DriftIdentities } from "@/components/network/drift/drift-identities"
import { DriftBlocklists } from "@/components/network/drift/drift-blocklists"
import { DriftRepairs, DriftReview } from "@/components/network/drift/drift-repairs"
import { ShortDigest } from "@/components/network/drift/marks"

const POLL = 30_000

/**
 * Reading register: where the saved network configuration is written, and
 * whether the host still holds what it says.
 *
 * Four grey tiles stood here — known differences, incomplete readings,
 * matching observations, owned repair proposals — over six panels of rows
 * that said one state word each, and nothing on the page moved. Each figure
 * went where it is said better, and the page opens on the host instead:
 *
 * - the identity line's verdict says the differences, in red while another
 *   owner holds one, and narrows the table to them; the table's chips count
 *   what differs, what is incomplete and what matches;
 * - the repair proposals are the plan's own count, on its review button;
 * - the picture (`network/drift/drift-picture.tsx`) wires `spec.json` to
 *   every place it is written, each as the product that reads it, every
 *   comparison a block in its status's colour, a pulse down the wires as
 *   each inspection lands;
 * - and beside it the inspections since the page opened, a beat each, with
 *   what moved between them (`drift-rhythm.tsx`).
 *
 * Under them, one framed table of every comparison worst first, the boot
 * unit's last activation as the run of its commands, each blocklist's three
 * generations as a chain, and the repair plan as cards you pick.
 */
export default function NetworkDriftPage() {
  const read = usePoll<DriftReport>((signal) => get("/network/drift", undefined, signal), POLL)
  if (!read.data)
    return (
      <Page className="animate-rise">
        <PageContext title="Network drift" />
        {read.error ? (
          <ErrorState error={read.error} onRetry={read.refresh} />
        ) : (
          <LoadingPanel plain />
        )}
      </Page>
    )
  return <Drift read={read} data={read.data} />
}

function Drift({ read, data }: { read: PollState<DriftReport>; data: DriftReport }) {
  const { can } = useAuth()
  const now = useNow(1000)
  const [selection, setSelection] = useState<{ key: string; ids: string[] }>({ key: "", ids: [] })
  const [review, setReview] = useState(false)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState<string>()
  const [scope, setScope] = useState<DriftScope>("all")
  const [domain, setDomain] = useState<DriftDomain>()
  const [query, setQuery] = useState("")
  // An inspection asked for is in flight until the poll settles: a new
  // success or a new failure. The scheduled one is in flight from the moment
  // its timer is due.
  const [asked, setAsked] = useState<{ lastSuccess?: number; error?: Error }>()

  const rows = driftRows(data)
  const history = useDriftHistory(data.checkedAt, rows)
  const key = driftReviewKey(data)
  const selected = selection.key === key ? selection.ids : []
  const items = data.repairPlan.items.filter((item) => selected.includes(item.id))
  const readFailed = Boolean(read.error)
  const verdict = driftVerdict(data, rows, readFailed)
  const blocked = readFailed || !data.consistent || data.repairPlan.status === "blocked"
  const canRepair = can("system.admin") && can("destructive")
  const request = selectedDriftRepairRequest(data, selected)
  const nextAt = read.lastSuccess === undefined ? undefined : read.lastSuccess + POLL
  const inspecting =
    (asked !== undefined && asked.lastSuccess === read.lastSuccess && asked.error === read.error) ||
    (!readFailed && nextAt !== undefined && now >= nextAt)
  const checked = new Date(data.checkedAt).getTime()

  const inspect = () => {
    setAsked({ lastSuccess: read.lastSuccess, error: read.error })
    read.refresh()
  }
  const applySelected = async () => {
    if (busy || blocked || !canRepair || !request || actionError) return
    setBusy(true)
    let requestSent = false
    try {
      const fresh = await get<DriftReport>("/network/drift")
      if (driftReviewKey(fresh) !== key || !selectedDriftRepairRequest(fresh, selected)) {
        throw new Error("The reviewed evidence changed. Close this review and inspect again.")
      }
      requestSent = true
      const result = await post<NetworkChangeStatus>("/network/drift/repairs", request, {
        networkApply: "pending",
      })
      const outcome = driftRepairOutcome(result)
      notify[outcome.tone](outcome.title, { description: outcome.description })
      setReview(false)
      setSelection({ key: "", ids: [] })
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      setActionError(
        requestSent
          ? `${message} Inspect the current change status before retrying.`
          : `${message} No repair request was sent.`,
      )
    } finally {
      setBusy(false)
      read.refresh()
    }
  }

  return (
    <Page className="animate-rise">
      <PageContext title="Network drift" />
      <HostIdentity
        logo={
          <span className="flex size-12 shrink-0 items-center justify-center rounded-xl border border-rule-brand bg-wash-brand text-brand">
            <LogoGlyph className="h-6" />
          </span>
        }
        title="Managed network"
        facts={
          <>
            <span className="numeric">
              inspected{" "}
              <time dateTime={data.checkedAt} title={new Date(data.checkedAt).toLocaleString()}>
                {now - checked < 5_000 ? "just now" : `${duration((now - checked) / 1000)} ago`}
              </time>
            </span>
            <FactDot />
            <span className="numeric">{plural(rows.length, "comparison")}</span>
            <FactDot />
            <span className="inline-flex items-center gap-1.5">
              spec <ShortDigest value={data.savedGeneration} />
            </span>
            {data.change && (
              <>
                <FactDot />
                <span>journal {data.change.phase.replaceAll("_", " ")}</span>
              </>
            )}
            {!data.consistent && (
              <>
                <FactDot />
                <span className="text-warning">changed during inspection</span>
              </>
            )}
          </>
        }
        aside={
          <div className="flex flex-wrap items-center gap-3">
            <Verdict
              {...verdict}
              inspecting={inspecting}
              pressed={verdict.narrows !== undefined && scope === verdict.narrows}
              onPress={
                verdict.narrows
                  ? () => {
                      setDomain(undefined)
                      setScope(scope === "differ" ? "all" : "differ")
                    }
                  : undefined
              }
            />
            <Button size="sm" variant="outline" disabled={busy} onClick={inspect}>
              <RefreshClockwise className={cn("size-3.5", inspecting && "animate-spin")} />
              Inspect again
            </Button>
          </div>
        }
      />
      <NetworkReadWarning
        error={read.error}
        refresh={read.refresh}
        lastSuccess={read.lastSuccess}
        reading="drift inspection"
      />
      {!data.consistent && (
        <Notice tone="warning" title="Inspect again before reviewing repairs">
          The saved configuration or recovery journal changed during this reading.
        </Notice>
      )}

      <div className="grid min-w-0 gap-x-10 gap-y-8 xl:grid-cols-[minmax(0,1.7fr)_minmax(0,1fr)]">
        <DriftPicture
          report={data}
          rows={rows}
          inspecting={inspecting}
          selected={domain}
          onDomain={(next) => {
            setScope("all")
            setDomain(domain === next ? undefined : next)
          }}
        />
        <div className="min-w-0 space-y-8">
          <DriftRhythm history={history} nextAt={nextAt} inspecting={inspecting} />
          <DriftIdentities report={data} />
        </div>
      </div>

      <DriftTable
        rows={rows}
        scope={scope}
        onScope={setScope}
        domain={domain}
        onDomain={setDomain}
        query={query}
        onQuery={setQuery}
      />

      <div
        className={cn(
          "grid min-w-0 gap-x-10 gap-y-8",
          data.blocklists.length > 0 && "xl:grid-cols-2",
        )}
      >
        <DriftBoot boot={data.boot} />
        {data.blocklists.length > 0 && <DriftBlocklists lists={data.blocklists} />}
      </div>

      <DriftRepairs
        plan={data.repairPlan}
        rows={rows}
        selected={selected}
        blocked={blocked}
        busy={busy}
        onToggle={(id, checked) =>
          setSelection({
            key,
            ids: checked ? [...selected, id] : selected.filter((other) => other !== id),
          })
        }
        onReview={() => {
          setActionError(undefined)
          setReview(true)
        }}
      />
      <DriftReview
        open={review}
        onOpenChange={(open) => {
          if (!busy) setReview(open)
        }}
        items={items}
        plan={data.repairPlan}
        canApply={canRepair && Boolean(request)}
        canRepair={canRepair}
        blocked={blocked}
        busy={busy}
        actionError={actionError}
        onClose={() => setReview(false)}
        onApply={applySelected}
      />
    </Page>
  )
}

/**
 * The verdict at the line's end: the worst thing true of the managed
 * network, counted. A press narrows the comparisons to the differences it
 * counts. While an inspection is in flight its words shimmer, since what it
 * says is about to be re-read.
 */
function Verdict({
  tone,
  label,
  inspecting,
  pressed,
  onPress,
}: {
  tone: "running" | "warning" | "danger" | "unknown"
  label: string
  inspecting: boolean
  pressed: boolean
  onPress?: () => void
}) {
  const words = (
    <span
      className={cn(
        "text-body font-medium",
        tone === "warning" && "text-warning",
        tone === "danger" && "text-destructive",
        tone === "running" && "text-success",
        tone === "unknown" && "text-muted-foreground",
      )}
    >
      {inspecting ? <TextShimmer>{label}</TextShimmer> : label}
    </span>
  )
  const dot = <StatusDot tone={tone} live={inspecting} />
  if (!onPress)
    return (
      <span className="inline-flex items-center gap-2" role="status">
        {dot}
        {words}
      </span>
    )
  return (
    <button
      type="button"
      aria-pressed={pressed}
      title="Show only the differences"
      onClick={onPress}
      className={cn(
        "inline-flex h-8 items-center gap-2 rounded-md px-2 focus-ring transition-colors hover:bg-row-hover",
        pressed && "bg-accent",
      )}
    >
      {dot}
      {words}
    </button>
  )
}
