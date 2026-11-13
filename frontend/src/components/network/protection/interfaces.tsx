"use client"

import { useState } from "react"
import type { InterfaceReading, ProtectionSetting } from "@/lib/types"
import { cn } from "@/lib/utils"
import { FormSection, InfoTip } from "@/components/form"
import { Row, RowList } from "@/components/row-list"
import { Segments } from "@/components/deploy/settings/segments"
import { choiceWord } from "@/components/network/protection/reading"

type Which = "differ" | "every"

/**
 * What each device actually runs for the five per-interface protections.
 *
 * The page sets the "all" value, but the kernel combines it with each
 * device's own, differently per setting: the higher reverse-path filter wins,
 * redirects need both or either depending on forwarding, source routing needs
 * both. A device whose effective value is not what "all" alone would give is
 * listed first, with its own value beside it, because that is the tunnel left
 * at strict filtering or the bridge still accepting redirects that "all"
 * cannot show.
 *
 * A section of the settings column: one line for each value of each device,
 * the device and the setting on the left, what it comes to and what it was
 * made of on the right. How the kernel combines a setting is behind its ⓘ.
 */
export function InterfaceValues({
  reading,
  settings,
}: {
  reading: InterfaceReading
  settings: ProtectionSetting[]
}) {
  const [which, setWhich] = useState<Which>("differ")
  const label = (key: string) => settings.find((s) => s.key === key)?.label ?? key
  const rule = (key: string) => reading.rules.find((r) => r.key === key)?.explain
  const differing = reading.interfaces.filter((d) => d.values.some((v) => v.differs))
  const all = which === "every"
  const lines = (all ? reading.interfaces : differing).flatMap((d) =>
    d.values.filter((v) => all || v.differs).map((v) => ({ device: d, value: v })),
  )
  return (
    <FormSection
      aside
      id="per-interface"
      data-slot="setting"
      className="border-t border-hairline pt-8"
      title="Per-interface values"
      hint={
        <span className="numeric">
          {differing.length > 0
            ? `${differing.length} ${differing.length === 1 ? "device differs" : "devices differ"} from all`
            : "every device follows all"}
          {reading.omitted > 0 && ` · ${reading.omitted} container devices left out`}
        </span>
      }
      actions={
        <Segments
          label="Which devices"
          value={which}
          options={[
            { value: "differ", label: "That differ" },
            { value: "every", label: "Every device" },
          ]}
          onChange={setWhich}
        />
      }
    >
      {lines.length === 0 ? (
        <p className="text-body text-muted-foreground">
          Every device runs what the &ldquo;all&rdquo; values above give it.
        </p>
      ) : (
        <RowList aria-label="Per-interface values">
          {lines.map(({ device, value }) => {
            const how = rule(value.key)
            const made = `device ${choiceWord(value.key, value.own)}, all ${choiceWord(value.key, value.all)}`
            return (
              <Row
                key={`${device.name}:${value.key}`}
                className="px-0"
                title={
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="truncate font-mono">{device.name}</span>
                    {device.forwarding && (
                      <span className="shrink-0 text-hint font-normal text-muted-foreground">
                        forwarding
                      </span>
                    )}
                  </span>
                }
                subtitle={
                  <>
                    {label(value.key)}
                    <span className="sm:hidden"> · {made}</span>
                  </>
                }
                trailing={
                  <>
                    <span className="font-mono text-hint text-muted-foreground max-sm:hidden">
                      {made}
                    </span>
                    <span
                      className={cn(
                        "w-14 text-right text-body font-medium",
                        value.differs && "text-warning",
                      )}
                    >
                      {choiceWord(value.key, value.effective)}
                    </span>
                    {how ? (
                      <InfoTip label={`How the kernel combines ${label(value.key)}`}>{how}</InfoTip>
                    ) : (
                      <span aria-hidden className="size-3.5 shrink-0" />
                    )}
                  </>
                }
              />
            )
          })}
        </RowList>
      )}
    </FormSection>
  )
}
