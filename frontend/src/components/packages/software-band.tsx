"use client"

import { BarReading } from "@/components/bar-reading"
import { ArrowCircleUp, ShieldOff } from "@/components/icons"
import { HUE, LiveBytes } from "@/components/overview/readings"
import { PackageMark, VersionTo } from "@/components/packages/marks"
import { softwareGroups } from "@/components/packages/inventory"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, plural } from "@/lib/format"
import type { PackageInventory, UpdateReport } from "@/lib/types"

/** How many software groups and pending packages each block names. */
const SHOWN = 5

/** The tile `PackageMark` draws, down to what the strip has room for. */
const MARK = "size-5 rounded-md [&_img]:size-3.5 [&_svg]:size-3"

const OTHER_UPDATE = "color-mix(in oklab, var(--brand) 50%, transparent)"

/**
 * Who occupies the disk, and what is waiting to be upgraded. The disk is a bar
 * of the five largest software groups, a press narrowing the inventory to one.
 * The updates are a bar with a span for each pending package, security first
 * in amber, the first five of them named and each opening its package, with
 * the upgrades themselves at the strip's end. Both read from polls minutes
 * apart, so neither has a trail nor is marked live.
 *
 * Both blocks are there before the inventory has landed, so what is under them
 * does not move when it does.
 */
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
  /** Nothing until the first read has landed. */
  inventory?: PackageInventory
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
  const packages = inventory?.packages ?? []
  const groups = softwareGroups(packages)
  const measured = groups.reduce((n, g) => n + g.size, 0)
  const total = Math.max(measured, inventory?.totalSize ?? 0)
  const shown = groups.filter((g) => g.size > 0).slice(0, SHOWN)
  const rest = Math.max(total - shown.reduce((n, g) => n + g.size, 0), 0)

  const failure = error?.message ?? report?.error
  const pending = [...(report?.packages ?? [])].sort(
    (a, b) => Number(b.security) - Number(a.security) || a.name.localeCompare(b.name),
  )
  const filtering = Boolean(report?.securityFiltering)
  const flagged = (p: { security?: boolean }) => filtering && Boolean(p.security)
  // Security sorts first, so what the five named leave out is the rest of it
  // and then the others; only the first is worth a span of its own colour.
  const moreSecurity = pending.slice(SHOWN).filter(flagged).length
  const moreOthers = Math.max(pending.length - SHOWN - moreSecurity, 0)

  return (
    <div data-slot="software-band" className="grid min-w-0 gap-x-10 gap-y-6 lg:grid-cols-2">
      <BarReading
        title="On disk"
        label="Installed software on disk"
        list="Largest software"
        hue={HUE.disk}
        rest={rest}
        restLabel="Other packages"
        format={(v) => bytes(v)}
        figure={
          inventory &&
          !inventory.error && (
            <span className="numeric flex h-7 items-center gap-1 text-hint text-muted-foreground">
              {total > 0 ? (
                <>
                  <span className="font-medium text-foreground">
                    <LiveBytes value={total} />
                  </span>
                  installed
                </>
              ) : (
                "Size not reported"
              )}
            </span>
          )
        }
        message={
          !inventory ? (
            <TextShimmer>Reading the installed packages…</TextShimmer>
          ) : inventory.error ? (
            "Could not read the package database."
          ) : groups.length === 0 ? (
            "No packages are installed."
          ) : undefined
        }
        note={
          packages.some((p) => !p.size) && (
            <span className="truncate" title="Sizes are not reported for every package.">
              Sizes are not reported for every package.
            </span>
          )
        }
        parts={(total > 0 ? shown : groups.slice(0, SHOWN)).map((g) => {
          const pressed = selected === g.key
          return {
            key: g.key,
            value: g.size,
            mark: <PackageMark name={g.sample.name} section={g.sample.section} className={MARK} />,
            name: g.name,
            detail: plural(g.count, "package"),
            figure: g.size > 0 ? <LiveBytes value={g.size} /> : "—",
            label: g.size > 0 ? `${g.name} ${bytes(g.size)}` : g.name,
            press: {
              label: `Only ${g.name} packages`,
              pressed,
              onPress: () => onSelect(pressed ? "" : g.key),
            },
          }
        })}
      />

      <BarReading
        title="Updates waiting"
        label="Updates waiting"
        list="Packages waiting"
        fixed
        rest={moreOthers}
        restLabel="Other updates"
        format={(n) => plural(n, "package")}
        figure={
          report &&
          !failure && (
            <span
              className="flex h-7 items-center"
              title={
                filtering ? undefined : `${inventory?.manager} publishes no security advisory data.`
              }
            >
              <Status
                verdict={pending.length ? (report.securityCount ? "warning" : "notice") : "ok"}
                label={
                  pending.length
                    ? `${pending.length} waiting${filtering ? ` · ${report.securityCount} security` : ""}`
                    : "Up to date"
                }
              />
            </span>
          )
        }
        message={
          failure ? (
            <span role="alert">Could not read updates. {failure}</span>
          ) : !report ? (
            <TextShimmer>Reading what is behind…</TextShimmer>
          ) : pending.length === 0 ? (
            "Every installed package is at the repository’s latest version."
          ) : undefined
        }
        note={
          <>
            {canUpgrade && filtering && (report?.securityCount ?? 0) > 0 && (
              <Button
                size="xs"
                aria-label="Install security updates"
                disabled={busy}
                onClick={() => onUpgrade(true)}
              >
                <ShieldOff className="size-3.5" />
                <Words full="Install security updates" short="Security" />
              </Button>
            )}
            {canUpgrade && (
              <Button
                size="xs"
                variant="outline"
                aria-label={`Upgrade all ${pending.length}`}
                disabled={busy}
                onClick={() => onUpgrade(false)}
              >
                <ArrowCircleUp className="size-3.5" />
                <Words full={`Upgrade all ${pending.length}`} short={`All ${pending.length}`} />
              </Button>
            )}
            <Button size="xs" variant="ghost" aria-label="Review updates" onClick={onUpdates}>
              <Words full="Review updates" short="Review" />
            </Button>
          </>
        }
        parts={[
          ...pending.slice(0, SHOWN).map((p) => ({
            key: p.name,
            value: 1,
            color: flagged(p) ? "var(--warning)" : OTHER_UPDATE,
            mark: <PackageMark name={p.name} className={MARK} />,
            name: <span className="font-mono">{p.name}</span>,
            detail: <VersionTo from={p.current} to={p.candidate} security={p.security} />,
            figure: flagged(p) ? (
              <span className="text-hint font-normal text-warning">security</span>
            ) : undefined,
            label: `${p.name}${flagged(p) ? " security" : ""} ${p.current} to ${p.candidate}`,
            title: `${p.current} to ${p.candidate}`,
            press: {
              label: `Inspect update for ${p.name}`,
              opens: true,
              onPress: () => onInspect(p.name),
            },
          })),
          ...(moreSecurity > 0
            ? [
                {
                  key: "\0more-security",
                  value: moreSecurity,
                  color: "var(--warning)",
                  mark: <ShieldOff aria-hidden className="size-3.5 text-warning" />,
                  name: plural(moreSecurity, "more security update"),
                  label: plural(moreSecurity, "more security update"),
                },
              ]
            : []),
        ]}
      />
    </div>
  )
}

/**
 * A control's words at the strip's end. Beside five marks in a block half the
 * page wide the full three do not fit until the widest screens, and the strip
 * has one line to hold them.
 */
function Words({ full, short }: { full: string; short: string }) {
  return (
    <>
      <span className="hidden 2xl:inline">{full}</span>
      <span className="2xl:hidden">{short}</span>
    </>
  )
}
