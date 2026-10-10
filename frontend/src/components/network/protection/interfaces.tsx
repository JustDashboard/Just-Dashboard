"use client"

import { useState } from "react"
import type { InterfaceReading, ProtectionSetting } from "@/lib/types"
import { Disclosure } from "@/components/form"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { choiceWord } from "@/components/network/protection/reading"

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
 */
export function InterfaceValues({
  reading,
  settings,
}: {
  reading: InterfaceReading
  settings: ProtectionSetting[]
}) {
  const [all, setAll] = useState(false)
  const label = (key: string) => settings.find((s) => s.key === key)?.label ?? key
  const differing = reading.interfaces.filter((d) => d.values.some((v) => v.differs))
  const shown = all ? reading.interfaces : differing
  return (
    <Disclosure
      quiet
      summary="Per-interface values"
      facts={
        differing.length > 0
          ? `${differing.length} device${differing.length === 1 ? "" : "s"} differ from all`
          : "every device follows all"
      }
    >
      <div className="min-w-0 space-y-3" aria-label="Per-interface values">
        {shown.length === 0 ? (
          <p className="text-hint text-muted-foreground">
            Every device runs what the &ldquo;all&rdquo; values above give it.
          </p>
        ) : (
          <ul className="space-y-2">
            {shown.map((d) => (
              <li key={d.name} className="min-w-0 text-hint">
                <p className="flex items-center gap-2">
                  <span className="font-mono text-foreground">{d.name}</span>
                  {d.forwarding && <Tag>forwarding</Tag>}
                </p>
                <ul className="mt-1 space-y-0.5 pl-4 text-muted-foreground">
                  {d.values
                    .filter((v) => all || v.differs)
                    .map((v) => (
                      <li key={v.key}>
                        {label(v.key)}:{" "}
                        <span
                          className={
                            v.differs ? "font-mono text-warning" : "font-mono text-foreground"
                          }
                        >
                          {choiceWord(v.key, v.effective)}
                        </span>{" "}
                        (device {choiceWord(v.key, v.own)}, all {choiceWord(v.key, v.all)})
                      </li>
                    ))}
                </ul>
              </li>
            ))}
          </ul>
        )}
        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" size="xs" variant="outline" onClick={() => setAll((v) => !v)}>
            {all ? "Only devices that differ" : "Every device"}
          </Button>
          {reading.omitted > 0 && (
            <span className="text-hint text-muted-foreground">
              {reading.omitted} container devices left out of the reading.
            </span>
          )}
        </div>
        <ul className="space-y-0.5 text-hint text-muted-foreground">
          {reading.rules.map((r) => (
            <li key={r.key}>
              <span className="text-foreground">{label(r.key)}.</span> {r.explain}
            </li>
          ))}
        </ul>
      </div>
    </Disclosure>
  )
}
