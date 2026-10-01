import { LockClosed } from "@/components/icons"
import { LANES, hueFor } from "@/lib/hue"
import { cn } from "@/lib/utils"
import { Tag } from "@/components/tag"

/**
 * The hues the four environments everybody has are known by. They are tag
 * hues, not the status ones: the label says what a database is for, and
 * nothing about how it is doing.
 */
const KNOWN: Record<string, string> = {
  production: "var(--tag-red)",
  staging: "var(--tag-amber)",
  development: "var(--tag-blue)",
  testing: "var(--tag-violet)",
}

/** The label's colour: the known four, and one stable hue for any other word. */
export function environmentHue(environment: string): string {
  const word = environment.trim().toLowerCase()
  return Object.hasOwn(KNOWN, word) ? KNOWN[word] : hueFor(word, LANES)
}

/**
 * What the operator said a database is for: production, staging, development,
 * testing, or a word of their own.
 *
 * A label is a property, not a state (§4), so it is a `Tag` and takes no
 * tone — a production database is not a warning. It keeps one fixed hue of
 * its own all the same, the exception §14 makes for a label somebody applied:
 * "the red one is production" is exactly what it is for, on a page where a
 * wrong database is the expensive mistake.
 *
 * It is the literal kind of tag, not the small-caps one: the word is whatever
 * the operator typed, and small caps would print `eu-west qa` as EU-WEST QA —
 * a name nobody gave it (§8). A long one is cut rather than allowed to push
 * the row it annotates apart, with the whole word on the pointer.
 */
export function EnvironmentTag({
  environment,
  className,
}: {
  environment: string | undefined
  className?: string
}) {
  const word = environment?.trim()
  if (!word) return null
  return (
    <Tag
      mono
      title={word}
      className={cn("max-w-48 min-w-0 shrink", className)}
      style={{ color: environmentHue(word) }}
    >
      <span className="truncate">{word}</span>
    </Tag>
  )
}

/**
 * A protected connection: the dashboard refuses every change to its data or
 * schema. A property of the connection, so it is a tag beside the name and
 * the pages below it simply draw no write control.
 */
export function ProtectedTag({ className }: { className?: string }) {
  return (
    <Tag icon={LockClosed} className={className}>
      protected
    </Tag>
  )
}
