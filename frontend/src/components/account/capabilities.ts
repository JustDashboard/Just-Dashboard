import type { Capability, Role } from "@/lib/types"

/**
 * What each capability lets an account do, in the words a person reading their
 * own profile needs. The backend's names are the contract; these sentences are
 * the explanation of why a page is missing from somebody's sidebar.
 */
export const CAPABILITIES: { key: Capability; title: string; description: string }[] = [
  {
    key: "read",
    title: "Read everything",
    description: "Metrics, logs, containers, files, databases and configuration, as they are.",
  },
  {
    key: "service.control",
    title: "Start and stop",
    description: "Restart containers, services and processes, and run deployments.",
  },
  {
    key: "file.write",
    title: "Change files",
    description: "Edit and upload files, and change what a deployment is built from.",
  },
  {
    key: "terminal",
    title: "Open a shell",
    description: "A root terminal on this server, with everything that implies.",
  },
  {
    key: "destructive",
    title: "Delete for good",
    description: "Volumes, images, backups, host accounts — things that cannot be got back.",
  },
  {
    key: "system.admin",
    title: "Administer the dashboard",
    description: "Dashboard users, everyone's API keys, the audit log and this panel's own settings.",
  },
]

/** One line per role, for a picker that has to say what it is handing out. */
export const ROLE_SUMMARY: Record<Role, string> = {
  admin: "Everything, including other accounts and this dashboard's settings",
  limited: "Start, stop and deploy, and edit files — no deleting, no accounts",
  readonly: "Look at everything, change nothing",
}
