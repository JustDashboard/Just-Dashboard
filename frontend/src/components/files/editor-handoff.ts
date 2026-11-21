// Kept in memory for the one navigation from sheet to page. Sensitive file
// contents never become a localStorage or sessionStorage preference.
import type { FileContent } from "@/lib/types"

const drafts = new Map<string, { content: string; language?: string; original?: FileContent }>()

export function handoffDraft(
  path: string,
  content: string,
  language?: string,
  original?: FileContent,
) {
  drafts.set(path, { content, language, original })
}

/** The draft handed over for this path, left in place: a render may be thrown away and run again. */
export function peekDraft(path: string) {
  return drafts.get(path)
}

/** Forget the draft once the editor that read it is on the page. */
export function dropDraft(path: string) {
  drafts.delete(path)
}
