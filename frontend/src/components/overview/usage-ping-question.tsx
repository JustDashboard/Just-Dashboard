"use client"

import { siteHost } from "@/lib/account-link"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { useUsagePing } from "@/hooks/use-usage-ping"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"

/**
 * The one time this server asks whether it may be counted: for an administrator, on the Overview,
 * until any administrator answers. It is on the page rather than over it, because nothing is lost
 * by leaving it unanswered: nothing is sent until somebody says yes. The report itself is printed
 * in the question, so the yes is to the bytes rather than to a description of them.
 */
export function UsagePingQuestion() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { status, answering, answer } = useUsagePing({ enabled: admin })

  if (!admin || !status?.enabled || status.choice !== "undecided") return null

  const reply = (on: boolean) =>
    answer(on).catch((err) => notify.error("Could not save that", errorMessage(err)))

  return (
    <Notice title="Count this server in the public install numbers?">
      <p>
        Once a day it would send this line to {siteHost(status.siteUrl)} and nothing else: no ID and
        no hostname, and the site keeps no IP address. You can change this under Settings →
        Configuration.
      </p>
      <pre className="mt-2 font-mono text-hint break-all whitespace-pre-wrap text-foreground">
        {status.nextBody}
      </pre>
      <div className="mt-3 flex flex-wrap gap-2">
        <Button size="xs" pending={answering} onClick={() => reply(true)}>
          Count this server
        </Button>
        <Button size="xs" variant="outline" disabled={answering} onClick={() => reply(false)}>
          No thanks
        </Button>
      </div>
    </Notice>
  )
}
