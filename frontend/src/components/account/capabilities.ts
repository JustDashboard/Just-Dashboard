import type { Capability, Role } from "@/lib/types"
import {
  Eye,
  FolderOpen,
  Play,
  SettingsGear,
  ShieldCheck,
  Terminal,
  Trash,
  Wrench,
  type Icon,
} from "@/components/icons"

/**
 * What each capability lets an account do, in the words a person reading their
 * own profile needs. The backend's names are the contract; these sentences are
 * the explanation of why a page is missing from somebody's sidebar.
 *
 * Each carries the glyph the sidebar draws for what it opens — the terminal's
 * for a shell, the file manager's for files, the settings' for the dashboard
 * itself — so the reader matches a granted power to the entry it put in their
 * rail without reading either (§14's wayfinding).
 */
export const CAPABILITIES: { key: Capability; title: string; description: string; icon: Icon }[] = [
  {
    key: "read",
    title: "Read everything",
    description: "Metrics, logs, containers, files, databases and configuration, as they are.",
    icon: Eye,
  },
  {
    key: "service.control",
    title: "Start and stop",
    description: "Restart containers, services and processes, and run deployments.",
    icon: Play,
  },
  {
    key: "file.write",
    title: "Change files",
    description: "Edit and upload files, and change what a deployment is built from.",
    icon: FolderOpen,
  },
  {
    key: "terminal",
    title: "Open a shell",
    description: "A root terminal on this server, with everything that implies.",
    icon: Terminal,
  },
  {
    key: "destructive",
    title: "Delete for good",
    description: "Volumes, images, backups, host accounts — things that cannot be got back.",
    icon: Trash,
  },
  {
    key: "system.admin",
    title: "Administer the dashboard",
    description:
      "Dashboard users, everyone's API keys, the audit log and this panel's own settings.",
    icon: SettingsGear,
  },
]

/** One line per role, for a picker that has to say what it is handing out. */
export const ROLE_SUMMARY: Record<Role, string> = {
  admin: "Everything, including other accounts and this dashboard's settings",
  limited: "Start, stop and deploy, and edit files — no deleting, no accounts",
  readonly: "Look at everything, change nothing",
}

/**
 * A role's mark, for the pickers that hand one out and the rows that show one:
 * a shield for the role that holds everything, a wrench for the one that
 * operates, an eye for the one that only looks.
 */
export const ROLE_MARK: Record<Role, Icon> = {
  admin: ShieldCheck,
  limited: Wrench,
  readonly: Eye,
}
