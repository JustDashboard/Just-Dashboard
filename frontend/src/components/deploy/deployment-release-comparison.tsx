"use client"

import Link from "next/link"
import { ArrowCircleUp, ArrowRight, Box, Key } from "@/components/icons"
import { Group, Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import type { Tone } from "@/components/tone"
import { Button } from "@/components/ui/button"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { bytes } from "@/lib/format"
import type { ReleaseComparisonResponse, ReleaseListChange, ReleaseUpdateStatus } from "@/lib/types"

const CHANGE_TONE: Record<ReleaseListChange["change"], Tone> = {
  added: "success",
  removed: "danger",
  changed: "warning",
  unchanged: "default",
}

const UPDATE_TEXT: Record<NonNullable<ReleaseUpdateStatus["state"]>, string> = {
  current: "The upstream tag still resolves to the digest this release pinned.",
  outdated: "The upstream tag now points at a different image. Deploying again would pick it up.",
  unknown: "The registry could not be asked about this tag.",
  local: "This release was built here. There is no upstream tag to compare against.",
  pinned: "This release names its image by digest, so the upstream tag cannot move under it.",
}

const UPDATE_TONE: Record<NonNullable<ReleaseUpdateStatus["state"]>, Tone> = {
  current: "success",
  outdated: "warning",
  unknown: "default",
  local: "default",
  pinned: "success",
}

/**
 * What actually changed between the previous release and this one, and whether
 * the artifacts a rollback would need are still on disk. Variables appear by
 * name and digest; their values are never read here.
 */
export function DeploymentReleaseComparison({
  projectID,
  environmentID,
  releaseID,
}: {
  projectID: number
  environmentID: number
  releaseID: number
}) {
  const result = usePoll(
    (signal) =>
      get<ReleaseComparisonResponse>(
        `/deploy/${projectID}/environments/${environmentID}/releases/${releaseID}/comparison`,
        undefined,
        signal,
      ),
    0,
    [projectID, environmentID, releaseID],
  )
  const comparison = result.data?.comparison
  const detail = comparison?.detail
  const changedFields = (detail?.fields ?? []).filter((field) => field.changed)
  return (
    <Panel>
      <PanelHeader
        title="What changed in this release"
        actions={
          <>
            {/* Which two releases. This was the panel's description; it names
                the subject rather than explaining the panel, so it stays — as
                a figure beside the title. */}
            {comparison && (
              <span className="numeric text-hint text-muted-foreground">
                Release {comparison.fromReleaseId} compared with release {comparison.toReleaseId}
              </span>
            )}
            <Button variant="ghost" size="xs" asChild>
              <Link href="/docker/images">
                Open images <ArrowRight className="size-3" />
              </Link>
            </Button>
          </>
        }
      />
      <PanelBody className="space-y-4">
        {result.error ? (
          <ErrorState error={result.error} />
        ) : !result.data ? (
          <LoadingRows />
        ) : (
          <>
            <UpdateEvidence update={result.data.update} />
            {!comparison ? (
              <EmptyNote>{result.data.reason ?? "There is nothing to compare."}</EmptyNote>
            ) : detail?.status !== "available" ? (
              <Notice title="Release detail unavailable" icon={Box}>
                {detail?.reason ?? "These releases carry no readable runtime snapshot."}
              </Notice>
            ) : (
              <>
                <section className="min-w-0 space-y-2" aria-label="Changed fields">
                  <h3 className="text-xs font-medium text-muted-foreground">Runtime and source</h3>
                  {changedFields.length === 0 ? (
                    <EmptyNote>
                      Source, image, command, ports, strategy and storage are identical.
                    </EmptyNote>
                  ) : (
                    <ul className="divide-y divide-hairline">
                      {changedFields.map((field) => (
                        <li
                          key={field.field}
                          className="min-w-0 space-y-1 py-2.5 first:pt-0 last:pb-0"
                        >
                          <p className="text-xs font-medium capitalize">{field.field}</p>
                          <p className="min-w-0 font-mono text-hint break-all text-muted-foreground">
                            {field.from || "none"} → {field.to || "none"}
                          </p>
                        </li>
                      ))}
                    </ul>
                  )}
                </section>
                <ChangeList
                  title="Variables"
                  note="Compared by value digest. No variable value is read to build this list."
                  changes={detail.variables}
                />
                <ChangeList title="Dependencies" changes={detail.dependencies} />
                <ChangeList title="Checks" changes={detail.checks} />
                <ChangeList title="Domains" changes={detail.domains} />
              </>
            )}
            <RetainedArtifacts artifacts={result.data.artifacts} />
          </>
        )}
      </PanelBody>
    </Panel>
  )
}

function UpdateEvidence({ update }: { update: ReleaseUpdateStatus }) {
  if (update.status !== "available" || !update.state) {
    return (
      <Notice title="Update status unavailable" icon={ArrowCircleUp}>
        {update.reason ?? "Docker could not be asked whether a newer image exists."}
      </Notice>
    )
  }
  return (
    <Group className="space-y-1.5">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <span className="text-xs font-medium">Upstream image</span>
        <Tag tone={UPDATE_TONE[update.state]}>{update.state}</Tag>
      </div>
      {update.reference && (
        <p className="min-w-0 font-mono text-hint break-all text-muted-foreground">
          {update.reference}
        </p>
      )}
      <p className="text-xs text-muted-foreground">{update.reason || UPDATE_TEXT[update.state]}</p>
    </Group>
  )
}

function ChangeList({
  title,
  note,
  changes,
}: {
  title: string
  note?: string
  changes: ReleaseListChange[]
}) {
  const moved = changes.filter((change) => change.change !== "unchanged")
  return (
    <section className="min-w-0 space-y-2 border-t border-hairline pt-3" aria-label={title}>
      <h3 className="text-xs font-medium text-muted-foreground">{title}</h3>
      {moved.length === 0 ? (
        <EmptyNote>No {title.toLowerCase()} changed between these releases.</EmptyNote>
      ) : (
        <ul className="divide-y divide-hairline">
          {moved.map((change) => (
            <li key={change.name} className="min-w-0 space-y-1 py-2.5 first:pt-0 last:pb-0">
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <span className="min-w-0 font-mono text-xs break-all">{change.name}</span>
                <Tag tone={CHANGE_TONE[change.change]}>{change.change}</Tag>
                {change.secret && <Tag icon={Key}>digest only</Tag>}
              </div>
              <p className="min-w-0 font-mono text-hint break-all text-muted-foreground">
                {change.from || "none"} → {change.to || "none"}
              </p>
            </li>
          ))}
        </ul>
      )}
      {note && <p className="text-hint text-muted-foreground">{note}</p>}
    </section>
  )
}

function RetainedArtifacts({ artifacts }: { artifacts: ReleaseComparisonResponse["artifacts"] }) {
  return (
    <section
      className="min-w-0 space-y-2 border-t border-hairline pt-3"
      aria-label="Retained artifacts"
    >
      <h3 className="text-xs font-medium text-muted-foreground">Retained artifacts</h3>
      {artifacts.length === 0 ? (
        <EmptyNote>This release recorded no artifacts.</EmptyNote>
      ) : (
        <ul className="divide-y divide-hairline">
          {artifacts.map((artifact) => (
            <li
              key={`${artifact.kind}:${artifact.reference}:${artifact.digest}`}
              className="min-w-0 space-y-1 py-2.5 first:pt-0 last:pb-0"
            >
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <span className="min-w-0 font-mono text-xs break-all">{artifact.reference}</span>
                <Tag>{artifact.kind}</Tag>
                <Tag tone={artifact.retained ? "success" : "warning"}>
                  {artifact.retained ? "retained" : "prunable"}
                </Tag>
                {artifact.sizeBytes > 0 && (
                  <span className="numeric text-hint text-muted-foreground">
                    {bytes(artifact.sizeBytes)}
                  </span>
                )}
              </div>
              <p className="text-xs text-muted-foreground">{artifact.reason}</p>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
