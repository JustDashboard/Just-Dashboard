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
  driftSummary,
  driftVerdict,
  selectedDriftRepairRequest,
  type DriftDomain,
  type DriftReport,
} from "@/lib/network-drift"
import { plural, relativeTime } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { useNow } from "@/components/deploy/vocabulary"
import { NetworkDevice } from "@/components/icons"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { ErrorState, LoadingPanel, Notice } from "@/components/state"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { Button } from "@/components/ui/button"
import { DriftBlocklists } from "@/components/network/drift/drift-blocklists"
import { DriftBoot } from "@/components/network/drift/drift-boot"
import { DriftPicture } from "@/components/network/drift/drift-picture"
import { DriftRecent } from "@/components/network/drift/drift-recent"
import { DriftRepairs } from "@/components/network/drift/drift-repairs"
import { DriftReview } from "@/components/network/drift/drift-review"
import { DriftTable } from "@/components/network/drift/drift-table"
import { Digest } from "@/components/network/drift/digest"
import { useDriftMoves } from "@/components/network/drift/use-drift-moves"

/** How often the host is inspected; the countdown in the identity line counts down to it. */
const POLL = 30_000

/**
 * Whether the host still matches what this dashboard saved, drawn as where the
 * saved configuration goes — its rendered files, the kernel objects, the boot
 * unit and the blocklist sets — with each line the reading of that place.
 *
 * It opened on four grey figures over five lists of rows, and nothing on it
 * moved. The figures are the table's chips now (differ, incomplete, matching),
 * which count and narrow where a tile could only count; the verdict is the end
 * of the identity line; and what the tiles never said — which place drifted
 * and what changed since the page opened — is the picture and the list beside
 * it. The identity line ticks: how long ago the host was inspected and how
 * long until it is again. Pressing Inspect again sends a pulse down every line
 * until the answer arrives.
 *
 * Reading register: the evidence is the page. Repairs stay under it, as a plan
 * to select from and a review dialog that shows the exact change.
 */
export default function NetworkDriftPage() {
  const { can } = useAuth()
  const read = usePoll<DriftReport>((signal) => get("/network/drift", undefined, signal), POLL)
  const now = useNow()
  const [selection, setSelection] = useState<{ key: string; ids: string[] }>({ key: "", ids: [] })
  const [review, setReview] = useState(false)
  const [busy, setBusy] = useState(false)
  const [asked, setAsked] = useState(0)
  const [domain, setDomain] = useState("")
  const [actionError, setActionError] = useState<string>()
  const data = read.data
  const rows = data ? driftRows(data) : []
  const moves = useDriftMoves(rows, data?.finishedAt ?? "")
  if (!data)
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
  const key = driftReviewKey(data)
  const selected = selection.key === key ? selection.ids : []
  const items = data.repairPlan.items.filter((item) => selected.includes(item.id))
  const verdict = driftVerdict(rows, data.consistent, Boolean(read.error))
  const blocked = Boolean(read.error) || !data.consistent || data.repairPlan.status === "blocked"
  const canRepair = can("system.admin") && can("destructive")
  const request = selectedDriftRepairRequest(data, selected)
  const inspecting = !read.error && asked > (read.lastSuccess ?? 0)
  const next = read.lastSuccess ? Math.max(0, Math.ceil((read.lastSuccess + POLL - now) / 1000)) : 0
  const inspect = () => {
    setAsked(Date.now())
    read.refresh()
  }
  const pick = (picked: DriftDomain) => {
    setDomain(domain === picked ? "" : picked)
    document.getElementById("comparisons")?.scrollIntoView({ block: "nearest" })
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
      <HostIdentity
        fallback={NetworkDevice}
        title="Managed network"
        facts={
          <>
            <span>
              Inspected{" "}
              <time dateTime={data.checkedAt} title={new Date(data.checkedAt).toLocaleString()}>
                {relativeTime(data.checkedAt)}
              </time>
            </span>
            <FactDot />
            <span className="numeric">{plural(rows.length, "comparison")}</span>
            {data.change && (
              <>
                <FactDot />
                <span>journal {data.change.phase.replaceAll("_", " ")}</span>
              </>
            )}
            {!read.error && read.lastSuccess && (
              <>
                <FactDot />
                <span className="numeric">
                  {inspecting ? "inspecting now" : `next inspection in ${next}s`}
                </span>
              </>
            )}
          </>
        }
        aside={
          <div className="flex flex-wrap items-center gap-3">
            <Status tone={verdict.tone} label={verdict.label} />
            <Button size="sm" variant="outline" disabled={busy} onClick={inspect}>
              Inspect again
            </Button>
          </div>
        }
      />
      <div className="grid min-w-0 gap-x-10 xl:grid-cols-[minmax(0,1fr)_20rem]">
        <DriftPicture
          config={driftSummary(rows, "config")}
          domains={(["files", "kernel", "boot", "blocklists"] as const).map((k) =>
            driftSummary(rows, k),
          )}
          inspecting={inspecting}
          picked={domain}
          onPick={pick}
        />
        <DriftRecent moves={moves.moves} inspections={moves.inspections} />
      </div>
      <DriftTable rows={rows} domain={domain} onDomain={setDomain} />
      <DriftBoot boot={data.boot} />
      {data.blocklists.length > 0 && <DriftBlocklists lists={data.blocklists} />}
      <Panel plain>
        <PanelHeader title="Configuration identities" />
        <PanelBody>
          <div className="space-y-2 text-body">
            <Digest label="Saved file" value={data.savedGeneration} />
            <Digest label="Normalized configuration" value={data.canonicalGeneration} />
            <Digest label="Journal candidate" value={data.change?.generation} />
            {data.change && (
              <p className="text-muted-foreground">
                Journal phase: {data.change.phase.replaceAll("_", " ")}. A recovered candidate can
                differ from the restored saved configuration.
              </p>
            )}
          </div>
        </PanelBody>
      </Panel>
      <DriftRepairs
        plan={data.repairPlan}
        selected={selected}
        disabled={blocked || busy}
        reviewDisabled={busy || blocked || !items.length}
        onSelect={(ids) => setSelection({ key, ids })}
        onReview={() => {
          setActionError(undefined)
          setReview(true)
        }}
      />
      <DriftReview
        open={review}
        busy={busy}
        blocked={blocked}
        canRepair={canRepair}
        executable={Boolean(request)}
        items={items}
        plan={data.repairPlan}
        actionError={actionError}
        onClose={() => setReview(false)}
        onApply={applySelected}
      />
    </Page>
  )
}
