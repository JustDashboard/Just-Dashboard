/**
 * The ids a log source is asked for by (`parseLogTarget` in the backend).
 *
 * They were spelled by hand wherever a page linked to a log — `docker:` here,
 * a bare path with no `file:` there — and a bare path is a different session
 * key from the `/logs` id for the same file, so the page opened on the wrong
 * remembered filter. Every id is built here and nowhere else.
 */

export function dockerSource(container: string) {
  return `docker:${container}`
}

export function fileSource(path: string) {
  return `file:${path}`
}

/** One unit's journal, or the whole journal for "". */
export function journalSource(unit = "") {
  return `journal:${unit}`
}

/**
 * The journal of what some programs logged under their own names
 * (`journalctl -t`): sshd and sudo on a host with no auth.log, `CRON`'s runs.
 */
export function journalIdSource(idents: readonly string[]) {
  return `journal-id:${idents.join(",")}`
}

/** The kernel's ring, through the journal — the firewall's drops on a host with no kern.log. */
export function kernelSource() {
  return "kernel:"
}

/** Every container of one compose project, merged by time. */
export function stackSource(project: string) {
  return `stack:${project}`
}

/**
 * One PM2 process of one daemon. The account and the name are escaped
 * because either may hold a slash, which the id uses to separate its parts;
 * the numeric id is what tells two processes of the same name apart.
 */
export function pm2Source(daemon: string, id: number, name: string) {
  return `pm2:${encodeURIComponent(daemon)}/${id}/${encodeURIComponent(name)}`
}

/** The kind an id names, from its prefix: `docker:abc` is a container. */
export function sourceKindOf(id: string): string {
  const at = id.indexOf(":")
  return at < 0 ? "" : id.slice(0, at)
}
