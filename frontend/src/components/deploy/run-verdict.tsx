import { cn } from "@/lib/utils"
import { Fact, FactLine } from "@/components/deploy/fact-line"
import { WireMark } from "@/components/deploy/wire"
import type { Verdict } from "@/components/deploy/run-verdicts"

/**
 * What a step concluded, as the Overview says a reading: one sentence on the
 * step's own mark, in the colour of how it went, and the figures behind it as
 * a line of facts. A replay's and a rehearsal's reports each opened on four
 * tiles — a figure over a caption, four times — and the verdict was the
 * sentence a reader made of them.
 */
export function RunVerdict({
  verdict,
  glyph,
  basis,
}: {
  verdict: Verdict
  /** The step's glyph, as its row in the steps rail draws it. */
  glyph: React.ReactNode
  /** The server's own wording of the verdict, for a `title`. */
  basis?: string
}) {
  return (
    <div data-slot="run-verdict" className="flex min-w-0 items-center gap-3.5">
      <WireMark size="md" tone={verdict.tone}>
        {glyph}
      </WireMark>
      <div className="min-w-0">
        <p title={basis} className="text-body leading-snug font-medium">
          {verdict.title}
        </p>
        <FactLine className="text-hint leading-snug text-muted-foreground">
          {verdict.facts.map((fact) => (
            <Fact
              key={fact.key}
              title={fact.basis}
              className={cn(
                "numeric",
                fact.tone === "danger" && "text-destructive",
                fact.tone === "warning" && "text-warning",
              )}
            >
              {fact.text}
            </Fact>
          ))}
        </FactLine>
      </div>
    </div>
  )
}
