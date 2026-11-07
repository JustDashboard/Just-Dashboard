import {
  Archive,
  Bell,
  Box,
  Code,
  Database,
  FirewallCheck,
  Gauge,
  GitHubMark,
  Globe,
  Key,
  Logs,
  MagnifyingGlass,
  Rocket,
  Servers,
  Terminal,
  Users,
  SettingsSliders,
  ShieldCheck,
  Sparkles,
  type Icon,
} from "@/components/icons"
import type { AgentCall } from "@/components/assistant/agent/model"

/**
 * What a step is drawn as: the product it touches when there is one, a glyph
 * otherwise. A list of grey wrenches is a wall of words; Docker's whale, Caddy's
 * lock and GitHub's cat are read long before their labels, the argument §14 makes
 * for a language's logo on a repository row.
 */
export interface StepMark {
  product?: string
  icon: Icon
}

const TOOLS: Record<string, StepMark> = {
  recent_runs: { icon: Rocket },
  run_transcript: { icon: Logs },
  container_logs: { product: "docker", icon: Box },
  release_metrics: { icon: Gauge },
  lifecycle_events: { product: "docker", icon: Box },
  host_alerts: { icon: Bell },
  firewall_rules: { icon: FirewallCheck },
  security_updates: { icon: ShieldCheck },
  // A repository may be GitHub's, another forge's or a folder on the host, so it
  // is drawn as code rather than as one of them.
  read_source: { icon: Code },
  wait_for_run: { icon: Rocket },
  find_routes: { icon: MagnifyingGlass },
  describe_operation: { icon: MagnifyingGlass },
  // A command on the host, as the terminal's account runs it.
  run_command: { icon: Terminal },
  // The chat's own terminal window, typed into and read.
  terminal_run: { icon: Terminal },
  terminal_send: { icon: Terminal },
  terminal_read: { icon: Terminal },
}

const ROUTES: [string, StepMark][] = [
  ["/backups", { icon: Archive }],
  ["/dns-record", { product: "cloudflare", icon: Globe }],
  ["/domains", { product: "caddy", icon: Globe }],
  ["/requests", { product: "caddy", icon: Globe }],
  ["/maintenance", { product: "caddy", icon: Globe }],
  ["/variables", { icon: Key }],
  ["/shared-variables", { icon: Key }],
  ["/source", { product: "github", icon: GitHubMark }],
  ["/commits", { product: "github", icon: GitHubMark }],
  ["/pull-requests", { product: "github", icon: GitHubMark }],
  ["/git-policy", { product: "github", icon: GitHubMark }],
  ["/database", { icon: Database }],
  ["/vulnerabilities", { icon: ShieldCheck }],
  ["/sizing", { icon: Gauge }],
  ["/insights", { icon: Gauge }],
  ["/lifecycle", { product: "docker", icon: Box }],
  ["/configuration", { icon: SettingsSliders }],
  ["/drafts", { icon: SettingsSliders }],
  ["/releases", { product: "docker", icon: Box }],
  ["/rollback", { product: "docker", icon: Box }],
  ["/runs", { icon: Rocket }],
  ["/run", { icon: Rocket }],
  ["/sleep", { product: "docker", icon: Box }],
  // The server's own routes, which a whole-server chat calls.
  ["/docker", { product: "docker", icon: Box }],
  ["/containers", { product: "docker", icon: Box }],
  ["/proxy", { product: "caddy", icon: Globe }],
  ["/certificates", { product: "caddy", icon: ShieldCheck }],
  ["/firewall", { icon: FirewallCheck }],
  ["/security", { icon: ShieldCheck }],
  ["/alerts", { icon: Bell }],
  ["/metrics", { icon: Gauge }],
  ["/system/services", { icon: Servers }],
  ["/services", { icon: Servers }],
  ["/processes", { icon: Terminal }],
  ["/users", { icon: Users }],
  ["/packages", { icon: Archive }],
  ["/logs", { icon: Logs }],
  ["/deploy", { icon: Rocket }],
]

export function stepMark(call: AgentCall): StepMark {
  const tool = TOOLS[call.tool]
  if (tool) return tool
  if (call.route) {
    const found = ROUTES.find(([part]) => call.route!.includes(part))
    if (found) return found[1]
  }
  return { icon: Sparkles }
}
