import { cn } from "@/lib/utils"
import { Panel } from "@/components/panel"

/**
 * The frame a danger zone's acts stand in: one edge in the danger rule
 * around all of them, a hairline between each. It is the page's only framed
 * block, so the edge says "careful" once instead of a red card per act.
 */
export function DangerRows({ children }: { children: React.ReactNode }) {
  return (
    <Panel framed className="divide-y divide-hairline border-rule-danger">
      {children}
    </Panel>
  )
}

/**
 * One act of a danger zone: what it is done to (`mark`), its name, what it
 * costs in a sentence, what exactly it touches as data, and its one button
 * beside them — so the button is never a page's width from the sentence that
 * explains it.
 *
 * With no `action` the act cannot be taken here, or not yet: the row stays,
 * its title goes quiet, and its sentence says why or what comes first. An act
 * the reader's role cannot take is not drawn at all; that is the caller's.
 *
 * Three ranks, one step apart on the ladder: the act at 14, its consequence
 * at 12, the data at 11. The consequence was once at the data's 11 and
 * muted, so the line a reader most needs before pressing was the hardest one
 * to read.
 *
 * The title is an h3: the zone's own "Danger zone" is the section's h2, and
 * as h4 the acts skipped a level. Both danger zones, a deployment's and a
 * database's, drew this row from their own copy, and the copies had drifted
 * in the gap and in the heading.
 *
 * The act's own block is keyed on its name and on whether it can be taken,
 * so Stop turning into Start, or a held act getting its button, arrives
 * (§11) rather than being repainted. Only that block: `children` carries
 * what must outlive it, such as the confirmation the button opens.
 */
export function DangerRow({
  mark,
  title,
  sentence,
  facts,
  action,
  children,
}: {
  /** What the act is done to, drawn as itself: a product's logo, an engine's mark. */
  mark?: React.ReactNode
  title: string
  /** The consequence, in one sentence; or why the act cannot be taken. */
  sentence?: React.ReactNode
  /** What the act touches, as data under the sentence. */
  facts?: React.ReactNode
  /** The one button; none when the act cannot be taken. */
  action?: React.ReactNode
  children?: React.ReactNode
}) {
  return (
    <div data-slot="danger-row" className="min-w-0 px-5 py-4">
      <div
        key={`${title}-${action ? "open" : "held"}`}
        className={cn(
          "grid min-w-0 animate-rise gap-x-4 gap-y-3 sm:items-center",
          mark ? "grid-cols-start-main sm:grid-cols-start-main-end" : "sm:grid-cols-main-end",
        )}
      >
        {mark}
        <div className="min-w-0 space-y-1">
          <h3 className={cn("text-sm font-medium", !action && "text-muted-foreground")}>{title}</h3>
          {sentence && (
            <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">{sentence}</p>
          )}
          {facts && (
            <div className="space-y-0.5 pt-1 text-hint leading-relaxed text-muted-foreground">
              {facts}
            </div>
          )}
        </div>
        {action && (
          <div className={cn("flex max-sm:[&>*]:w-full", mark && "col-span-2 sm:col-span-1")}>
            {action}
          </div>
        )}
      </div>
      {children}
    </div>
  )
}
