"use client"

import { useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { CrossCircle } from "@/components/icons"
import { get } from "@/lib/api"
import { duration, relativeTime } from "@/lib/format"
import type { TLSScan } from "@/lib/proxy/types-tls"
import type { DeepScan, VersionSuites } from "@/lib/proxy/types-tls-deep"
import {
  RATINGS,
  RATING_VERDICT,
  alpnReading,
  browserGroupReading,
  deepHref,
  dhReading,
  effectiveFilter,
  groupReading,
  http3Reading,
  newDeepFindings,
  ratingCounts,
  resumptionReading,
  sniReading,
  suiteFacts,
  suiteGroups,
  versionCounts,
  versionReading,
  type Rating,
  type Reading,
} from "@/lib/tls-deep"
import { useSessionState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Toolbar } from "@/components/page"
import { RowList } from "@/components/row-list"
import { FindingList } from "@/components/finding-list"
import { ErrorState, Notice, Spinner } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { useNow } from "@/components/deploy/vocabulary"
import { Button } from "@/components/ui/button"

/**
 * The TLS report's deep scan: what the quick report leaves out because it
 * takes forty connections rather than six.
 *
 * It is asked for, not run on every visit — `?deep=1` in the address, so a
 * link or a reload asks for it again — and it runs after the quick scan, on
 * a request of its own, so the report is on screen while it works. Each new
 * quick scan of the target runs it again, since the two describe one server
 * at one moment.
 */
export function DeepScanSection({ scan }: { scan: TLSScan }) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const router = useRouter()
  const params = useSearchParams()
  const deep = params.get("deep") === "1"
  const key = `${scan.domain}|${scan.port}|${scan.checkedAt}`

  const [cancelled, setCancelled] = useState<{ key: string; after: number }>()
  if (cancelled && cancelled.key !== key) setCancelled(undefined)
  const result = usePoll(
    (signal) =>
      get<DeepScan>("/certificates/scan/deep", { domain: scan.domain, port: scan.port }, signal),
    0,
    [key],
    { enabled: admin && deep && !cancelled },
  )
  // usePoll says loading only while there is nothing to show, so a scan
  // asked for again over an error or an unreachable answer is tracked here,
  // until an answer or an error replaces what it was asked over.
  const [againOver, setAgainOver] = useState<{ key: string; data?: DeepScan; error?: Error }>()
  if (
    againOver &&
    (againOver.key !== key || againOver.data !== result.data || againOver.error !== result.error)
  )
    setAgainOver(undefined)
  const ask = (on: boolean) => router.replace(deepHref(params, on), { scroll: false })
  const again = () => {
    setCancelled(undefined)
    setAgainOver({ key, data: result.data, error: result.error })
    result.refresh()
  }
  const cancel = (after: number) => {
    setCancelled({ key, after })
    setAgainOver(undefined)
  }

  if (!admin) return null
  if (!deep)
    return (
      <Panel plain>
        <PanelHeader
          title="Cipher suites"
          actions={
            <Button type="button" variant="outline" size="xs" onClick={() => ask(true)}>
              Deep scan
            </Button>
          }
        />
        <PanelBody>
          <p className="text-body leading-relaxed text-muted-foreground">
            A deep scan lists every cipher suite each version accepts, the key exchange groups,
            post-quantum ones included, HTTP/2 against the site form, HTTP/3, session resumption,
            and the certificate a client gets when it names no site. It opens about forty more
            connections to this address.
          </p>
        </PanelBody>
      </Panel>
    )
  if (cancelled)
    return (
      <Panel plain>
        <PanelHeader title="Deep scan" actions={<TurnOff onClick={() => ask(false)} />} />
        <PanelBody className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <p className="text-body text-muted-foreground">
            The deep scan was cancelled after {duration(cancelled.after)}.
          </p>
          <Button type="button" variant="outline" size="xs" onClick={again}>
            Scan again
          </Button>
        </PanelBody>
      </Panel>
    )
  if (result.loading || againOver)
    return (
      <Panel plain>
        <PanelHeader title="Deep scan" />
        <PanelBody>
          <DeepProgress onCancel={cancel} />
        </PanelBody>
      </Panel>
    )
  if (result.error || !result.data)
    return (
      <Panel plain>
        <PanelHeader title="Deep scan" actions={<TurnOff onClick={() => ask(false)} />} />
        <PanelBody className="space-y-3">
          {result.error && <ErrorState error={result.error} />}
          <Button type="button" variant="outline" size="xs" onClick={again}>
            Scan again
          </Button>
        </PanelBody>
      </Panel>
    )
  const report = result.data
  if (!report.reachable)
    return (
      <Panel plain>
        <PanelHeader title="Deep scan" actions={<TurnOff onClick={() => ask(false)} />} />
        <PanelBody>
          <Notice tone="danger" icon={CrossCircle} title="The deep scan could not connect">
            <p className="break-words">{report.error}</p>
            <div className="pt-2">
              <Button type="button" variant="outline" size="xs" onClick={again}>
                Scan again
              </Button>
            </div>
          </Notice>
        </PanelBody>
      </Panel>
    )
  return <DeepReport deep={report} scan={scan} onTurnOff={() => ask(false)} />
}

function TurnOff({ onClick }: { onClick: () => void }) {
  return (
    <Button type="button" variant="outline" size="xs" onClick={onClick}>
      Turn off deep scan
    </Button>
  )
}

/**
 * A deep scan in flight: how long it has run, and the way out of it. It is
 * given 45 seconds, and a spinner with no clock is a page that may have hung.
 */
function DeepProgress({ onCancel }: { onCancel: (seconds: number) => void }) {
  const [started] = useState(() => Date.now())
  const now = useNow(1000)
  const seconds = Math.max(0, Math.floor((now - started) / 1000))
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
      <p className="flex min-w-0 items-center gap-2 text-body text-muted-foreground">
        <Spinner className="size-3.5 shrink-0" />
        <span>
          Asking for every cipher suite, key exchange group and connection feature…{" "}
          <span className="numeric">{duration(seconds)}</span>
        </span>
      </p>
      <Button type="button" variant="outline" size="xs" onClick={() => onCancel(seconds)}>
        Cancel
      </Button>
    </div>
  )
}

function DeepReport({
  deep,
  scan,
  onTurnOff,
}: {
  deep: DeepScan
  scan: TLSScan
  onTurnOff: () => void
}) {
  const router = useRouter()
  const site = deep.alpn?.site
  const findings = newDeepFindings(deep, scan).map((finding) =>
    site && finding.id.startsWith("tls.alpn.")
      ? {
          ...finding,
          action: {
            label: `Open ${site.name}`,
            onClick: () => router.push(`/proxy/sites?site=${encodeURIComponent(site.name)}`),
          },
        }
      : finding,
  )
  // An answer from another machine is that machine's configuration, which
  // every reading below describes.
  const elsewhere = deep.where === "elsewhere" || deep.where === "cloudflare"
  return (
    <div className="grid animate-rise items-start gap-8 xl:grid-cols-[minmax(0,1fr)_24rem] [&>*]:min-w-0">
      <div className="min-w-0 space-y-8">
        <Panel plain>
          <PanelHeader title="Deep scan" actions={<TurnOff onClick={onTurnOff} />} />
          <PanelBody className="space-y-3">
            <p className="text-hint text-muted-foreground">
              <CheckedWhen at={deep.checkedAt} /> · {deep.connections} connections to{" "}
              <span className="font-mono break-all">{deep.address}</span>
              {elsewhere && ", which is not this server"}
            </p>
            <FindingList findings={findings} emptyLabel="Nothing beyond the findings above" />
          </PanelBody>
        </Panel>
        <SuitesPanel deep={deep} />
      </div>
      <div className="min-w-0 space-y-8">
        <KeyExchangePanel deep={deep} />
        <ConnectionPanel deep={deep} />
      </div>
    </div>
  )
}

/** "Checked 3 minutes ago", kept true while the page stays open. */
function CheckedWhen({ at }: { at: string }) {
  useNow(30_000)
  return <>Checked {relativeTime(at)}</>
}

const RATING_LABEL: Record<Rating, string> = {
  strong: "Strong",
  weak: "Weak",
  insecure: "Insecure",
}

/**
 * Every accepted suite, version by version, in the order the server chose
 * them. Filtered by version and by rating; the filters are kept for the tab,
 * and one that matches nothing in a new scan shows everything instead.
 */
function SuitesPanel({ deep }: { deep: DeepScan }) {
  const [version, setVersion] = useSessionState<string>("proxy.tls.deep.version", "all")
  const [rating, setRating] = useSessionState<Rating | "any">("proxy.tls.deep.rating", "any")
  const filter = effectiveFilter(deep, { version, rating })
  const versions = versionCounts(deep)
  const ratings = ratingCounts(deep, filter.version)
  const total = versions.reduce((sum, v) => sum + v.count, 0)
  const groups = suiteGroups(deep, filter)
  return (
    <Panel plain>
      <PanelHeader title="Cipher suites" />
      {total > 0 && (
        <Toolbar className="flex-col items-stretch gap-2 pt-3 sm:flex-row sm:items-center sm:justify-between">
          <ChipStrip role="group" aria-label="Version">
            <FilterChip selected={filter.version === "all"} onClick={() => setVersion("all")}>
              All versions <ChipCount>{total}</ChipCount>
            </FilterChip>
            {versions.map((v) => (
              <FilterChip
                key={v.name}
                selected={filter.version === v.name}
                onClick={() => setVersion(v.name)}
              >
                {v.name} <ChipCount>{v.count}</ChipCount>
              </FilterChip>
            ))}
          </ChipStrip>
          <ChipStrip role="group" aria-label="Rating">
            <FilterChip selected={filter.rating === "any"} onClick={() => setRating("any")}>
              Any rating
            </FilterChip>
            {RATINGS.filter((r) => ratings[r] > 0).map((r) => (
              <FilterChip key={r} selected={filter.rating === r} onClick={() => setRating(r)}>
                {RATING_LABEL[r]} <ChipCount>{ratings[r]}</ChipCount>
              </FilterChip>
            ))}
          </ChipStrip>
        </Toolbar>
      )}
      <PanelBody className="space-y-6">
        {groups.map((v) => (
          <VersionGroup key={v.name} version={v} />
        ))}
        {total === 0 && (
          <p className="text-body text-muted-foreground">
            No suite was accepted in any version this scan could ask for.
          </p>
        )}
        <p className="text-hint leading-relaxed text-muted-foreground">
          Each version is offered every suite this scan knows, the export, anonymous and NULL ones
          included, then offered again without each one the server picks, until it refuses. Strong
          is forward secrecy with an authenticated cipher. PSK and SRP suites need a secret agreed
          beforehand and GOST ones a GOST certificate, so they are not asked about.
        </p>
      </PanelBody>
    </Panel>
  )
}

function VersionGroup({ version }: { version: VersionSuites }) {
  const reading = versionReading(version)
  return (
    <section aria-label={version.name} className="min-w-0 space-y-1">
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-1">
        <h3 className="font-mono text-xs font-medium">{version.name}</h3>
        <ReadingStatus reading={reading} />
      </div>
      {reading.detail && (
        <p className="text-hint leading-relaxed break-words text-muted-foreground">
          {reading.detail}
        </p>
      )}
      {version.suites.length > 0 && (
        <RowList aria-label={`${version.name} suites`}>
          {version.suites.map((suite) => (
            <DeepRow
              key={suite.id}
              title={
                <span className="font-mono text-xs" title={suite.name}>
                  {suite.openssl}
                </span>
              }
              subtitle={`${suiteFacts(suite)}${suite.reasons.length ? ` — ${suite.reasons.join(", ")}` : ""}`}
              trailing={<Status verdict={RATING_VERDICT[suite.rating]} label={suite.rating} />}
            />
          ))}
        </RowList>
      )}
    </section>
  )
}

/** Each TLS 1.3 group asked for alone, and the one a browser's offer gets. */
function KeyExchangePanel({ deep }: { deep: DeepScan }) {
  const browser = browserGroupReading(deep)
  return (
    <Panel plain>
      <PanelHeader title="Key exchange" />
      <PanelBody flush>
        <RowList aria-label="Key exchange">
          <DeepRow
            title="A browser gets"
            subtitle={browser.detail}
            trailing={<ReadingStatus reading={browser} />}
          />
          {deep.groups.map((group) => {
            const reading = groupReading(group)
            return (
              <DeepRow
                key={group.id}
                title={
                  <span className="inline-flex flex-wrap items-center gap-2">
                    <span className="font-mono text-xs">{group.name}</span>
                    {group.postQuantum && <Tag>post-quantum</Tag>}
                  </span>
                }
                subtitle={reading.detail}
                trailing={<ReadingStatus reading={reading} />}
              />
            )
          })}
          {deep.dhBits ? (
            <DeepRow
              title="DHE group"
              subtitle={dhReading(deep.dhBits).detail}
              trailing={<ReadingStatus reading={dhReading(deep.dhBits)} />}
            />
          ) : null}
        </RowList>
        {deep.groups.length === 0 && (
          <p className="pt-3 text-hint leading-relaxed text-muted-foreground">
            Groups are asked for one by one in TLS 1.3, which this server did not accept.
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

/** HTTP/2, HTTP/3, resumption, and what a client naming no site gets. */
function ConnectionPanel({ deep }: { deep: DeepScan }) {
  return (
    <Panel plain>
      <PanelHeader title="Connection" />
      <PanelBody flush>
        <RowList aria-label="Connection">
          {deep.alpn && (
            <DeepRow
              title="HTTP/2"
              subtitle={alpnReading(deep.alpn).detail}
              trailing={<ReadingStatus reading={alpnReading(deep.alpn)} />}
            />
          )}
          {deep.http3 && (
            <DeepRow
              title="HTTP/3"
              subtitle={http3Reading(deep.http3).detail}
              trailing={<ReadingStatus reading={http3Reading(deep.http3)} />}
            />
          )}
          {deep.resumption.map((r) => (
            <DeepRow
              key={r.version}
              title={`Resumption, ${r.version}`}
              subtitle={resumptionReading(r).detail}
              trailing={<ReadingStatus reading={resumptionReading(r)} />}
            />
          ))}
          {deep.sni.map((probe) => {
            const reading = sniReading(probe)
            return (
              <DeepRow
                key={probe.kind}
                title={reading.title}
                subtitle={reading.detail}
                trailing={<ReadingStatus reading={reading} />}
              />
            )
          })}
        </RowList>
        {!deep.http3 && (
          <p className="pt-3 text-hint leading-relaxed text-muted-foreground">
            Port {deep.port} is registered to a service that is not a website, so HTTP/3 was not
            asked about.
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

function ReadingStatus({ reading }: { reading: Reading }) {
  return <Status verdict={reading.verdict} tone={reading.tone} label={reading.label} />
}

/** A row that wraps: suite names and details must read on a phone and beside the rail. */
function DeepRow({
  title,
  subtitle,
  trailing,
}: {
  title: React.ReactNode
  subtitle?: React.ReactNode
  trailing?: React.ReactNode
}) {
  return (
    <li className="flex min-w-0 flex-col gap-1.5 py-2 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
      <div className="min-w-0 space-y-0.5">
        <div className="text-body font-medium break-all">{title}</div>
        {subtitle && (
          <div className="text-hint leading-relaxed break-words text-muted-foreground">
            {subtitle}
          </div>
        )}
      </div>
      {trailing && (
        <div className="max-w-full shrink-0 text-hint sm:max-w-[45%] [&_*]:whitespace-normal">
          {trailing}
        </div>
      )}
    </li>
  )
}
