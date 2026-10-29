"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, ShieldOff } from "@/components/icons"
import { get, post } from "@/lib/api"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  DeploymentEngineRun,
  ImageScan,
  ImageScanReading,
  VulnerabilityFinding,
} from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  SEVERITIES,
  ageWords,
  countAtLeast,
  floorWords,
  inFlight,
  scanVerdict,
  severityTone,
  shortDigest,
  vulnerable,
} from "@/components/deploy/vulnerabilities-model"

/** How many fixable findings the panel lists before "Show all". */
const LISTED = 6

/**
 * The live release's image, read for known vulnerabilities (`deploy/
 * image_scan.go`): what the scanner found at each severity, the findings a
 * published fix exists for at or above the environment's floor, and whether
 * a base the release was pinned on has a newer image under its tag — which,
 * with a fixable finding, is the alert's condition and the one thing the
 * reader can act on here: rebuild the same commit, which re-pins the base.
 *
 * The rebuild is the runs route's own force_build, so it is queued, checked
 * and audited as any build is; the panel only says which run to ask for. The
 * terms — scanning on or off, and the floor — are Build settings.
 */
export function Vulnerabilities({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const { can } = useAuth()
  const router = useRouter()
  const base = `/deploy/${projectId}/environments/${environmentId}`
  const [fast, setFast] = useState(false)
  const poll = usePoll(
    async (signal) => {
      const reading = await get<ImageScanReading>(`${base}/vulnerabilities`, undefined, signal)
      setFast(inFlight(reading.scan))
      return reading
    },
    fast ? 5000 : 60000,
    [base],
    { enabled: environmentId > 0 },
  )
  const [asking, setAsking] = useState<"scan" | "rebuild">()
  const [all, setAll] = useState(false)
  const reading = poll.data
  const scan = reading?.scan ?? null

  const scanNow = async () => {
    setAsking("scan")
    try {
      await post(`${base}/vulnerabilities/scan`, {})
      setFast(true)
      poll.refresh()
    } catch (error) {
      notify.error("Could not start the scan", error)
    } finally {
      setAsking(undefined)
    }
  }
  const rebuild = async () => {
    if (!reading?.rebuild) return
    setAsking("rebuild")
    try {
      const run = await post<DeploymentEngineRun>(`${base}/runs`, reading.rebuild)
      router.push(`/deploy/${projectId}/runs/${run.id}`)
    } catch (error) {
      notify.error("Could not start the rebuild", error)
      setAsking(undefined)
    }
  }

  const verdict = reading ? scanVerdict(reading) : undefined
  const canScan =
    can("service.control") && reading?.available && reading.policy.enabled && reading.releaseId
  // A rebuild is the command when it would re-pin a moved base; for a build
  // whose bases the dashboard does not pin (a Dockerfile, Compose) a fixable
  // finding is reason enough to offer one, since the build pulls them again.
  const rebuildMode = rebuildOffer(reading, scan)
  const offerRebuild = Boolean(rebuildMode && can("service.control"))

  return (
    <Panel plain id="vulnerabilities" className="scroll-mt-6">
      <PanelHeader
        title="Vulnerabilities"
        actions={
          <>
            {verdict && (
              <Status
                tone={verdict.tone}
                icon={verdict.tone === "danger" ? ShieldOff : undefined}
                label={verdict.line}
                className="max-sm:hidden"
              />
            )}
            {canScan && (
              <Button
                size="sm"
                variant="outline"
                disabled={Boolean(asking) || inFlight(scan)}
                pending={asking === "scan"}
                onClick={() => void scanNow()}
              >
                {inFlight(scan) ? "Scanning…" : "Scan now"}
              </Button>
            )}
            {offerRebuild && (
              <Button
                size="sm"
                variant={rebuildMode === "command" ? undefined : "outline"}
                title={
                  rebuildMode === "command"
                    ? undefined
                    : "This build's bases are not pinned here; a rebuild pulls them again"
                }
                disabled={Boolean(asking)}
                pending={asking === "rebuild"}
                onClick={() => void rebuild()}
              >
                {asking === "rebuild" ? "Rebuilding…" : "Rebuild"}
              </Button>
            )}
          </>
        }
      />
      <PanelBody>
        {poll.error && !reading && <ErrorState error={poll.error} onRetry={poll.refresh} />}
        {poll.loading && !reading && <LoadingRows rows={1} />}
        {reading && (
          <VulnerabilityBody
            reading={reading}
            scan={scan}
            all={all}
            onShowAll={() => setAll(true)}
            settingsHref={`/deploy/${projectId}/settings/build#vulnerabilities`}
          />
        )}
      </PanelBody>
    </Panel>
  )
}

/** Whether the panel offers a rebuild, and as the command or as a verb. */
function rebuildOffer(
  reading: ImageScanReading | undefined,
  scan: ImageScan | null,
): "command" | "verb" | undefined {
  if (!reading?.rebuild || !scan || scan.state !== "scanned") return undefined
  if (vulnerable(scan)) return "command"
  const unpinned = scan.baseUpdates.length === 0 && scan.rebuildable
  return unpinned && scan.summary.fixable.length > 0 ? "verb" : undefined
}

function VulnerabilityBody({
  reading,
  scan,
  all,
  onShowAll,
  settingsHref,
}: {
  reading: ImageScanReading
  scan: ImageScan | null
  all: boolean
  onShowAll: () => void
  settingsHref: string
}) {
  if (!reading.policy.enabled)
    return (
      <EmptyNote className="px-0 py-4 text-left">
        Scanning is off for this environment.{" "}
        <SettingsLink href={settingsHref}>Turn it on in Build settings</SettingsLink>
      </EmptyNote>
    )
  if (!reading.available)
    return <EmptyNote className="px-0 py-4 text-left">{reading.reason}</EmptyNote>
  if (!reading.releaseId)
    return (
      <EmptyNote className="px-0 py-4 text-left">
        The first release is scanned once it is live.
      </EmptyNote>
    )
  if (!scan || (!scan.finishedAt && scan.state !== "failed"))
    return (
      <EmptyNote className="px-0 py-4 text-left">
        {inFlight(scan)
          ? "The live release is being scanned. The first scan also downloads the vulnerability database, which takes a minute."
          : "The live release has not been scanned yet."}
      </EmptyNote>
    )
  if (scan.state === "failed" && scan.targets.length === 0)
    return <EmptyNote className="px-0 py-4 text-left">{scan.reason}</EmptyNote>

  const { summary } = scan
  const fixable = summary.fixable
  const listed = all ? fixable : fixable.slice(0, LISTED)
  const outdated = scan.baseUpdates.filter((update) => update.state === "outdated")
  const endOfLife = scan.targets.find((target) => target.kind === "release" && target.endOfLife)
  const failedTargets = scan.targets.filter((target) => target.state === "failed")
  const fixableAtFloor = countAtLeast(summary.fixableCounts, scan.floor)

  return (
    <div className="space-y-4">
      <StatGrid columns={4} dense className="[&_[data-slot=stat-tile]]:py-3">
        {SEVERITIES.map((severity) => {
          const key = severity.toLowerCase() as "critical" | "high" | "medium" | "low"
          const count = summary.counts[key]
          const withFix = summary.fixableCounts[key]
          return (
            <StatTile
              key={severity}
              label={severity.charAt(0) + severity.slice(1).toLowerCase()}
              value={<span className="numeric">{count}</span>}
              tone={
                count === 0
                  ? undefined
                  : severityTone(severity) === "danger"
                    ? "danger"
                    : severityTone(severity) === "warning"
                      ? "warning"
                      : undefined
              }
              hint={count === 0 ? "none found" : `${withFix} with a fix`}
            />
          )
        })}
      </StatGrid>

      {scan.state === "failed" && (
        <Notice tone="warning" title="The latest scan did not finish">
          {scan.reason} The counts above are from the scan before it.
        </Notice>
      )}
      {vulnerable(scan) && outdated.length > 0 && (
        <Notice tone="danger" icon={ShieldOff} title="A rebuild is likely to fix some of these">
          {outdated.map((update) => (
            <span key={update.reference} className="block">
              <span className="font-mono">{update.reference}</span> now names a newer image (
              <span className="font-mono">{shortDigest(update.current)}</span>) than the one release{" "}
              {scan.releaseNumber} was pinned on (
              <span className="font-mono">{shortDigest(update.pinned)}</span>).
            </span>
          ))}
          {reading.rebuild
            ? "Rebuilding builds the same commit without the cache, which pins the base again."
            : reading.rebuildReason}
        </Notice>
      )}
      {endOfLife && (
        <Notice tone="warning" title={`${endOfLife.os} no longer gets security fixes`}>
          The distribution this image runs on has reached its end of life, so what the scanner
          reports is a floor: nothing found since will ever be fixed in it. A newer base is the only
          way forward.
        </Notice>
      )}

      {fixable.length > 0 ? (
        <section aria-label="Fixable vulnerabilities" className="min-w-0 space-y-1">
          <div className="flex min-w-0 items-baseline justify-between gap-3 border-b border-hairline pb-2">
            <h3 className="text-body leading-tight font-semibold tracking-tight">
              With a fix, {floorWords(scan.floor)}
            </h3>
            <span className="numeric shrink-0 text-hint text-muted-foreground">
              {fixableAtFloor}
            </span>
          </div>
          <ul className="divide-y divide-hairline">
            {listed.map((finding) => (
              <FindingLine
                key={`${finding.id}:${finding.package}:${finding.target}`}
                finding={finding}
              />
            ))}
          </ul>
          {!all && fixable.length > LISTED && (
            <Button size="sm" variant="ghost" className="-ml-2" onClick={onShowAll}>
              Show all {fixable.length}
            </Button>
          )}
        </section>
      ) : (
        <EmptyNote className="px-0 py-2 text-left">
          {summary.unfixed.length > 0
            ? `${plural(summary.unfixed.length, "finding")} ${floorWords(scan.floor)}, none with a published fix yet.`
            : `Nothing ${floorWords(scan.floor)} was found.`}
        </EmptyNote>
      )}

      {failedTargets.length > 0 && (
        <p className="text-hint text-muted-foreground">
          Not read:{" "}
          {failedTargets.map((target, index) => (
            <span key={target.reference + index}>
              {index > 0 && "; "}
              <span className="font-mono">{target.reference}</span> — {target.reason}
            </span>
          ))}
        </p>
      )}
      <p className="text-hint text-muted-foreground">
        Release {scan.releaseNumber} read{" "}
        <span title={timestamp(scan.finishedAt)}>{relativeTime(scan.finishedAt)}</span> with{" "}
        {scan.scanner ? `Trivy ${scan.scanner}` : reading.scanner}
        {scan.databaseAgeHours !== undefined &&
          `, against a vulnerability database ${ageWords(scan.databaseAgeHours)}`}
        {scan.targets.some((target) => target.kind === "base") &&
          ` · ${plural(
            scan.targets.filter((target) => target.kind === "base").length,
            "base image",
          )} read beside it`}
        . <SettingsLink href={settingsHref}>Scanning terms</SettingsLink>
      </p>
    </div>
  )
}

function FindingLine({ finding }: { finding: VulnerabilityFinding }) {
  return (
    <li className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-0.5 py-2">
      <span className="flex min-w-0 items-baseline gap-2">
        <Status
          tone={severityTone(finding.severity)}
          label={finding.severity.toLowerCase()}
          className="w-16 shrink-0"
        />
        {finding.url ? (
          <a
            href={finding.url}
            target="_blank"
            rel="noreferrer noopener"
            className="shrink-0 rounded-sm font-mono text-xs focus-ring hover:underline"
          >
            {finding.id}
          </a>
        ) : (
          <span className="shrink-0 font-mono text-xs">{finding.id}</span>
        )}
        <span className="min-w-0 truncate text-body" title={finding.title}>
          {finding.package}
        </span>
      </span>
      <span className="flex shrink-0 items-baseline gap-2 text-hint text-muted-foreground">
        {finding.fromBase && <Tag title="The base image carries it too">base</Tag>}
        <span className="font-mono">
          {finding.installed} → {finding.fixed}
        </span>
      </span>
      <span className="col-span-2 min-w-0 truncate pl-[4.5rem] text-hint text-muted-foreground">
        {finding.title ? `${finding.title} · ` : ""}
        {finding.target}
      </span>
    </li>
  )
}

function SettingsLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Link
      href={href}
      className="inline-flex items-center gap-1 rounded-sm font-medium text-foreground focus-ring hover:underline"
    >
      {children} <ArrowRight className="size-3" />
    </Link>
  )
}
