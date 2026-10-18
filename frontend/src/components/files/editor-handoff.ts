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

export function takeDraft(path: string) {
  const draft = drafts.get(path)
  drafts.delete(path)
  return draft
}
