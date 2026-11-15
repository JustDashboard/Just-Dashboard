"use client"

import { useSearchParams } from "next/navigation"
import { ChartActivity } from "@/components/icons"
import { bytes, percent } from "@/lib/format"
import { ProductLogo, pm2Product } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { Rail, RailFigures, RailLine, RailName, RailRow, railInk } from "@/components/rail"
import { pm2AppHref, pm2AppState, pm2ModeWords } from "@/components/procs/pm2-facts"
import { pm2Failing, type PM2App } from "@/components/procs/pm2-shared"
import { cpuTone } from "@/components/procs/shared"

/**
 * The PM2 list, folded into a column beside the application it opened: its
 * search, its state chip and its order — what is crashing first, then by
 * name — so the next application to check is one press away. A cluster is one
 * row here, as it is one span of the list's band: each row is the application
 * as what runs it with its state in the logo's corner, its name, how it is
 * doing in a few words and, while it runs, what it takes; the one on screen
 * is filled.
 *
 * The rows hold their order while the pointer or focus is in the column
 * (`useInspectionOrder`), so an application that crashes under the pointer
 * does not move the row about to be pressed.
 */
export function PM2AppRail({
  apps,
  current,
  several,
  loading,
  error,
  onRetry,
  query,
  onQuery,
  filtered,
  onClearFilters,
  caption,
  onOpen,
  bindings,
  className,
}: {
  apps: PM2App[]
  /** The key of the application on screen. */
  current: string | null
  /** More than one account runs PM2, so each row says whose it is. */
  several: boolean
  loading: boolean
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  /** Opens an application carrying the page's view; the row's link does the same without it. */
  onOpen?: (app: PM2App) => void
  bindings: React.HTMLAttributes<HTMLElement>
  className?: string
}) {
  const tab = useSearchParams().get("tab") ?? undefined
  // The account is in the address only where the name alone would not say which.
  const shared = (app: PM2App) =>
    apps.some((other) => other.name === app.name && other.daemonId !== app.daemonId)
  return (
    <Rail
      slot="pm2-rail"
      label="PM2 applications"
      listLabel="Applications to open"
      search={{
        label: "Search the applications",
        placeholder: "Name, script or account",
        query,
        onQuery,
      }}
      rows={apps}
      keyOf={(app) => app.key}
      current={(app) => app.key === current}
      row={(app, on) => (
        <PM2Row
          app={app}
          current={on}
          several={several}
          href={pm2AppHref(app, { tab, account: shared(app) })}
          onOpen={onOpen}
        />
      )}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty="No application matches."
      filtered={filtered}
      onClearFilters={onClearFilters}
      caption={caption}
      bindings={bindings}
      className={className}
    />
  )
}

/** How an application is doing, in the rail's few words. */
function railLine(app: PM2App, several: boolean): { text: string; tone?: "danger" | "warning" } {
  const state = pm2AppState(app)
  const whose = several ? `${app.daemonId} · ` : ""
  const unstable = app.instances.reduce((n, p) => n + p.unstableRestarts, 0)
  if (app.online === 0 && app.instances.some(pm2Failing)) {
    return {
      text: `${whose}${state.word}${unstable > 0 ? ` · ${unstable} unstable` : ""}`,
      tone: "danger",
    }
  }
  if (state.partial) return { text: `${whose}${state.word}`, tone: "warning" }
  if (app.online === 0) return { text: `${whose}${state.word}` }
  return { text: `${whose}${pm2ModeWords(app)}` }
}

function PM2Row({
  app,
  current,
  several,
  href,
  onOpen,
}: {
  app: PM2App
  current: boolean
  several: boolean
  href: string
  onOpen?: (app: PM2App) => void
}) {
  const line = railLine(app, several)
  const state = pm2AppState(app)
  const tone = cpuTone(app.cpu)
  return (
    <RailRow
      item={app.key}
      name={app.name}
      label={several ? `${app.name}, ${app.daemonId}` : app.name}
      current={current}
      href={href}
      onOpen={onOpen && (() => onOpen(app))}
      mark={
        <ProductLogo
          id={pm2Product(app.interpreter)}
          size="sm"
          fallback={ChartActivity}
          className="size-7 rounded-md [&_img]:size-4"
        />
      }
      dot={
        state.partial ? (
          <StatusDot tone="warning" className="size-1.5" />
        ) : (
          <StatusDot state={state.status} className="size-1.5" />
        )
      }
      end={
        app.online > 0 && (
          <RailFigures
            top={percent(app.cpu)}
            bottom={bytes(app.memory)}
            ink={tone === "danger" ? "font-medium text-destructive" : railInk(tone)}
          />
        )
      }
    >
      <RailName current={current}>{app.name}</RailName>
      <RailLine className="numeric" ink={railInk(line.tone)}>
        {line.text}
      </RailLine>
    </RailRow>
  )
}
