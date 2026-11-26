"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Users } from "@/components/icons"
import { cn } from "@/lib/utils"
import { plural } from "@/lib/format"
import { FactLine } from "@/components/fact-line"
import { DetailPage, siblingSteps } from "@/components/detail-page"
import { FormFact, FormFacts } from "@/components/form"
import { Modal } from "@/components/modal"
import { EmptyState } from "@/components/state"
import { ViewTabs } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { TabsContent } from "@/components/ui/tabs"
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
  /** The list could not be read: the rail says so in place of its rows. */
  error?: Error
  onRetry?: () => void
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
 * it opens on, its views as the view strip with the view in the address
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
  // An account the list does not hold steps into it from its ends, as every
  // detail page's arrows do.
  const held = accounts.some((account) => account.key === current) ? current : null
  const { previous, next } = siblingSteps(
    accounts.map((account) => account.key),
    held,
    (key) => {
      const to = accounts.find((account) => account.key === key)
      if (to && !ask(to.href)) router.push(to.href, { scroll: false })
    },
  )
  const label = host ? `${name}@${host}` : name
  const page = engine.section("access")?.title ?? "Access"
  const one = list.label === "Users" ? "user" : "account"

  return (
    <>
      <DetailPage
        name={one === "user" ? "User" : "Account"}
        what={one}
        stateKey={`databases.${id}.account`}
        steps={{ previous, next }}
        rail={
          <AccountRail
            label={list.label}
            accounts={accounts}
            current={current}
            pinned={{ key: current, name, host, line: "", href: hrefOf({ name, host }) }}
            loading={list.loading}
            error={list.error}
            onRetry={list.onRetry}
            search={list.search}
            caption={list.caption}
            filtered={list.filtered}
            onClearFilters={list.onClearFilters}
            onLeave={ask}
          />
        }
        back={{ href: accessHref(id, list.show), label: page }}
        title={`${conn.name} · ${page} · ${label}`}
        identity={identity}
      >
        <ViewTabs
          label={`Views of ${label}`}
          value={tab}
          onValueChange={setTab}
          hideWhenSingle
          rootClassName="min-h-0 flex-1"
          views={views.map((view) => ({
            key: view.key,
            label: view.label,
            count: view.count,
            dot: view.unsaved ? { tone: "warning", label: "unsaved changes" } : undefined,
          }))}
        >
          {children(tab)}
        </ViewTabs>
      </DetailPage>
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
                <FormFact label="Not saved">{plural(unsaved, "change")}</FormFact>
              </FormFacts>
            </div>
          </div>
          <p className="text-body leading-relaxed">
            Nothing here has been sent to the server yet. Going on throws it away.
          </p>
        </div>
      </Modal>
    </>
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
    <FactLine nowrap className="whitespace-nowrap">
      {children}
    </FactLine>
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
