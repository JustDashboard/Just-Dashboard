import { Tag } from "@/components/tag"
import { statementVerb } from "@/components/database/home/kinds"

/**
 * What a console command does, said one way in both consoles — MongoDB's
 * `db.runCommand` prompt and the Redis console — and in the Redis command
 * reference.
 *
 * The two consoles had two vocabularies: MongoDB coloured the command's first
 * word in the statement-effect hue and showed no tag, Redis tagged it
 * ("writes", "removes data", "not run here") and coloured nothing. Each class
 * now has one word, one tag tone and one hue, the hue being the statement
 * legend's (`home/kinds.ts`), so a write is the violet it is in every list of
 * statements. A read is the ordinary case: it has its hue and no tag.
 */
export type CommandClass = "read" | "write" | "removes" | "refused"

export const COMMAND_CLASS: Record<
  CommandClass,
  { word: string; tone: "default" | "warning" | "danger"; color: string | undefined }
> = {
  read: { word: "reads", tone: "default", color: statementVerb("select")?.color },
  write: { word: "writes", tone: "default", color: statementVerb("update")?.color },
  removes: { word: "removes or stops", tone: "warning", color: statementVerb("delete")?.color },
  // Never sent: nothing it would do happens, so it takes no effect's hue.
  refused: { word: "not run here", tone: "danger", color: undefined },
}

/** The tag a command's class takes in a transcript or a reference row; nothing for a read. */
export function CommandClassTag({ value, admin }: { value: CommandClass; admin?: boolean }) {
  return (
    <>
      {value !== "read" && <Tag tone={COMMAND_CLASS[value].tone}>{COMMAND_CLASS[value].word}</Tag>}
      {admin && <Tag>admin</Tag>}
    </>
  )
}

/** How long a command took, one way in both transcripts: "0.42 ms", "18 ms", "1,204 ms". */
export function commandMs(ms: number): string {
  return `${ms < 10 ? ms.toFixed(2) : Math.round(ms).toLocaleString("en-US")} ms`
}

/** How many commands a console's transcript keeps, in either console. */
export const TRANSCRIPT_KEPT = 60
