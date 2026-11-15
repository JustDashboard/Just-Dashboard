import { cn } from "@/lib/utils"
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
 *
 * The dot that parts two facts belongs to the second of them, and the line
 * is drawn one dot's width to the left of its box: a fact that wraps takes
 * its dot with it, and at the start of a line the dot is outside the box,
 * where it is not drawn. A dot that was an item of its own was left hanging
 * at the end of the line above.
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
      className={cn("min-w-0 overflow-hidden text-body text-muted-foreground", className)}
      {...props}
    >
      <span className="-ml-4 flex flex-wrap items-center gap-y-0.5">
        {facts.map((fact, index) => (
          <span key={fact.key} title={fact.title} className="flex max-w-full min-w-0 items-center">
            <span aria-hidden className="w-4 shrink-0 text-center text-muted-foreground/40">
              {index > 0 && "·"}
            </span>
            <span className="min-w-0 truncate">
              {fact.lead && <span>{fact.lead} </span>}
              <span className={cn("text-foreground", fact.mono ? "font-mono text-xs" : "numeric")}>
                {fact.text}
              </span>
              {fact.tail && <span> {fact.tail}</span>}
            </span>
          </span>
        ))}
      </span>
    </p>
  )
}
