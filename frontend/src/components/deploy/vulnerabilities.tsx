"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Box, ShieldOff } from "@/components/icons"
import { get, post } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
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
import { EmptyNote, ErrorState, Notice } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { ProductGlyph, hasProductLogo, imageProduct } from "@/components/product-logo"
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { WireMark, WireNode } from "@/components/wire/wire"
import {
  cleanTitle,
  countsLine,
  dependencyWords,
  findingProduct,
  inFlight,
  overviewLead,
  scanActions,
  scanVerdict,
  severityTone,
  upgradeTitle,
  type FindingGroup,
  type ScanAction,
} from "@/components/deploy/vulnerabilities-model"

/** How many rows a list draws before "Show all". */
export const LISTED = 5

const WORST_TEXT = {
  danger: "text-destructive",
  warning: "text-warning",
} as const

/**
 * The live release's scan as a page reads it (`deploy/image_scan.go`), with
 * its two verbs: Scan now, and the rebuild the runs route already takes
 * (force_build at the live release's commit), so it is queued, checked and
 * audited as any build is.
 */
export function useScanReading(projectId: number, environmentId: number) {
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
  const canScan = Boolean(
    can("service.control") && reading?.available && reading.policy.enabled && reading.releaseId,
  )
  // A rebuild is offered when it fixes something: a base whose tag names a
  // newer image, or a package the build installs from its distribution. Not
  // while scanning is off: the scan it would act on is no longer kept up.
  const offerRebuild = Boolean(
    reading?.rebuild &&
    reading.policy.enabled &&
    scan?.state === "scanned" &&
    can("service.control") &&
    scanActions(scan).some((action) => action.kind === "rebuild"),
  )
  return { poll, reading, scan, asking, scanNow, rebuild, canScan, offerRebuild }
}

export type ScanReadingState = ReturnType<typeof useScanReading>

/** Scan now, as a header's verb. */
export function ScanNowButton({ state }: { state: ScanReadingState }) {
  if (!state.canScan) return null
  return (
    <Button
      size="sm"
      variant="outline"
      disabled={Boolean(state.asking) || inFlight(state.scan)}
      pending={state.asking === "scan"}
      onClick={() => void state.scanNow()}
    >
      {inFlight(state.scan) ? "Scanning…" : "Scan now"}
    </Button>
  )
}

/** Rebuild, as a header's verb, when a rebuild fixes something. */
export function RebuildButton({ state }: { state: ScanReadingState }) {
  if (!state.offerRebuild) return null
  return (
    <Button
      size="sm"
      variant="outline"
      disabled={Boolean(state.asking)}
      pending={state.asking === "rebuild"}
      onClick={() => void state.rebuild()}
    >
      {state.asking === "rebuild" ? "Rebuilding…" : "Rebuild"}
    </Button>
  )
}

/**
 * The project overview's Vulnerabilities panel: what the reader can do about
 * the live release's scan, and only that. A scan counts everything Trivy
 * finds, most of it in development tools shipped with the application or a
 * base that is already the newest, so a total and a bar of severities read as
 * an alarm with nothing behind it. Here it is one sentence, then a row for
 * each upgrade or rebuild that fixes something at the floor. Everything else
 * is in the report a link away (`vulnerability-report.tsx`), under whose it
 * is. The terms — scanning on or off, and the floor — are Build settings.
 */
export function Vulnerabilities({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useScanReading(projectId, environmentId)
  const { poll, reading, scan } = state
  const verdict = reading ? scanVerdict(reading) : undefined
  const report = `/deploy/${projectId}/vulnerabilities`

  return (
    <Panel plain id="vulnerabilities" className="scroll-mt-6">
      <PanelHeader
        title="Vulnerabilities"
        link={scan?.finishedAt ? { href: report, label: "Report" } : undefined}
        actions={
          <>
            {/* Quiet when there is nothing to do: the sentence says so. */}
            {verdict && verdict.tone !== "running" && (
              <Status
                tone={verdict.tone}
                icon={verdict.tone === "danger" ? ShieldOff : undefined}
                label={verdict.line}
                className="max-sm:hidden"
              />
            )}
            <RebuildButton state={state} />
          </>
        }
      />
      <PanelBody>
        {poll.error && !reading && <ErrorState error={poll.error} onRetry={poll.refresh} />}
        {poll.loading && !reading && <LoadingRows rows={1} />}
        {reading && (
          <ScanUnread
            reading={reading}
            scan={scan}
            settingsHref={`/deploy/${projectId}/settings/build#vulnerabilities`}
          >
            {(scan) => <OverviewBody reading={reading} scan={scan} report={report} />}
          </ScanUnread>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * What a page says before there is a scan to read — scanning off, no
 * scanner, no release yet, the first scan still running — and the scan
 * itself once there is one.
 */
export function ScanUnread({
  reading,
  scan,
  settingsHref,
  children,
}: {
  reading: ImageScanReading
  scan: ImageScan | null
  settingsHref: string
  children: (scan: ImageScan) => React.ReactNode
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
  return <>{children(scan)}</>
}

/** The notices a scan raises wherever it is read: a scan that did not finish, a distribution past its end. */
export function ScanNotices({ scan }: { scan: ImageScan }) {
  const endOfLife = scan.targets.find((target) => target.kind === "release" && target.endOfLife)
  return (
    <>
      {scan.state === "failed" && (
        <Notice tone="warning" title="The latest scan did not finish">
          {scan.reason} What is shown is from the scan before it.
        </Notice>
      )}
      {endOfLife && (
        <Notice tone="warning" title={`${endOfLife.os} no longer gets security fixes`}>
          The distribution this image runs on has reached its end of life, so nothing found in it
          from now on will be fixed. A newer base image is the only way forward.
        </Notice>
      )}
    </>
  )
}

/** Trivy's mark, with the verdict's dot in its corner. */
export function TrivyMark({ reading }: { reading: ImageScanReading }) {
  return (
    <span className="relative flex">
      <WireMark tone="logo" size="md">
        <ProductGlyph id="trivy" />
      </WireMark>
      <StatusDot tone={scanVerdict(reading).tone} corner="md" />
    </span>
  )
}

function OverviewBody({
  reading,
  scan,
  report,
}: {
  reading: ImageScanReading
  scan: ImageScan
  report: string
}) {
  const lead = overviewLead(scan)
  const actions = scanActions(scan)
  const listed = actions.slice(0, LISTED)
  return (
    <div className="space-y-4 pt-1">
      <WireNode
        mark={<TrivyMark reading={reading} />}
        title={lead.title}
        hint={
          <>
            {lead.where && `${lead.where} `}
            Read by {scan.scanner ? `Trivy ${scan.scanner}` : reading.scanner}{" "}
            <span title={timestamp(scan.finishedAt)}>{relativeTime(scan.finishedAt)}</span>.
          </>
        }
      />
      <ScanNotices scan={scan} />
      {listed.length > 0 && (
        <ul aria-label="What to do" className="divide-y divide-hairline">
          {listed.map((action) => (
            <ActionRow
              key={
                action.kind === "rebuild"
                  ? "rebuild"
                  : `${action.group.package}:${action.group.installed}:${action.group.path ?? action.group.target}`
              }
              action={action}
              scan={scan}
              rebuildReason={reading.rebuild ? undefined : reading.rebuildReason}
            />
          ))}
        </ul>
      )}
      {actions.length > listed.length && (
        <SettingsLink href={report}>
          {actions.length - listed.length} more in the report
        </SettingsLink>
      )}
    </div>
  )
}

/** One thing to do: the command, what it fixes, and where the package came from. */
function ActionRow({
  action,
  scan,
  rebuildReason,
}: {
  action: ScanAction
  scan: ImageScan
  /** Why this release cannot be rebuilt here (an upstream image), when it cannot. */
  rebuildReason?: string
}) {
  if (action.kind === "rebuild") {
    const outdated = scan.baseUpdates
      .filter((update) => update.state === "outdated")
      .map((update) => update.reference)
    const tone = severityTone(action.worst)
    return (
      <li className="flex min-w-0 items-center gap-3 py-3" data-slot="scan-action">
        <WireMark tone="logo" size="sm">
          {outdated[0] ? <BaseGlyph reference={outdated[0]} /> : <Box aria-hidden />}
        </WireMark>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
            <span className="text-body font-medium">
              {action.newerBase && outdated.length > 0
                ? `Rebuild on the newer ${outdated.join(" and ")}`
                : "Rebuild to install the fixed packages"}
            </span>
            <WorstCounts tone={tone}>{countsLine(action.counts)}</WorstCounts>
          </div>
          <p className="text-hint text-muted-foreground">
            Fixes {packageList(action.packages)}
            {rebuildReason && `. ${rebuildReason}`}
          </p>
        </div>
      </li>
    )
  }
  const { group } = action
  const from = dependencyWords(group)
  return (
    <li className="flex min-w-0 items-center gap-3 py-3" data-slot="scan-action">
      <PackageMark group={group} />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
          <span className="text-body font-medium">{upgradeTitle(group)}</span>
          <WorstCounts tone={severityTone(group.worst)}>{countsLine(group.counts)}</WorstCounts>
        </div>
        <p className="truncate text-hint text-muted-foreground" title={group.path ?? group.target}>
          <span className="font-mono">{group.installed}</span> now
          {from && ` · ${from}`}
        </p>
      </div>
    </li>
  )
}

function packageList(packages: string[]) {
  if (packages.length <= 3) return packages.join(", ")
  return `${packages.slice(0, 3).join(", ")} and ${packages.length - 3} more`
}

function WorstCounts({ tone, children }: { tone: string; children: React.ReactNode }) {
  return (
    <span
      className={cn(
        "numeric text-hint font-medium",
        tone === "danger" || tone === "warning"
          ? WORST_TEXT[tone as keyof typeof WORST_TEXT]
          : "text-muted-foreground",
      )}
    >
      {children}
    </span>
  )
}

export function BaseGlyph({ reference }: { reference: string }) {
  const product = imageProduct(reference)
  return hasProductLogo(product) ? (
    <ProductGlyph id={product} />
  ) : (
    <Box aria-hidden className="size-3.5 shrink-0" />
  )
}

function PackageMark({ group }: { group: FindingGroup }) {
  const product = findingProduct(group)
  return (
    <WireMark tone="logo" size="sm">
      {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Box aria-hidden />}
    </WireMark>
  )
}

/**
 * One package in the report: on its ecosystem's mark, the version installed
 * (and the one that fixes it, where the fix is the reader's to take), where it
 * came from, and each advisory under it.
 */
export function PackageRow({ group, showFix }: { group: FindingGroup; showFix: boolean }) {
  const tone = severityTone(group.worst)
  const from = dependencyWords(group)
  return (
    <li className="min-w-0 py-3" data-slot="vulnerable-package">
      <div className="flex min-w-0 items-center gap-3">
        <PackageMark group={group} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
            <span className="truncate text-body font-medium">{group.package}</span>
            <WorstCounts tone={tone}>{countsLine(group.counts)}</WorstCounts>
          </div>
          <p
            className="truncate text-hint text-muted-foreground"
            title={group.path ?? group.target}
          >
            <span className="font-mono">
              {showFix && group.fix ? `${group.installed} → ${group.fix}` : group.installed}
            </span>
            {from && ` · ${from}`} · {group.path ?? group.target}
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

export function SettingsLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Link
      href={href}
      className="inline-flex items-center gap-1 rounded-sm font-medium text-foreground focus-ring hover:underline"
    >
      {children} <ArrowRight className="size-3" />
    </Link>
  )
}
