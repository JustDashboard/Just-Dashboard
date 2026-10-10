"use client"

import { useRef, type RefObject } from "react"
import { FileText, FloppyDisk, NetworkDevice, Play, Shield, type Icon } from "@/components/icons"
import { DRIFT_DOMAINS, type DriftDomain, type DriftSummary } from "@/lib/network-drift"
import { cn } from "@/lib/utils"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"

const GLYPH: Record<DriftDomain, Icon> = {
  files: FileText,
  kernel: NetworkDevice,
  boot: Play,
  blocklists: Shield,
}

const MARK: Record<DriftSummary["tone"], "success" | "warning" | "danger" | "neutral"> = {
  success: "success",
  warning: "warning",
  danger: "danger",
  default: "neutral",
}

/**
 * Where the saved configuration is supposed to have gone, and whether it got
 * there: the configuration this dashboard owns on the left, wired to each place
 * it is written — the files it renders, the kernel objects it creates, the unit
 * that restores them at boot and the blocklist sets — in the wiring vocabulary
 * the firewall and backup pictures already speak.
 *
 * A line is the reading, so nothing on it is decoration: green where every
 * comparison in the domain matched, amber where one differs, red where
 * something else occupies a name this dashboard owns, plain grey where the
 * comparison is incomplete, and dashed where there is nothing to compare. A
 * pulse runs down every line while an inspection the reader asked for is on its
 * way, because that is the one moment something is travelling along them.
 * Pressing a domain narrows the table under it.
 */
export function DriftPicture({
  config,
  domains,
  inspecting,
  picked,
  onPick,
}: {
  config: DriftSummary
  domains: DriftSummary[]
  inspecting: boolean
  picked: string
  onPick: (domain: DriftDomain) => void
}) {
  const container = useRef<HTMLDivElement>(null)
  const saved = useRef<HTMLDivElement>(null)
  return (
    <SettingPicture
      label="Where the saved network configuration is written, and whether each place matches"
      containerRef={container}
      className="-mt-2"
      start={[
        <WireNode
          key="saved"
          nodeRef={saved}
          align="end"
          mark={
            <WireMark tone={MARK[config.tone]}>
              <FloppyDisk aria-hidden />
            </WireMark>
          }
          eyebrow="Saved"
          title="Configuration"
          hint={
            inspecting ? (
              <TextShimmer>Inspecting…</TextShimmer>
            ) : (
              `${config.matching} of ${config.total} match`
            )
          }
        />,
      ]}
      lines={null}
      end={
        <ul className="flex min-w-0 flex-col gap-5">
          {DRIFT_DOMAINS.map(({ key, label }, index) => (
            <DomainNode
              key={key}
              domain={key}
              label={label}
              summary={domains.find((d) => d.key === key)!}
              container={container}
              saved={saved}
              inspecting={inspecting}
              picked={picked === key}
              index={index}
              onPick={onPick}
            />
          ))}
        </ul>
      }
    />
  )
}

/** One domain and the line into it, drawn here so the node owns the ref its line ends on. */
function DomainNode({
  domain,
  label,
  summary,
  container,
  saved,
  inspecting,
  picked,
  index,
  onPick,
}: {
  domain: DriftDomain
  label: string
  summary: DriftSummary
  container: RefObject<HTMLDivElement | null>
  saved: RefObject<HTMLDivElement | null>
  inspecting: boolean
  picked: boolean
  index: number
  onPick: (domain: DriftDomain) => void
}) {
  const node = useRef<HTMLDivElement>(null)
  const Glyph = GLYPH[domain]
  const nothing = summary.total === 0
  return (
    <li className="min-w-0">
      <AnimatedBeam
        containerRef={container}
        fromRef={saved}
        toRef={node}
        shape="s"
        still={!inspecting}
        dashed={nothing}
        tone={summary.tone}
        duration={1.4}
        delay={index * 0.2}
      />
      <WireNode
        nodeRef={node}
        mark={
          <WireMark tone={MARK[summary.tone]} size="md" shape="square">
            <Glyph aria-hidden />
          </WireMark>
        }
        title={
          <button
            type="button"
            aria-pressed={picked}
            title={`Show only ${label.toLowerCase()} in the table`}
            onClick={() => onPick(domain)}
            className={cn(
              "-mx-1 rounded px-1 text-left focus-ring transition-colors hover:text-brand",
              picked && "text-brand",
            )}
          >
            {label}
          </button>
        }
        hint={<Reading summary={summary} />}
      />
    </li>
  )
}

function Reading({ summary }: { summary: DriftSummary }) {
  if (summary.total === 0) return <>nothing to compare</>
  return (
    <span className="numeric">
      {summary.matching} of {summary.total} match
      {summary.differences > 0 && (
        <span className={summary.tone === "danger" ? "text-destructive" : "text-warning"}>
          {" · "}
          {summary.differences} {summary.differences === 1 ? "differs" : "differ"}
        </span>
      )}
      {summary.unknown > 0 && ` · ${summary.unknown} incomplete`}
    </span>
  )
}
