"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { Database, Warning } from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import type { VolumeDetail } from "@/lib/types"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { LiveBytes } from "@/components/overview/readings"
import { Hint } from "@/components/docker/explain"
import { DatabaseStorageWarning, looksLikeDatabase } from "@/components/docker/shared"
import {
  STANDING_COLOR,
  STANDING_LABEL,
  VOLUME_HUE,
  holderHue,
  holderOf,
  prunable,
  redactOption,
  sizeReading,
  standing,
  volumeProduct,
  withLiveStates,
  type Backup,
} from "@/components/docker/volumes"
import { FileBrowser } from "@/components/files/inline-browser"
import { Detail, DetailList } from "@/components/page"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import type { Verb } from "@/components/verbs"
import { cn } from "@/lib/utils"

/**
 * One volume's sheet, opened from its row and addressed as `?volume=` so a
 * deployment's settings can link straight at the volume it depends on.
 *
 * It opens on four readings — how much is in it, who mounts it, when it was
 * last backed up, how old it is — then says what a prune would do to it when
 * that is to delete it. The containers that mount it are a table of their own:
 * each by name and state, the path it sees the volume at and whether it may
 * write there, each a way to that container. Then what is in it, because a
 * volume is a directory on this server and the dashboard has a file manager,
 * and what Docker was told about it.
 */
export function VolumeSheet({
  name,
  states,
  productOf,
  backup,
  stacks,
  verbs,
  onOpenChange,
}: {
  name: string | null
  /** Each container's state as the socket last said, over the detail's own. */
  states: Map<string, string>
  productOf: Map<string, string>
  backup?: Backup
  /** The stacks this dashboard can open, so a stack named here is a link only when it exists. */
  stacks: Set<string>
  verbs: (volume: VolumeDetail) => Verb[]
  onOpenChange: (open: boolean) => void
}) {
  const { data, error, loading } = usePoll<VolumeDetail>(
    (signal) =>
      get<VolumeDetail>(`/docker/volumes/${encodeURIComponent(name ?? "")}`, undefined, signal),
    0,
    [name],
    { enabled: name !== null },
  )
  const volume = data && withLiveStates([data], states)[0]
  const actions = volume ? verbs(volume) : []

  // A volume nothing mounts is not a database anybody is running, whatever it
  // is called: the warning is about writing underneath a live process.
  const databaseFiles =
    volume !== undefined &&
    volume.usedBy.length > 0 &&
    looksLikeDatabase(volume.name, ...volume.usedBy.flatMap((u) => [u.name, u.destination]))

  return (
    <SidePanel
      open={name !== null}
      onOpenChange={onOpenChange}
      width="xl"
      initialFocus="body"
      title={
        <>
          <ProductLogo
            id={volume ? volumeProduct(volume, productOf) : undefined}
            fallback={Database}
            size="sm"
          />
          <span className="min-w-0 truncate font-mono">{name ?? "Volume"}</span>
        </>
      }
      description={volume?.mountpoint}
      actions={volume && <SheetStanding volume={volume} />}
      footer={
        actions.length > 0 &&
        actions.map((verb) => (
          // A refused verb keeps its reason as its name, the word on its face.
          <Button
            key={verb.key}
            size="sm"
            variant={verb.danger ? "destructive" : "outline"}
            disabled={verb.disabled}
            aria-label={verb.label}
            title={verb.disabled ? verb.label : undefined}
            onClick={verb.run}
          >
            <verb.icon className="size-4" />
            {verb.label.split(" — ")[0]}
          </Button>
        ))
      }
    >
      {error && <ErrorState error={error} />}
      {loading && !volume && <LoadingRows rows={6} />}
      {volume && (
        <div data-slot="volume-readout" className="animate-rise space-y-7">
          <Readings volume={volume} backup={backup} />
          <PruneNotice volume={volume} stacks={stacks} />
          <MountedBy volume={volume} productOf={productOf} />

          {/*
            The contents rather than a link to them. "Browse files" was a
            button that closed this panel, changed page, and asked the
            operator to recognise the volume again by a path under
            /var/lib/docker; the answer is a few lines long and fits here.
          */}
          {volume.mountpoint && (
            <section className="space-y-2">
              <p className="eyebrow">Contents</p>
              {databaseFiles && <DatabaseStorageWarning />}
              <FileBrowser
                root={volume.mountpoint}
                label={volume.name}
                emptyNote="Nothing has been written to this volume yet."
              />
            </section>
          )}

          <Configuration volume={volume} />
        </div>
      )}
    </SidePanel>
  )
}

function SheetStanding({ volume }: { volume: VolumeDetail }) {
  const s = standing(volume)
  return (
    <span className="inline-flex items-center gap-1.5 text-xs font-medium">
      <span
        aria-hidden
        className="size-1.5 rounded-full"
        style={{ background: STANDING_COLOR[s] }}
      />
      <span className={cn(prunable(volume) ? "text-warning" : "text-muted-foreground")}>
        {STANDING_LABEL[s]}
      </span>
    </span>
  )
}

/** Four readings across the top, as the image, package and process sheets open. */
function Readings({ volume, backup }: { volume: VolumeDetail; backup?: Backup }) {
  const size = sizeReading(volume)
  const running = volume.usedBy.filter((u) => u.state === "running").length
  return (
    <div className="grid min-w-0 grid-cols-2 gap-x-6 gap-y-5 border-b border-hairline pb-6 sm:grid-cols-4">
      <div className="min-w-0 space-y-1.5">
        <p className="eyebrow">On disk</p>
        {size.bytes !== undefined ? (
          <p
            className="numeric text-2xl font-semibold tracking-tight"
            style={{ color: VOLUME_HUE }}
          >
            <LiveBytes value={size.bytes} />
          </p>
        ) : (
          <p className="text-title font-medium text-muted-foreground">{size.word}</p>
        )}
        <p className="truncate text-hint text-muted-foreground">
          {volume.driver === "local" ? "on this server" : `kept by ${volume.driver}`}
        </p>
      </div>
      <div className="min-w-0 space-y-1.5">
        <p className="eyebrow">Mounted by</p>
        <p className="numeric text-2xl font-semibold tracking-tight">{volume.usedBy.length}</p>
        <p className="truncate text-hint text-muted-foreground">
          {volume.usedBy.length === 0
            ? "no container"
            : running === volume.usedBy.length
              ? "all running"
              : `${running} running`}
        </p>
      </div>
      <div className="min-w-0 space-y-1.5">
        <p className="eyebrow">Backup</p>
        {backup === undefined ? (
          <p className="text-title font-medium text-muted-foreground">—</p>
        ) : backup.state === "none" ? (
          <p className="text-title font-medium text-muted-foreground">none</p>
        ) : (
          <p
            className={cn(
              "truncate text-title font-medium",
              backup.state === "protected" ? "text-success" : "text-warning",
            )}
            title={backup.lastAt ? timestamp(backup.lastAt) : undefined}
          >
            {backup.state === "paused"
              ? "paused"
              : backup.lastAt
                ? relativeTime(backup.lastAt)
                : "never ran"}
          </p>
        )}
        <p className="truncate text-hint text-muted-foreground">
          {backup === undefined
            ? "coverage not readable"
            : backup.jobs.length > 0
              ? backup.jobs.map((j) => j.name).join(", ")
              : "no job covers it"}
        </p>
      </div>
      <div className="min-w-0 space-y-1.5">
        <p className="eyebrow">Created</p>
        <p
          className="truncate text-title font-medium"
          title={volume.createdAt ? timestamp(volume.createdAt) : undefined}
        >
          {volume.createdAt ? relativeTime(volume.createdAt) : "—"}
        </p>
        <p className="truncate text-hint text-muted-foreground">{volume.driver} driver</p>
      </div>
    </div>
  )
}

/**
 * What a prune would do, said only when the answer is to delete it. A stopped
 * container's volume gets no notice: the daemon keeps it, and its row in the
 * table below already says the container is stopped.
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
 * The containers that mount it, as a table: a container is the next place a
 * reader goes from here, and which path it sees the volume at, and whether it
 * may write there, are read down a column. On a phone the state is the dot
 * before the name and read-only a tag after the path, so the path keeps the
 * width it is read for (§12: replaced, nothing dropped).
 */
function MountedBy({
  volume,
  productOf,
}: {
  volume: VolumeDetail
  productOf: Map<string, string>
}) {
  const router = useRouter()
  const roomy = useMediaQuery("(min-width: 640px)")
  return (
    <section className="space-y-2">
      <p className="eyebrow">
        Mounted by{volume.usedBy.length > 0 && ` · ${plural(volume.usedBy.length, "container")}`}
      </p>
      {volume.usedBy.length === 0 ? (
        <Hint>No container mounts it, running or stopped.</Hint>
      ) : (
        // Framed, as every table is (§2), beside the file browser's own frame.
        <div className="overflow-hidden rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Container</TableHead>
                {roomy && <TableHead className="w-32">State</TableHead>}
                <TableHead>Sees it at</TableHead>
                {roomy && <TableHead className="w-24 text-right">Access</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {volume.usedBy.map((u) => (
                <TableRow
                  key={`${u.id}-${u.destination}`}
                  onActivate={() => router.push(`/docker/containers/${u.id}`)}
                >
                  <TableCell>
                    <span className="flex min-w-0 items-center gap-2.5">
                      <ProductLogo
                        id={productOf.get(u.id)}
                        fallback={Database}
                        size="sm"
                        className="size-6 rounded-md [&_img]:size-3.5"
                      />
                      <span className="min-w-0">
                        <span className="flex min-w-0 items-center gap-1.5">
                          {!roomy && <StatusDot state={u.state} />}
                          <Link
                            href={`/docker/containers/${u.id}`}
                            title={roomy ? undefined : `${u.name} — ${u.state}`}
                            className="truncate rounded-sm text-body font-medium focus-ring hover:underline"
                          >
                            {u.name}
                          </Link>
                        </span>
                        {u.stack && (
                          <span
                            className="block truncate text-hint"
                            style={{ color: holderHue({ kind: "stack", name: u.stack }) }}
                          >
                            {u.stack}
                          </span>
                        )}
                      </span>
                    </span>
                  </TableCell>
                  {roomy && (
                    <TableCell>
                      <Status state={u.state} />
                    </TableCell>
                  )}
                  <TableCell className="max-w-0">
                    <span className="flex min-w-0 items-center gap-2">
                      <span
                        className="truncate font-mono text-muted-foreground"
                        title={u.destination}
                      >
                        {u.destination}
                      </span>
                      {!roomy && u.readOnly && <Tag className="shrink-0">ro</Tag>}
                    </span>
                  </TableCell>
                  {roomy && (
                    <TableCell className="text-right">
                      {u.readOnly ? (
                        <Tag>read-only</Tag>
                      ) : (
                        <span className="text-muted-foreground">read-write</span>
                      )}
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </section>
  )
}

/** What Docker was told: the driver and its options, and every label on it. */
function Configuration({ volume }: { volume: VolumeDetail }) {
  const options = Object.entries(volume.options ?? {})
  const labels = Object.entries(volume.labels).sort(([a], [b]) => a.localeCompare(b))
  return (
    <section className="space-y-2">
      <p className="eyebrow">Configuration</p>
      <DetailList>
        <Detail label="Driver">
          <span className="font-mono">{volume.driver}</span>
        </Detail>
        <Detail label="Scope">{volume.scope}</Detail>
        <Detail label="On disk at">
          <span className="font-mono break-all">{volume.mountpoint}</span>
        </Detail>
        {options.map(([key, value]) => (
          <Detail key={`o-${key}`} label={key}>
            <span className="font-mono break-all">{redactOption(key, value)}</span>
          </Detail>
        ))}
      </DetailList>
      {labels.length > 0 && (
        <DetailList className="border-t border-hairline pt-2">
          {labels.map(([key, value]) => (
            <Detail key={key} label={<span className="font-mono">{key}</span>}>
              <span className="font-mono break-all text-muted-foreground">{value || "—"}</span>
            </Detail>
          ))}
        </DetailList>
      )}
    </section>
  )
}
