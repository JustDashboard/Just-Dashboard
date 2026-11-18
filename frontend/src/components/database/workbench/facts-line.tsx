import { cn } from "@/lib/utils"
import { Fact, FactLine } from "@/components/fact-line"
import type { Fact as FactData } from "@/components/database/workbench/table-facts"

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
 *
 * It is the shared `FactLine` (`components/fact-line.tsx`) fed from data:
 * the dot that parts two facts belongs to the second of them, so none is
 * left hanging at the end of a wrapped line.
 */
export function FactsLine({
  facts,
  className,
  ...props
}: { facts: readonly FactData[] } & Omit<React.ComponentProps<"p">, "children">) {
  if (facts.length === 0) return null
  return (
    <p
      data-slot="facts-line"
      className={cn("min-w-0 text-body text-muted-foreground", className)}
      {...props}
    >
      <FactLine>
        {facts.map((fact) => (
          <Fact key={fact.key} title={fact.title} className="truncate">
            {fact.lead && <span>{fact.lead} </span>}
            <span className={cn("text-foreground", fact.mono ? "font-mono text-xs" : "numeric")}>
              {fact.text}
            </span>
            {fact.tail && <span> {fact.tail}</span>}
          </Fact>
        ))}
      </FactLine>
    </p>
  )
}
