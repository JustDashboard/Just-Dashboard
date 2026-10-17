import { plural } from "@/lib/format"
import type { BackupArchiveKey, BackupJob } from "@/lib/types"

/**
 * Whether a job encrypts: what the operator chose, or — until they choose —
 * the server's own default, which seals an archive that leaves the server and
 * leaves one on this server's disk, beside the key that would open it, as is.
 */
export function encrypts(choice: boolean | null, targetKind: BackupJob["targetKind"]): boolean {
  return choice ?? targetKind !== "local"
}

/** What depends on the backup key, as the panel's header says it. */
export function keyReach(key: BackupArchiveKey): string {
  const parts: string[] = []
  if (key.encryptedArchives > 0) parts.push(plural(key.encryptedArchives, "archive"))
  if (key.encryptingJobs > 0) parts.push(`${plural(key.encryptingJobs, "job")} encrypting`)
  return parts.join(" · ")
}
