"use client"

import Link from "next/link"
import { External } from "@/components/icons"
import { siteHost } from "@/lib/account-link"
import { errorMessage } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useUsagePing } from "@/hooks/use-usage-ping"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { Well } from "@/components/panel"
import { SwitchRow, SwitchRows } from "@/components/settings-column"
import { Status } from "@/components/status-dot"

/**
 * Settings → Configuration's usage count: the switch, and the report printed as it is sent.
 *
 * It is a section of the settings column and not one of the column's edits to `.env`: it needs no
 * Apply and no restart, and takes effect when it is pressed, which its head says. What the switch
 * does is behind the ⓘ beside its name; what stays on the page is the report itself, byte for
 * byte, since that is the whole of what leaves this server. In agent mode the route does not
 * exist and the section draws nothing.
 */
export function UsagePingSection() {
  const { status, answering, answer } = useUsagePing()
  if (!status) return null

  const site = siteHost(status.siteUrl)
  const on = status.choice === "on"

  return (
    <SettingSection
      title="Usage count"
      state={
        status.enabled ? (
          <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <Status tone={on ? "running" : "unknown"} label={on ? "on" : "off"} />
            <span>applies at once, with no restart</span>
          </span>
        ) : (
          <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <Status tone="stopped" label="turned off" />
            <span>
              <code className="font-mono text-foreground">{status.offBy}=false</code> in this
              server&apos;s <code className="font-mono">.env</code>, so nothing is reported to{" "}
              {site}
            </span>
          </span>
        )
      }
      actions={
        status.enabled && (
          <a
            href={`${status.siteUrl}/stats`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex h-8 items-center gap-1.5 rounded-sm text-xs text-muted-foreground focus-ring transition-colors hover:text-foreground"
          >
            See the totals
            <External aria-hidden className="size-3.5" />
          </a>
        )
      }
    >
      {status.enabled && (
        <>
          <SwitchRows>
            <SwitchRow
              title="Count this server"
              info={`Once a day, adds this server to the public install numbers on ${site}. No ID and no hostname, and the site keeps no IP address.`}
              checked={on}
              disabled={answering}
              onCheckedChange={(next) =>
                answer(next).catch((err) => notify.error("Could not save that", errorMessage(err)))
              }
            />
          </SwitchRows>
          <div className="min-w-0 space-y-1.5">
            <p className="text-xs text-muted-foreground">
              {status.lastSentAt
                ? `Everything sent, ${relativeTime(status.lastSentAt)}:`
                : "Everything it would send:"}
            </p>
            <Well className="text-hint break-all whitespace-pre-wrap">
              {status.lastBody ?? status.nextBody}
            </Well>
            {status.lastError && (
              <p className="text-xs text-warning">
                The last report did not reach {site}: {status.lastError}. It tries again within the
                hour.
              </p>
            )}
          </div>
        </>
      )}
      <p className="text-xs text-muted-foreground">
        Linking this server to an account on {site} is{" "}
        <Link
          href="/dashboard/link"
          className="rounded-sm text-foreground underline-offset-4 focus-ring hover:underline"
        >
          Settings → Connect
        </Link>
        .
      </p>
    </SettingSection>
  )
}
