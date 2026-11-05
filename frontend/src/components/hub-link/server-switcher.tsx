"use client"

import Link from "next/link"
import { ArrowUpRight, ChevronDown } from "@/components/icons"
import { openable, orderFleet, serverAddress, serverReading } from "@/lib/hub-link"
import { cn } from "@/lib/utils"
import type { ShellHubValue } from "@/hooks/use-hub-link"
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
 * stays and the name takes the wordmark's place, with the chevron that says it opens.
 *
 * The menu is the hub's last word on each server, cached by this server's backend: a dot in the
 * server's state and its name, this one first and marked, then the rest by name with their
 * version and an arrow, since pressing one leaves for another origin. They open in this tab,
 * which is the point — the hub's own Open button is the way to a new one — and they are real
 * links, so a middle click still makes a tab. While the hub cannot be reached the others' dots go
 * grey, because what it said last may be minutes old, and the menu says why.
 *
 * `strip` is the phone's top strip, where the rail is a closed sheet: the same trigger and menu,
 * so which server a phone is on is never a swipe away.
 */
export function ServerSwitcher({ hub, strip }: { hub: ShellHubValue; strip?: boolean }) {
  const { state, isMobile } = useSidebar()
  const collapsed = !strip && !isMobile && state === "collapsed"
  const name = hub.self?.name
  const servers = orderFleet(hub.fleet)
  const hubUrl = openable(hub.url)

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
          ) : !hub.answered && !hub.failed ? (
            <Skeleton className="h-3 flex-1 group-data-[collapsible=icon]:hidden" />
          ) : (
            <span className="min-w-0 flex-1 truncate text-body font-semibold group-data-[collapsible=icon]:hidden">
              This server
            </span>
          )}
          {!hub.reachable && (
            <span className="flex shrink-0 group-data-[collapsible=icon]:hidden">
              <StatusDot tone="warning" />
              <span className="sr-only">. Hub unreachable</span>
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
          {!hub.reachable && (
            <Status
              tone="warning"
              label="Hub unreachable"
              className="text-hint tracking-normal normal-case"
            />
          )}
        </DropdownMenuLabel>
        {servers.length === 0 && (
          <p className="px-2 py-1.5 text-hint text-muted-foreground">
            The hub lists its servers after the first report.
          </p>
        )}
        {servers.map((server) => {
          const reading = serverReading(server, hub.reachable)
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
              <a href={address} rel="noreferrer">
                {dot}
                {label}
                {server.version && (
                  <span className="numeric shrink-0 text-hint text-muted-foreground">
                    {server.version}
                  </span>
                )}
                <ArrowUpRight aria-hidden />
              </a>
            </DropdownMenuItem>
          )
        })}
        {hubUrl && (
          <>
            <DropdownMenuSeparator className="mx-1 bg-hairline" />
            <DropdownMenuItem asChild className="gap-2.5">
              <a href={hubUrl} rel="noreferrer">
                <LogoGlyph small className="h-3.5 w-auto shrink-0 text-brand" />
                <span className="min-w-0 flex-1 truncate">Open Just Hub</span>
                <ArrowUpRight aria-hidden />
              </a>
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
