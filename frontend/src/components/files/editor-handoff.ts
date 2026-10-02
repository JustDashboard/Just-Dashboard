// Kept in memory for the one navigation from sheet to page. Sensitive file
// contents never become a localStorage or sessionStorage preference.
const drafts = new Map<string, { content: string; language?: string }>()

export function handoffDraft(path: string, content: string, language?: string) {
  drafts.set(path, { content, language })
}

export function takeDraft(path: string) {
  const draft = drafts.get(path)
  drafts.delete(path)
  return draft
}
