"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowLeft, ChevronDown, ChevronUp, Users } from "@/components/icons"
import { cn } from "@/lib/utils"
import { IconAction } from "@/components/icon-action"
import { FormFact, FormFacts } from "@/components/form"
import { Modal } from "@/components/modal"
import { Page, PageContext } from "@/components/page"
import { EmptyState } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useLinkGuard, useUnloadGuard } from "@/components/database/data/guard"
import {
  accessHref,
  accountHref,
  isAccountTab,
  type AccountTab,
} from "@/components/database/ops/account-address"
import { AccountRail, type RailAccount } from "@/components/database/ops/account-rail"
import { AccountMark, AccountName } from "@/components/database/ops/access-parts"
import { useDatabase } from "@/components/database/shell/database-context"

/** One view of an account's page, as its strip names it. */
export type AccountView = {
  key: AccountTab
  label: string
  count?: number
  /** The view holds work that has not been saved: a dot beside its name. */
  unsaved?: boolean
}

/**
 * The list an account's page stands beside: what the rail draws, in the
 * list's own filter and order.
 */
export type AccountList = {
  /** "Accounts" or "Users", as the list page calls them. */
  label: string
  accounts: Omit<RailAccount, "href">[]
  loading: boolean
  search?: { value: string; onChange: (value: string) => void; placeholder: string }
  caption: string
  filtered: boolean
  onClearFilters: () => void
  /** The list's filter, carried by the way back so the list is as it was left. */
  show?: string
}

/**
 * One account, user or role of a database's server, as a place of its own.
 *
 * It was a sheet over the Access list in three engines' clothes: a SQL
 * account's attributes and its whole grants editor, a key–value user's rule
 * and the form that rewrites it, a document user's roles — each in a drawer's
 * width, over the list it came from. So an account is a destination with a
 * way back, the way a process and a unit are (`procs/process-page`): the line
 * it opens on, its views as the underlined strip with the view in the address
 * (`?tab=`), and from `xl` the list beside it as a rail in the list's own
 * filter, so the next account is one press, Alt+↑ and Alt+↓ walk it, and the
 * view on screen goes with the reader. On a phone it is one pane that scrolls
 * as a whole.
 *
 * The page holds the largest unsaved work of the section — a set of staged
 * grants, a rule being rewritten — so every way off it asks first while any is
 * held: a link out, another account in the rail, the two arrows, a reload.
 * The browser's own Back is the one way that cannot be asked.
 */
export function AccountFrame({
  name,
  host,
  current,
  list,
  identity,
  views,
  unsaved = 0,
  children,
}: {
  name: string
  host?: string
  /** The account's key among the list's. */
  current: string
  list: AccountList
  /** The line the page opens on. */
  identity: React.ReactNode
  /** One view draws no strip. */
  views: AccountView[]
  /** How many changes the page holds that were not saved. */
  unsaved?: number
  children: (tab: AccountTab) => React.ReactNode
}) {
  const { id, conn, engine, param, select } = useDatabase()
  const router = useRouter()
  const asked = param("tab")
  const tab = views.find((view) => view.key === asked)?.key ?? views[0].key
  const setTab = useCallback(
    (next: string) => {
      if (!isAccountTab(next)) return
      // Replaced, not pushed: a view is not a place Back should step through.
      select({ tab: next === views[0].key ? null : next })
    },
    [select, views],
  )

  const [leaving, setLeaving] = useState<string>()
  // Asks before the page is left over unsaved work, and says whether it did.
  const ask = useCallback(
    (href: string) => {
      if (unsaved === 0) return false
      setLeaving(href)
      return true
    },
    [unsaved],
  )
  useLinkGuard(unsaved > 0, setLeaving)
  useUnloadGuard(unsaved > 0)

  const hrefOf = (account: { name: string; host?: string }) => accountHref(id, account, tab)
  const accounts = list.accounts.map((account) => ({ ...account, href: hrefOf(account) }))
  const at = accounts.findIndex((account) => account.key === current)
  const step = (direction: number) => {
    const next = at >= 0 ? accounts[at + direction] : accounts[direction > 0 ? 0 : -1]
    if (next && !ask(next.href)) router.push(next.href, { scroll: false })
  }
  const previous = { disabled: at <= 0, run: () => step(-1) }
  const next = {
    disabled: accounts.length === 0 || at >= accounts.length - 1,
    run: () => step(1),
  }
  const label = host ? `${name}@${host}` : name
  const page = engine.section("access")?.title ?? "Access"
  const one = list.label === "Users" ? "user" : "account"

  return (
    <Workspace
      name={one === "user" ? "User" : "Account"}
      stateKey={`databases.${id}.account`}
      commands={[
        {
          id: "previous",
          label: `Previous ${one}`,
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: `Next ${one}`,
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <AccountRail
            className="hidden w-72 shrink-0 xl:flex"
            label={list.label}
            accounts={accounts}
            current={current}
            pinned={{ key: current, name, host, line: "", href: hrefOf({ name, host }) }}
            loading={list.loading}
            search={list.search}
            caption={list.caption}
            filtered={list.filtered}
            onClearFilters={list.onClearFilters}
            onLeave={ask}
          />
          <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
            <div className="flex min-w-0 shrink-0 flex-col gap-5">
              <PageContext
                eyebrow={
                  <Link
                    href={accessHref(id, list.show)}
                    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
                  >
                    <ArrowLeft className="size-3" /> {page}
                  </Link>
                }
                title={`${conn.name} · ${page} · ${label}`}
                actions={
                  <span className="flex items-center">
                    <IconAction
                      label={`Previous ${one}`}
                      disabled={previous.disabled}
                      onClick={previous.run}
                    >
                      <ChevronUp />
                    </IconAction>
                    <IconAction label={`Next ${one}`} disabled={next.disabled} onClick={next.run}>
                      <ChevronDown />
                    </IconAction>
                    <span className="max-sm:hidden">
                      <WorkspaceHelp compact />
                    </span>
                  </span>
                }
              />
              {identity}
            </div>
            <Tabs value={tab} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
              {views.length > 1 && (
                <TabsPrimitive.List
                  aria-label={`Views of ${label}`}
                  className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
                >
                  {views.map((view) => (
                    <TabsPrimitive.Trigger
                      key={view.key}
                      value={view.key}
                      className={tabClasses(tab === view.key, "h-10")}
                    >
                      {view.label}
                      {view.count !== undefined && view.count > 0 && (
                        <ChipCount>{view.count}</ChipCount>
                      )}
                      {view.unsaved && (
                        <>
                          <StatusDot tone="warning" />
                          <span className="sr-only">unsaved changes</span>
                        </>
                      )}
                    </TabsPrimitive.Trigger>
                  ))}
                </TabsPrimitive.List>
              )}
              {children(tab)}
            </Tabs>
          </div>
        </div>
      </Page>
      <Modal
        open={leaving !== undefined}
        onOpenChange={(open) => !open && setLeaving(undefined)}
        size="sm"
        title="Unsaved changes"
        description={`${label} holds changes that have not been saved`}
        footer={
          <>
            <Button variant="outline" onClick={() => setLeaving(undefined)}>
              Keep editing
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                const href = leaving
                setLeaving(undefined)
                if (href) router.push(href)
              }}
            >
              Discard and go on
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <div className="flex min-w-0 items-center gap-3">
            <AccountMark name={name} />
            <div className="min-w-0 space-y-0.5">
              <p className="truncate">
                <AccountName name={name} host={host} className="text-body font-medium" />
              </p>
              <FormFacts>
                <FormFact label="Not saved">
                  {unsaved === 1 ? "1 change" : `${unsaved} changes`}
                </FormFact>
              </FormFacts>
            </div>
          </div>
          <p className="text-body leading-relaxed">
            Nothing here has been sent to the server yet. Going on throws it away.
          </p>
        </div>
      </Modal>
    </Workspace>
  )
}

/**
 * One view of the page, under the strip. `keep` leaves it mounted while
 * another view is on screen: the grants staged in one are still staged after
 * a look at the settings.
 */
export function AccountPane({
  view,
  keep,
  className,
  children,
}: {
  view: AccountTab
  keep?: boolean
  className?: string
  children: React.ReactNode
}) {
  return (
    <TabsContent
      value={view}
      forceMount={keep || undefined}
      className={cn(
        "min-h-0 flex-1 pt-6 data-[state=inactive]:hidden xl:overflow-y-auto",
        // Room at the sides for what reaches past the column's edge — a
        // switch row's wash, a focus ring — which the scroller would cut,
        // and then scroll sideways to show.
        "xl:-mx-3 xl:overflow-x-hidden xl:px-3",
        className,
      )}
    >
      {children}
    </TabsContent>
  )
}

/**
 * The facts under an account's name, held to one line: what does not fit is
 * cut at the line's end rather than wrapped to a second, so the line is the
 * same height before the account is read and after, on every width.
 */
export function AccountFacts({ children }: { children: React.ReactNode }) {
  return (
    <span className="flex max-w-full min-w-0 items-center gap-x-2 overflow-hidden whitespace-nowrap">
      {children}
    </span>
  )
}

/** The address names an account the server does not have. */
export function NoSuchAccount({
  name,
  host,
  noun,
  children,
}: {
  name: string
  host?: string
  noun: "account" | "user"
  /** Why it may be gone, in a sentence. */
  children: React.ReactNode
}) {
  const { id } = useDatabase()
  return (
    <EmptyState
      icon={Users}
      title={`No ${noun} named ${host ? `${name}@${host}` : name} on this server`}
      description={children}
      action={
        <Button size="sm" variant="outline" asChild>
          <Link href={accessHref(id)}>Back to the {noun}s</Link>
        </Button>
      }
    />
  )
}
