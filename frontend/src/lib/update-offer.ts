/**
 * What the Version page says about the release it offers.
 *
 * A version is released when it has a tag `v<version>` on the repository: the
 * project's release workflow makes that tag only once the checks on the merge
 * have passed. For the project's own repository nothing without one is ever
 * offered, so the note confirms it. A fork that never tags is offered the head
 * of its branch instead, and the note is then the only thing telling its
 * operator that what they are about to install is not a release.
 */

type Offer = {
  latest?: string
  available: boolean
  tagged?: boolean
  pending?: string
  check: { ref: string }
}

/** Whether the version on offer is a tagged release, or "" with nothing on offer. */
export function offerTagNote(report: Offer): string {
  if (!report.available || !report.latest) return ""
  return report.tagged
    ? `tagged v${report.latest}`
    : `no release tag, installs ${report.check.ref} as it stands`
}

/**
 * A newer version the tracked branch already names that has no tag yet.
 *
 * It is not offered. Saying so is what answers "the repository says 0.8.6, why
 * does my dashboard say it is up to date".
 */
export function pendingNote(report: Offer): string {
  return report.pending ? `${report.pending} is on ${report.check.ref}, not released yet` : ""
}

/** The update dialog's one sentence. */
export function installSentence(report: Offer & { version: string }): string {
  const target = report.latest ?? ""
  return report.tagged
    ? `Installs ${target} over ${report.version} and restarts the dashboard.`
    : `Installs ${target} over ${report.version} from the head of ${report.check.ref}, which has no release tag, and restarts the dashboard.`
}
