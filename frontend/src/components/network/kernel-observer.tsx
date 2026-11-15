"use client"

import {
  flowBytes,
  flowCounter,
  observerReading,
  type FlowSettings,
  type KernelObserverEvidence,
  type ObserverQuality,
} from "@/lib/network-flows"
import { Panel, PanelHeader, PanelBody } from "@/components/panel"
import { Disclosure, InfoTip } from "@/components/form"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { UTCTime } from "@/components/network/flows/marks"

const qualityReadings: [keyof Omit<ObserverQuality, "shutdownTailUnknown">, string][] = [
  ["events", "Delivered events"],
  ["ringDrops", "Ring delivery drops"],
  ["budgetOmissions", "Kernel budget omissions"],
  ["headerGaps", "Unreadable packet headers"],
  ["identityGaps", "Missing socket identities"],
  ["stateAdmissionGaps", "Socket state admission gaps"],
  ["parserGaps", "Packet parser gaps"],
  ["byteGaps", "Unknown payload lengths"],
  ["pendingOmissions", "Pending row cap omissions"],
  ["attributionGaps", "Unknown owner events"],
  ["readerBudgetPauses", "Reader budget pauses"],
  ["unsavedEvents", "Unsaved events"],
  ["timestampGaps", "Uncertain UTC timestamps"],
]

export function ObserverQualityReading({ quality }: { quality?: ObserverQuality }) {
  if (!quality) return <p>Observer quality was not retained for this reading.</p>
  return (
    <div className="space-y-2 text-body">
      <dl className="grid gap-x-8 gap-y-2 sm:grid-cols-2">
        {qualityReadings.map(([key, label]) => (
          <div key={key} className="flex justify-between gap-4 border-b border-hairline pb-2">
            <dt className="min-w-0">{label}</dt>
            <dd className="shrink-0 tabular-nums">
              {flowCounter(quality[key])?.toString() ?? "Unknown"}
            </dd>
          </div>
        ))}
      </dl>
      <p className="text-muted-foreground">
        These counters describe observer coverage, not end-to-end packet loss. A zero counter does
        not prove complete traffic coverage.
      </p>
      {quality.shutdownTailUnknown && <p>The final events at shutdown remain unknown.</p>}
    </div>
  )
}

/**
 * The kernel packet observer: an opt-in second channel beside the sampled
 * socket history, switched on and off only through a reviewed change. Its
 * state and the reason for it lead; what it was bounded to and what it
 * missed fold under them.
 */
export function KernelObserverPanel({
  observer,
  settings,
  disabled,
  pending,
  stale,
  canControl,
  onReview,
}: {
  observer: KernelObserverEvidence
  settings: FlowSettings
  disabled: boolean
  pending: boolean
  stale: boolean
  canControl: boolean
  onReview: (enabled: boolean) => void
}) {
  const reading = observerReading(observer.status)
  const retained = Boolean(settings.kernelObserverEnabled || observer.attachmentsRetained)
  const supported = settings.kernelObserverEnabled !== undefined
  return (
    <Panel plain id="flow-observer" aria-label="Kernel packet observer" className="scroll-mt-6">
      <PanelHeader
        title="Kernel packet observer"
        actions={
          <div className="flex flex-wrap items-center gap-3">
            <InfoTip label="What the observer does">
              Explicit opt-in observes TCP and UDP transport payload at the declared cgroup hooks.
              Observed byte subtotals remain separate from sampled TCP counters. Opening this page,
              enabling ordinary history and restarting the dashboard do not attach the observer.
            </InfoTip>
            <Status
              tone={stale ? "warning" : reading.tone}
              label={stale ? "Last known observer state" : reading.label}
            />
            {canControl && supported && (
              <Button
                size="sm"
                variant="outline"
                pending={pending}
                disabled={disabled || (!retained && !settings.enabled)}
                onClick={() => onReview(!retained)}
              >
                {retained ? "Review observer stop" : "Review observer activation"}
              </Button>
            )}
          </div>
        }
      />
      <PanelBody>
        <div className="space-y-3 text-body">
          <p>{observer.reason}</p>
          {supported && !retained && !settings.enabled && (
            <p className="text-muted-foreground">
              Start history recording before reviewing observer activation.
            </p>
          )}
          {settings.enabled && retained && (
            <p className="text-muted-foreground">
              Stop the kernel observer before stopping ordinary history recording.
            </p>
          )}
          {observer.attachmentsRetained && observer.status !== "recording" && (
            <p role="status">
              Owned attachments remain. Review stop to retry draining and detaching.
            </p>
          )}
          <p className="flex flex-wrap gap-x-3 gap-y-1 text-hint text-muted-foreground">
            <span>
              Checked <UTCTime value={observer.checkedAt} form="short" />
            </span>
            <span>
              Started <UTCTime value={observer.startedAt} form="short" />
            </span>
            <span>
              Stopped <UTCTime value={observer.stoppedAt} form="short" />
            </span>
            <span>Docker owner evidence {observer.dockerStatus || "unknown"}</span>
            <span>
              Read <UTCTime value={observer.dockerCheckedAt} form="short" />
            </span>
            <span>Sources {observer.dockerSources ?? "unknown"}</span>
            <span>Omitted sources {observer.omittedDockerSources ?? "unknown"}</span>
          </p>
          {observer.dockerError && <p>{observer.dockerError}</p>}
          {observer.timestampReason && <p>{observer.timestampReason}</p>}
          <Disclosure quiet summary="Observer bounds and retained quality">
            <div className="space-y-3">
              <p>
                Ring{" "}
                {observer.ringBytes === undefined
                  ? "unknown"
                  : flowBytes(BigInt(observer.ringBytes))}{" "}
                · Event budget {observer.eventsPerSecond ?? "unknown"}/s · Socket state cap{" "}
                {observer.socketCapacity ?? "unknown"} · Pending row cap{" "}
                {observer.pendingCapacity ?? "unknown"} · History retention {settings.retentionDays}{" "}
                days
              </p>
              <ObserverQualityReading quality={observer.quality} />
              <dl className="space-y-2 break-all text-muted-foreground">
                <div>
                  <dt>Target cgroup</dt>
                  <dd>{observer.targetCgroup || "Unknown"}</dd>
                </div>
                <div>
                  <dt>Kernel and boot</dt>
                  <dd>
                    {observer.kernelRelease || "Unknown"} · {observer.bootId || "Unknown"}
                  </dd>
                </div>
                <div>
                  <dt>Packaged program digest</dt>
                  <dd>{observer.digest || "Unknown"}</dd>
                </div>
                <div>
                  <dt>Recorded program IDs</dt>
                  <dd>{observer.programIds?.join(", ") || "None recorded"}</dd>
                </div>
                <div>
                  <dt>Recorded link IDs</dt>
                  <dd>{observer.linkIds?.join(", ") || "None recorded"}</dd>
                </div>
              </dl>
            </div>
          </Disclosure>
        </div>
      </PanelBody>
    </Panel>
  )
}
