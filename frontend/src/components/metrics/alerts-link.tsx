"use client"

import Link from "next/link"
import { Bell } from "@/components/icons"
import { get } from "@/lib/api"
import { hostAlertCounts, type HostAlerts } from "@/lib/host-alerts"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Button } from "@/components/ui/button"

/**
 * The way from Metrics to its Alerts, with what is firing counted in its
 * tone, as the proxy overview's bar has it: a bell with nothing beside it
 * says nothing about whether anybody is being told something right now. Only
 * an administrator has the page, so only an administrator asks.
 */
export function HostAlertsLink() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const alerts = usePoll(
    (signal) => get<HostAlerts>("/system/alerts", undefined, signal),
    60_000,
    [],
    { enabled: admin },
  )
  if (!admin) return null
  const { firing, holding } = hostAlertCounts(alerts.error ? [] : (alerts.data?.subjects ?? []))
  return (
    <Button asChild size="sm" variant="ghost">
      <Link
        href="/metrics/alerts"
        aria-label={firing > 0 ? `Alerts, ${firing} firing` : "Alerts"}
        className="gap-1.5"
      >
        <Bell className="size-3.5" />
        Alerts
        {firing > 0 ? (
          <span className="numeric flex items-center gap-1 text-hint font-medium text-destructive">
            <span aria-hidden className="size-1.5 animate-pulse rounded-full bg-destructive" />
            {firing}
          </span>
        ) : holding > 0 ? (
          <span className="numeric flex items-center gap-1 text-hint font-medium text-warning">
            <span aria-hidden className="size-1.5 rounded-full bg-warning" />
            {holding}
          </span>
        ) : null}
      </Link>
    </Button>
  )
}
