"use client"

import { External, Slash } from "@/components/icons"
import { siteHost } from "@/lib/account-link"
import { errorMessage } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useUsagePing } from "@/hooks/use-usage-ping"
import { OptionList, OptionRow } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"

/**
 * Settings → Configuration's usage count: the switch, and the report printed as it is sent.
 *
 * It sits apart from the Settings panel above it because it is not one of that panel's edits to
 * `.env`: it needs no Apply and no restart, and takes effect when it is pressed. In agent mode the
 * route does not exist and the panel draws nothing.
 */
export function UsagePingPanel() {
  const { status, answering, answer } = useUsagePing()
  if (!status) return null

  const site = siteHost(status.siteUrl)

  return (
    <Panel plain>
      <PanelHeader
        title="Usage count"
        actions={
          status.enabled && (
            <a
              href={`${status.siteUrl}/stats`}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 rounded-sm text-xs text-muted-foreground focus-ring transition-colors hover:text-foreground"
            >
              See the totals
              <External aria-hidden className="size-3.5" />
            </a>
          )
        }
      />
      <PanelBody className="space-y-4">
        {status.enabled ? (
          <>
            <OptionList>
              <OptionRow
                title="Count this server"
                hint={`Once a day, adds this server to the public install numbers on ${site}. No ID and no hostname, and the site keeps no IP address.`}
                checked={status.choice === "on"}
                disabled={answering}
                onCheckedChange={(on) =>
                  answer(on).catch((err) => notify.error("Could not save that", errorMessage(err)))
                }
              />
            </OptionList>
            <div className="min-w-0 space-y-1.5">
              <p className="text-xs text-muted-foreground">
                {status.lastSentAt
                  ? `Everything sent, ${relativeTime(status.lastSentAt)}:`
                  : "Everything it would send:"}
              </p>
              <pre className="font-mono text-hint break-all whitespace-pre-wrap text-foreground">
                {status.lastBody ?? status.nextBody}
              </pre>
              {status.lastError && (
                <p className="text-xs text-warning">
                  The last report did not reach {site}: {status.lastError}. It tries again within
                  the hour.
                </p>
              )}
            </div>
          </>
        ) : (
          <Notice icon={Slash} title="Turned off">
            <code className="font-mono">{status.offBy}=false</code> is set in this server&apos;s{" "}
            <code className="font-mono">.env</code>, so it never reports anything to {site}, and
            nobody here is asked.
          </Notice>
        )}
      </PanelBody>
    </Panel>
  )
}
