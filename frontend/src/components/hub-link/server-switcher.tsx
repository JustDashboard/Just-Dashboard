"use client"

import Link from "next/link"
import { Check, ChevronDown } from "@/components/icons"
import { openable, orderFleet, serverAddress, serverReading } from "@/lib/hub-link"
import { cn } from "@/lib/utils"
import { VERSION } from "@/lib/version"
import type { ShellHubValue } from "@/hooks/use-hub-link"
import { useHostName } from "@/hooks/use-metrics"
import { LogoGlyph } from "@/components/logo"
import { Status, StatusDot } from "@/components/status-dot"
import { Skeleton } from "@/components/ui/skeleton"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useSidebar } from "@/components/ui/sidebar"

/**
 * The rail's logo row on a server linked to a Just Hub: this server's name, and under it every
 * server the hub holds (design report §4.7).
 *
 * Unlinked, the row is the wordmark it always was and this is never mounted. Linked, the
 * product's name says nothing a tab of the same product does not — the server's does, so the J
 * stays and the name takes the wordmark's place, with the chevron that says it opens. The name is
 * this server's own host name, the Overview's title: the one the hub lists it under is the hub's
 * text, which a hub that is taken over could make read like this dashboard's own prompt, and the
 * wordmark's place is the dashboard's to speak in, so that name is said only in the menu, under
 * whose list it is (hub-link-protocol §8.10).
 *
 * The menu, headed "Servers", is the hub's last word on each server, cached by this server's
 * backend. It used to say whose list it was under its head ("Listed by Just Hub (hub)"); the
 * operator had that line cut (28 Nov 2026), and the menu's last row, Just Hub, says where the list
 * comes from. The names stay plain text, never a prompt of this dashboard's
 * (hub-link-protocol §8.10). A dot in the server's state and its name, this one first on the
 * chosen face with a tick and the installed version the wordmark used to carry, then the rest by
 * name, with the state in words where it is news (late, offline, updating). The hub sends no
 * version for them (§6.1). Another server, and the hub, open in this tab as any link does, and in
 * a new one on a modified or middle click; the operator asked for that over the new tab every
 * press used to open (Nov 2026). Either way there is no opener and no referrer, so nothing of this
 * dashboard's goes with the address the hub handed over (§8.10). While the hub
 * cannot be reached the others' dots go grey, because what it said last may be minutes old, and
 * the menu says why; before the first report nothing has failed, so nothing says so. An idle
 * link offers no way to the hub at all: its address may now answer on a machine this server
 * refused (§2.4), so `shellHub` has already dropped it and "Open Just Hub" has nothing to open.
 *
 * `strip` is the phone's top strip, where the rail is a closed sheet: the same trigger and menu,
 * so which server a phone is on is never a swipe away.
 */
export function ServerSwitcher({ hub, strip }: { hub: ShellHubValue; strip?: boolean }) {
  const { state, isMobile } = useSidebar()
  const collapsed = !strip && !isMobile && state === "collapsed"
  const name = useHostName()
  const servers = orderFleet(hub.fleet)
  const hubUrl = openable(hub.url)
  const trouble = hub.idle ? "Link idle" : "Hub unreachable"
  // A link whose first report is still due is not in trouble: nothing has failed yet.
  const troubled = !hub.reachable && !hub.due

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          data-slot="server-switcher"
          className={cn(
            "group/switcher flex h-8 min-w-0 items-center gap-2 rounded-lg px-1.5 text-left focus-ring transition-[color,background-color] hover:bg-sidebar-accent data-[state=open]:bg-sidebar-accent",
            strip
              ? "max-w-[min(18rem,100%)] flex-1"
              : // In the rail, 8px in from the head's edge, as the wordmark it stands
                // for is: the glass of the mark on the rows' glyph line.
                "flex-1 px-2 group-data-[collapsible=icon]:w-8 group-data-[collapsible=icon]:flex-none group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0",
          )}
        >
          <LogoGlyph className="h-5 w-auto shrink-0 text-brand" />
          <span className="sr-only">Switch server. This is </span>
          {name ? (
            <span className="min-w-0 flex-1 truncate text-body font-semibold group-data-[collapsible=icon]:hidden">
              {name}
            </span>
          ) : name === undefined ? (
            <Skeleton className="h-3 flex-1 group-data-[collapsible=icon]:hidden" />
          ) : (
            <span className="min-w-0 flex-1 truncate text-body font-semibold group-data-[collapsible=icon]:hidden">
              This server
            </span>
          )}
          {troubled && (
            <span className="flex shrink-0 group-data-[collapsible=icon]:hidden">
              <StatusDot tone={hub.idle ? "danger" : "warning"} />
              <span className="sr-only">. {trouble}</span>
            </span>
          )}
          <ChevronDown
            aria-hidden
            className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[collapsible=icon]:hidden group-data-[state=open]/switcher:rotate-180"
          />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        side={collapsed ? "right" : "bottom"}
        align="start"
        sideOffset={6}
        aria-label="Servers"
        // Radix names the menu after its trigger, whose words are this server's name.
        aria-labelledby={undefined}
        className={cn(
          "max-w-[calc(100vw-1rem)] rounded-lg p-1",
          strip || collapsed ? "w-72" : "w-(--radix-dropdown-menu-trigger-width) min-w-60",
        )}
      >
        <div className="flex min-w-0 items-center justify-between gap-3 px-2 pt-2 pb-1.5">
          <DropdownMenuLabel className="truncate p-0 text-title font-semibold tracking-normal text-foreground normal-case">
            Servers
          </DropdownMenuLabel>
          {troubled && (
            <Status
              tone={hub.idle ? "danger" : "warning"}
              label={trouble}
              className="shrink-0 text-hint"
            />
          )}
        </div>
        {servers.length === 0 && (
          <p className="px-2 py-1.5 text-hint text-muted-foreground">
            The hub lists its servers after the first report.
          </p>
        )}
        {servers.map((server) => {
          const reading = serverReading(server, hub.reachable, hub.due)
          const address = serverAddress(server)
          const dot = <StatusDot tone={reading.tone} className="mx-0.5" />
          // The state in words beside the name where it is news: a green dot says online, and
          // while the hub is unreachable the head already says why every dot is grey. Otherwise
          // the word is only in the row's accessible name.
          const word =
            !server.self && reading.tone !== "running" && reading.tone !== "unknown"
              ? reading.label
              : undefined
          const label = (
            <span className="min-w-0 flex-1 truncate">
              {server.name}
              {!word && <span className="sr-only">, {reading.label}</span>}
            </span>
          )
          if (server.self) {
            return (
              <DropdownMenuItem
                key={server.serverId}
                asChild
                className="gap-2.5 bg-chosen font-medium text-foreground"
              >
                <Link href="/" data-self aria-current="page">
                  {dot}
                  {label}
                  {/* The wordmark carried the installed version; its row here does now. */}
                  <span className="numeric shrink-0 text-hint font-normal text-faint">
                    {VERSION}
                  </span>
                  <span className="sr-only">, this server</span>
                  <Check aria-hidden className="text-foreground" />
                </Link>
              </DropdownMenuItem>
            )
          }
          if (!address) {
            return (
              <DropdownMenuItem key={server.serverId} disabled className="gap-2.5">
                {dot}
                {label}
                {word && <span className="sr-only">, {word}</span>}
                <span className="shrink-0 text-hint text-muted-foreground">
                  <span className="sr-only">, </span>No address
                </span>
              </DropdownMenuItem>
            )
          }
          // Another server opens in this tab, as any link does; a modified click or a middle
          // click opens it in a new one. `noreferrer` either way: nothing of this dashboard's goes
          // with it (§8.10).
          return (
            <DropdownMenuItem key={server.serverId} asChild className="gap-2.5">
              <a href={address} rel="noopener noreferrer">
                {dot}
                {label}
                {word && (
                  <span className="shrink-0 text-hint text-muted-foreground">
                    <span className="sr-only">, </span>
                    {word}
                  </span>
                )}
                {server.version && (
                  <span className="numeric shrink-0 text-hint text-faint">{server.version}</span>
                )}
              </a>
            </DropdownMenuItem>
          )
        })}
        {hubUrl && (
          <>
            <DropdownMenuSeparator className="mx-1 bg-hairline" />
            <DropdownMenuItem asChild className="gap-2.5">
              <a href={hubUrl} rel="noopener noreferrer">
                <LogoGlyph small className="mx-0.5 h-3.5 w-auto shrink-0 text-brand" />
                <span className="min-w-0 flex-1 truncate">Open Just Hub</span>
              </a>
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
