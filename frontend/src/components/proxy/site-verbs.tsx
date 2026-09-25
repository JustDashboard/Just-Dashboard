"use client"

import { useRouter } from "next/navigation"
import {
  CheckCircle,
  Code,
  Copy,
  External,
  Inspect,
  Logs,
  Pencil,
  Slash,
  Trash,
} from "@/components/icons"
import type { VHost } from "@/lib/types"
import type { Verb } from "@/components/verbs"

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

/** The domain to scan, when the site has one and is on TLS. */
export function scanDomain(vhost: VHost): string | undefined {
  if (!vhost.tls) return undefined
  return vhost.serverNames.find((n) => n !== "_" && !n.startsWith("*") && !n.startsWith("~"))
}

/**
 * The access log the site form writes for a site, addressed the way the log
 * viewer addresses a file. A hand-written site may log elsewhere; the viewer
 * says so rather than this guessing.
 */
export function accessLogSource(vhost: VHost): string | undefined {
  if (vhost.kind !== "nginx") return undefined
  const stem = vhost.name.replace(/\.conf$/, "")
  return `/var/log/nginx/${stem}.access.log`
}

export function useSiteVerbs({
  vhost,
  admin,
  busy,
  onEdit,
  onRaw,
  onDuplicate,
  onToggle,
  onDelete,
}: {
  vhost: VHost
  admin: boolean
  /** The present participle a row reports while a change is in flight. */
  busy?: string
  onEdit: (vhost: VHost) => void
  onRaw: (vhost: VHost) => void
  onDuplicate: (vhost: VHost) => void
  onToggle: (vhost: VHost, enabled: boolean) => void
  onDelete: (vhost: VHost) => void
}): Verb[] {
  const router = useRouter()
  const verbs: Verb[] = []
  const managedByForm = vhost.kind === "nginx"
  // A Docker Caddy route has no file on the host: its config lives inside
  // the container and is owned by the deployment that made it.
  const hasFile = Boolean(vhost.path)
  const url = siteUrl(vhost)
  const domain = scanDomain(vhost)
  const log = accessLogSource(vhost)

  if (managedByForm && admin) {
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
      label: admin ? "Raw config" : "View config",
      icon: Code,
      inline: true,
      run: () => onRaw(vhost),
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
  if (log) {
    verbs.push({
      key: "log",
      label: "Access log",
      icon: Logs,
      run: () => router.push(`/logs?source=${encodeURIComponent(log)}`),
    })
  }
  if (managedByForm && admin) {
    verbs.push({
      key: "duplicate",
      label: "Duplicate",
      icon: Copy,
      run: () => onDuplicate(vhost),
    })
  }
  // A conf.d host has no sites-enabled to link into, so there is nothing
  // for a toggle to do — every file there is active. An empty enabledPath
  // is what says which layout this is.
  if (managedByForm && admin && vhost.enabledPath) {
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
  if (managedByForm && admin) {
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
