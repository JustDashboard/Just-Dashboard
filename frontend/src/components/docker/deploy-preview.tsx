"use client"

import { useState } from "react"
import {
  CheckCircle,
  ChevronDown,
  Clock,
  Copy,
  Information,
  Warning,
} from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { copyText } from "@/lib/clipboard"
import type { DeployPreview, DiffLine, ServiceChange, StackDeployment } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"

/**
 * What pressing Deploy is going to do, before it does it.
 *
 * A compose deploy is the most consequential button in this product and the
 * least predictable: `up` recreates whatever it decides has changed, and what
 * it decides is invisible until afterwards. An operator pressing it on a
 * Friday evening cannot tell whether one container restarts or four, whether
 * the database volume is about to be removed, or whether the image about to be
 * pulled is a different image at all.
 *
 * The three things it is careful about: it never claims a volume is removed
 * when it is not, it says once that compose makes the final call rather than
 * repeating it on every row, and when there is no previous deployment to
 * compare against it says so instead of reporting "nothing changed".
 */

/** `text` colours the row's icon; `tag` is the mark at the end of the row. */
const CHANGE = {
  recreate: { label: "Will recreate", text: "text-warning", tag: "warning" },
  create: { label: "Will create", text: "text-foreground", tag: "default" },
  start: { label: "Will start", text: "text-foreground", tag: "default" },
  remove: { label: "Will remove", text: "text-destructive", tag: "danger" },
  unchanged: { label: "Unchanged", text: "text-muted-foreground", tag: "default" },
} as const

export function DeployPreviewPanel({ stack }: { stack: string }) {
  const { data, error, loading } = usePoll<DeployPreview>(
    (signal) =>
      get<DeployPreview>(`/docker/stacks/${encodeURIComponent(stack)}/preview`, undefined, signal),
    0,
    [stack],
  )

  if (loading && !data) return <LoadingRows />
  if (error) return <ErrorState error={error} />
  if (!data) return null

  // Services that change get a row each and their reason; the unchanged rest
  // is one line naming them, because four copies of "Its configuration is
  // identical" is the same sentence four times. This is the rule the attention
  // panel follows when one finding repeats across several containers.
  const changed = data.services.filter((s) => s.change !== "unchanged")
  const unchanged = data.services.filter((s) => s.change === "unchanged")

  return (
    // This content mounts when its tab opens — a disclosure — so its arrival
    // is said once, quietly, by the token that means "not here a moment ago".
    <div className="animate-rise space-y-5">
      <div className="rounded-lg border border-hairline bg-surface-header/40 px-3.5 py-3">
        <p className="text-body leading-snug font-semibold">{data.summary}</p>
        {data.diffAgainst && (
          <p className="mt-1 text-hint leading-relaxed text-muted-foreground">
            Compared against {data.diffAgainst}.
          </p>
        )}
      </div>

      <section className="space-y-2">
        <p className="eyebrow">Services</p>
        <div className="overflow-hidden rounded-lg border border-hairline">
          <ul className="divide-y divide-hairline">
            {changed.map((service) => (
              <ServiceChangeRow key={service.name} change={service} />
            ))}
            {unchanged.length > 0 && <UnchangedRow services={unchanged} />}
          </ul>
        </div>
      </section>

      {/* The number that means data is destroyed, stated whether or not it is zero. */}
      <section className="space-y-2">
        <p className="eyebrow">Volumes</p>
        {data.volumesRemoved.length > 0 ? (
          <p className="flex items-start gap-2 text-body leading-relaxed text-destructive">
            <Warning className="mt-0.5 size-3.5 shrink-0" />
            <span>
              {data.volumesRemoved.join(", ")} will be removed. Everything stored in{" "}
              {data.volumesRemoved.length === 1 ? "it" : "them"} will be destroyed permanently.
            </span>
          </p>
        ) : (
          <p className="flex items-start gap-2 text-body leading-relaxed text-muted-foreground">
            <CheckCircle className="mt-0.5 size-3.5 shrink-0 text-success" />
            <span>
              No volumes will be removed
              {data.volumesKept.length > 0 && ` — ${data.volumesKept.join(", ")} will survive`}.
            </span>
          </p>
        )}
      </section>

      {data.diff.length > 0 && (
        <section className="space-y-2">
          <p className="eyebrow">Compose file changes</p>
          <DiffView lines={data.diff} />
        </section>
      )}

      {data.caveats.length > 0 && (
        <section className="space-y-2">
          <p className="eyebrow">What this cannot know</p>
          {data.caveats.map((caveat, i) => (
            <p key={i} className="flex items-start gap-2 text-hint leading-relaxed text-muted-foreground">
              <Information className="mt-0.5 size-3 shrink-0" />
              <span>{caveat}</span>
            </p>
          ))}
        </section>
      )}
    </div>
  )
}

function ServiceChangeRow({ change }: { change: ServiceChange }) {
  const meta = CHANGE[change.change] ?? CHANGE.unchanged
  const imageChanged = Boolean(change.imageBefore && change.imageAfter)
  return (
    <li className="space-y-1 px-3.5 py-3">
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <span className="min-w-0 truncate text-body font-semibold">{change.name}</span>
        <Tag tone={meta.tag}>{meta.label}</Tag>
      </div>
      <p className="text-body leading-relaxed text-muted-foreground">{change.reason}</p>
      {imageChanged && (
        <p className="font-mono text-hint leading-relaxed">
          <span className="text-destructive">− {change.imageBefore}</span>
          <br />
          <span className="text-success">+ {change.imageAfter}</span>
        </p>
      )}
    </li>
  )
}

/** The unchanged tail, named once rather than explained once per service. */
function UnchangedRow({ services }: { services: ServiceChange[] }) {
  return (
    <li className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5 px-3.5 py-2.5">
      <span className="text-body leading-snug font-medium">
        {services.length === 1
          ? "1 service will remain unchanged"
          : `${services.length} services will remain unchanged`}
      </span>
      <span className="min-w-0 text-hint leading-relaxed text-muted-foreground">
        {services.map((s) => s.name).join(", ")}
      </span>
    </li>
  )
}

function DiffView({ lines }: { lines: DiffLine[] }) {
  return (
    <div className="overflow-x-auto rounded-md border border-hairline bg-surface-header/40">
      <pre className="min-w-fit py-1 font-mono text-hint leading-relaxed">
        {lines.map((line, i) => (
          <div
            key={i}
            className={cn(
              "px-3 whitespace-pre",
              line.kind === "added" && "bg-wash-success text-success",
              line.kind === "removed" && "bg-wash-danger text-destructive",
              line.kind === "gap" && "text-muted-foreground/50",
              line.kind === "same" && "text-muted-foreground",
            )}
          >
            {line.kind === "added" ? "+" : line.kind === "removed" ? "−" : " "} {line.text}
          </div>
        ))}
      </pre>
    </div>
  )
}

/**
 * Deployment history, which exists because Docker keeps none.
 *
 * Bringing a project up replaces what was running, and unless the compose file
 * happened to be committed a minute earlier the previous configuration is
 * gone. Each row here is the state the dashboard captured immediately before
 * it was replaced — the file, the digests that were actually running, and who
 * asked.
 */
export function DeploymentHistoryPanel({ stack }: { stack: string }) {
  const { data, error, loading } = usePoll<StackDeployment[]>(
    (signal) =>
      get<StackDeployment[]>(
        `/docker/stacks/${encodeURIComponent(stack)}/deployments`,
        undefined,
        signal,
      ),
    0,
    [stack],
  )

  if (loading && !data) return <LoadingRows />
  if (error) return <ErrorState error={error} />
  if (!data?.length) {
    return (
      <EmptyState
        icon={Clock}
        title="No deployments recorded yet"
        description="The dashboard writes a stack down just before it changes it. Nothing has been deployed through here yet, so there is nothing to restore."
      />
    )
  }

  return (
    <div className="animate-rise space-y-2.5">
      {data.map((record) => (
        <DeploymentRow key={record.id} stack={stack} record={record} />
      ))}
      <p className="pt-1 text-hint leading-relaxed text-muted-foreground">
        Each entry is the state that was <em>replaced</em>. Environment values are hashed, never
        stored.
      </p>
    </div>
  )
}

function DeploymentRow({ stack, record }: { stack: string; record: StackDeployment }) {
  const [open, setOpen] = useState(false)
  const detail = usePoll<StackDeployment>(
    (signal) =>
      get<StackDeployment>(
        `/docker/stacks/${encodeURIComponent(stack)}/deployments/${record.id}`,
        undefined,
        signal,
      ),
    0,
    [record.id, open],
    { enabled: open },
  )

  return (
    <div className="overflow-hidden rounded-lg border border-hairline">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-2.5 px-3.5 py-2.5 text-left focus-ring-inset transition-colors hover:bg-row-hover"
      >
        <span className="min-w-0 flex-1">
          <span className="block text-body leading-snug font-semibold">
            {record.action ?? "deploy"} · {relativeTime(record.createdAt)}
          </span>
          <span className="mt-0.5 block text-hint leading-relaxed text-muted-foreground">
            {record.actor ? `by ${record.actor}` : "actor unknown"}
            {record.gitCommit && ` · ${record.gitCommit.slice(0, 8)}`}
            {record.gitDirty && " (uncommitted changes)"}
          </span>
        </span>
        <Tag mono>{record.configHash.slice(0, 8)}</Tag>
        <Button
          size="xs"
          variant="ghost"
          aria-label="Copy deployment id"
          className="shrink-0"
          onClick={(e) => {
            e.stopPropagation()
            void copyText(String(record.id), "Deployment id copied")
          }}
        >
          <Copy className="size-3" />
        </Button>
        <ChevronDown
          className={cn(
            "size-3.5 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-180",
          )}
        />
      </button>
      {open && (
        <div className="space-y-2.5 border-t border-hairline bg-surface-header/30 px-3.5 py-3 text-hint leading-relaxed">
          {detail.loading && !detail.data && <LoadingRows rows={2} />}
          {detail.data && (
            <>
              {/*
                Whether a rollback is even possible. An image that is gone from
                this host and from its registry cannot be restored, and saying
                so before the operator commits is the difference between a
                rollback and an outage.
              */}
              <p
                className={cn(
                  "flex items-start gap-2",
                  detail.data.restorable ? "text-muted-foreground" : "text-warning",
                )}
              >
                {detail.data.restorable ? (
                  <CheckCircle className="mt-0.5 size-3 shrink-0 text-success" />
                ) : (
                  <Warning className="mt-0.5 size-3 shrink-0" />
                )}
                <span>
                  {detail.data.restorable
                    ? "Every image is still on this server, so this configuration can be restored exactly."
                    : `${detail.data.missing?.join(", ")} ${detail.data.missing?.length === 1 ? "is" : "are"} no longer on this server, so a restore would pull whatever those tags point at now.`}
                </span>
              </p>

              {Object.keys(detail.data.imageDigests).length > 0 && (
                <details>
                  <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
                    Image digests
                  </summary>
                  <ul className="mt-1 space-y-0.5 font-mono text-xs leading-relaxed text-muted-foreground">
                    {Object.entries(detail.data.imageDigests).map(([service, digest]) => (
                      <li
                        key={service}
                        className="group flex items-center gap-1.5 break-all"
                      >
                        <span className="min-w-0 flex-1">
                          {service}: {digest}
                        </span>
                        <Button
                          size="xs"
                          variant="ghost"
                          aria-label={`Copy ${service} digest`}
                          className="shrink-0"
                          onClick={() => void copyText(digest, "Digest copied")}
                        >
                          <Copy className="size-3" />
                        </Button>
                      </li>
                    ))}
                  </ul>
                </details>
              )}

              {detail.data.config && (
                <details>
                  <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
                    The compose file as it was
                  </summary>
                  <div className="relative mt-1">
                    <pre className="max-h-64 overflow-auto rounded-md border border-hairline bg-surface-header/40 p-3 font-mono text-xs leading-relaxed whitespace-pre">
                      {detail.data.config}
                    </pre>
                    <Button
                      size="xs"
                      variant="outline"
                      className="absolute top-2 right-2"
                      onClick={() =>
                        void copyText(detail.data?.config ?? "", "Compose file copied")
                      }
                    >
                      <Copy className="size-3" />
                      Copy
                    </Button>
                  </div>
                  <p className="mt-1 text-muted-foreground">
                    Shown rather than applied — the dashboard will not overwrite a compose file you
                    have not read.
                  </p>
                </details>
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}
