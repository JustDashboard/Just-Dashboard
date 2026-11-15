"use client"

import type { WGFamilyState, WGInterface } from "@/lib/types"
import { cn } from "@/lib/utils"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Switch } from "@/components/ui/switch"
import type { TunnelControl } from "./tunnel-identity"
import { familyReading } from "./tunnel-reading"

/**
 * Whether a tunnel's clients leave for the internet through this server, one
 * line per address family: the family's tunnel network and whether it is
 * there, where its exit leaves and how many client connections it has
 * translated, whether its rules were verified, and the switch that turns it
 * on or off. It was a list of facts under a box of alerts with the switches
 * up in the tunnel's head; the switch now sits on the line it changes.
 *
 * What the readings do not prove is behind the ⓘ, where it was a paragraph
 * under every tunnel.
 */
export function TunnelExit({ tunnel, control }: { tunnel: WGInterface; control: TunnelControl }) {
  const v4 = tunnel.families?.ipv4
  const v6 = tunnel.families?.ipv6
  return (
    <Panel plain>
      <PanelHeader
        title="Internet exit"
        actions={
          <InfoTip label="What these readings prove">
            Rules and a fresh route from the first client address are local evidence, and the
            translation count is measured use of this exit since its rules were loaded. Public
            endpoint, provider reachability and another client&rsquo;s source policy remain
            untested.
          </InfoTip>
        }
      />
      <PanelBody flush>
        <ul
          aria-label={`${tunnel.name} address family evidence`}
          className="divide-y divide-hairline"
        >
          <Family
            label="IPv4"
            state={v4}
            fallback={{
              network: tunnel.subnet || tunnel.addresses[0] || "No tunnel network",
              exit: tunnel.exitNode ? "Exit configured" : "Exit off",
            }}
            control={
              tunnel.managed && (
                <Switch
                  checked={tunnel.exitNode}
                  disabled={
                    control.busy || (!tunnel.exitNode && v4?.exit.capability.writable === false)
                  }
                  onCheckedChange={control.setExit}
                  aria-label={`${tunnel.name} as an exit node`}
                />
              )
            }
          />
          {(v6 || tunnel.ipv6Enabled) && (
            <Family
              label="IPv6"
              state={v6}
              fallback={{ network: "No tunnel network", exit: "Exit off" }}
              control={
                tunnel.managed &&
                tunnel.ipv6Enabled && (
                  <Switch
                    checked={Boolean(v6?.exit.configured)}
                    disabled={
                      control.busy ||
                      (!v6?.exit.configured && v6?.exit.capability.writable === false)
                    }
                    onCheckedChange={control.setIPv6Exit}
                    aria-label={`${tunnel.name} IPv6 exit`}
                  />
                )
              }
            />
          )}
        </ul>
      </PanelBody>
    </Panel>
  )
}

/**
 * One family: two lines in every state, so a row with a reason and one
 * without are the same height and the switch never moves.
 */
function Family({
  label,
  state,
  fallback,
  control,
}: {
  label: string
  state: WGFamilyState | undefined
  /** What the tunnel itself says, where the server sent no family evidence. */
  fallback: { network: string; exit: string }
  control: React.ReactNode
}) {
  const reading = state ? familyReading(state) : undefined
  const exit = [reading?.exit ?? fallback.exit, reading?.reason].filter(Boolean).join(" · ")
  return (
    <li className="flex min-h-14 min-w-0 items-center gap-3 py-2 max-sm:min-h-[4.5rem] sm:gap-4">
      <span className="eyebrow w-9 shrink-0">{label}</span>
      <span className="min-w-0 flex-1">
        <span className="block truncate text-body font-medium">
          <span className={cn((!reading || state?.subnet) && "font-mono")}>
            {reading?.network ?? fallback.network}
          </span>
          {reading && (
            <span className="font-normal text-muted-foreground"> · {reading.runtime}</span>
          )}
        </span>
        {/* Two lines on a phone, where the verdict and the switch leave the
            sentence half a row; the row is that tall in every state there. */}
        <span
          className="block text-hint text-muted-foreground max-sm:line-clamp-2 sm:truncate"
          title={exit}
        >
          {exit}
        </span>
      </span>
      {reading?.verdict && <Status tone={reading.verdict.tone} label={reading.verdict.label} />}
      {control}
    </li>
  )
}
