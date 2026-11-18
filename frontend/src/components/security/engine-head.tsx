import { Bug } from "@/components/icons"
import { FactLine } from "@/components/fact-line"
import { ProductLogo } from "@/components/product-logo"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { LoadingRows } from "@/components/loading"
import { Status } from "@/components/status-dot"
import { WireMark } from "@/components/wire/wire"
import type { EngineId } from "@/components/security/intrusion-model"

/**
 * The line an engine's view opens on, one step down from the page's own
 * identity line (PGP-22): the section head's anatomy with the engine's mark
 * at 32px before its name, its state and its facts on the one line —
 * "fail2ban · running · 1 jail · 41 bans" — and its one command at the far
 * end, over a hairline. A full identity line here, under the engine tabs,
 * matched the page's own header in size, and the page read as two headers.
 * Suricata has no mark in the logo collection and keeps a glyph on the tile,
 * never a guessed logo.
 */
export function EngineHead({
  id,
  name,
  title,
  state,
  facts,
  command,
}: {
  id: EngineId
  name: string
  /** The name with something after it (a version), where it is more than the name. */
  title?: React.ReactNode
  state: React.ReactNode
  facts: React.ReactNode
  command?: React.ReactNode
}) {
  return (
    <section
      aria-label={`About ${name}`}
      data-slot="engine-head"
      className="flex min-h-12 min-w-0 flex-wrap items-center gap-x-3 gap-y-2 border-b border-hairline pb-3"
    >
      <ProductLogo
        id={id === "suricata" ? undefined : id}
        fallback={Bug}
        className="size-8 rounded-lg [&_img]:size-5 [&>svg]:size-4"
      />
      <h2 className="min-w-0 truncate text-title leading-tight font-semibold tracking-tight">
        {title ?? name}
      </h2>
      <span className="flex shrink-0 items-center text-xs">{state}</span>
      <span className="min-w-0 flex-[1_1_12rem] text-xs text-muted-foreground">
        <FactLine>{facts}</FactLine>
      </span>
      {command && <div className="ml-auto flex shrink-0 items-center gap-2">{command}</div>}
    </section>
  )
}

/**
 * An engine's view while its read is out: its line, the lines its readings
 * will stand on, and grey rows, so the view has its shape before it has its
 * figures.
 */
export function EngineWaiting({
  id,
  name,
  lines,
}: {
  id: EngineId
  name: string
  /** How many reading lines the view opens with. */
  lines: number
}) {
  return (
    <>
      <EngineHead
        id={id}
        name={name}
        state={<Status tone="unknown" label="reading" />}
        facts={<span>&nbsp;</span>}
      />
      {lines > 0 && (
        <ReadingLines aria-hidden>
          {Array.from({ length: lines }, (_, index) => (
            <ReadingLine
              key={index}
              mark={
                <WireMark size="md">
                  <span />
                </WireMark>
              }
              name={" "}
            >
              <NoReading>No reading yet</NoReading>
            </ReadingLine>
          ))}
        </ReadingLines>
      )}
      <LoadingRows rows={4} hold />
    </>
  )
}
