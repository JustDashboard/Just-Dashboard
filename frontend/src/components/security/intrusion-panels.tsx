"use client"

import { useState } from "react"
import { useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { Play, RefreshClockwise, ShieldOff, Slash } from "@/components/icons"
import { get, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BlocksView, CrowdSecView, SuricataView } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { PageContext } from "@/components/page"
import { BanDialog, banKey, type BanTarget } from "@/components/security/ban-dialog"
import { BlocksPanel } from "@/components/security/blocks-panel"
import { CrowdSecPanel } from "@/components/security/crowdsec-panel"
import { Fail2banPanel } from "@/components/security/fail2ban-panel"
import {
  ENGINE_NAME,
  posture,
  shownTab,
  standings,
  type Fail2banStatus,
  type IntrusionTab,
} from "@/components/security/intrusion-model"
import { IntrusionPicture } from "@/components/security/intrusion-picture"
import { Address } from "@/components/security/marks"
import { useSecurity } from "@/components/security/security-context"
import { SuricataPanel } from "@/components/security/suricata-panel"
import { LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { VerbMenu, type Verb } from "@/components/verbs"

/**
 * Intrusion prevention: three tools that read different things, one at a
 * time under one picture of all three.
 *
 * fail2ban reads this host's logs and bans what it saw misbehave; CrowdSec
 * reads them too and adds what other servers saw, enforced by bouncers;
 * Suricata reads the packets. They are not alternatives (a server can run
 * all three), so the page opens on what is true of them together: its own
 * line with the worst thing true of any engine as its state and the one
 * command that bans by hand, then the picture (`intrusion-picture.tsx`) of
 * the internet, this server and each engine with what it holds.
 *
 * Under the picture one engine is on screen at a time, as a view in the
 * address (`?tab=`), with the addresses every engine and the firewall refuse
 * folded into one list as the fourth view. They used to be stacked: a merged
 * table, then each engine's identity line, tiles, tables and log, nearly six
 * thousand pixels on a laptop, each section waiting on the one above it. An
 * address without a view opens on the first engine that is installed, and one
 * that names an engine that is not shows that engine's way to install it.
 *
 * The page reads all three engines itself, because the line and the picture
 * need every one of them, and hands each view its own read.
 */
export function IntrusionPanels() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { exposure } = useSecurity()

  // One read of every engine's refused addresses, shared by the merged list
  // and by the ban form, which says what already holds an address.
  const blocks = usePoll<BlocksView>((signal) => get("/security/blocks", undefined, signal), 30_000)
  const fail2ban = usePoll<Fail2banStatus>((signal) => get("/fail2ban/", undefined, signal), 20_000)
  const crowdsec = usePoll<CrowdSecView>(
    (signal) => get("/security/crowdsec/", undefined, signal),
    20_000,
  )
  const suricata = usePoll<SuricataView>(
    (signal) => get("/security/suricata/", undefined, signal),
    20_000,
    [],
    { enabled: admin },
  )

  // An engine whose read failed has answered: the page stops waiting on it,
  // and its own view says what went wrong.
  const engines = standings({
    admin,
    fail2ban: fail2ban.data,
    crowdsec: crowdsec.data,
    suricata: suricata.data,
    failed: {
      fail2ban: Boolean(fail2ban.error),
      crowdsec: Boolean(crowdsec.error),
      suricata: Boolean(suricata.error),
    },
  })
  const state = posture(engines)

  // The view is in the address, replaced rather than pushed: an engine is not
  // a place Back should step through. A link that names a view while the page
  // is open (the picture's own nodes do not navigate) is followed.
  const requested = useSearchParams().get("tab")
  const [chosen, setChosen] = useState(requested)
  const [seen, setSeen] = useState(requested)
  if (requested !== seen) {
    setSeen(requested)
    if (requested) setChosen(requested)
  }
  const tab = shownTab(chosen, engines)
  const setTab = (next: string) => {
    setChosen(next)
    const url = new URL(window.location.href)
    url.searchParams.set("tab", next)
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }

  const refreshAll = () => {
    blocks.refresh()
    fail2ban.refresh()
    crowdsec.refresh()
    if (admin) suricata.refresh()
  }
  // What a ban, a release or a start changes: the engine it was made in and
  // the merged list.
  const changed = () => {
    blocks.refresh()
    fail2ban.refresh()
    crowdsec.refresh()
  }

  // Who can take a ban by hand right now: CrowdSec while it runs, and each
  // jail of a running fail2ban.
  const targets: BanTarget[] = [
    ...(crowdsec.data?.installed && crowdsec.data.active ? [{ engine: "crowdsec" as const }] : []),
    ...(fail2ban.data?.running
      ? fail2ban.data.jails.map((jail) => ({ engine: "fail2ban" as const, jail: jail.name }))
      : []),
  ]
  const [banning, setBanning] = useState(false)
  const firstJail = targets.find((target) => target.engine === "fail2ban")
  const banFrom =
    tab === "fail2ban" && firstJail ? banKey(firstJail) : banKey({ engine: "crowdsec" })

  const [starting, setStarting] = useState(false)
  const startFail2ban = async () => {
    setStarting(true)
    try {
      await post("/systemd/fail2ban.service/start")
      notify.success("fail2ban started")
      changed()
    } catch (err) {
      notify.error("Could not start fail2ban", err)
    } finally {
      setStarting(false)
    }
  }
  const verbs: Verb[] = [
    { key: "refresh", label: "Read again", icon: RefreshClockwise, run: refreshAll },
    ...(fail2ban.data?.available && !fail2ban.data.running && can("service.control")
      ? [
          {
            key: "start-fail2ban",
            label: "Start fail2ban",
            icon: Play,
            disabled: starting,
            run: () => void startFail2ban(),
          },
        ]
      : []),
  ]

  const waiting = state === undefined
  const failing = fail2ban.data?.running
    ? fail2ban.data.jails.reduce((n, jail) => n + jail.currentlyFailed, 0)
    : undefined
  const views: { key: IntrusionTab; label: string; count?: number }[] = [
    { key: "fail2ban", label: ENGINE_NAME.fail2ban },
    { key: "crowdsec", label: ENGINE_NAME.crowdsec },
    { key: "suricata", label: ENGINE_NAME.suricata },
    { key: "blocked", label: "Blocked", count: blocks.data?.distinct },
  ]

  return (
    <>
      <PageContext eyebrow="Security" title="Intrusion prevention" />

      <HostIdentity
        label="About intrusion prevention"
        slot="intrusion-identity"
        fallback={ShieldOff}
        title="Intrusion prevention"
        state={
          state ? (
            <Status tone={state.tone} label={state.label} />
          ) : (
            <Status tone="unknown" label="reading" />
          )
        }
        facts={
          <>
            <span className="numeric">
              {blocks.data
                ? `${plural(blocks.data.distinct, "address", "addresses")} refused`
                : blocks.error
                  ? "refused addresses not read"
                  : "reading the engines"}
            </span>
            {blocks.data && blocks.data.duplicated > 0 && (
              <>
                <FactDot />
                <span className="numeric">{blocks.data.duplicated} held more than once</span>
              </>
            )}
            {exposure?.client && (
              <>
                <FactDot />
                <span className="inline-flex items-center gap-1">
                  <span>you</span>
                  <Address ip={exposure.client} className="text-foreground" />
                </span>
              </>
            )}
          </>
        }
        command={
          admin && targets.length > 0 ? (
            <Button size="sm" onClick={() => setBanning(true)}>
              <Slash aria-hidden />
              <span className="max-sm:sr-only">Ban an address</span>
            </Button>
          ) : undefined
        }
        menu={<VerbMenu verbs={verbs} label="Intrusion actions" />}
      />

      <IntrusionPicture
        engines={engines}
        blocks={blocks.data}
        failing={failing}
        loading={waiting}
        onOpen={setTab}
      />

      <Tabs value={tab ?? ""} onValueChange={setTab} className="min-w-0 gap-0">
        <TabsPrimitive.List
          aria-label="Engines"
          className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
        >
          {views.map((view) => (
            <TabsPrimitive.Trigger
              key={view.key}
              value={view.key}
              className={tabClasses(tab === view.key, "h-10")}
            >
              {view.label}
              {view.count !== undefined && view.count > 0 && <ChipCount>{view.count}</ChipCount>}
            </TabsPrimitive.Trigger>
          ))}
        </TabsPrimitive.List>

        {/* Before the engines have answered there is no view to pick, unless
            the address names one: grey rows hold the place of whichever it
            turns out to be. */}
        {tab === undefined && (
          <div className="pt-6">
            <LoadingRows rows={6} hold />
          </div>
        )}
        <TabsContent value="fail2ban" className={VIEW}>
          {tab === "fail2ban" && <Fail2banPanel view={fail2ban} onChanged={changed} />}
        </TabsContent>
        <TabsContent value="crowdsec" className={VIEW}>
          {tab === "crowdsec" && <CrowdSecPanel view={crowdsec} onChanged={changed} />}
        </TabsContent>
        <TabsContent value="suricata" className={VIEW}>
          {tab === "suricata" && <SuricataPanel view={suricata} />}
        </TabsContent>
        <TabsContent value="blocked" className={VIEW}>
          {tab === "blocked" && (
            <BlocksPanel
              data={blocks.data}
              error={blocks.error}
              loading={blocks.loading}
              onRetry={blocks.refresh}
            />
          )}
        </TabsContent>
      </Tabs>

      <BanDialog
        open={banning}
        onOpenChange={setBanning}
        targets={targets}
        initial={banFrom}
        blocks={blocks.data}
        onBanned={changed}
      />
    </>
  )
}

/** A view's own sections, spaced as the page spaces its blocks. */
const VIEW = "min-w-0 flex-col gap-6 pt-6 data-[state=active]:flex md:gap-8"
