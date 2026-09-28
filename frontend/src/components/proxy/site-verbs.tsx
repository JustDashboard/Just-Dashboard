"use client"

import { useRouter } from "next/navigation"
import {
  ChartActivity,
  CheckCircle,
  Clock,
  CloudUpload,
  Code,
  Copy,
  External,
  Inspect,
  Logs,
  Pencil,
  Slash,
  Trash,
  Warning,
  Wrench,
} from "@/components/icons"
import type { VHost } from "@/lib/types"
import type { Verb } from "@/components/verbs"
import { activeOwner } from "@/components/proxy/site-details"

/**
 * A site's verbs, declared once.
 *
 * The table row, the narrow list row and the editor's own header all draw
 * these, so they cannot disagree about what can be done to a site — the
 * pattern `components/verbs.tsx` describes. Two go inline: edit, because it
 * is the daily one, and the raw file, because the form does not own every
 * site. Everything else is a word with a sentence in the menu.
 */

/** The address a visitor would type: the first real name, on the scheme the site serves. */
export function siteUrl(vhost: VHost): string | undefined {
  const name = vhost.serverNames.find((n) => n !== "_" && !n.startsWith("*") && !n.startsWith("~"))
  if (!name) return undefined
  return `${vhost.tls ? "https" : "http"}://${name}`
}

/**
 * Whether the config editor opens the site's file: there is one, and it is
 * inside the proxy's directories. A Docker Caddy route and a link to nothing
 * have no file; a site file linked in from an application's repository is
 * one the editor refuses.
 */
export function opensFile(vhost: VHost): boolean {
  return Boolean(vhost.path) && !vhost.resolvesTo
}

/** The domain to scan, when the site has one and is on TLS. */
export function scanDomain(vhost: VHost): string | undefined {
  if (!vhost.tls) return undefined
  return vhost.serverNames.find((n) => n !== "_" && !n.startsWith("*") && !n.startsWith("~"))
}

/**
 * The site's own page: what it is, its verbs, and its requests and errors
 * read where it is. Every site has one — a Caddy route with no file on the
 * host still has a log to read.
 */
export function sitePath(name: string): string {
  return `/proxy/sites/${encodeURIComponent(name)}`
}

/** The log viewer's address for a file on the host. */
function logHref(path: string) {
  return `/logs?source=${encodeURIComponent(path)}`
}

/**
 * Whether the enable switch applies. It moves a link in sites-enabled and
 * nothing else, so it needs a file in sites-available, not one the form can
 * save: a site file linked in from an application's repository is switched
 * here too. A conf.d host has no sites-enabled to link into, and every file
 * there is active; an empty enabledPath is what says which layout this is. A
 * copy sitting where the link belongs is not replaced by an enable, which
 * says so and changes nothing.
 */
export function siteSwitchable(vhost: VHost): boolean {
  const copied = vhost.broken === "stale" && !vhost.linkTarget
  return (
    vhost.kind === "nginx" &&
    vhost.layout === "sites-available" &&
    Boolean(vhost.enabledPath) &&
    !copied
  )
}

/**
 * Whether Delete applies. It acts by name: sites-available first, then
 * conf.d/<name>, and it removes a sites-enabled entry of that name on the
 * way — so it refuses, and is not offered, where that entry is not the
 * site's own link: a copy nginx serves instead, a link to another site, or a
 * link under another name it would leave pointing at nothing. Nor where the
 * file resolves outside the nginx directory, which the delete does not touch.
 */
export function siteDeletable(vhost: VHost): boolean {
  return (
    !vhost.resolvesTo &&
    ((vhost.formEditable && vhost.broken !== "stale" && !vhost.linkedAs?.length) ||
      (vhost.layout === "conf.d" && vhost.name.endsWith(".conf")))
  )
}

export function useSiteVerbs({
  vhost,
  admin,
  busy,
  ambiguous = false,
  traffic = false,
  history = false,
  onEdit,
  onRaw,
  onServed,
  onDuplicate,
  onToggle,
  onMaintenance,
  onDelete,
  onUnlink,
  onOverride,
}: {
  vhost: VHost
  admin: boolean
  /** The present participle a row reports while a change is in flight. */
  busy?: string
  /** Another nginx entry has this name, so a verb that acts by name alone could act on it. */
  ambiguous?: boolean
  /**
   * The traffic summary and the configuration history answer on this
   * host. Their pages are another part of the proxy's; a verb to one that
   * is not there would lead nowhere.
   */
  traffic?: boolean
  history?: boolean
  onEdit: (vhost: VHost) => void
  onRaw: (vhost: VHost) => void
  /** Opens the separate file sites-enabled holds under the site's name. */
  onServed: (vhost: VHost) => void
  onDuplicate: (vhost: VHost) => void
  onToggle: (vhost: VHost, enabled: boolean) => void
  /** Turns the site's maintenance page on or off; left out where the verb has no place. */
  onMaintenance?: (vhost: VHost, on: boolean) => void
  onDelete: (vhost: VHost) => void
  onUnlink: (vhost: VHost) => void
  /** Asks first, then opens a deployment's route for editing all the same. */
  onOverride: (vhost: VHost) => void
}): Verb[] {
  const router = useRouter()
  const verbs: Verb[] = []
  // A deployment writes its route again on every deploy: an edit made here
  // is lost then, and a disable or a delete takes the application offline
  // behind the deployment's back. Its verbs lead to the deployment instead,
  // and editing anyway is one confirmation away.
  const owner = activeOwner(vhost)
  // The form writes a site back only where it read it from; a conf.d file on
  // a Debian host, or one only in sites-enabled, it would save beside the
  // original under the same server names.
  const form = vhost.formEditable && admin && !owner
  // A Docker Caddy route has no file on the host: its config lives inside
  // the container and is owned by the deployment that made it. Nor has a
  // link to nothing.
  const hasFile = opensFile(vhost)
  const url = siteUrl(vhost)
  const domain = scanDomain(vhost)
  // A separate file sitting in sites-enabled under the site's name: that
  // copy is what nginx serves, not the file the card describes.
  const copied = vhost.broken === "stale" && !vhost.linkTarget
  // A link in sites-enabled that no site's switch owns: one to nothing, or
  // one to a file kept outside sites-available.
  const strayLink =
    vhost.broken === "dangling" || (vhost.layout === "sites-enabled" && Boolean(vhost.enabledPath))

  if (owner) {
    verbs.push({
      key: "deployment",
      label: "Open deployment",
      icon: CloudUpload,
      inline: true,
      run: () => router.push(`/deploy/${owner.projectId}`),
    })
  }
  if (form) {
    verbs.push({
      key: "edit",
      label: "Edit",
      icon: Pencil,
      inline: true,
      disabled: Boolean(busy),
      run: () => onEdit(vhost),
    })
  }
  if (hasFile) {
    verbs.push({
      key: "raw",
      label: admin && !owner ? "Raw config" : "View config",
      icon: Code,
      inline: true,
      run: () => onRaw(vhost),
    })
  }
  if (copied && vhost.enabledPath) {
    verbs.push({
      key: "served",
      label: "Served copy",
      icon: Code,
      run: () => onServed(vhost),
    })
  }
  if (url) {
    verbs.push({
      key: "open",
      label: "Open site",
      icon: External,
      run: () => window.open(url, "_blank", "noopener,noreferrer"),
    })
  }
  if (domain && admin) {
    verbs.push({
      key: "scan",
      label: "TLS report",
      icon: Inspect,
      run: () => router.push(`/proxy/tls?domain=${encodeURIComponent(domain)}`),
    })
  }
  // The site's logs on its own page, where they are read as its requests
  // and its errors rather than as a file on the host Logs page.
  verbs.push({
    key: "log",
    label: "Logs",
    icon: Logs,
    run: () => router.push(sitePath(vhost.name)),
  })
  // The files the site really writes to, as its file and nginx.conf name
  // them, for reading them whole on the host Logs page. The listing keeps
  // only the ones the Logs page lists, so a site that logs nowhere the page
  // can open has no such verb.
  const accessLog = vhost.accessLog
  if (accessLog) {
    verbs.push({
      key: "access-log",
      label: "Access log file",
      icon: Logs,
      run: () => router.push(logHref(accessLog)),
    })
  }
  const errorLog = vhost.errorLog
  if (errorLog) {
    verbs.push({
      key: "errors",
      label: "Error log file",
      icon: Logs,
      run: () => router.push(logHref(errorLog)),
    })
  }
  if (traffic && vhost.kind === "nginx" && vhost.accessLog) {
    verbs.push({
      key: "traffic",
      label: "Traffic",
      icon: ChartActivity,
      run: () => router.push(`/proxy/traffic?site=${encodeURIComponent(vhost.name)}`),
    })
  }
  if (history && admin && hasFile) {
    verbs.push({
      key: "history",
      label: "History",
      icon: Clock,
      run: () => router.push(`/proxy/config?history=${encodeURIComponent(vhost.path)}`),
    })
  }
  if (form) {
    verbs.push({
      key: "duplicate",
      label: "Duplicate",
      icon: Copy,
      run: () => onDuplicate(vhost),
    })
  }
  const switchable = siteSwitchable(vhost)
  // An enable puts a deployment's route back the way the deployment left
  // it; a disable takes its application offline.
  if (admin && switchable && !(owner && vhost.enabled)) {
    verbs.push(
      vhost.enabled
        ? {
            key: "disable",
            label: "Disable",
            icon: Slash,
            progressive: "Disabling",
            disabled: Boolean(busy),
            run: () => onToggle(vhost, false),
          }
        : {
            key: "enable",
            label: "Enable",
            icon: CheckCircle,
            progressive: "Enabling",
            disabled: Boolean(busy),
            run: () => onToggle(vhost, true),
          },
    )
  }
  if (admin && strayLink) {
    verbs.push({
      key: "unlink",
      label: "Remove link",
      icon: Trash,
      danger: true,
      progressive: "Removing",
      disabled: Boolean(busy),
      run: () => onUnlink(vhost),
    })
  }
  const deletable = siteDeletable(vhost)
  if (admin && owner && (vhost.formEditable || hasFile)) {
    verbs.push({
      key: "override",
      label: "Edit anyway",
      icon: Warning,
      disabled: Boolean(busy),
      run: () => onOverride(vhost),
    })
  }
  if (form && onMaintenance) {
    verbs.push(
      vhost.maintenance
        ? {
            key: "maintenance",
            label: "End maintenance",
            icon: Wrench,
            progressive: "Ending maintenance",
            disabled: Boolean(busy),
            run: () => onMaintenance(vhost, false),
          }
        : {
            key: "maintenance",
            label: "Start maintenance",
            icon: Wrench,
            progressive: "Starting maintenance",
            disabled: Boolean(busy),
            run: () => onMaintenance(vhost, true),
          },
    )
  }
  if (admin && deletable && !ambiguous && !owner) {
    verbs.push({
      key: "delete",
      label: "Delete",
      icon: Trash,
      danger: true,
      disabled: Boolean(busy),
      run: () => onDelete(vhost),
    })
  }
  return verbs
}
