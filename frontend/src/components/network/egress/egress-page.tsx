"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import {
  egressDraftFromGroup,
  egressSimulationReading,
  type EgressDraft,
  type EgressGroup,
  type EgressView,
} from "@/lib/network/egress"
import type { PollState } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import { Lifebuoy } from "@/components/icons"
import { DetailPage, useDetailTab, type Step } from "@/components/detail-page"
import { FactDot, HostIdentity } from "@/components/identity-header"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Status } from "@/components/status-dot"
import { ViewTabList } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
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
  egressSibling,
  egressSimulationFact,
  findEgressGroup,
  type EgressTab,
} from "@/components/network/egress/reading"
import { useEgressLinks, useEgressView } from "@/components/network/egress/use-egress"
import { decodeSegment } from "@/lib/route-params"

// One inset under the strip on every Network detail page.
const VIEW_PANE = "pt-6"

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
  const router = useRouter()
  const name = decodeSegment(params.name)
  const [tab, setTab] = useDetailTab(EGRESS_TABS)

  const read = useEgressView()
  const links = useEgressLinks(true)
  const groups = read.data?.groups ?? []
  const group = findEgressGroup(groups, name)
  // How many members each group had when it was last read, so its picture
  // waits at the height it will have: a group of three grew when it landed.
  const [members, setMembers] = useViewState<Record<string, number>>("network.egress.members", {})
  const known = group ? members[group.name.toLowerCase()] : undefined
  const count = group?.members.length
  useEffect(() => {
    if (group && count !== undefined && known !== count)
      setMembers((held) => ({ ...held, [group.name.toLowerCase()]: count }))
  }, [group, count, known, setMembers])
  // A simulation the line started runs on the Simulation view.
  const [simulating, setSimulating] = useState(false)
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
  const view = read.data

  return (
    <DetailPage
      name="Egress group"
      what="group"
      stateKey="network.egress.group"
      refresh={read.refresh}
      steps={{ previous, next }}
      rail={
        <EgressRail
          groups={groups}
          current={group?.id}
          tab={tab}
          loading={!read.data && !read.error}
          error={!read.data ? read.error : undefined}
          onRetry={read.refresh}
        />
      }
      back={{ href: "/network/egress", label: "Egress groups" }}
      title={group?.name ?? (name || "Egress group")}
      identity={
        group && view ? (
          <GroupIdentity
            key={group.id}
            group={group}
            view={view}
            read={read}
            links={links.data ?? []}
            tab={tab}
            onSimulating={setSimulating}
          />
        ) : (
          !view && !read.error && <LoadingIdentity name={name} />
        )
      }
    >
      {group && view ? (
        <GroupViews
          key={group.id}
          group={group}
          view={view}
          read={read}
          links={links.data ?? []}
          tab={tab}
          setTab={setTab}
          running={simulating ? view.running : undefined}
        />
      ) : view ? (
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
        <Loading members={members[name.toLowerCase()]} />
      )}
    </DetailPage>
  )
}

/** The line before the read lands, with the verbs' place held. */
function LoadingIdentity({ name }: { name: string }) {
  return (
    <HostIdentity
      slot="egress-group-header"
      fallback={Lifebuoy}
      title={name}
      state={<Status tone="unknown" label="Reading" />}
      // The verbs' place, so on a phone the line holds the row they land on.
      menu={false}
      facts={<TextShimmer>Reading the group…</TextShimmer>}
    />
  )
}

/** The views' final shape before the read lands: the strip, then the picture that opens Members. */
function Loading({ members }: { members?: number }) {
  return (
    <div className="flex min-w-0 flex-col">
      <div className="h-10 border-b border-hairline" />
      <div className={cn(VIEW_PANE, "flex min-w-0 flex-col gap-6")}>
        <EgressPicture links={[]} expected={members} />
        <LoadingRows rows={3} />
      </div>
    </div>
  )
}

/**
 * The group's line, with Run simulation as its one command and every other
 * verb in the `⋯`, and the edit form those verbs open.
 */
function GroupIdentity({
  group,
  view,
  read,
  links,
  tab,
  onSimulating,
}: {
  group: EgressGroup
  view: EgressView
  read: PollState<EgressView>
  links: NetworkLink[]
  tab: EgressTab
  onSimulating: (simulating: boolean) => void
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
  useEffect(() => {
    onSimulating(simulating)
    return () => onSimulating(false)
  }, [simulating, onSimulating])
  const carrying = egressCarrying(group)
  const sim = egressSimulationReading(group.lastSimulation, group.fingerprint)

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

/**
 * The strip directly under the line and the view on screen. The picture of
 * what the group does is the top of Members, as a device's picture is the
 * top of its Traffic: it belongs to the group, not to the three views.
 */
function GroupViews({
  group,
  view,
  read,
  links,
  tab,
  setTab,
  running,
}: {
  group: EgressGroup
  view: EgressView
  read: PollState<EgressView>
  links: NetworkLink[]
  tab: EgressTab
  setTab: (tab: string) => void
  running: EgressView["running"] | undefined
}) {
  return (
    <>
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
      <Tabs value={tab} onValueChange={setTab} className="gap-0">
        <ViewTabList
          label={`Views of ${group.name}`}
          views={[
            { key: "members", label: "Members", count: group.members.length },
            { key: "decisions", label: "Decisions", count: group.events.length },
            { key: "simulation", label: "Simulation" },
          ]}
          value={tab}
          sticky
          className="max-xl:static"
        />
        <TabsContent value="members" className={cn(VIEW_PANE, "flex min-w-0 flex-col gap-6")}>
          <EgressPicture group={group} links={links} />
          <EgressMembers group={group} />
        </TabsContent>
        <TabsContent value="decisions" className={VIEW_PANE}>
          <EgressDecisions group={group} />
        </TabsContent>
        <TabsContent value="simulation" className={VIEW_PANE}>
          <EgressSimulationPanel group={group} running={running} />
        </TabsContent>
      </Tabs>
    </>
  )
}
