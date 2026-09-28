import { relativeTime } from "@/lib/format"
import type { PendingFile, ProxyPending, VHost } from "@/lib/types"

/**
 * What the running nginx has not loaded, as the Sites page says it.
 *
 * A save that did not reload, an edit made over SSH, a switch whose reload
 * failed: nginx keeps serving what it loaded, and the page drew every enabled
 * site as serving what its file says. The backend compares the files with
 * the load nginx is running; these turn its answer into a card's word and the
 * strip's sentences.
 */

/** An answer that can be compared with: a running nginx whose load is known. */
export function loadKnown(
  pending: ProxyPending | undefined,
): pending is ProxyPending & { lastReload: string; generation: string } {
  return Boolean(pending?.running && pending.lastReload && pending.generation)
}

/** The changes that are this card's: its file, as nginx reads it. */
export function siteChanges(pending: ProxyPending | undefined, vhost: VHost): PendingFile[] {
  if (!loadKnown(pending) || vhost.kind !== "nginx") return []
  return pending.files.filter((f) => f.site === vhost.name && f.layout === vhost.layout)
}

/**
 * What a card says in place of "serving" while nginx runs its file as it
 * was: a site linked since is "enabled, not live", an edit is "saved, not
 * live", and a site taken out since — which nginx still serves — is
 * "disabled, not live". A disabled site nginx never loaded has nothing
 * waiting.
 */
export function notLiveLabel(vhost: VHost, changes: PendingFile[]): string | undefined {
  const has = (change: PendingFile["change"]) => changes.some((c) => c.change === change)
  if (!vhost.enabled) return has("removed") ? "disabled, not live" : undefined
  if (has("added")) return "enabled, not live"
  if (has("changed")) return "saved, not live"
  return has("removed") ? "changed, not live" : undefined
}

/** "3 changes on disk are not live yet". */
export function pendingTitle(count: number): string {
  return count === 1
    ? "1 change on disk is not live yet"
    : `${count.toLocaleString()} changes on disk are not live yet`
}

/** A file as the strip names it: from the nginx directory down. */
export function configName(path: string, nginxDir: string | undefined): string {
  const dir = nginxDir?.replace(/\/+$/, "")
  return dir && path.startsWith(dir + "/") ? path.slice(dir.length + 1) : path
}

/** What happened to a file, in one word. */
export function changeVerb(file: PendingFile): string {
  if (file.change === "added") return "linked"
  if (file.change === "removed") return "taken out"
  return "saved"
}

/**
 * Whether nginx has loaded its configuration since the page saw `before`.
 * `nginx -s reload` answers once the signal is sent; nginx starts new workers
 * only if it can load what it reads, and keeps the old ones when it cannot.
 */
export function loadedSince(before: string | undefined, after: ProxyPending | undefined): boolean {
  return Boolean(before && loadKnown(after) && after.generation !== before)
}

/** What the page says when a reload went out and nginx did not load anything newer. */
export const KEPT_LOAD = "nginx kept the configuration it had"

export function keptLoad(lastReload: string): string {
  return `The reload was sent, but nginx is still running what it loaded ${relativeTime(lastReload)}. Its error log says why — often a port another process holds, or a file it cannot open.`
}

const ERROR_LINE = /\[(?:emerg|alert|crit|error)\] (?:\d+#\d+: )?(?:\*\d+ )?(.+)$/m

/**
 * nginx's first error in the output of a refused reload, without its prefix,
 * which is all the reload route answers with.
 */
export function outputHeadline(output: string): string {
  const error = ERROR_LINE.exec(output)
  if (error) return error[1].trim()
  return (
    output
      .split("\n")
      .map((line) => line.trim())
      .find(Boolean) ?? output
  )
}
