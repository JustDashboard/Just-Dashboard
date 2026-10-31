/**
 * Which GitHub account a commit's email belongs to, where the email says so
 * itself: GitHub's private-email addresses carry the login.
 */
export function noreplyLogin(email: string | undefined): string | undefined {
  return /^(?:\d+\+)?([a-z0-9-]+)@users\.noreply\.github\.com$/i.exec((email ?? "").trim())?.[1]
}

/** The account behind a commit: GitHub's own answer first, then the address itself. */
export function commitLogin(
  authors: Record<string, string> | undefined,
  email: string | undefined,
): string | undefined {
  const key = (email ?? "").trim().toLowerCase()
  return (key && authors?.[key]) || noreplyLogin(key)
}

/**
 * A merge of a pull request as what it brought in. GitHub's sentence puts the
 * only two facts at its two ends and the owner's name between them, so a run
 * of merges read as one long repeated line.
 */
export function commitHeadline(subject: string): string {
  const merge = /^Merge pull request #(\d+) from [^/\s]+\/(\S+)$/.exec(subject)
  return merge ? `Merged #${merge[1]} from ${merge[2]}` : subject
}
