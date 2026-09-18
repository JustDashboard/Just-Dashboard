"use client"

import { useState } from "react"
import { Users, Warning } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { GamePlayers as GamePlayersResponse } from "@/lib/types"

const PLAYER_ACTIONS: { action: string; label: string; destructive?: boolean }[] = [
  { action: "op", label: "Make operator" },
  { action: "deop", label: "Remove operator" },
  { action: "whitelist_add", label: "Add to whitelist" },
  { action: "whitelist_remove", label: "Remove from whitelist" },
  { action: "kick", label: "Kick", destructive: true },
  { action: "ban", label: "Ban", destructive: true },
]

/**
 * Player controls appear only when a tested adapter can actually report
 * identities. A game whose reply this dashboard cannot read gets the console
 * and nothing that pretends to be more. Ported from the pre-rebuild
 * `GamePlayersTab`.
 */
export function GamePlayers({ projectId }: { projectId: number }) {
  const { can } = useAuth()
  const [busy, setBusy] = useState("")
  const players = usePoll(
    (signal) => get<GamePlayersResponse>(`/deploy/${projectId}/game/players`, undefined, signal),
    20000,
    [projectId],
  )

  const act = async (action: string, name: string) => {
    setBusy(`${action}:${name}`)
    try {
      await post(`/deploy/${projectId}/game/players/${action}`, { name })
      notify.success(`${name}: ${action.replace("_", " ")} sent`)
      players.refresh()
    } catch (error) {
      notify.error(`Could not ${action.replace("_", " ")} ${name}`, error)
    } finally {
      setBusy("")
    }
  }

  return (
    <Panel plain>
      <PanelHeader title="Players" />
      <PanelBody className="space-y-3">
        {players.error ? (
          <ErrorState error={players.error} />
        ) : !players.data ? (
          <LoadingRows rows={3} />
        ) : !players.data.supported ? (
          <Notice title="Player list not available" icon={Users}>
            {players.data.reason ??
              "No tested adapter can report player identities for this server. The console remains available."}
          </Notice>
        ) : (
          <>
            {players.data.reason && (
              <Notice title="Partial player list" tone="warning" icon={Warning}>
                {players.data.reason}
              </Notice>
            )}
            {players.data.names.length === 0 ? (
              <EmptyState
                icon={Users}
                title="Nobody is online"
                description={`The server reported ${players.data.online} players connected.`}
                className="border-0 py-6"
              />
            ) : (
              <ul aria-label="Online players" className="divide-y divide-hairline">
                {players.data.names.map((name) => (
                  <li
                    key={name}
                    className="flex min-w-0 flex-wrap items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0"
                  >
                    <span className="min-w-0 font-mono text-body break-all">{name}</span>
                    {can("service.control") && (
                      <div className="flex min-w-0 flex-wrap gap-1.5">
                        {PLAYER_ACTIONS.map((entry) => (
                          <Button
                            key={entry.action}
                            size="xs"
                            variant={entry.destructive ? "outline" : "ghost"}
                            className={entry.destructive ? "text-destructive" : undefined}
                            disabled={busy === `${entry.action}:${name}`}
                            onClick={() => act(entry.action, name)}
                          >
                            {entry.label}
                          </Button>
                        ))}
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            )}
            <p className="text-hint text-muted-foreground">
              Observed {relativeTime(players.data.observedAt)} by asking the server directly.
            </p>
          </>
        )}
      </PanelBody>
    </Panel>
  )
}
