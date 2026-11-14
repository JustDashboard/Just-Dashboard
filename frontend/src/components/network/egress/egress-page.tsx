"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import {
  egressDraftFromGroup,
  egressSimulationReading,
  type EgressDraft,
  type EgressGroup,
  type EgressView,
} from "@/lib/network-egress"
import type { PollState } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { ArrowLeft, ChevronDown, ChevronUp, Lifebuoy } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import type { NetworkLink } from "@/lib/types"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { EgressDecisions } from "@/components/network/egress/egress-decisions"
import { EgressGroupForm } from "@/components/network/egress/egress-form"
import { MemoryOnly } from "@/components/network/egress/egress-list"
import { EgressMembers } from "@/components/network/egress/egress-members"
import { EgressPicture } from "@/components/network/egress/egress-picture"
import { EgressRail } from "@/components/network/egress/egress-rail"
import { EgressSimulationPanel } from "@/components/network/egress/egress-simulation"
import { useEgressVerbs } from "@/components/network/egress/egress-verbs"
import { DOT, EgressMark } from "@/components/network/egress/marks"
import {
  EGRESS_TABS,
  egressCarrying,
  egressHref,
  egressNameOf,
  egressSibling,
  egressSimulationFact,
  findEgressGroup,
  type EgressTab,
} from "@/components/network/egress/reading"
import { useEgressLinks, useEgressView } from "@/components/network/egress/use-egress"

const BACK = (
  <Link
    href="/network/egress"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Egress groups
  </Link>
)

type Step = { disabled: boolean; run: () => void }

/**
 * One egress group, as a place of its own.
 *
 * Every group used to be laid out in full on the list, about a thousand
 * pixels each. Its page opens the way a project's does: the identity line
 * with the member carrying its traffic as the state, Run simulation as the
 * one command and every other verb in the `⋯` — fail over, fail back,
 * enable or disable, automation, edit and remove, each confirmed as it
 * always was. Under it the picture of what the group does (the selected
 * traffic, the group, its members, the carrying wire moving), then its
 * members, its decisions and its simulation as views in the address
 * (`?tab=`).
 *
 * From `xl` the list stands beside it (`EgressRail`); Alt+↑ and Alt+↓ walk
 * it and the view on screen goes with you. On a phone the page is one pane
 * that scrolls as a whole.
 */
export function EgressGroupPage() {
  const params = useParams<{ name: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const name = egressNameOf(params.name)
  const requested = search.get("tab") ?? "members"
  const [chosen, setChosen] = useState(requested)
  const tab: EgressTab = (EGRESS_TABS as readonly string[]).includes(chosen)
    ? (chosen as EgressTab)
    : "members"
  const setTab = useCallback((next: string) => {
    setChosen(next)
    // Replaced rather than pushed: the view is not a place Back should step through.
    const url = new URL(window.location.href)
    if (next === "members") url.searchParams.delete("tab")
    else url.searchParams.set("tab", next)
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [])

  const read = useEgressView()
  const links = useEgressLinks(true)
  const groups = read.data?.groups ?? []
  const group = findEgressGroup(groups, name)
  const go = (target?: EgressGroup) => target && router.push(egressHref(target.name, tab))
  const at = group ? groups.indexOf(group) : -1
  const previous: Step = {
    disabled: groups.length === 0 || at === 0,
    run: () => go(egressSibling(groups, name, -1)),
  }
  const next: Step = {
    disabled: groups.length === 0 || at === groups.length - 1,
    run: () => go(egressSibling(groups, name, 1)),
  }

  return (
    <Workspace
      name="Egress group"
      stateKey="network.egress.group"
      refresh={read.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous group",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next group",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list stays
          put while the group scrolls; narrower, the page scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <EgressRail
            className="hidden w-72 shrink-0 xl:flex"
            groups={groups}
            current={group?.id}
            tab={tab}
            loading={!read.data && !read.error}
            failed={!read.data && Boolean(read.error)}
          />
          <div className="flex min-w-0 flex-1 flex-col gap-5 xl:min-h-0 xl:overflow-y-auto xl:pr-1">
            <PageContext
              eyebrow={BACK}
              title={group?.name ?? (name || "Egress group")}
              actions={
                <span className="flex items-center">
                  <IconAction
                    label="Previous group"
                    disabled={previous.disabled}
                    onClick={previous.run}
                  >
                    <ChevronUp />
                  </IconAction>
                  <IconAction label="Next group" disabled={next.disabled} onClick={next.run}>
                    <ChevronDown />
                  </IconAction>
                  <span className="max-sm:hidden">
                    <WorkspaceHelp compact />
                  </span>
                </span>
              }
            />
            {group && read.data ? (
              <GroupMain
                key={group.id}
                group={group}
                view={read.data}
                read={read}
                links={links.data ?? []}
                tab={tab}
                setTab={setTab}
              />
            ) : read.data ? (
              <EmptyState
                icon={Lifebuoy}
                title={`No egress group named ${name || "that"}`}
                description="It may have been removed or renamed."
                action={
                  <Button size="sm" variant="outline" asChild>
                    <Link href="/network/egress">Egress groups</Link>
                  </Button>
                }
              />
            ) : read.error ? (
              <ErrorState error={read.error} onRetry={read.refresh} />
            ) : (
              <Loading name={name} />
            )}
          </div>
        </div>
      </Page>
    </Workspace>
  )
}

/** The page's final shape before the read lands: the line, the picture and the strip. */
function Loading({ name }: { name: string }) {
  return (
    <>
      <HostIdentity
        slot="egress-group-header"
        fallback={Lifebuoy}
        title={name}
        state={<Status tone="unknown" label="Reading" />}
        facts={<TextShimmer>Reading the group…</TextShimmer>}
      />
      <EgressPicture links={[]} />
      <div className="h-10 border-b border-hairline" />
      <LoadingRows rows={3} />
    </>
  )
}

function GroupMain({
  group,
  view,
  read,
  links,
  tab,
  setTab,
}: {
  group: EgressGroup
  view: EgressView
  read: PollState<EgressView>
  links: NetworkLink[]
  tab: EgressTab
  setTab: (tab: string) => void
}) {
  const router = useRouter()
  const [form, setForm] = useState<{ draft: EgressDraft; key: number }>()
  const { simulation, simulating, verbs, dialog, busy } = useEgressVerbs({
    group,
    running: view.running,
    onChanged: read.refresh,
    onEdit: (g) => setForm({ draft: egressDraftFromGroup(g), key: Date.now() }),
    onRemoved: () => router.push("/network/egress"),
  })
  const carrying = egressCarrying(group)
  const sim = egressSimulationReading(group.lastSimulation, group.fingerprint)
  const running = simulating ? view.running : undefined

  return (
    <>
      <HostIdentity
        label={`About ${group.name}`}
        slot="egress-group-header"
        logo={<EgressMark group={group} size="lg" />}
        title={
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate">{group.name}</span>
            {group.family === "inet6" && <Tag>IPv6</Tag>}
          </span>
        }
        state={
          <Status
            tone={DOT[carrying.tone]}
            label={carrying.label}
            className="max-w-full [&>span:last-child]:truncate"
          />
        }
        command={
          simulation && (
            <Button
              size="sm"
              disabled={simulation.disabled}
              pending={busy || simulating}
              onClick={simulation.run}
              title={
                view.running && !simulating ? "Another group's simulation is running" : undefined
              }
            >
              <simulation.icon className="size-3.5" />
              {simulating ? simulation.progressive : simulation.label}
            </Button>
          )
        }
        menu={verbs.length > 0 && <VerbMenu verbs={verbs} label="Group actions" />}
        facts={
          <>
            {group.enabled && (
              <>
                <span>{group.automation ? "automatic" : "switches by hand"}</span>
                <FactDot />
              </>
            )}
            <span className={cn(sim.tone === "danger" && "text-destructive")} title={sim.label}>
              {egressSimulationFact(group.lastSimulation, group.fingerprint)}
            </span>
            {group.runtime?.status === "verified" && (
              <>
                <FactDot />
                <span className="text-success" title={group.runtime.route}>
                  route verified
                </span>
              </>
            )}
          </>
        }
      />
      <NetworkReadWarning
        error={read.error}
        refresh={read.refresh}
        lastSuccess={read.lastSuccess}
        reading="egress readings"
      />
      {!view.persistent && <MemoryOnly />}
      {group.runtime && group.runtime.status !== "verified" && (
        <Notice tone="danger" title="The kernel does not hold the decided route">
          {group.runtime.status === "drift"
            ? `Missing: ${group.runtime.missing?.join(", ")}.`
            : group.runtime.error}
        </Notice>
      )}

      <EgressPicture group={group} links={links} />

      <Tabs value={tab} onValueChange={setTab} className="gap-0">
        <TabsPrimitive.List
          aria-label={`Views of ${group.name}`}
          className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
        >
          {(
            [
              { key: "members", label: "Members", count: group.members.length },
              { key: "decisions", label: "Decisions", count: group.events.length },
              { key: "simulation", label: "Simulation" },
            ] as { key: EgressTab; label: string; count?: number }[]
          ).map((view) => (
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
        <TabsContent value="members" className="pt-5">
          <EgressMembers group={group} />
        </TabsContent>
        <TabsContent value="decisions" className="pt-3">
          <EgressDecisions group={group} />
        </TabsContent>
        <TabsContent value="simulation" className="pt-5">
          <EgressSimulationPanel group={group} running={running} />
        </TabsContent>
      </Tabs>
      {dialog}
      {form && (
        <EgressGroupForm
          key={form.key}
          open
          onOpenChange={(open) => !open && setForm(undefined)}
          initial={form.draft}
          editing={group}
          links={links}
          onSaved={(saved) => {
            read.refresh()
            if (saved && saved.toLowerCase() !== group.name.toLowerCase())
              router.replace(egressHref(saved, tab))
          }}
        />
      )}
    </>
  )
}
