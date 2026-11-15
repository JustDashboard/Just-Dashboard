"use client"

import { InfoTip } from "@/components/form"

/**
 * A section's facts line with the paragraph it used to print behind a mark.
 *
 * A switch said what it does in three or four lines under its own name, on
 * every visit. The switch keeps one clause; what the section currently is
 * stays the line under its title; and the reasoning a reader wants once is
 * here, beside that line, where it can be asked for whether the switch is on
 * or off — the fields a switch reveals are not on the page while it is off.
 *
 * Keyed on the line, so a change of state arrives (§11) and the mark beside
 * it stays put.
 */
export function FactsLine({
  line,
  about,
  children,
}: {
  line: string
  /** The mark's accessible name: "How traffic replay works". */
  about: string
  children: React.ReactNode
}) {
  return (
    <span className="flex min-w-0 items-start gap-1.5">
      <span key={line} className="min-w-0 animate-rise">
        {line}
      </span>
      <span className="flex h-[1lh] shrink-0 items-center">
        <InfoTip label={about}>{children}</InfoTip>
      </span>
    </span>
  )
}
