"use client"

import { useState } from "react"
import { get, post } from "@/lib/api"
import { useAuth } from "@/hooks/use-auth"
import { notify } from "@/lib/toast"
import type { NetworkChangeStatus } from "@/lib/types"
import {
  driftCounts,
  driftReading,
  driftRepairOutcome,
  driftReportObservations,
  driftReviewKey,
  selectedDriftRepairRequest,
  type DriftObservation,
  type DriftReport,
} from "@/lib/network-drift"
import { plural } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { ErrorState, LoadingPanel, EmptyNote, Notice } from "@/components/state"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { Disclosure } from "@/components/form"
import { Row, RowList } from "@/components/row-list"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"

/** Reading register: compare measured configuration with what the dashboard owns. */
export default function NetworkDriftPage() {
  const { can } = useAuth()
  const read = usePoll<DriftReport>((signal) => get("/network/drift", undefined, signal), 30_000)
  const [selection, setSelection] = useState<{ key: string; ids: string[] }>({ key: "", ids: [] })
  const [review, setReview] = useState(false)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState<string>()
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
  const data = read.data
  const key = driftReviewKey(data)
  const selected = selection.key === key ? selection.ids : []
  const items = data.repairPlan.items.filter((item) => selected.includes(item.id))
  const counts = driftCounts(driftReportObservations(data))
  const reading = driftReading(data.status)
  const blocked = Boolean(read.error) || !data.consistent || data.repairPlan.status === "blocked"
  const activation = data.boot.execution
  const canRepair = can("system.admin") && can("destructive")
  const request = selectedDriftRepairRequest(data, selected)
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
      <Panel plain>
        <PanelHeader
          title="Managed network evidence"
          actions={
            <div className="flex flex-wrap items-center gap-3">
              <Status
                tone={read.error ? "warning" : reading.tone}
                label={read.error ? "Last known evidence" : reading.label}
              />
              <Button size="sm" variant="outline" disabled={busy} onClick={read.refresh}>
                Inspect again
              </Button>
            </div>
          }
        />
        <PanelBody>
          <p className="text-body text-muted-foreground">
            Observed{" "}
            <time dateTime={data.checkedAt}>{new Date(data.checkedAt).toLocaleString()}</time>
            {" · "}
            {data.consistent
              ? "Configuration unchanged during inspection"
              : "Configuration changed during inspection"}
          </p>
        </PanelBody>
      </Panel>
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
      <StatGrid columns={4}>
        <StatTile
          label="Known differences"
          value={counts.differences}
          tone={counts.differences ? "warning" : "default"}
        />
        <StatTile label="Incomplete readings" value={counts.unknown} />
        <StatTile label="Matching observations" value={counts.matching} />
        <StatTile label="Owned repair proposals" value={data.repairPlan.items.length} />
      </StatGrid>
      <Observations
        title="Saved configuration and journal"
        observations={[data.spec, data.journal]}
      />
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
      <Observations title="Owned rendered files" observations={data.files} />
      <Observations title="Managed kernel objects" observations={data.runtime} />
      {data.blocklists.length > 0 && (
        <Panel plain>
          <PanelHeader title="Blocklist generations" />
          <PanelBody>
            <RowList>
              {data.blocklists.map((list) => (
                <li key={list.id} className="space-y-2 py-3">
                  <div className="flex items-center justify-between gap-4">
                    <span className="text-body font-medium">{list.name}</span>
                    <Status
                      tone={
                        list.enforcement === "verified"
                          ? "running"
                          : list.enforcement === "degraded"
                            ? "warning"
                            : "unknown"
                      }
                      label={list.enforcement}
                    />
                  </div>
                  <p className="text-body text-muted-foreground">
                    Cache: {list.cache.status} · Runtime set: {list.runtime.status}
                  </p>
                  <Disclosure quiet summary="Set evidence">
                    <div className="space-y-2 text-body">
                      <Digest label="Cached contents" value={list.cache.generation} />
                      <Digest label="Rendered contents" value={list.renderedGeneration} />
                      <Digest label="Kernel contents" value={list.runtime.generation} />
                      {list.cache.error && <p>{list.cache.error}</p>}
                      {list.runtime.error && <p>{list.runtime.error}</p>}
                    </div>
                  </Disclosure>
                </li>
              ))}
            </RowList>
          </PanelBody>
        </Panel>
      )}
      <Panel plain>
        <PanelHeader
          title="Boot unit and measured activation"
          actions={<Status {...driftReading(data.boot.status)} />}
        />
        <PanelBody>
          <div className="space-y-3 text-body">
            <p className="font-mono break-words">{data.boot.unit}</p>
            <p className="text-muted-foreground">
              Loaded: {data.boot.loadState || "unknown"}
              {" · "}Persistent enablement: {data.boot.unitFileState || "unknown"}
              {" · "}Needs reload: {data.boot.needDaemonReload || "unknown"}
            </p>
            {data.boot.reason && <p>{data.boot.reason}</p>}
            <Status
              tone={
                activation.status === "succeeded"
                  ? "running"
                  : activation.status === "failed"
                    ? "warning"
                    : "unknown"
              }
              label={
                activation.status === "unrecorded"
                  ? "No recorded activation"
                  : `Last activation: ${activation.status}`
              }
            />
            {activation.startedAt && (
              <p>
                Measured start: <span className="font-mono">{activation.startedAt}</span>
              </p>
            )}
            {activation.finishedAt && (
              <p>
                Measured finish: <span className="font-mono">{activation.finishedAt}</span>
              </p>
            )}
            {activation.reason && <p className="text-muted-foreground">{activation.reason}</p>}
            <Notice title="Reboot attribution is unknown">
              A measured unit activation does not establish that it ran at boot or that reboot
              restoration succeeded.
            </Notice>
            <Disclosure quiet summary="Execution evidence">
              <div className="space-y-2 text-body">
                <Digest label="Current boot" value={activation.bootId} />
                <Digest label="Invocation" value={activation.invocationId} />
                {activation.commands.map((command, index) => (
                  <p key={index} className="break-words">
                    <span className="font-mono">{command.path}</span>
                    {" · "}
                    {command.exitStatus === undefined
                      ? "Exit outcome unknown"
                      : `Exit ${command.exitStatus}`}
                    {command.ignoreErrors && " · Unit ignores this command's failure"}
                  </p>
                ))}
                {!activation.commands.length && (
                  <p className="text-muted-foreground">
                    No per-command execution outcomes are available.
                  </p>
                )}
              </div>
            </Disclosure>
          </div>
        </PanelBody>
      </Panel>
      <Panel plain>
        <PanelHeader
          title="Owned repair plan"
          actions={
            <Button
              size="sm"
              variant="outline"
              disabled={busy || blocked || !items.length}
              onClick={() => {
                setActionError(undefined)
                setReview(true)
              }}
            >
              Review selected ({items.length})
            </Button>
          }
        />
        <PanelBody>
          <div className="space-y-3">
            <Notice
              title={data.repairPlan.executable ? "Selected repairs available" : "Review only"}
            >
              {data.repairPlan.executable
                ? "Reviewed boot files and owned admission rules can be repaired individually. Other proposals remain available for review."
                : "This plan cannot execute repairs. It proposes owned resources for review and preserves foreign resources."}
            </Notice>
            {data.repairPlan.blockers.map((reason) => (
              <p key={reason} className="text-body text-warning">
                {reason}
              </p>
            ))}
            {data.repairPlan.items.length ? (
              <RowList>
                {data.repairPlan.items.map((item) => (
                  <Row
                    key={item.id}
                    title={
                      <label htmlFor={`repair-${item.id}`} className="cursor-pointer break-all">
                        {item.resource}
                      </label>
                    }
                    subtitle={
                      <>
                        {item.reason}
                        {item.blocker && <span className="block">Review only: {item.blocker}</span>}
                      </>
                    }
                    leading={
                      <Checkbox
                        id={`repair-${item.id}`}
                        aria-label={`Select repair for ${item.resource}`}
                        disabled={blocked || busy}
                        checked={selected.includes(item.id)}
                        onCheckedChange={(checked) =>
                          setSelection({
                            key,
                            ids: checked
                              ? [...selected, item.id]
                              : selected.filter((id) => id !== item.id),
                          })
                        }
                      />
                    }
                  />
                ))}
              </RowList>
            ) : (
              <EmptyNote>No known owned repair can be proposed from this evidence.</EmptyNote>
            )}
            {data.repairPlan.excluded.length > 0 && (
              <Disclosure
                quiet
                summary={`${plural(data.repairPlan.excluded.length, "excluded resource")}`}
              >
                <RowList>
                  {data.repairPlan.excluded.map((item) => (
                    <Row
                      key={item.observationId}
                      title={item.observationId}
                      subtitle={item.reason}
                    />
                  ))}
                </RowList>
              </Disclosure>
            )}
          </div>
        </PanelBody>
      </Panel>
      <Modal
        open={review}
        onOpenChange={(open) => {
          if (!busy) setReview(open)
        }}
        title="Review selected owned repairs"
        description="Inspect the selected exact changes and their preconditions before applying."
        size="lg"
        footer={
          <>
            <Button variant="outline" disabled={busy} onClick={() => setReview(false)}>
              Close review
            </Button>
            {canRepair && request && (
              <Button
                variant="destructive"
                disabled={blocked || Boolean(actionError) || busy}
                pending={busy}
                onClick={applySelected}
              >
                Apply selected repairs
              </Button>
            )}
          </>
        }
      >
        <div className="space-y-4">
          {!request || !canRepair ? (
            <Notice title="No changes will be applied">
              {canRepair
                ? "The selections include resources available for review only."
                : "An administrator with destructive permission can apply executable selected repairs."}
            </Notice>
          ) : (
            <Notice title="Apply only these reviewed resources">
              File selections repair saved boot inputs. Admission selections repair owned rules in
              the current kernel. The server rereads ownership and evidence before applying.
            </Notice>
          )}
          {actionError && (
            <Notice tone="warning" title="Repair needs review">
              {actionError}
            </Notice>
          )}
          {blocked || !items.length ? (
            <Notice tone="warning" title="The reviewed evidence changed">
              Close this review and inspect the network again before selecting repairs.
            </Notice>
          ) : (
            <div className="space-y-6">
              {items.map((item) => (
                <section key={item.id} className="space-y-3">
                  <h3 className="font-mono text-body font-medium break-all">{item.resource}</h3>
                  <p className="text-body text-muted-foreground">{item.reason}</p>
                  {item.blocker && (
                    <p className="text-body text-warning">Review only: {item.blocker}</p>
                  )}
                  {item.effect && (
                    <p className="text-body">
                      {item.effect === "boot_files"
                        ? "Repairs this saved boot input. A future restore will read it."
                        : "Repairs this owned admission rule in the current kernel."}
                    </p>
                  )}
                  {item.after && (
                    <>
                      <p className="text-hint text-muted-foreground">Before</p>
                      <pre className="font-mono text-hint break-all whitespace-pre-wrap">
                        {item.before || "File absent"}
                      </pre>
                      <p className="text-hint text-muted-foreground">After</p>
                      <pre className="font-mono text-hint break-all whitespace-pre-wrap">
                        {item.after}
                      </pre>
                    </>
                  )}
                  <ul className="list-disc space-y-1 pl-5 text-body text-muted-foreground">
                    {item.preconditions.map((text) => (
                      <li key={text}>{text}</li>
                    ))}
                  </ul>
                </section>
              ))}
            </div>
          )}
          <Digest label="Reviewed generation" value={data.repairPlan.generation} />
          <ul className="list-disc space-y-1 pl-5 text-body text-muted-foreground">
            {data.repairPlan.preconditions.map((text) => (
              <li key={text}>{text}</li>
            ))}
          </ul>
        </div>
      </Modal>
    </Page>
  )
}

function Digest({ label, value }: { label: string; value?: string }) {
  return (
    <p className="min-w-0 break-words">
      <span className="text-muted-foreground">{label}: </span>
      <span className="font-mono">{value || "Unknown"}</span>
    </p>
  )
}

function Observations({
  title,
  observations,
}: {
  title: string
  observations: DriftObservation[]
}) {
  return (
    <Panel plain>
      <PanelHeader
        title={title}
        actions={
          <span className="text-hint text-muted-foreground">
            {plural(observations.length, "observation")}
          </span>
        }
      />
      <PanelBody>
        {observations.length ? (
          <RowList>
            {observations.map((item) => (
              <li key={item.id} className="py-3">
                <div className="flex min-w-0 flex-wrap items-start justify-between gap-2">
                  <p className="min-w-0 font-mono text-body break-all">{item.resource}</p>
                  <Status {...driftReading(item.status)} />
                </div>
                {item.reason && (
                  <p className="mt-1 text-body text-muted-foreground">{item.reason}</p>
                )}
                <Disclosure quiet summary="Comparison evidence">
                  <div className="space-y-2 text-body">
                    <p>
                      Coverage: {item.coverage.replaceAll("-", " ")}
                      {" · "}
                      {item.owned ? "Saved managed resource" : "Ownership cannot be established"}
                    </p>
                    {Object.entries(item.expected ?? {}).map(([name, value]) => (
                      <Digest key={name} label={`Expected ${name}`} value={value} />
                    ))}
                    {Object.entries(item.observed ?? {}).map(([name, value]) => (
                      <Digest key={name} label={`Observed ${name}`} value={value} />
                    ))}
                  </div>
                </Disclosure>
              </li>
            ))}
          </RowList>
        ) : (
          <EmptyNote>No managed objects are recorded in this domain.</EmptyNote>
        )}
      </PanelBody>
    </Panel>
  )
}
