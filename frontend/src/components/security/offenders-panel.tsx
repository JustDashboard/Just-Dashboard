"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { notify } from "@/lib/toast"
import { get, ApiError } from "@/lib/api"
import { networkOf } from "@/lib/clients"
import { relativeTime } from "@/lib/format"
import type { BanSummary } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { RankList, RankSkeleton, type RankItem } from "@/components/rank-list"
import { EmptyNote, ErrorState } from "@/components/state"
import { Sparkline } from "@/components/metrics/sparkline"
import { VerbActions } from "@/components/verbs"
import { addressVerbs, blockAddress } from "@/components/security/address-verbs"

/**
 * Who keeps coming back.
 *
 * A jail lists the bans in force this instant, and a ban expires, so the
 * address banned eleven times this week is invisible the moment its eleventh
 * ban lapses, and the page reads as a quiet night. Folding the log the other
 * way round turns it into the one question worth asking of it: this is not a
 * passing scanner, it is somebody working through your host, and a permanent
 * firewall rule is a better answer than another ten-minute ban.
 *
 * It is a ranked list with its figures in columns: how many times each
 * address was banned and its share of every ban in the log. It was a framed
 * table with a red bar under each address, which measured a row against the
 * worst one and said nothing the figure did not.
 *
 * The fold reads fail2ban.log. Where the view's Activity reads fail2ban's
 * journal instead (`journal`: no file, or none the dashboard may read) an
 * empty fold is not "nothing was banned", and Activity's Insights, which
 * rank the addresses banned more than once, is where the same answer is.
 */
export function OffendersPanel({
  onBlocked,
  journal,
}: {
  onBlocked?: () => void
  journal?: boolean
}) {
  const { can } = useAuth()
  const router = useRouter()
  const [blocking, setBlocking] = useState<string | null>(null)
  const { data, error, loading, refresh } = usePoll<BanSummary>(
    (signal) => get("/fail2ban/offenders", { top: 15 }, signal),
    120000,
  )
  const unavailable = error instanceof ApiError && error.code === "fail2ban_unavailable"

  const block = async (ip: string) => {
    setBlocking(ip)
    try {
      await blockAddress(ip, "repeat offender")
      notify.success(`${ip} blocked at the firewall`, {
        description: "A firewall rule outlives a ban, which expires.",
      })
      onBlocked?.()
      refresh()
    } catch (err) {
      notify.error("Could not add the rule", err)
    } finally {
      setBlocking(null)
    }
  }

  const offenders = data?.offenders ?? []
  const items: RankItem[] = offenders.map((offender) => {
    const seen = `first banned ${relativeTime(offender.first)}, last ${relativeTime(offender.last)}`
    return {
      key: offender.ip,
      label: offender.ip,
      hint: `${offender.jails.join(", ")} · last ${relativeTime(offender.last)} · ${networkOf(offender.ip).label}`,
      title: `${offender.ip}: ${offender.bans} bans in ${offender.jails.join(", ")}, ${seen}`,
      value: `${offender.bans.toLocaleString()} ${offender.bans === 1 ? "ban" : "bans"}`,
      share: data && data.bans > 0 ? offender.bans / data.bans : undefined,
      trailing: (
        <VerbActions
          dim
          verbs={addressVerbs({
            ip: offender.ip,
            block: can("system.admin") ? () => void block(offender.ip) : undefined,
            blocking: blocking === offender.ip,
            navigate: (href) => router.push(href),
          })}
        />
      ),
    }
  })

  const pointer = journal && offenders.length === 0 && (unavailable || data)

  return (
    <Panel plain>
      <PanelHeader
        title="Repeat offenders"
        actions={
          data &&
          offenders.length > 0 && (
            <span className="flex min-w-0 items-center gap-3">
              <span className="numeric truncate text-hint text-muted-foreground">
                {data.bans} bans · {data.unbans} releases · since{" "}
                {data.since ? relativeTime(data.since) : "—"}
              </span>
              {data.perDay.length > 1 && (
                <Sparkline
                  values={data.perDay.map((d) => d.count)}
                  color="var(--tag-amber)"
                  label={`Bans per day: ${data.bans} over ${data.perDay.length} days`}
                  className="max-sm:hidden"
                />
              )}
            </span>
          )
        }
      />
      <PanelBody flush className="pt-1">
        {pointer ? (
          <EmptyNote>
            No bans in fail2ban.log. Activity reads fail2ban&rsquo;s journal instead, and its
            Insights rank the addresses banned more than once.
          </EmptyNote>
        ) : unavailable ? (
          <EmptyNote>
            No fail2ban log on this host: it logs only to the journal, so there is no file to fold.
          </EmptyNote>
        ) : error && !data ? (
          <ErrorState error={error} onRetry={refresh} className="mt-3" />
        ) : loading && !data ? (
          <RankSkeleton rows={3} />
        ) : (
          <RankList items={items} emptyLabel="Nothing has been banned yet." />
        )}
      </PanelBody>
    </Panel>
  )
}
