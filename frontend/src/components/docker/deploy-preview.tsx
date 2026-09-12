"use client"

import { useState } from "react"
import {
  ChartActivity,
  CheckCircle,
  ChevronDown,
  Clock,
  Information,
  Warning,
} from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { DeployPreview, DiffLine, ServiceChange, StackDeployment } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Tag } from "@/components/tag"

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
 * when it is not, it labels every row as inferred because compose makes the
 * final call, and when there is no previous deployment to compare against it
 * says so instead of reporting "nothing changed".
 */

/** `text` colours the row's icon; `tag` is the mark at the end of the row. */
const CHANGE = {
  recreate: { label: "Recreated", text: "text-warning", tag: "warning" },
  create: { label: "Created", text: "text-foreground", tag: "default" },
  start: { label: "Started", text: "text-foreground", tag: "default" },
  remove: { label: "Removed", text: "text-destructive", tag: "danger" },
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

  return (
    <div className="space-y-4">
      <div className="rounded-lg border border-hairline px-3 py-2.5">
        <p className="text-body font-medium">{data.summary}</p>
        {data.diffAgainst && (
          <p className="mt-0.5 text-hint text-muted-foreground">
            Compared against {data.diffAgainst}.
          </p>
        )}
      </div>

      <section className="space-y-1.5">
        <p className="eyebrow">Services</p>
        {data.services.map((service) => (
          <ServiceChangeRow key={service.name} change={service} />
        ))}
      </section>

      {/* The number that means data is destroyed, stated whether or not it is zero. */}
      <section className="space-y-1.5">
        <p className="eyebrow">Volumes</p>
        {data.volumesRemoved.length > 0 ? (
          <p className="flex items-start gap-2 text-body text-destructive">
            <Warning className="mt-0.5 size-3.5 shrink-0" />
            <span>
              {data.volumesRemoved.join(", ")} would be removed. Everything stored in{" "}
              {data.volumesRemoved.length === 1 ? "it" : "them"} is destroyed permanently.
            </span>
          </p>
        ) : (
          <p className="flex items-start gap-2 text-body text-muted-foreground">
            <CheckCircle className="mt-0.5 size-3.5 shrink-0 text-success" />
            <span>
              No volume is removed
              {data.volumesKept.length > 0 && ` — ${data.volumesKept.join(", ")} survive`}.
            </span>
          </p>
        )}
      </section>

      {data.diff.length > 0 && (
        <section className="space-y-1.5">
          <p className="eyebrow">Compose file changes</p>
          <DiffView lines={data.diff} />
        </section>
      )}

      {data.caveats.length > 0 && (
        <section className="space-y-1.5">
          <p className="eyebrow">What this cannot know</p>
          {data.caveats.map((caveat, i) => (
            <p key={i} className="flex items-start gap-2 text-hint text-muted-foreground">
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
  const [open, setOpen] = useState(false)
  const meta = CHANGE[change.change] ?? CHANGE.unchanged
  return (
    <div className="rounded-md border border-hairline">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left focus-ring-inset"
      >
        <span className="min-w-0 flex-1 truncate text-body font-medium">{change.name}</span>
        <Tag tone={meta.tag}>{meta.label}</Tag>
        <ChevronDown
          className={cn(
            "size-3.5 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-180",
          )}
        />
      </button>
      {open && (
        <div className="space-y-1.5 border-t border-hairline px-3 py-2">
          <p className="text-hint leading-relaxed text-muted-foreground">{change.reason}</p>
          {change.imageBefore && change.imageAfter && (
            <p className="font-mono text-hint">
              <span className="text-destructive">− {change.imageBefore}</span>
              <br />
              <span className="text-success">+ {change.imageAfter}</span>
            </p>
          )}
          {change.inferred && (
            <p className="text-micro text-muted-foreground">
              Inferred by comparing the compose file against the last recorded deployment.
            </p>
          )}
        </div>
      )}
    </div>
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
        description="The dashboard writes down a stack's compose file and running image digests immediately before it changes them. Nothing has been deployed through here since that started, so there is nothing to compare against or roll back to."
      />
    )
  }

  return (
    <div className="space-y-2">
      {data.map((record) => (
        <DeploymentRow key={record.id} stack={stack} record={record} />
      ))}
      <p className="pt-1 text-hint text-muted-foreground">
        Each entry is the state that was <em>replaced</em>, captured just before the deploy that
        replaced it. Environment values are hashed rather than stored — an .env file beside a
        compose file holds every password the stack uses.
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
    <div className="rounded-md border border-hairline">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left focus-ring-inset"
      >
        <ChartActivity className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1">
          <span className="block text-body font-medium">
            {record.action ?? "deploy"} · {relativeTime(record.createdAt)}
          </span>
          <span className="block text-hint text-muted-foreground">
            {record.actor ? `by ${record.actor}` : "actor unknown"}
            {record.gitCommit && ` · ${record.gitCommit.slice(0, 8)}`}
            {record.gitDirty && " (uncommitted changes)"}
          </span>
        </span>
        <Tag mono>{record.configHash.slice(0, 8)}</Tag>
        <ChevronDown
          className={cn(
            "size-3.5 shrink-0 text-muted-foreground transition-transform",
            open && "rotate-180",
          )}
        />
      </button>
      {open && (
        <div className="space-y-2 border-t border-hairline px-3 py-2 text-hint">
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
                    ? "Every image this deployment used is still on this server, so restoring this compose file would bring back exactly what was running."
                    : `${detail.data.missing?.join(", ")} ${detail.data.missing?.length === 1 ? "is" : "are"} no longer on this server. Restoring this configuration would pull whatever those tags point at now, which is not what was running.`}
                </span>
              </p>

              {Object.keys(detail.data.imageDigests).length > 0 && (
                <div>
                  <p className="eyebrow mb-1">Image digests at the time</p>
                  <ul className="space-y-0.5 font-mono text-micro text-muted-foreground">
                    {Object.entries(detail.data.imageDigests).map(([service, digest]) => (
                      <li key={service} className="truncate">
                        {service}: {digest}
                      </li>
                    ))}
                  </ul>
                </div>
              )}

              {detail.data.config && (
                <details>
                  <summary className="cursor-pointer text-muted-foreground hover:text-foreground">
                    The compose file as it was
                  </summary>
                  <pre className="mt-1 max-h-64 overflow-auto rounded-sm border border-hairline bg-surface-header/40 p-2 font-mono text-micro whitespace-pre">
                    {detail.data.config}
                  </pre>
                  <p className="mt-1 text-muted-foreground">
                    Rolling back means putting this file back in place and deploying. It is shown
                    rather than applied: the dashboard will not overwrite a compose file you have
                    not read.
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
