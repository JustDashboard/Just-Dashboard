"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import {
  ArrowLeft,
  ChevronDown,
  ChevronUp,
  Crosshair,
  LockClosed,
  LockOpen,
  SettingsSliders,
  Shield,
  Slash,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BlocksView, Fail2banJail, JailPolicy } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { WireMark } from "@/components/deploy/wire"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
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
import { EmptyNote, EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"

const BACK_HREF = "/security/intrusion?tab=fail2ban"

const BACK = (
  <Link
    href={BACK_HREF}
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    {/* The size every item page's way back has: a larger arrow made the
        line taller than the row centres, and the words stood two pixels low. */}
    <ArrowLeft aria-hidden className="size-3" />
    Intrusion
  </Link>
)

/**
 * One jail: what it holds, the policy it runs, and the way to ban by hand.
 *
 * It was a sheet over the Intrusion page: a field to ban an address, the
 * policy and the banned addresses stacked in 380px over the list it came
 * from. A jail's detail is more than a glance (the policy alone is the rule,
 * what it reads, each action and whether it blocks, the ports it covers and
 * the values a restart would change), so it is a page of its own on the
 * shape a process's page has: the way back, the jail's own line with its
 * verbs, and from `xl` the other jails in a rail, the current one filled,
 * Alt+↑ and Alt+↓ to walk them. On a phone it is one pane that scrolls as a
 * whole.
 */
export function JailPage() {
  const params = useParams<{ jail: string }>()
  const name = decodeURIComponent(params.jail ?? "")
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

  const at = jails.findIndex((candidate) => candidate.name === name)
  const previous = at > 0 ? jails[at - 1] : undefined
  const next = at >= 0 ? jails[at + 1] : undefined
  const previousName = previous?.name
  const nextName = next?.name
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (!event.altKey || event.ctrlKey || event.metaKey) return
      const target = event.key === "ArrowUp" ? previousName : nextName
      if ((event.key !== "ArrowUp" && event.key !== "ArrowDown") || !target) return
      event.preventDefault()
      router.push(jailHref(target))
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [previousName, nextName, router])

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

  const walk = (
    <span className="flex items-center">
      <IconAction
        label="Previous jail"
        disabled={!previous}
        onClick={() => previous && router.push(jailHref(previous.name))}
      >
        <ChevronUp />
      </IconAction>
      <IconAction
        label="Next jail"
        disabled={!next}
        onClick={() => next && router.push(jailHref(next.name))}
      >
        <ChevronDown />
      </IconAction>
    </span>
  )

  const missing = status.data && !jail
  const verbs: Verb[] = [
    { key: "tune", label: "Tune", icon: SettingsSliders, run: () => setTuning(true) },
    {
      key: "release",
      label: "Release every ban",
      icon: LockOpen,
      disabled: release.busy || !jail || jail.bannedIps.length === 0,
      run: () => void release.releaseAll(name),
    },
  ]

  return (
    <Page className="animate-rise">
      <div className="flex min-w-0 gap-6">
        <JailRail jails={jails} current={name} loading={status.loading && !status.data} />
        <div className="flex min-w-0 flex-1 flex-col gap-6 md:gap-8">
          <PageContext eyebrow={BACK} title={name} actions={walk} />

          {status.error && !status.data ? (
            <ErrorState error={status.error} onRetry={status.refresh} retryable />
          ) : missing ? (
            <EmptyState
              icon={Slash}
              title={
                status.data?.running
                  ? `fail2ban has no jail named ${name}`
                  : "fail2ban is not running"
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
              <HostIdentity
                label={`About ${name}`}
                slot="jail-identity"
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
                    <Status tone="unknown" label="reading" />
                  )
                }
                facts={
                  jail ? (
                    <>
                      <span>fail2ban jail</span>
                      <FactDot />
                      <span className="min-w-0 truncate font-mono">
                        {jail.fileList.join(", ") || "no log named"}
                      </span>
                    </>
                  ) : (
                    <span>&nbsp;</span>
                  )
                }
                command={
                  admin && jail ? (
                    <Button size="sm" onClick={() => setBanning(true)}>
                      <Slash aria-hidden />
                      <span className="max-sm:sr-only">Ban an address</span>
                    </Button>
                  ) : undefined
                }
                menu={
                  admin && jail ? (
                    <VerbMenu verbs={verbs} label={`Actions for ${name}`} />
                  ) : undefined
                }
              />

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
                <PanelHeader
                  title="Banned now"
                  actions={
                    jail && (
                      <span className="numeric text-hint text-muted-foreground">
                        {jail.bannedIps.length}
                      </span>
                    )
                  }
                />
                <PanelBody flush>
                  {!jail ? (
                    <LoadingRows rows={3} hold />
                  ) : jail.bannedIps.length === 0 ? (
                    <EmptyNote>
                      Nothing is banned right now. Bans expire: the record is Activity, on the
                      Intrusion page.
                    </EmptyNote>
                  ) : (
                    <LineList columns="minmax(0,1fr) auto" label="Banned addresses">
                      {jail.bannedIps.map((ip) => (
                        <LineRow key={ip} className="h-11">
                          <LineCell className="min-w-0">
                            <AddressLine value={ip} />
                          </LineCell>
                          <LineCell end>
                            {admin && (
                              <>
                                <IconAction
                                  label={`Unban ${ip}`}
                                  disabled={busy === ip}
                                  onClick={() => void act(ip, "unban", { ip }, `${ip} unbanned`)}
                                >
                                  <LockOpen />
                                </IconAction>
                                <IconAction
                                  label={`Never ban ${ip} again`}
                                  disabled={busy === ip}
                                  onClick={() =>
                                    void act(
                                      ip,
                                      "ignore",
                                      { ip, add: true },
                                      `${ip} added to the allowlist`,
                                    )
                                  }
                                >
                                  <Shield />
                                </IconAction>
                              </>
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
        </div>
      </div>

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
    </Page>
  )
}

/**
 * The other jails, beside the one being read: the list this page was opened
 * from, so the next jail is one press and the current one is filled.
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
    <nav
      aria-label="Jails"
      data-slot="jail-rail"
      className="hidden w-64 shrink-0 flex-col border-r border-hairline pr-4 xl:flex"
    >
      <p className="eyebrow flex h-8 items-center justify-between">
        Jails
        <span className="numeric tracking-normal">{jails.length > 0 && jails.length}</span>
      </p>
      <ol className="-mx-2 space-y-0.5 px-2">
        {loading && <LoadingRows rows={3} />}
        {jails.map((jail) => {
          const here = jail.name === current
          return (
            <li key={jail.name} className="min-w-0">
              <Link
                href={jailHref(jail.name)}
                aria-current={here ? "page" : undefined}
                className={cn(
                  "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 focus-ring-inset transition-colors",
                  here ? "bg-accent" : "hover:bg-row-hover",
                )}
              >
                <JailMark
                  name={jail.name}
                  className="size-7 rounded-md [&_img]:size-4 [&_svg]:size-4"
                />
                <span className="min-w-0 flex-1">
                  <span
                    className={cn(
                      "block truncate text-body",
                      here ? "font-semibold" : "font-medium",
                    )}
                  >
                    {jail.name}
                  </span>
                  <span className="block truncate font-mono text-hint text-muted-foreground">
                    {jail.fileList[0] ?? "no log named"}
                  </span>
                </span>
                <span className="flex shrink-0 items-center gap-1.5">
                  <StatusDot tone={jail.currentlyBanned > 0 ? "warning" : "running"} />
                  <span className="numeric font-mono text-hint text-muted-foreground">
                    {jail.currentlyBanned}
                  </span>
                </span>
              </Link>
            </li>
          )
        })}
      </ol>
    </nav>
  )
}
