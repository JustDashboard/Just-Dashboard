"use client"

import { Filter, ArrowCircleUp, ShieldOff } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { LiveBytes, HUE } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { PackageMark, VersionTo } from "@/components/packages/marks"
import { softwareGroups } from "@/components/packages/inventory"
import { Status } from "@/components/status-dot"
import { ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { bytes } from "@/lib/format"
import type { PackageInventory, UpdateReport } from "@/lib/types"
import { cn } from "@/lib/utils"

const STEPS = [100, 78, 60, 44, 32]
const shade = (rank: number) => `color-mix(in oklab, ${HUE.disk} ${STEPS[rank]}%, transparent)`

/** Like Processes' workload band: who occupies the disk, and what needs attention. */
export function SoftwareBand({
  inventory,
  report,
  error,
  selected,
  onSelect,
  onInspect,
  onUpdates,
  onUpgrade,
  canUpgrade,
  busy,
}: {
  inventory: PackageInventory
  report?: UpdateReport
  error?: Error
  selected: string
  onSelect: (key: string) => void
  onInspect: (name: string) => void
  onUpdates: () => void
  onUpgrade: (security: boolean) => void
  canUpgrade: boolean
  busy: boolean
}) {
  const groups = softwareGroups(inventory.packages)
  const measured = groups.reduce((n, g) => n + g.size, 0)
  const total = Math.max(measured, inventory.totalSize ?? 0)
  const shown = groups.filter((g) => g.size > 0).slice(0, 5)
  const rest = Math.max(total - shown.reduce((n, g) => n + g.size, 0), 0)
  const pending = [...(report?.packages ?? [])].sort(
    (a, b) => Number(b.security) - Number(a.security) || a.name.localeCompare(b.name),
  )
  return (
    <div data-slot="software-band" className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2">
      <Panel plain aria-label="Installed software on disk">
        <PanelHeader
          title="On disk"
          className="min-h-10"
          actions={
            <span className="numeric text-body font-medium" style={{ color: HUE.disk }}>
              {total > 0 ? <LiveBytes value={total} /> : "Size not reported"}
            </span>
          }
        />
        <PanelBody className="space-y-3">
          {total > 0 && (
            <div
              role="img"
              aria-label={`Installed size: ${shown.map((g) => `${g.name} ${bytes(g.size)}`).join(", ")}${rest ? `, other packages ${bytes(rest)}` : ""}`}
              className="flex h-3 overflow-hidden rounded-sm bg-meter-track"
            >
              {shown.map((g, rank) => (
                <span
                  key={g.key}
                  title={`${g.name}: ${bytes(g.size)}`}
                  className="h-full shrink-0 border-r border-background transition-[width] duration-700 motion-reduce:transition-none"
                  style={{ width: `${(g.size / total) * 100}%`, background: shade(rank) }}
                />
              ))}
              {rest > 0 && (
                <span
                  title={`Other packages: ${bytes(rest)}`}
                  className="h-full bg-muted-foreground/25"
                  style={{ width: `${(rest / total) * 100}%` }}
                />
              )}
            </div>
          )}
          <ul className="-mx-2">
            {(total > 0 ? shown : groups.slice(0, 5)).map((g, rank) => (
              <li key={g.key}>
                <button
                  type="button"
                  aria-pressed={selected === g.key}
                  aria-label={`Only ${g.name} packages`}
                  onClick={() => onSelect(selected === g.key ? "" : g.key)}
                  className={cn(
                    "group flex min-h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors",
                    selected === g.key ? "bg-accent" : "hover:bg-row-hover",
                  )}
                >
                  <span
                    aria-hidden
                    className="h-3 w-0.5 shrink-0 rounded-full"
                    style={{ background: shade(rank) }}
                  />
                  <PackageMark name={g.sample.name} section={g.sample.section} className="size-5" />
                  <span className="min-w-0 truncate font-medium">{g.name}</span>
                  <span className="numeric shrink-0 text-hint text-muted-foreground">
                    {g.count} package{g.count === 1 ? "" : "s"}
                  </span>
                  <span className="numeric ml-auto shrink-0 font-medium">
                    {g.size > 0 ? <LiveBytes value={g.size} /> : "—"}
                  </span>
                  <Filter
                    aria-hidden
                    className={cn(
                      "size-3.5 shrink-0 text-muted-foreground",
                      selected === g.key ? "text-foreground" : rowReveal(),
                    )}
                  />
                </button>
              </li>
            ))}
          </ul>
          {inventory.packages.some((p) => !p.size) && (
            <p className="text-hint text-muted-foreground">
              Sizes are not reported for every package.
            </p>
          )}
        </PanelBody>
      </Panel>
      <Panel plain aria-label="Updates waiting">
        <PanelHeader
          title="Updates waiting"
          className="min-h-10"
          actions={
            report &&
            !error &&
            !report.error && (
              <Status
                verdict={pending.length ? (report.securityCount ? "warning" : "notice") : "ok"}
                label={
                  pending.length
                    ? `${pending.length} waiting${report.securityFiltering ? ` · ${report.securityCount} security` : ""}`
                    : "Up to date"
                }
              />
            )
          }
        />
        <PanelBody className="space-y-3">
          {error || report?.error ? (
            <ErrorState error={error ?? new Error(report?.error)} />
          ) : !report ? (
            <LoadingRows rows={3} />
          ) : (
            <>
              {pending.length > 0 ? (
                <>
                  <div
                    role="img"
                    aria-label={
                      report.securityFiltering
                        ? `${report.securityCount} security updates, ${pending.length - report.securityCount} other updates`
                        : `${pending.length} updates; security advisory data unavailable`
                    }
                    className="flex h-3 overflow-hidden rounded-sm bg-meter-track"
                  >
                    {report.securityFiltering && report.securityCount > 0 && (
                      <span
                        className="h-full bg-warning"
                        style={{ width: `${(report.securityCount / pending.length) * 100}%` }}
                      />
                    )}
                    <span
                      className="h-full bg-brand/50"
                      style={{
                        width: `${((pending.length - (report.securityFiltering ? report.securityCount : 0)) / pending.length) * 100}%`,
                      }}
                    />
                  </div>
                  <ul className="-mx-2">
                    {pending.slice(0, 5).map((p) => (
                      <li key={p.name}>
                        <button
                          type="button"
                          aria-label={`Inspect update for ${p.name}`}
                          onClick={() => onInspect(p.name)}
                          className="flex min-h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2 text-left text-body focus-ring-inset transition-colors hover:bg-row-hover"
                        >
                          <PackageMark name={p.name} className="size-5" />
                          <span className="min-w-0 flex-1 truncate font-mono">{p.name}</span>
                          {p.security && <span className="text-hint text-warning">security</span>}
                          <VersionTo
                            from={p.current}
                            to={p.candidate}
                            security={p.security}
                            className="max-w-[45%] truncate text-hint"
                          />
                        </button>
                      </li>
                    ))}
                  </ul>
                </>
              ) : (
                <p className="py-3 text-body text-muted-foreground">
                  Every installed package is at the repository&rsquo;s latest version.
                </p>
              )}
              {!report.securityFiltering && (
                <p className="text-hint text-muted-foreground">
                  {inventory.manager} publishes no security advisory data.
                </p>
              )}
              <div className="flex flex-wrap items-center gap-2">
                {canUpgrade && report.securityFiltering && report.securityCount > 0 && (
                  <Button size="sm" disabled={busy} onClick={() => onUpgrade(true)}>
                    <ShieldOff className="size-4" />
                    Install security updates
                  </Button>
                )}
                {canUpgrade && pending.length > 0 && (
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy}
                    onClick={() => onUpgrade(false)}
                  >
                    <ArrowCircleUp className="size-4" />
                    Upgrade all {pending.length}
                  </Button>
                )}
                {pending.length > 0 && (
                  <Button size="sm" variant="ghost" onClick={onUpdates}>
                    Review updates
                  </Button>
                )}
              </div>
            </>
          )}
        </PanelBody>
      </Panel>
    </div>
  )
}
