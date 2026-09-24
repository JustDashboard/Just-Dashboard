"use client"

import { useState } from "react"
import {
  Logout,
  Shield,
  ShieldOff,
  Slash,
  UserMinus,
  UserPlus,
  Users,
  Warning,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { GamePlayers as GamePlayersResponse } from "@/lib/types"
import { useArrivals } from "@/hooks/use-arrivals"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { InitialsMark } from "@/components/account/user-avatar"
import { useConfirm } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { VerbActions, type Verb } from "@/components/verbs"
import { GameIdentity, useGameOverview } from "@/components/deploy/game/identity"

type PlayerAction = "op" | "deop" | "whitelist_add" | "whitelist_remove" | "kick" | "ban"

/** What each action does to a player, for the menu's sentence and the toast after it. */
const ACTIONS: Record<
  PlayerAction,
  { label: string; progressive: string; done: string; detail: string }
> = {
  op: {
    label: "Make operator",
    progressive: "Promoting",
    done: "is now an operator",
    detail: "Lets them run server commands from inside the game.",
  },
  deop: {
    label: "Remove operator",
    progressive: "Demoting",
    done: "is no longer an operator",
    detail: "Takes server commands away from them.",
  },
  whitelist_add: {
    label: "Add to whitelist",
    progressive: "Whitelisting",
    done: "was added to the whitelist",
    detail: "Lets them join while Whitelist only is on.",
  },
  whitelist_remove: {
    label: "Remove from whitelist",
    progressive: "Removing",
    done: "was removed from the whitelist",
    detail: "They can no longer join while the whitelist is on.",
  },
  kick: {
    label: "Kick",
    progressive: "Kicking",
    done: "was kicked",
    detail: "Disconnects them now; they can join again straight away.",
  },
  ban: {
    label: "Ban",
    progressive: "Banning",
    done: "was banned",
    detail: "Disconnects them and refuses them until they are pardoned from the console.",
  },
}

/**
 * Who is on the server, each drawn as a person is drawn everywhere else in
 * the product — initials in a hue picked by their name (§14), the same hue
 * their name takes in the game console's replies — with the moderation verbs
 * declared once (§13): kicking is the one pressed daily and stays inline, the
 * rest go behind one menu where each carries its sentence, and a ban asks
 * first. While an action is on its way the row says so in the present tense
 * until the server's next answer lands.
 *
 * Player controls appear only when a tested adapter can actually report
 * identities. A game whose reply this dashboard cannot read gets the console
 * and nothing that pretends to be more.
 */
export function GamePlayers({ projectId }: { projectId: number }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  // One entry per player, so an answer for one never clears another's.
  // `sent` marks an action the server took, waiting for the list to show it.
  const [pending, setPending] = useState<Record<string, { action: PlayerAction; sent: boolean }>>(
    {},
  )
  const overview = useGameOverview(projectId)
  const players = usePoll(
    (signal) => get<GamePlayersResponse>(`/deploy/${projectId}/game/players`, undefined, signal),
    20000,
    [projectId],
  )
  // A list that landed after an action was taken is the one that shows it,
  // so that is when its row stops saying "Kicking…" — not when the request
  // returned, which left the row still for a moment and then jumped. Adjusted
  // during render, as `useArrivals` is: no effect and no second paint.
  const [answered, setAnswered] = useState(players.data)
  if (players.data !== answered) {
    setAnswered(players.data)
    if (Object.values(pending).some((entry) => entry.sent))
      setPending((previous) =>
        Object.fromEntries(Object.entries(previous).filter(([, entry]) => !entry.sent)),
      )
  }
  const names = players.data?.names ?? []
  const arrived = useArrivals(names)
  const address = overview.data?.address

  const act = async (action: PlayerAction, name: string) => {
    setPending((previous) => ({ ...previous, [name]: { action, sent: false } }))
    try {
      await post(`/deploy/${projectId}/game/players/${action}`, { name })
      notify.success(`${name} ${ACTIONS[action].done}`)
      setPending((previous) => ({ ...previous, [name]: { action, sent: true } }))
      players.refresh()
      overview.refresh()
    } catch (error) {
      notify.error(`${ACTIONS[action].label} did not reach ${name}`, error)
      setPending((previous) =>
        Object.fromEntries(Object.entries(previous).filter(([key]) => key !== name)),
      )
    }
  }

  const verbsFor = (name: string): Verb[] => {
    const verb = (action: PlayerAction, icon: Verb["icon"], extra: Partial<Verb> = {}): Verb => ({
      key: action,
      label: ACTIONS[action].label,
      detail: ACTIONS[action].detail,
      progressive: ACTIONS[action].progressive,
      icon,
      disabled: name in pending,
      run: () => void act(action, name),
      ...extra,
    })
    return [
      verb("kick", Logout, { inline: true, danger: true }),
      verb("op", Shield, { group: "Operator" }),
      verb("deop", ShieldOff, { group: "Operator" }),
      verb("whitelist_add", UserPlus, { group: "Whitelist" }),
      verb("whitelist_remove", UserMinus, { group: "Whitelist" }),
      verb("ban", Slash, {
        danger: true,
        run: () =>
          confirm({
            title: "Ban this player",
            subject: {
              mark: <InitialsMark name={name} size="sm" />,
              name,
              facts: address && (
                <FormFact label="Server" mono>
                  {address}
                </FormFact>
              ),
            },
            description: ACTIONS.ban.detail,
            confirmLabel: "Ban",
            action: () => act("ban", name),
          }),
      }),
    ]
  }

  return (
    <div className="space-y-6">
      {overview.data && <GameIdentity overview={overview.data} />}
      <Panel plain>
        <PanelHeader
          title="Players"
          actions={
            players.data?.supported && (
              <span className="text-hint text-muted-foreground">
                observed {relativeTime(players.data.observedAt)}
              </span>
            )
          }
        />
        <PanelBody className="space-y-3">
          {players.error ? (
            <ErrorState error={players.error} />
          ) : !players.data ? (
            <LoadingRows rows={3} />
          ) : !players.data.supported ? (
            <Notice title="Player list not available" icon={Slash}>
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
              {names.length === 0 ? (
                <EmptyState
                  icon={Users}
                  title="Nobody is online"
                  description={
                    <>
                      Up to {players.data.maximum} can join
                      {address && (
                        <>
                          {" "}
                          at <span className="font-mono text-foreground">{address}</span>
                        </>
                      )}
                      .
                    </>
                  }
                  className="border-0 py-6"
                />
              ) : (
                <RowList aria-label="Online players">
                  {names.map((name) => (
                    <Row
                      key={name}
                      className={
                        arrived.has(name)
                          ? "group animate-rise transition-colors hover:bg-row-hover"
                          : "group transition-colors hover:bg-row-hover"
                      }
                      leading={<InitialsMark name={name} size="sm" />}
                      title={name}
                      trailing={
                        pending[name] && (
                          <Status
                            tone="warning"
                            label={`${ACTIONS[pending[name].action].progressive}…`}
                          />
                        )
                      }
                    >
                      {can("service.control") && (
                        <VerbActions dim verbs={verbsFor(name)} menuLabel={`Actions for ${name}`} />
                      )}
                    </Row>
                  ))}
                </RowList>
              )}
            </>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </div>
  )
}
