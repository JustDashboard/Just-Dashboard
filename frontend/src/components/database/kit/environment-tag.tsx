import { LockClosed } from "@/components/icons"
import { LANES, hueFor } from "@/lib/hue"
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
  return KNOWN[word] ?? hueFor(word, LANES)
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
    <Tag className={className} style={{ color: environmentHue(word) }}>
      {word}
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
