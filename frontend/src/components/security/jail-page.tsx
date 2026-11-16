"use client"

import { useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { Crosshair, LockClosed, LockOpen, SettingsSliders, Shield, Slash } from "@/components/icons"
import { get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BlocksView, Fail2banJail, JailPolicy } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { WireMark } from "@/components/wire/wire"
import { DetailPage, siblingSteps } from "@/components/detail-page"
import { Fact } from "@/components/fact-line"
import { IdentityHeader, IdentityVerbs } from "@/components/identity-header"
import { Rail, RailFigure, RailLine, RailName, RailRow } from "@/components/rail"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ReadingLine, ReadingLines } from "@/components/reading-line"
import { BanDialog } from "@/components/security/ban-dialog"
import type { Fail2banStatus } from "@/components/security/intrusion-model"
import {
  JailMark,
  JailPolicySection,
  JailTuning,
  jailHref,
  useJailRelease,
} from "@/components/security/jail-panel"
import { AddressLine, LineCell, LineList, LineRow } from "@/components/security/line-list"
import { useSecurity } from "@/components/security/security-context"
import { EmptyNote, EmptyState, ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { decodeSegment } from "@/lib/route-params"

const BACK_HREF = "/security/intrusion?tab=fail2ban"

const BACK = { href: BACK_HREF, label: "Intrusion" }

/**
 * One jail: what it holds, the policy it runs, and the way to ban by hand.
 *
 * It was a sheet over the Intrusion page: a field to ban an address, the
 * policy and the banned addresses stacked in 380px over the list it came
 * from. A jail's detail is more than a glance (the policy alone is the rule,
 * what it reads, each action and whether it blocks, the ports it covers and
 * the values a restart would change), so it is a page of its own on the
 * detail frame every item page shares (`DetailPage`): the way back with the
 * arrows, the jail's own line with its verbs on it (`IdentityVerbs`), and
 * from `xl` the other jails in a rail (`Rail`), the current one filled,
 * Alt+↑ and Alt+↓ to walk them. On a phone it is one pane that scrolls as a
 * whole.
 */
export function JailPage() {
  const params = useParams<{ jail: string }>()
  const name = decodeSegment(params.jail)
  const router = useRouter()
  const { can } = useAuth()
  const admin = can("system.admin")
  const { exposure } = useSecurity()

  const status = usePoll<Fail2banStatus>((signal) => get("/fail2ban/", undefined, signal), 20_000)
  const blocks = usePoll<BlocksView>((signal) => get("/security/blocks", undefined, signal), 30_000)
  const jails = status.data?.jails ?? []
  const jail = jails.find((candidate) => candidate.name === name)
  const policy = usePoll<JailPolicy>(
    (signal) => get(`/fail2ban/${encodeURIComponent(name)}/policy`, undefined, signal),
    0,
    [name],
    { enabled: Boolean(jail) },
  )

  const changed = () => {
    status.refresh()
    blocks.refresh()
  }
  const [banning, setBanning] = useState(false)
  const [tuning, setTuning] = useState(false)
  const [busy, setBusy] = useState<string>()
  const release = useJailRelease(changed)

  const steps = siblingSteps(
    jails.map((candidate) => candidate.name),
    jail ? name : undefined,
    (target) => router.push(jailHref(target)),
  )

  const act = async (ip: string, path: string, body: unknown, done: string) => {
    setBusy(ip)
    try {
      await post(`/fail2ban/${encodeURIComponent(name)}/${path}`, body)
      notify.success(done)
      changed()
    } catch (err) {
      notify.error("Could not apply", err)
    } finally {
      setBusy(undefined)
    }
  }

  const missing = status.data && !jail
  const verbs: Verb[] = [
    {
      key: "ban",
      label: "Ban an address",
      icon: Slash,
      inline: true,
      run: () => setBanning(true),
    },
    { key: "tune", label: "Tune", icon: SettingsSliders, run: () => setTuning(true) },
    {
      key: "release",
      label: "Release every ban",
      icon: LockOpen,
      disabled: release.busy || !jail || jail.bannedIps.length === 0,
      run: () => void release.releaseAll(name),
    },
  ]

  const lineVerbs = admin && jail ? IdentityVerbs({ verbs, name }) : {}

  return (
    <DetailPage
      name="Jail"
      what="jail"
      stateKey="security.jail.page"
      refresh={status.refresh}
      steps={steps}
      rail={<JailRail jails={jails} current={name} loading={status.loading && !status.data} />}
      back={BACK}
      title={name}
      identity={
        status.error && !status.data ? undefined : missing ? undefined : (
          <IdentityHeader
            label={`About ${name}`}
            slot="jail-identity"
            className="border-b-0 pb-0"
            loading={!jail}
            logo={<JailMark name={name} className="size-10 rounded-lg [&_img]:size-6" />}
            title={jail ? name : <TextShimmer>{`Reading ${name}…`}</TextShimmer>}
            state={
              jail ? (
                <Status
                  tone={jail.currentlyBanned > 0 ? "warning" : "running"}
                  label={
                    jail.currentlyBanned > 0 ? `${jail.currentlyBanned} banned now` : "Watching"
                  }
                />
              ) : (
                true
              )
            }
            facts={
              jail ? (
                <>
                  <Fact>fail2ban jail</Fact>
                  <Fact mono className="truncate">
                    {jail.fileList.join(", ") || "no log named"}
                  </Fact>
                </>
              ) : undefined
            }
            command={jail ? lineVerbs.command : admin ? true : undefined}
            menu={jail ? lineVerbs.menu : admin ? false : undefined}
          />
        )
      }
    >
      {status.error && !status.data ? (
        <ErrorState error={status.error} onRetry={status.refresh} retryable />
      ) : missing ? (
        <EmptyState
          icon={Slash}
          title={
            status.data?.running ? `fail2ban has no jail named ${name}` : "fail2ban is not running"
          }
          description={
            status.data?.running
              ? "It may have been disabled since this link was made."
              : "A jail is read from the running server, so there is nothing to show until it is."
          }
          action={
            <Button size="sm" variant="outline" asChild>
              <Link href={BACK_HREF}>Back to Intrusion</Link>
            </Button>
          }
        />
      ) : (
        <>
          {/* One to a row beside the rail, where two would cut their sentences. */}
          <ReadingLines className="xl:grid-cols-1 2xl:grid-cols-2">
            <ReadingLine
              mark={
                <WireMark size="md" hue="var(--tag-amber)">
                  <Crosshair aria-hidden />
                </WireMark>
              }
              name="Failing now"
              hint={
                jail && (
                  <span className="numeric">
                    {plural(jail.totalFailed, "failure")} since fail2ban last started
                  </span>
                )
              }
            >
              {!jail ? (
                <span className="text-muted-foreground">No reading yet</span>
              ) : jail.currentlyFailed === 0 ? (
                "Nobody is inside the window"
              ) : (
                <span className="numeric">
                  {plural(jail.currentlyFailed, "attempt")} inside the window
                </span>
              )}
            </ReadingLine>
            <ReadingLine
              mark={
                <WireMark size="md" hue="var(--tag-red)">
                  <LockClosed aria-hidden />
                </WireMark>
              }
              name="Banned"
              hint={
                jail && (
                  <span className="numeric">
                    {plural(jail.totalBanned, "ban")} since fail2ban last started
                  </span>
                )
              }
            >
              {!jail ? (
                <span className="text-muted-foreground">No reading yet</span>
              ) : jail.currentlyBanned === 0 ? (
                "No address is held"
              ) : (
                <span className="numeric">
                  {plural(jail.currentlyBanned, "address", "addresses")} held right now
                </span>
              )}
            </ReadingLine>
          </ReadingLines>

          <Panel plain>
            <PanelHeader title="Banned now" count={jail?.bannedIps.length} />
            <PanelBody flush>
              {!jail ? (
                <LoadingRows rows={3} hold />
              ) : jail.bannedIps.length === 0 ? (
                <EmptyNote>
                  Nothing is banned right now. Bans expire: the record is Activity, on the Intrusion
                  page.
                </EmptyNote>
              ) : (
                <LineList columns="minmax(0,1fr) auto" label="Banned addresses">
                  {jail.bannedIps.map((ip) => (
                    <LineRow key={ip} className="h-11">
                      <LineCell className="min-w-0">
                        <AddressLine value={ip} />
                      </LineCell>
                      <LineCell end>
                        {/* The row's verbs behind its one ⋯, named and worded, where
                                two bare icons (an open lock, a shield) said neither. */}
                        {admin && (
                          <VerbMenu
                            label={`Actions for ${ip}`}
                            verbs={[
                              {
                                key: "unban",
                                label: `Unban ${ip}`,
                                icon: LockOpen,
                                disabled: busy === ip,
                                run: () => void act(ip, "unban", { ip }, `${ip} unbanned`),
                              },
                              {
                                key: "ignore",
                                label: `Never ban ${ip} again`,
                                icon: Shield,
                                disabled: busy === ip,
                                run: () =>
                                  void act(
                                    ip,
                                    "ignore",
                                    { ip, add: true },
                                    `${ip} added to the allowlist`,
                                  ),
                              },
                            ]}
                          />
                        )}
                      </LineCell>
                    </LineRow>
                  ))}
                </LineList>
              )}
            </PanelBody>
          </Panel>

          <JailPolicySection policy={policy} />
        </>
      )}

      <BanDialog
        open={banning}
        onOpenChange={setBanning}
        targets={[{ engine: "fail2ban", jail: name }]}
        blocks={blocks.data}
        onBanned={changed}
      />
      {tuning && (
        <JailTuning
          jail={name}
          clientIp={exposure?.client}
          open
          onOpenChange={(open) => !open && setTuning(false)}
          onSaved={() => {
            changed()
            policy.refresh()
          }}
        />
      )}
    </DetailPage>
  )
}

/**
 * The other jails, beside the one being read (`Rail`): the list this page
 * was opened from, so the next jail is one press and the current one is
 * filled. Each row is the jail's mark with its state in the corner, its name
 * over the log it reads, and how many it holds.
 */
function JailRail({
  jails,
  current,
  loading,
}: {
  jails: Fail2banJail[]
  current: string
  loading: boolean
}) {
  return (
    <Rail
      slot="jail-rail"
      label="Jails"
      caption={loading ? "Reading the jails…" : plural(jails.length, "jail")}
      rows={jails}
      keyOf={(jail) => jail.name}
      current={(jail) => jail.name === current}
      row={(jail, here) => (
        <RailRow
          item={jail.name}
          name={jail.name}
          label={`${jail.name}, ${jail.currentlyBanned} banned`}
          current={here}
          href={jailHref(jail.name)}
          mark={
            <JailMark
              name={jail.name}
              className="size-7 rounded-md [&_img]:size-4 [&_svg]:size-4"
            />
          }
          dot={
            <StatusDot
              tone={jail.currentlyBanned > 0 ? "warning" : "running"}
              className="size-1.5"
            />
          }
          end={<RailFigure>{jail.currentlyBanned}</RailFigure>}
        >
          <RailName current={here}>{jail.name}</RailName>
          <RailLine className="font-mono">{jail.fileList[0] ?? "no log named"}</RailLine>
        </RailRow>
      )}
      loading={loading}
      loadingRows={3}
    />
  )
}
