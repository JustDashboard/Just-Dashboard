"use client"

import Link from "next/link"
import { ArrowUpRight, ChevronDown } from "@/components/icons"
import { hubLabel, openable, orderFleet, serverAddress, serverReading } from "@/lib/hub-link"
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
 * The menu is the hub's last word on each server, cached by this server's backend, and says so
 * under its label ("Listed by Just Hub (hub)"): every name in it is the hub's text
 * (hub-link-protocol §8.10). A dot in the server's state and its name, this one first with the
 * installed version the wordmark used to carry, then the rest by name with an arrow, since pressing
 * one leaves for another origin. The hub sends no version for them (§6.1). Another server, and the
 * hub, open in a new tab with no opener and no referrer: the address is one the hub handed over,
 * so it never takes this tab, and nothing of this dashboard's goes with it (§8.10). While the hub
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
            "group/switcher flex h-8 min-w-0 items-center gap-2 rounded-lg px-1.5 text-left focus-ring transition-colors hover:bg-sidebar-accent data-[state=open]:bg-sidebar-accent",
            strip
              ? "max-w-[min(18rem,100%)] flex-1"
              : "-ml-1.5 flex-1 group-data-[collapsible=icon]:ml-0 group-data-[collapsible=icon]:w-8 group-data-[collapsible=icon]:flex-none group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0",
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
        <DropdownMenuLabel className="flex min-w-0 items-center justify-between gap-3">
          <span className="truncate">Servers</span>
          {troubled && (
            <Status
              tone={hub.idle ? "danger" : "warning"}
              label={trouble}
              className="text-hint tracking-normal normal-case"
            />
          )}
        </DropdownMenuLabel>
        <p
          data-slot="hub-attribution"
          className="truncate px-2 pb-1.5 text-hint text-muted-foreground"
        >
          Listed by {hubLabel(hub.name)}
        </p>
        {servers.length === 0 && (
          <p className="px-2 py-1.5 text-hint text-muted-foreground">
            The hub lists its servers after the first report.
          </p>
        )}
        {servers.map((server) => {
          const reading = serverReading(server, hub.reachable, hub.due)
          const address = serverAddress(server)
          const dot = <StatusDot tone={reading.tone} className="mx-0.5" />
          const label = (
            <span className="min-w-0 flex-1 truncate">
              {server.name}
              <span className="sr-only">, {reading.label}</span>
            </span>
          )
          if (server.self) {
            return (
              <DropdownMenuItem key={server.serverId} asChild className="gap-2.5">
                <Link href="/" data-self>
                  {dot}
                  {label}
                  {/* The wordmark carried the installed version; its row here does now. */}
                  <span className="numeric shrink-0 text-hint text-muted-foreground">
                    {VERSION}
                  </span>
                  <span className="shrink-0 text-hint text-muted-foreground">this one</span>
                </Link>
              </DropdownMenuItem>
            )
          }
          if (!address) {
            return (
              <DropdownMenuItem key={server.serverId} disabled className="gap-2.5">
                {dot}
                {label}
                <span className="shrink-0 text-hint text-muted-foreground">no address</span>
              </DropdownMenuItem>
            )
          }
          return (
            <DropdownMenuItem key={server.serverId} asChild className="gap-2.5">
              <a href={address} target="_blank" rel="noopener noreferrer">
                {dot}
                {label}
                {server.version && (
                  <span className="numeric shrink-0 text-hint text-muted-foreground">
                    {server.version}
                  </span>
                )}
                <span className="sr-only">, opens in a new tab</span>
                <ArrowUpRight aria-hidden />
              </a>
            </DropdownMenuItem>
          )
        })}
        {hubUrl && (
          <>
            <DropdownMenuSeparator className="mx-1 bg-hairline" />
            <DropdownMenuItem asChild className="gap-2.5">
              <a href={hubUrl} target="_blank" rel="noopener noreferrer">
                <LogoGlyph small className="h-3.5 w-auto shrink-0 text-brand" />
                <span className="min-w-0 flex-1 truncate">Open Just Hub</span>
                <span className="sr-only">, opens in a new tab</span>
                <ArrowUpRight aria-hidden />
              </a>
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
