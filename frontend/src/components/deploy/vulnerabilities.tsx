"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Box, ShieldOff } from "@/components/icons"
import { get, post } from "@/lib/api"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import { notify } from "@/lib/toast"
import type {
  DeploymentEngineRun,
  ImageScan,
  ImageScanReading,
  VulnerabilityCounts,
  VulnerabilityFinding,
} from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { ProductGlyph, hasProductLogo, imageProduct } from "@/components/product-logo"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { WireMark, WireNode } from "@/components/deploy/wire"
import {
  SEVERITIES,
  SEVERITY_COLOR,
  ageWords,
  cleanTitle,
  countAtLeast,
  countsLine,
  findingProduct,
  floorWords,
  groupFindings,
  inFlight,
  scanLead,
  scanVerdict,
  severityTone,
  shortDigest,
  vulnerable,
  type FindingGroup,
} from "@/components/deploy/vulnerabilities-model"

/** How many packages the panel lists before "Show all". */
const LISTED = 5

const WORST_TEXT = {
  danger: "text-destructive",
  warning: "text-warning",
} as const

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
  const groups = groupFindings(fixable)
  const listed = all ? groups : groups.slice(0, LISTED)
  const outdated = scan.baseUpdates.filter((update) => update.state === "outdated")
  const endOfLife = scan.targets.find((target) => target.kind === "release" && target.endOfLife)
  const failedTargets = scan.targets.filter((target) => target.state === "failed")
  const fixableAtFloor = countAtLeast(summary.fixableCounts, scan.floor)
  const bases = scan.targets.filter((target) => target.kind === "base")
  const lead = scanLead(scan)
  const verdict = scanVerdict(reading)

  return (
    <div className="space-y-4 pt-1">
      <WireNode
        mark={
          <span className="relative flex">
            <WireMark tone="logo" size="md">
              <ProductGlyph id="trivy" />
            </WireMark>
            <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background">
              <StatusDot tone={verdict.tone} />
            </span>
          </span>
        }
        title={lead.title}
        hint={
          <>
            {lead.fix && `${lead.fix}. `}
            Read by {scan.scanner ? `Trivy ${scan.scanner}` : reading.scanner}{" "}
            <span title={timestamp(scan.finishedAt)}>{relativeTime(scan.finishedAt)}</span>
            {scan.databaseAgeHours !== undefined &&
              `, against a vulnerability database ${ageWords(scan.databaseAgeHours)}`}
            . <SettingsLink href={settingsHref}>Scanning terms</SettingsLink>
          </>
        }
      />

      <SeverityBar counts={summary.counts} />

      {bases.length > 0 && (
        <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
          <span>Built on</span>
          {bases.map((base) => (
            <span key={base.reference} className="inline-flex min-w-0 items-center gap-1.5">
              <BaseGlyph reference={base.reference} />
              <span className="truncate font-mono">{base.reference}</span>
            </span>
          ))}
        </p>
      )}

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
              {plural(fixableAtFloor, "finding")} · {groups.length}{" "}
              {groups.length === 1 ? "upgrade" : "upgrades"}
            </span>
          </div>
          <ul className="divide-y divide-hairline">
            {listed.map((group) => (
              <PackageRow
                key={`${group.package}:${group.installed}:${group.target}`}
                group={group}
              />
            ))}
          </ul>
          {!all && groups.length > LISTED && (
            <Button size="sm" variant="ghost" className="-ml-2" onClick={onShowAll}>
              Show all {groups.length}
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
    </div>
  )
}

/**
 * How the scan's findings split, as one bar and its key in the words. Four
 * figures in a row said the same thing and gave a zero the room of a six; here
 * the proportions are the reading, and a severity with nothing in it is absent.
 */
function SeverityBar({ counts }: { counts: VulnerabilityCounts }) {
  const parts = SEVERITIES.map((severity) => ({
    severity,
    count: counts[severity.toLowerCase() as "critical" | "high" | "medium" | "low"],
  })).filter((part) => part.count > 0)
  const total = parts.reduce((sum, part) => sum + part.count, 0)
  return (
    <div className="min-w-0 space-y-2">
      <div
        role="img"
        aria-label={total > 0 ? `By severity: ${countsLine(counts)}` : "Nothing found"}
        data-testid="severity-bar"
        className={cn(
          "flex h-2.5 animate-rise gap-0.5 overflow-hidden rounded-sm",
          total === 0 ? "bg-success/70" : "bg-meter-track",
        )}
      >
        {parts.map((part) => (
          <span
            key={part.severity}
            className="block h-full [transition:width_700ms_ease-out]"
            style={{
              width: `${(part.count / total) * 100}%`,
              minWidth: 4,
              background: SEVERITY_COLOR[part.severity],
            }}
          />
        ))}
      </div>
      {parts.length > 0 && (
        <p className="numeric flex flex-wrap gap-x-4 gap-y-1 text-hint text-muted-foreground">
          {parts.map((part) => (
            <span key={part.severity} className="inline-flex items-center gap-1.5">
              <span
                aria-hidden
                className="size-2 rounded-[2px]"
                style={{ background: SEVERITY_COLOR[part.severity] }}
              />
              <span className="font-medium text-foreground">{part.count}</span>
              {part.severity.toLowerCase()}
            </span>
          ))}
        </p>
      )}
    </div>
  )
}

function BaseGlyph({ reference }: { reference: string }) {
  const product = imageProduct(reference)
  return hasProductLogo(product) ? (
    <ProductGlyph id={product} />
  ) : (
    <Box aria-hidden className="size-3.5 shrink-0" />
  )
}

/** One upgrade: the package on its ecosystem's mark, and the advisories it answers. */
function PackageRow({ group }: { group: FindingGroup }) {
  const product = findingProduct(group)
  const tone = severityTone(group.worst)
  return (
    <li className="min-w-0 py-3" data-slot="vulnerable-package">
      <div className="flex min-w-0 items-center gap-3">
        <WireMark tone="logo" size="sm">
          {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Box aria-hidden />}
        </WireMark>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
            <span className="truncate text-body font-medium">{group.package}</span>
            <span
              className={cn(
                "numeric text-hint font-medium",
                tone === "danger" || tone === "warning"
                  ? WORST_TEXT[tone]
                  : "text-muted-foreground",
              )}
            >
              {countsLine(group.counts)}
            </span>
            {group.fromBase && <Tag title="The base image carries it too">base image</Tag>}
          </div>
          <p className="truncate text-hint text-muted-foreground" title={group.target}>
            <span className="font-mono">
              {group.installed} → {group.fix}
            </span>{" "}
            · {group.target}
          </p>
        </div>
      </div>
      <ul className="mt-2 space-y-1 pl-12">
        {group.findings.map((finding) => (
          <FindingLine key={finding.id} finding={finding} />
        ))}
      </ul>
    </li>
  )
}

function FindingLine({ finding }: { finding: VulnerabilityFinding }) {
  const title = cleanTitle(finding.title, finding.package)
  return (
    <li className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-hint sm:flex-nowrap">
      <span title={finding.severity.toLowerCase()} className="inline-flex translate-y-[-1px]">
        <StatusDot tone={severityTone(finding.severity)} />
      </span>
      {finding.url ? (
        <a
          href={finding.url}
          target="_blank"
          rel="noreferrer noopener"
          className="shrink-0 rounded-sm font-mono focus-ring hover:underline"
        >
          {finding.id}
        </a>
      ) : (
        <span className="shrink-0 font-mono">{finding.id}</span>
      )}
      {title && (
        <span className="min-w-0 text-muted-foreground sm:truncate" title={title}>
          {title}
        </span>
      )}
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
