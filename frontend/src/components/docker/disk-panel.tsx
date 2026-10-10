"use client"

import { bytes } from "@/lib/format"
import type { DockerDiskUsage } from "@/lib/types"

/**
 * The same four figures as one bar, for a page whose subject is not disk.
 *
 * The overview used to answer "where is my disk" with a sentence — "Only 412
 * MB could be reclaimed, not worth a sweep yet" — which is a true and
 * completely unreadable way to present four quantities. A reader wants to know
 * the shape of it: whether the space is images, or a build cache nobody knew
 * existed, or volumes that hold the actual data and must not be swept.
 *
 * One stacked bar says that in a glance and costs three lines. It is a chart,
 * so it takes the chart tokens rather than the status hues — none of these
 * segments is a reading of good or bad, and painting the largest one amber
 * would say it was.
 */
const DISK_SEGMENTS = [
  {
    key: "images",
    label: "Images",
    color: "var(--chart-1)",
    // What the layers occupy, not the sum of every image's claimed size: the
    // second double-counts every shared layer and is always the larger.
    of: (d: DockerDiskUsage) => d.layersSize,
  },
  {
    key: "buildCache",
    label: "Build cache",
    color: "var(--chart-2)",
    of: (d: DockerDiskUsage) => d.buildCacheSize,
  },
  {
    key: "volumes",
    label: "Volumes",
    color: "var(--chart-5)",
    of: (d: DockerDiskUsage) => d.volumesSize,
  },
  {
    key: "containers",
    label: "Containers",
    color: "var(--chart-4)",
    of: (d: DockerDiskUsage) => d.containersSize,
  },
] as const

export function DiskSummary({ usage }: { usage: DockerDiskUsage }) {
  const segments = DISK_SEGMENTS.map((s) => ({ ...s, size: s.of(usage) })).filter((s) => s.size > 0)
  const total = segments.reduce((sum, s) => sum + s.size, 0)
  if (total === 0) return null

  return (
    <div className="space-y-2.5">
      <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <p className="numeric text-sm font-medium">{bytes(total)} on disk</p>
        <p className="text-hint text-muted-foreground">
          {usage.images.total} image{usage.images.total === 1 ? "" : "s"} · {usage.volumes.total}{" "}
          volume{usage.volumes.total === 1 ? "" : "s"}
        </p>
      </div>

      {/* One track, four widths. The bar carries no ticks and no axis: it is
          answering "what shape is it", and anything more precise is the figure
          printed under each swatch. */}
      <div
        className="flex h-2 w-full overflow-hidden rounded-full bg-meter-track"
        role="img"
        aria-label={segments.map((s) => `${s.label} ${bytes(s.size)}`).join(", ")}
      >
        {segments.map((segment) => (
          <span
            key={segment.key}
            className="h-full transition-[width] first:rounded-l-full last:rounded-r-full"
            style={{
              width: `${(segment.size / total) * 100}%`,
              backgroundColor: segment.color,
            }}
          />
        ))}
      </div>

      <ul className="flex flex-wrap gap-x-4 gap-y-1">
        {segments.map((segment) => (
          <li key={segment.key} className="flex min-w-0 items-center gap-1.5">
            <span
              aria-hidden
              className="size-1.5 shrink-0 rounded-full"
              style={{ backgroundColor: segment.color }}
            />
            <span className="truncate text-hint text-muted-foreground">{segment.label}</span>
            <span className="numeric text-hint">{bytes(segment.size)}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
