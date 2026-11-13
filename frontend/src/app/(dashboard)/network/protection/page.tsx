"use client"

import { useEffect, useState } from "react"
import { useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { useAuth } from "@/hooks/use-auth"
import {
  Globe,
  ListUnordered,
  Plus,
  RefreshClockwise,
  ShieldCheck,
  Warning,
} from "@/components/icons"
import { get } from "@/lib/api"
import { bytes, calendarDate, plural } from "@/lib/format"
import type {
  ProtectionBlocklist,
  ProtectionLimit,
  ProtectionPressure,
  ProtectionView,
} from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, ProductLogos, hasProductLogo } from "@/components/product-logo"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { VerbMenu, type Verb } from "@/components/verbs"
import {
  BlocklistList,
  BlocklistModal,
  CardRowsSkeleton,
} from "@/components/network/protection/blocklists"
import {
  ExceptionList,
  ExceptionModal,
  ROW_HEIGHT,
} from "@/components/network/protection/exceptions"
import {
  KernelSettings,
  KernelSkeleton,
  type KernelDraft,
} from "@/components/network/protection/kernel"
import { LimitList, LimitModal } from "@/components/network/protection/limits"
import { ProtectionPicture, pictureRows } from "@/components/network/protection/picture"
import { ConnectionTable } from "@/components/network/protection/pressure"
import { compact } from "@/components/network/gateway/reading"
import {
  limitProduct,
  protectionTotals,
  stagedChanges,
} from "@/components/network/protection/reading"
import { TrustedList } from "@/components/network/protection/trusted"

type Editor =
  | { kind: "list"; list?: ProtectionBlocklist }
  | { kind: "limit"; limit?: ProtectionLimit }
  | { kind: "exception" }

/**
 * The line of facts as one block. On a phone it wraps to two lines once the
 * counters have a few digits, so it holds two lines of room there from the
 * start and the picture under it does not move when the read lands.
 */
const FACTS = "flex min-w-0 flex-wrap content-start items-center gap-x-3 gap-y-1 max-sm:min-h-9"

type View = "overview" | "table" | "kernel"

/** What each list held at the last visit, so the grey page is the page's own shape. */
type Shape = { lists: number; limits: number; exceptions: number; trusted: number }
const FIRST_VISIT: Shape = { lists: 3, limits: 2, exceptions: 1, trusted: 4 }

/**
 * What this server refuses before anything answers.
 *
 * It opened on four tiles with nothing above them. It opens on what it is
 * about now: the identity line says how many lists and limits there are and
 * what they have dropped and refused since the table was loaded, with New
 * blocklist as its command, and under it the picture draws who is refused,
 * this server's filter and what gets through (`protection/picture.tsx`). Each
 * tile's figure went where it is said better: the dropped and refused totals
 * to that line and to their sections' heads, the networks blocked to the
 * blocklists' head, and the connection table onto the picture's own node,
 * with a view of its own for why it is filling.
 *
 * Three views, in the address (`?tab=`): the lists, limits, exceptions and
 * the addresses nothing may refuse; the connection table, an administrator's
 * reading; and the kernel's own defences as a settings column with the one
 * save bar. Kernel edits are staged here rather than in that view, so they
 * outlive a look at another one, and the tab counts them.
 *
 * Every drop here is preceded by the trusted set, so the operator cannot lock
 * themselves out with a list; a list that holds their address says so on its
 * row, and the set is listed so it can be read rather than taken on trust.
 *
 * The page has its shape before its read lands: the line, the tabs, the
 * picture at its one height and each section's head stand where they will,
 * with as many grey rows as the lists held last time.
 */
export default function NetworkProtectionPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const protection = usePoll<ProtectionView>(
    (signal) => get("/network/protection", undefined, signal),
    10_000,
  )
  // The table's breakdown names who connects, so it is an administrator's
  // reading, and slower: it walks the whole connection table.
  const pressure = usePoll<ProtectionPressure>(
    (signal) => get("/network/protection/pressure", undefined, signal),
    30_000,
    [],
    { enabled: admin },
  )
  const [editor, setEditor] = useState<Editor>()
  const [draft, setDraft] = useState<KernelDraft>({ pending: {} })
  const [shape, setShape] = useViewState<Shape>("network.protection.shape", FIRST_VISIT)

  // The view is in the address, so a link to the kernel's settings opens on
  // them; replaced rather than pushed, because a view is not a place Back
  // should step through.
  const requested = useSearchParams().get("tab")
  const [followed, setFollowed] = useState(requested)
  const [chosen, setChosen] = useState<string>(requested ?? "overview")
  if (requested !== followed) {
    setFollowed(requested)
    setChosen(requested ?? "overview")
  }
  const views: { key: View; label: string; count?: number }[] = [
    { key: "overview", label: "Overview" },
    ...(admin ? [{ key: "table" as const, label: "Connection table" }] : []),
    { key: "kernel", label: "Kernel" },
  ]
  const tab: View = views.find((v) => v.key === chosen)?.key ?? "overview"
  const setTab = (next: string) => {
    setChosen(next)
    const url = new URL(window.location.href)
    if (next === "overview") url.searchParams.delete("tab")
    else url.searchParams.set("tab", next)
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }

  const view = protection.data
  useEffect(() => {
    if (!view) return
    const next: Shape = {
      lists: view.blocklists.length,
      limits: view.limits.length,
      exceptions: view.exceptions?.length ?? 0,
      trusted: view.trusted.length,
    }
    setShape((held) =>
      held.lists === next.lists &&
      held.limits === next.limits &&
      held.exceptions === next.exceptions &&
      held.trusted === next.trusted
        ? held
        : next,
    )
  }, [view, setShape])

  const failed = !view && protection.error
  const totals = view ? protectionTotals(view) : undefined
  const lists = view?.blocklists ?? []
  const limits = view?.limits ?? []
  const exceptions = view?.exceptions ?? []
  const holdingYou = lists.filter((l) => l.containsYou)
  const staged = view ? Object.keys(stagedChanges(view.settings, draft.pending)).length : 0
  const mayExcept = admin && can("destructive")

  const newList = () => setEditor({ kind: "list" })
  const newLimit = () => setEditor({ kind: "limit" })
  const newException = () => setEditor({ kind: "exception" })
  const verbs: Verb[] = [
    ...(admin && view ? [{ key: "limit", label: "New limit", icon: Plus, run: newLimit }] : []),
    ...(mayExcept && view
      ? [{ key: "exception", label: "New exception", icon: Plus, run: newException }]
      : []),
    {
      key: "refresh",
      label: "Refresh",
      icon: RefreshClockwise,
      run: () => {
        protection.refresh()
        pressure.refresh()
      },
    },
  ]

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Protection" />
      {/* The line and its views are one block, as a process's page has them:
          the strip's hairline is the rule under the line. */}
      <div className="flex min-w-0 flex-col gap-4">
        <HostIdentity
          label="About protection"
          className="border-b-0 pb-0"
          fallback={ShieldCheck}
          title="Protection"
          state={
            failed ? (
              <Status tone="danger" label="Not read" />
            ) : !view || !totals ? (
              <Skeleton className="h-4 w-20" />
            ) : (
              <>
                <Status
                  tone={!totals.inForce ? "stopped" : view.loaded ? "running" : "warning"}
                  label={
                    !totals.inForce ? "Nothing in force" : view.loaded ? "In force" : "Not loaded"
                  }
                />
                {totals.failing > 0 && (
                  <Status tone="danger" label={`${plural(totals.failing, "list")} not fetched`} />
                )}
              </>
            )
          }
          facts={
            failed ? (
              <span>The lists and limits could not be read.</span>
            ) : !totals ? (
              <span className={FACTS}>
                <Skeleton className="my-0.5 h-3 w-72 max-w-full" />
              </span>
            ) : (
              <span className={FACTS}>
                <span className="numeric">{plural(totals.lists, "blocklist")}</span>
                <FactDot />
                <span className="numeric">{plural(totals.limits, "limit")}</span>
                <FactDot />
                <span className="numeric">
                  <span className="text-foreground">{totals.dropped.toLocaleString()}</span> dropped
                  since load
                </span>
                <FactDot />
                <span className="numeric">
                  <span className="text-foreground">{totals.refused.toLocaleString()}</span> refused
                </span>
              </span>
            )
          }
          command={
            admin && (
              <Button size="sm" onClick={newList} disabled={!view}>
                <Plus className="size-3.5" />
                New blocklist
              </Button>
            )
          }
          menu={<VerbMenu verbs={verbs} label="Protection actions" />}
        />
        {view && (
          <NetworkReadWarning
            error={protection.error}
            refresh={protection.refresh}
            lastSuccess={protection.lastSuccess}
          />
        )}
        {view && totals?.inForce && !view.loaded && (
          <Notice tone="warning" icon={Warning} title="Protection is not loaded into the kernel">
            Its lists and limits are saved but not in force: the host was started without the unit
            that loads them, or something deleted the table. Saving any of them loads it again.
          </Notice>
        )}

        {failed ? (
          <ErrorState error={protection.error!} onRetry={protection.refresh} />
        ) : (
          <Tabs value={tab} onValueChange={setTab} className="gap-0">
            <TabsPrimitive.List
              aria-label="Views of protection"
              className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
            >
              {views.map((v) => (
                <TabsPrimitive.Trigger
                  key={v.key}
                  value={v.key}
                  className={tabClasses(tab === v.key, "h-10")}
                >
                  {v.label}
                  {v.key === "kernel" && staged > 0 && (
                    <ChipCount className="text-[var(--git-modified)]">{staged}</ChipCount>
                  )}
                </TabsPrimitive.Trigger>
              ))}
            </TabsPrimitive.List>

            <TabsContent value="overview" className="flex min-w-0 flex-col gap-6 pt-2 md:gap-8">
              <ProtectionPicture
                view={view}
                rows={pictureRows(shape.lists, shape.limits)}
                writable={admin}
                onNewList={newList}
                onNewLimit={newLimit}
                onOpenList={(list) => setEditor({ kind: "list", list })}
                onOpenLimit={(limit) => setEditor({ kind: "limit", limit })}
              />

              <Panel plain id="blocklists" className="scroll-mt-6">
                <PanelHeader title="Blocklists">
                  <SectionFact>
                    {totals && totals.lists > 0 && (
                      <>
                        {totals.networks.toLocaleString()} networks in{" "}
                        {plural(totals.listsOn, "list")} · {bytes(totals.droppedBytes)} dropped
                        since load
                        {totals.droppedSince &&
                          ` · ${compact(totals.droppedEver)} packets since ${calendarDate(totals.droppedSince)}`}
                      </>
                    )}
                  </SectionFact>
                </PanelHeader>
                <PanelBody>
                  <div className="flex min-w-0 flex-col gap-4">
                    {view && holdingYou.length > 0 && (
                      <Notice
                        tone="warning"
                        icon={Warning}
                        title={`${holdingYou.map((l) => l.name).join(", ")} ${holdingYou.length === 1 ? "holds" : "hold"} your own address`}
                      >
                        Nothing is cut off while{" "}
                        <span className="font-mono text-foreground">{view.client}</span> stays among
                        the addresses never refused.
                      </Notice>
                    )}
                    {!view ? (
                      <SectionSkeleton rows={shape.lists} card />
                    ) : lists.length === 0 ? (
                      <EmptyState
                        className={EMPTY}
                        mark={
                          <span aria-hidden className="flex items-center gap-1.5">
                            <ProductLogo size="sm" fallback={Globe} />
                            <ProductLogo size="sm" fallback={ListUnordered} />
                          </span>
                        }
                        title="No network is blocked"
                        action={
                          admin && (
                            <Button size="sm" variant="outline" onClick={newList}>
                              <Plus aria-hidden />
                              Block a country, a feed or addresses
                            </Button>
                          )
                        }
                      />
                    ) : (
                      <BlocklistList
                        lists={lists}
                        presets={view.presets}
                        onOpen={(list) => setEditor({ kind: "list", list })}
                        onChanged={protection.refresh}
                      />
                    )}
                  </div>
                </PanelBody>
              </Panel>

              <Panel plain id="limits" className="scroll-mt-6">
                <PanelHeader
                  title="Rate limits"
                  actions={
                    admin && (
                      <Button size="xs" variant="outline" onClick={newLimit} disabled={!view}>
                        <Plus aria-hidden />
                        New limit
                      </Button>
                    )
                  }
                >
                  <SectionFact>
                    {totals && totals.limits > 0 && (
                      <>
                        {totals.limitsOn} in force · {bytes(totals.refusedBytes)} refused since load
                        {totals.refusedSince &&
                          ` · ${compact(totals.refusedEver)} packets since ${calendarDate(totals.refusedSince)}`}
                      </>
                    )}
                  </SectionFact>
                </PanelHeader>
                <PanelBody>
                  {!view ? (
                    <SectionSkeleton rows={shape.limits} card />
                  ) : limits.length === 0 ? (
                    <EmptyState
                      className={EMPTY}
                      mark={<LimitMarks view={view} />}
                      title="No port is limited"
                      action={
                        admin && (
                          <Button size="sm" variant="outline" onClick={newLimit}>
                            <Plus aria-hidden />
                            Limit a port
                          </Button>
                        )
                      }
                    />
                  ) : (
                    <LimitList
                      limits={limits}
                      pressure={pressure.data}
                      onOpen={(limit) => setEditor({ kind: "limit", limit })}
                      onChanged={protection.refresh}
                    />
                  )}
                </PanelBody>
              </Panel>

              <Panel plain id="exceptions" className="scroll-mt-6">
                <PanelHeader
                  title="Exceptions"
                  actions={
                    <>
                      {view && (
                        <span className="numeric text-hint text-muted-foreground">
                          {exceptions.length}
                        </span>
                      )}
                      {mayExcept && (
                        <Button size="xs" variant="outline" onClick={newException} disabled={!view}>
                          <Plus aria-hidden />
                          New exception
                        </Button>
                      )}
                    </>
                  }
                />
                <PanelBody flush={exceptions.length > 0 || (!view && shape.exceptions > 0)}>
                  {!view ? (
                    <SectionSkeleton rows={shape.exceptions} />
                  ) : (
                    <ExceptionList
                      exceptions={exceptions}
                      onChanged={protection.refresh}
                      onNew={mayExcept ? newException : undefined}
                    />
                  )}
                </PanelBody>
              </Panel>

              <Panel plain id="never-refused" className="scroll-mt-6">
                <PanelHeader
                  title="Never refused"
                  actions={
                    view && (
                      <span className="numeric text-hint text-muted-foreground">
                        {view.trusted.length}
                      </span>
                    )
                  }
                />
                <PanelBody flush>
                  {!view ? (
                    <SectionSkeleton rows={shape.trusted} />
                  ) : (
                    <TrustedList view={view} onChanged={protection.refresh} />
                  )}
                </PanelBody>
              </Panel>
            </TabsContent>

            {admin && (
              <TabsContent value="table" className="min-w-0 pt-6">
                {view ? (
                  <ConnectionTable table={view.conntrack} read={pressure} />
                ) : (
                  <Skeleton className="h-24 w-full rounded-xl" />
                )}
              </TabsContent>
            )}

            <TabsContent value="kernel" className="min-w-0 pt-6">
              {view ? (
                <KernelSettings
                  settings={view.settings}
                  resetNote={view.resetNote}
                  profiles={view.kernelProfiles}
                  interfaces={view.interfaces}
                  draft={draft}
                  onDraft={setDraft}
                  onChanged={protection.refresh}
                />
              ) : (
                <KernelSkeleton />
              )}
            </TabsContent>
          </Tabs>
        )}
      </div>

      {view && editor?.kind === "list" && (
        <BlocklistModal
          key={editor.list?.id ?? "new"}
          list={editor.list}
          presets={view.presets}
          exceptions={exceptions.filter((e) => e.scope === `blocklist:${editor.list?.id}`)}
          onOpenChange={(open) => !open && setEditor(undefined)}
          onSaved={protection.refresh}
        />
      )}
      {view && editor?.kind === "limit" && (
        <LimitModal
          key={editor.limit?.id ?? "new"}
          limit={editor.limit}
          profiles={view.profiles}
          capacity={pressure.data?.limits.find((l) => l.id === editor.limit?.id)}
          onOpenChange={(open) => !open && setEditor(undefined)}
          onSaved={protection.refresh}
        />
      )}
      {view && editor?.kind === "exception" && (
        <ExceptionModal
          lists={lists}
          onOpenChange={(open) => !open && setEditor(undefined)}
          onSaved={protection.refresh}
        />
      )}
    </Page>
  )
}

/**
 * A section's own totals, at the far end of its title line. On a phone it is
 * the line under the title, and holds two lines of room whatever it says, so
 * the rows under it start in one place before and after the read.
 */
function SectionFact({ children }: { children: React.ReactNode }) {
  return (
    <p className="numeric ml-auto min-w-0 text-hint leading-4 text-muted-foreground max-sm:order-last max-sm:min-h-8 max-sm:basis-full">
      {children}
    </p>
  )
}

/** An empty section's block, and the grey block that stands for it while the read is out. */
const EMPTY = "py-8"
const EMPTY_HEIGHT = "h-[10.5rem]"

/**
 * A section's rows while the first read is out: as many as it held at the
 * last visit, at their height, or the block an empty section draws.
 */
function SectionSkeleton({ rows, card }: { rows: number; card?: boolean }) {
  if (rows === 0) return <Skeleton aria-hidden className={`${EMPTY_HEIGHT} w-full rounded-xl`} />
  if (card) return <CardRowsSkeleton rows={rows} />
  return (
    <ul aria-hidden className="divide-y divide-hairline">
      {Array.from({ length: rows }, (_, i) => (
        <li key={i} className={`flex items-center py-3 ${ROW_HEIGHT}`}>
          <Skeleton className="h-4 w-40" />
        </li>
      ))}
    </ul>
  )
}

/** What a limit guards, as the products its service profiles name: SSH, a database, a web server. */
function LimitMarks({ view }: { view: ProtectionView }) {
  const products = [
    ...new Set((view.profiles ?? []).map((p) => limitProduct(p)).filter(hasProductLogo)),
  ]
  if (products.length === 0) return <ProductLogo size="sm" fallback={ShieldCheck} />
  return <ProductLogos ids={products} />
}
