"use client"

import { useState } from "react"
import { get } from "@/lib/api"
import {
  driftCounts,
  driftReading,
  driftReportObservations,
  driftReviewKey,
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
  const read = usePoll<DriftReport>((signal) => get("/network/drift", undefined, signal), 30_000)
  const [selection, setSelection] = useState<{ key: string; ids: string[] }>({ key: "", ids: [] })
  const [review, setReview] = useState(false)
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
              <Button size="sm" variant="outline" onClick={read.refresh}>
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
              disabled={blocked || !items.length}
              onClick={() => setReview(true)}
            >
              Review selected ({items.length})
            </Button>
          }
        />
        <PanelBody>
          <div className="space-y-3">
            <Notice title="Review only">
              This plan cannot execute repairs. It proposes owned resources for review and preserves
              foreign resources.
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
                    subtitle={item.reason}
                    leading={
                      <Checkbox
                        id={`repair-${item.id}`}
                        aria-label={`Select repair for ${item.resource}`}
                        disabled={blocked}
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
        onOpenChange={setReview}
        title="Review selected owned repairs"
        description="Inspect the proposed changes and preconditions. This plan cannot apply changes."
        size="lg"
        footer={
          <Button variant="outline" onClick={() => setReview(false)}>
            Close review
          </Button>
        }
      >
        <div className="space-y-4">
          <Notice title="No changes will be applied">
            Repair execution is unavailable for this plan.
          </Notice>
          {blocked || !items.length ? (
            <Notice tone="warning" title="The reviewed evidence changed">
              Close this review and inspect the network again before selecting repairs.
            </Notice>
          ) : (
            <RowList>
              {items.map((item) => (
                <Row key={item.id} title={item.resource} subtitle={item.reason} />
              ))}
            </RowList>
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
