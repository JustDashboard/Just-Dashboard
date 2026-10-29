"use client"

import { get } from "@/lib/api"
import { bytes, percent } from "@/lib/format"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import {
  FORECAST_RULE,
  basisText,
  confidenceText,
  growingApps,
  growthText,
  mountTone,
  mountVerdict,
  type AppGrowth,
  type CapacityForecast,
  type MountForecast,
} from "@/lib/capacity"
import { Meter, utilisationTone } from "@/components/meter"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Section } from "@/components/page"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote } from "@/components/state"
import { Tag } from "@/components/tag"
import { InfoTip } from "@/components/form"

/**
 * The Metrics page's Capacity forecast: when each filesystem fills at the
 * rate the record shows, and whose volumes are growing fastest. Read every
 * five minutes — a line through a week does not move faster — and drawn only
 * when the host records history; with history off, the Storage charts above
 * already say so.
 */
export function CapacityForecastSection() {
  const state = usePoll(
    (signal) => get<CapacityForecast>("/system/metrics/capacity", undefined, signal),
    300_000,
    [],
  )
  const forecast = state.data
  if (!forecast || forecast.status !== "available") return null
  return (
    <Section
      title="Capacity forecast"
      actions={<InfoTip label="How the forecast is worked out">{FORECAST_RULE}</InfoTip>}
    >
      <div className="grid gap-8 lg:grid-cols-2 [&>*]:min-w-0">
        <Panel plain>
          <PanelHeader title="Days until full" />
          <PanelBody>
            {forecast.mounts.length === 0 ? (
              <EmptyNote className="px-0 py-2 text-left">
                No filesystem has been recorded yet.
              </EmptyNote>
            ) : (
              <ul aria-label="Filesystem forecasts" className="grid min-w-0 gap-y-5">
                {forecast.mounts.map((mount) => (
                  <MountRow key={mount.mountpoint} mount={mount} />
                ))}
              </ul>
            )}
          </PanelBody>
        </Panel>
        <Panel plain>
          <PanelHeader title="Fastest-growing apps" />
          <PanelBody>
            <AppList apps={forecast.apps} intervalSeconds={forecast.volumeIntervalSeconds} />
          </PanelBody>
        </Panel>
      </div>
    </Section>
  )
}

/**
 * One filesystem: its date, or that it is not filling, at the row's end in
 * the tone it deserves; the bar of how full it is now; and the line's
 * growth, its confidence and what it was fitted to.
 */
function MountRow({ mount }: { mount: MountForecast }) {
  const tone = mountTone(mount)
  const fit = [growthText(mount.growthBytesPerDay), confidenceText(mount.confidence, mount.r2)]
    .filter((part) => part && part !== "—")
    .join(" · ")
  return (
    <li className="min-w-0 space-y-2">
      <div className="flex min-w-0 items-baseline justify-between gap-3">
        <span className="truncate text-body font-medium">{mount.mountpoint}</span>
        <span
          className={cn(
            "numeric shrink-0 text-title font-semibold tracking-tight",
            tone === "warning" && "text-warning",
            tone === "danger" && "text-destructive",
            tone === "default" && mount.status !== "filling" && "text-muted-foreground",
          )}
          title={
            mount.fullAt
              ? `At this rate, full around ${new Date(mount.fullAt).toLocaleDateString()}`
              : undefined
          }
        >
          {mountVerdict(mount)}
        </span>
      </div>
      {/* The bar is how full it is now, in the tone fullness takes on every
          other page; when it fills is the verdict's colour, above it. */}
      <Meter
        value={mount.usedPercent}
        tone={utilisationTone(mount.usedPercent)}
        label={`${mount.mountpoint} used`}
        className="h-2"
      />
      <div className="numeric flex min-w-0 flex-wrap justify-between gap-x-3 gap-y-1 text-hint text-muted-foreground">
        <span className="truncate">
          {percent(mount.usedPercent, 0)} of {bytes(mount.totalBytes)}
          {fit && ` · ${fit}`}
        </span>
        <span className="shrink-0">{basisText(mount.points, mount.from, mount.to)}</span>
      </div>
    </li>
  )
}

/**
 * The deployments whose volumes grow fastest, each a way to its project,
 * with its size now and its growth a day. A volume is sampled every quarter
 * hour, so until the record holds six hours a row says it is still measuring.
 */
function AppList({ apps, intervalSeconds }: { apps: AppGrowth[]; intervalSeconds: number }) {
  if (apps.length === 0)
    return (
      <EmptyNote className="px-0 py-2 text-left">
        No deployment owns a volume. A managed volume&apos;s size is recorded
        {intervalSeconds > 0 ? ` every ${Math.round(intervalSeconds / 60)} minutes` : ""} once one
        does.
      </EmptyNote>
    )
  return (
    <RowList aria-label="Fastest-growing apps">
      {growingApps(apps).map((app) => {
        const volumes = app.volumes.map((volume) => volume.name)
        const fastest = app.volumes[0]
        return (
          <Row
            key={app.environmentId}
            href={`/deploy/${app.projectId}`}
            title={
              <span className="flex min-w-0 items-baseline gap-2">
                <span className="truncate">{app.projectName}</span>
                {app.environmentName !== "production" && <Tag>{app.environmentName}</Tag>}
              </span>
            }
            subtitle={
              <span className="numeric">
                {bytes(app.sizeBytes)} in{" "}
                {volumes.length === 1 ? volumes[0] : `${volumes.length} volumes`}
                {fastest?.confidence && ` · ${confidenceText(fastest.confidence, fastest.r2)}`}
              </span>
            }
            trailing={
              <span
                className={cn(
                  "numeric shrink-0 text-body font-medium",
                  app.growthBytesPerDay === null && "text-muted-foreground",
                )}
              >
                {app.growthBytesPerDay === null ? "measuring" : growthText(app.growthBytesPerDay)}
              </span>
            }
          />
        )
      })}
    </RowList>
  )
}
