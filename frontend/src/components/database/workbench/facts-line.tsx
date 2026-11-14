import { Fragment } from "react"
import { cn } from "@/lib/utils"
import { FactDot } from "@/components/metrics/host-identity"
import type { Fact } from "@/components/database/workbench/table-facts"

/**
 * What a thing amounts to, as one line of words under its name: "~9,000 rows
 * · 3.2 MB · owned by app".
 *
 * These were strips of small labelled figures — ROWS, SIZE, OWNER, each a
 * caption over a value — on the landing of Data, on a table's Structure and
 * at the head of a table in Schema. A figure under a caption is a tile the
 * reader has to turn back into a sentence; the line is the sentence. It wraps
 * between facts, never inside one, and a long run reads on to a second line
 * rather than being cut.
 */
export function FactsLine({
  facts,
  className,
  ...props
}: { facts: readonly Fact[] } & Omit<React.ComponentProps<"p">, "children">) {
  if (facts.length === 0) return null
  return (
    <p
      data-slot="facts-line"
      className={cn(
        "flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-body text-muted-foreground",
        className,
      )}
      {...props}
    >
      {facts.map((fact, index) => (
        <Fragment key={fact.key}>
          {index > 0 && <FactDot />}
          <span title={fact.title} className="max-w-full min-w-0 truncate">
            {fact.lead && <span>{fact.lead} </span>}
            <span className={cn("text-foreground", fact.mono ? "font-mono text-xs" : "numeric")}>
              {fact.text}
            </span>
            {fact.tail && <span> {fact.tail}</span>}
          </span>
        </Fragment>
      ))}
    </p>
  )
}
