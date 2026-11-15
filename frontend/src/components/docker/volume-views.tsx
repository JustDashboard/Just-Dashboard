"use client"

import Link from "next/link"
import { Archive, Box, CloudUpload, Database, Servers, Warning } from "@/components/icons"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import type { BackupJob, VolumeDetail } from "@/lib/types"
import { useMediaQuery } from "@/hooks/use-mobile"
import { destinations } from "@/components/backups/marks"
import { scheduleLabel, targetLabel } from "@/components/backups/shared"
import { DatabaseStorageWarning, looksLikeDatabase } from "@/components/docker/shared"
import {
  holderHue,
  holderOf,
  prunable,
  redactOption,
  standing,
  type Backup,
} from "@/components/docker/volumes"
import { FileBrowser } from "@/components/files/inline-browser"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { EmptyNote, EmptyState, LoadingRows, Notice } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { cn } from "@/lib/utils"

/** A section's count, at the far end of its title line. */
function Count({ children }: { children: React.ReactNode }) {
  return <span className="numeric text-hint text-muted-foreground">{children}</span>
}

/**
 * What is in the volume: the file browser every page embeds, clamped to the
 * volume's own directory. It was a section of the sheet under three others;
 * it is the view the page opens on, because "what is in it" is the question a
 * volume is opened with.
 */
export function VolumeFiles({ volume }: { volume: VolumeDetail }) {
  // A volume nothing mounts is not a database anybody is running, whatever it
  // is called: the warning is about writing underneath a live process.
  const databaseFiles =
    volume.usedBy.length > 0 &&
    looksLikeDatabase(volume.name, ...volume.usedBy.flatMap((u) => [u.name, u.destination]))
  if (!volume.mountpoint) {
    return (
      <EmptyState
        icon={Database}
        title="Its files are not on this server"
        description={
          <>
            <span className="font-mono">{volume.driver}</span> keeps this volume somewhere this
            dashboard cannot read. The containers that mount it see its contents.
          </>
        }
      />
    )
  }
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {databaseFiles && <DatabaseStorageWarning />}
      <FileBrowser
        root={volume.mountpoint}
        label={volume.name}
        emptyNote="Nothing has been written to this volume yet."
      />
    </div>
  )
}

/**
 * The containers that mount it, one line each: the container as the product it
 * runs, the path it sees the volume at beside its name, its state and whether
 * it may write, and the way to its page. It was a table in the sheet; two or
 * three things you open are cards (§5), all one height.
 */
export function VolumeMounts({
  volume,
  productOf,
  stacks,
}: {
  volume: VolumeDetail
  productOf: Map<string, string>
  stacks: Set<string>
}) {
  const roomy = useMediaQuery("(min-width: 640px)")
  return (
    <div className="flex min-w-0 flex-col gap-5">
      <PruneNotice volume={volume} stacks={stacks} />
      {volume.usedBy.length > 0 && (
        <Panel plain>
          <PanelHeader title="Mounted by" actions={<Count>{volume.usedBy.length}</Count>} />
          <ChoiceList className="pt-3" aria-label="Containers mounting this volume">
            {volume.usedBy.map((user) => (
              <ChoiceRow
                key={`${user.id}-${user.destination}`}
                href={`/docker/containers/${user.id}`}
                verb={`Open ${user.name}`}
                leading={<ProductLogo id={productOf.get(user.id)} fallback={Box} size="sm" />}
                title={
                  <>
                    {user.name}
                    {user.stack && (
                      <span
                        className="ml-2 text-hint font-normal"
                        style={{ color: holderHue({ kind: "stack", name: user.stack }) }}
                      >
                        {user.stack}
                      </span>
                    )}
                  </>
                }
                description={
                  <span className="font-mono" title={user.destination}>
                    {user.destination}
                  </span>
                }
                trailing={
                  roomy ? (
                    <>
                      <span className="flex w-24 justify-start">
                        <Status state={user.state} />
                      </span>
                      <span className="flex w-20 justify-end text-hint">
                        {user.readOnly ? (
                          <Tag>read-only</Tag>
                        ) : (
                          <span className="text-muted-foreground">read-write</span>
                        )}
                      </span>
                    </>
                  ) : (
                    // On a phone the state is its dot and read-only two
                    // letters, so the path keeps the width it is read for.
                    <span className="flex items-center gap-2" title={user.state}>
                      {user.readOnly && <Tag>ro</Tag>}
                      <StatusDot state={user.state} />
                      <span className="sr-only">{user.state}</span>
                    </span>
                  )
                }
              />
            ))}
          </ChoiceList>
        </Panel>
      )}
    </div>
  )
}

/**
 * What a prune would do, said only where nothing mounts the volume. A stopped
 * container's volume gets no notice: the daemon keeps it, and its row above
 * already says the container is stopped.
 */
function PruneNotice({ volume, stacks }: { volume: VolumeDetail; stacks: Set<string> }) {
  if (volume.usedBy.length > 0) return null
  const s = standing(volume)
  const holder = holderOf(volume)
  const size = volume.size > 0 ? ` and the ${bytes(volume.size)} in it` : ""
  if (!prunable(volume)) {
    return (
      // Only what is certain: a prune leaves it alone. What removing it does to
      // the data is the driver's business — a CSI or EBS plugin deletes the
      // disk — so it is not promised either way.
      <Notice title="Nothing mounts this volume">
        A prune leaves it alone: Docker prunes only local volumes without driver options, and this
        one{" "}
        {volume.driver === "local"
          ? `is a ${volume.mountType} mount`
          : `is kept by ${volume.driver}`}
        .
      </Notice>
    )
  }
  return (
    <Notice tone="warning" icon={Warning} title="Nothing mounts this volume">
      <p>
        {s === "down" ? (
          <>
            <span className="font-medium" style={{ color: holderHue(holder) }}>
              {holder.name}
            </span>{" "}
            was taken down and left it behind.{" "}
          </>
        ) : s === "anonymous" ? (
          "Docker made it for a container that has since been removed. "
        ) : (
          "No container has it mounted, running or stopped. "
        )}
        <code className="font-mono text-xs">docker volume prune</code> would delete it{size}.
      </p>
      {s === "down" && stacks.has(holder.name) && (
        <Link
          href={`/docker/stacks/${encodeURIComponent(holder.name)}`}
          className="mt-1.5 inline-block rounded-sm font-medium text-foreground focus-ring hover:underline"
        >
          Open {holder.name} to deploy it again
        </Link>
      )}
    </Notice>
  )
}

/**
 * The backup jobs that cover it, one line each: where the archives go, the
 * job's name over its destination, when it runs and when it last finished, and
 * the way to the job. The sheet said only the newest backup's age and the jobs'
 * names; a job's schedule and place were a page away.
 */
export function VolumeBackups({
  coverage,
  backup,
  jobs,
  local,
  onBackUp,
}: {
  coverage: "reading" | "unreadable" | "read"
  backup?: Backup
  jobs?: BackupJob[]
  /** A local volume is a directory a job can archive; another driver's is not. */
  local: boolean
  onBackUp?: () => void
}) {
  const roomy = useMediaQuery("(min-width: 640px)")
  const covering = backup?.jobs ?? []
  return (
    <Panel plain>
      <PanelHeader
        title="Backup jobs"
        actions={covering.length > 0 && <Count>{covering.length}</Count>}
      />
      <div className="pt-3">
        {coverage === "reading" ? (
          <LoadingRows rows={2} />
        ) : coverage === "unreadable" || !backup ? (
          <EmptyNote>Backup coverage is not readable with this account.</EmptyNote>
        ) : covering.length === 0 ? (
          <EmptyState
            icon={Archive}
            title="No backup job covers this volume"
            description={
              local
                ? "Its data exists only on this server's disk."
                : "Its driver keeps the data elsewhere, outside this dashboard's backups."
            }
            action={
              onBackUp && (
                <Button size="sm" onClick={onBackUp}>
                  <Archive className="size-4" />
                  Back up…
                </Button>
              )
            }
          />
        ) : (
          <ChoiceList aria-label="Backup jobs covering this volume">
            {covering.map((entry) => {
              const job = jobs?.find((j) => j.id === entry.id)
              const destination = job ? destinations([job])[0] : undefined
              return (
                <ChoiceRow
                  key={entry.id}
                  href={`/backups/${entry.id}`}
                  verb={`Open ${entry.name}`}
                  leading={
                    <ProductLogo
                      id={destination?.product}
                      size="sm"
                      fallback={destination && !destination.offsite ? Servers : CloudUpload}
                    />
                  }
                  title={entry.name}
                  // A job the list did not name still holds its second line.
                  description={
                    job ? <span title={targetLabel(job)}>{targetLabel(job)}</span> : "Backup job"
                  }
                  trailing={
                    <>
                      {job && roomy && (
                        <span className="w-40 truncate text-hint text-muted-foreground">
                          {scheduleLabel(job.schedule)}
                        </span>
                      )}
                      <span className={cn("flex justify-end text-hint", roomy && "w-40")}>
                        <JobStanding
                          enabled={job ? job.enabled : entry.enabled}
                          job={job}
                          last={job?.lastSuccessAt ?? backup.lastAt}
                        />
                      </span>
                    </>
                  }
                />
              )
            })}
          </ChoiceList>
        )}
      </div>
    </Panel>
  )
}

/** How a covering job stands, in the words its line ends on. */
function JobStanding({ enabled, job, last }: { enabled: boolean; job?: BackupJob; last?: string }) {
  if (!enabled) return <span className="text-warning">paused</span>
  if (job?.lastRun?.status === "running") return <TextShimmer>backing up…</TextShimmer>
  if (job?.lastRun?.status === "failed")
    return (
      <span className="text-destructive" title={timestamp(job.lastRun.startedAt)}>
        last run failed
      </span>
    )
  if (!last) return <span className="text-muted-foreground">never ran</span>
  return (
    <span className="truncate text-success" title={timestamp(last)}>
      backed up {relativeTime(last)}
    </span>
  )
}

/** What Docker was told: the driver and its options, and every label on it. */
export function VolumeConfiguration({ volume }: { volume: VolumeDetail }) {
  const options = Object.entries(volume.options ?? {})
  const labels = Object.entries(volume.labels).sort(([a], [b]) => a.localeCompare(b))
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <Panel plain>
        <PanelHeader title="What Docker was told" />
        <DetailList className="pt-3">
          <Detail label="Name">
            <span className="font-mono break-all">{volume.name}</span>
          </Detail>
          <Detail label="Driver">
            <span className="font-mono">{volume.driver}</span>
          </Detail>
          <Detail label="Scope">{volume.scope}</Detail>
          <Detail label="Created" className="numeric">
            {volume.createdAt ? timestamp(volume.createdAt) : "—"}
          </Detail>
          <Detail label="On disk at">
            <span className="font-mono break-all">{volume.mountpoint || "—"}</span>
          </Detail>
          {options.map(([key, value]) => (
            <Detail key={`o-${key}`} label={key}>
              <span className="font-mono break-all">{redactOption(key, value)}</span>
            </Detail>
          ))}
        </DetailList>
      </Panel>
      <Panel plain>
        <PanelHeader title="Labels" actions={labels.length > 0 && <Count>{labels.length}</Count>} />
        {labels.length > 0 ? (
          <DetailList className="pt-3">
            {labels.map(([key, value]) => (
              <Detail key={key} label={<span className="font-mono">{key}</span>}>
                <span className="font-mono break-all text-muted-foreground">{value || "—"}</span>
              </Detail>
            ))}
          </DetailList>
        ) : (
          <p className="pt-3 text-body text-muted-foreground">No labels.</p>
        )}
      </Panel>
    </div>
  )
}
