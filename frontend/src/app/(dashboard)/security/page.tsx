"use client"

import { get } from "@/lib/api"
import type { Connections, Fail2banJail, LoginSession, SSHDConfig } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { Page, PageContext } from "@/components/page"
import { ExposureIdentity } from "@/components/security/exposure-panel"
import { Perimeter, type Read, type SSHWords } from "@/components/security/perimeter"
import { BoundaryPanel } from "@/components/security/boundary-view"
import { useSecurity } from "@/components/security/security-context"
import { sshReading } from "@/components/security/ssh-reading"

type Fail2banReply = { available: boolean; running: boolean; jails: Fail2banJail[] }

const read = <T,>(poll: PollState<T>): Read<T> => ({
  data: poll.data,
  failed: Boolean(poll.error && !poll.data),
})

/**
 * How the panel is reached, the ways onto the machine as one picture whose
 * marks are the ways to the section's pages, and the access-boundary report.
 * The picture carries the readings a row of five tiles used to: each stood
 * over the node that already said it.
 */
export default function SecurityOverviewPage() {
  const { firewall, firewallError, exposure } = useSecurity()
  const { can } = useAuth()
  const admin = can("system.admin")

  const fail2ban = usePoll<Fail2banReply>((signal) => get("/fail2ban/", undefined, signal), 20_000)
  const connections = usePoll<Connections>(
    (signal) => get("/connections", undefined, signal),
    10_000,
  )
  const sessions = usePoll<LoginSession[]>(
    (signal) => get("/ssh-sessions", undefined, signal),
    10_000,
  )
  // sshd's settings name the accounts that hold keys, so the read is an
  // administrator's, as the SSH page's is; it changes only when somebody
  // applies a change there, so it is read once.
  const sshd = usePoll<SSHDConfig>((signal) => get("/ssh/config", undefined, signal), 0, [], {
    enabled: admin,
  })
  // The door that matters most says how it stands: keys only, passwords
  // allowed, or root's password, with where sshd listens.
  const ssh: SSHWords | undefined = !admin
    ? {
        value: "Administrators only",
        tone: "default",
        hint: "its settings name who holds a key",
      }
    : sshd.error && !sshd.data
      ? {
          value: "Unreadable",
          tone: "warning",
          hint: "sshd's configuration could not be read",
        }
      : sshReading(sshd.data ?? undefined)

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Protection" title="Security" />

      <ExposureIdentity exposure={exposure} />

      <Perimeter
        exposure={exposure}
        firewall={{ data: firewall, failed: Boolean(firewallError && !firewall) }}
        fail2ban={read(fail2ban)}
        connections={read(connections)}
        sessions={read(sessions)}
        ssh={ssh}
      />

      {/* The ways in as boundaries every change is judged against: Caddy
          alone on a routable address, the allowlist before sign-in, the
          tailnet, SSH for a tunnel and tailnet-only previews. */}
      <BoundaryPanel />
    </Page>
  )
}
