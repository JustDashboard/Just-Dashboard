"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"
import { ArrowRight, Home, Question } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { WireHost, WireMark, WirePlaceholder } from "@/components/wire/wire"
import { addressTail, nearestPage } from "@/components/missing-page-model"
import { Button } from "@/components/ui/button"
import { WireFlow, type WireFlowNode } from "@/components/wire/wire-flow"

/**
 * An address no page claims, drawn as what it is: the address that was asked
 * for on a dashed ring, wired to this server by a line nothing travels, and on
 * the server's other side the pages that are there, the nearest first. It was
 * a question mark in a dashed box over "Back to the Overview", whatever was
 * asked for; under a section the reader was a press from, the way on is that
 * section's own page (`missing-page-model`), and the Overview is the second
 * way.
 *
 * The picture's marks are the links, so on a phone it keeps the marks and the
 * words under it say the rest.
 */
export function MissingPage() {
  const pathname = usePathname()
  const { can } = useAuth()
  const nearest = nearestPage(pathname, can)
  // Only the last part is drawn, so a deeper address says it has more before it.
  const deeper = pathname.split("/").filter(Boolean).length > 1
  const Nearest = nearest?.icon

  const there: WireFlowNode[] = [
    ...(nearest && Nearest
      ? [
          {
            id: "nearest",
            mark: (size: "lg" | "md" | "sm") => (
              <WireMark tone="logo" size={size}>
                <Nearest aria-hidden />
              </WireMark>
            ),
            eyebrow: "Nearest page",
            title: nearest.title,
            hint: <span className="font-mono">{nearest.href}</span>,
            name: nearest.title,
            href: nearest.href,
            label: `Open ${nearest.title}`,
            line: { state: "up" as const, moving: true },
          },
        ]
      : []),
    {
      id: "overview",
      mark: (size: "lg" | "md" | "sm") => (
        <WireMark tone="logo" size={size}>
          <Home aria-hidden />
        </WireMark>
      ),
      eyebrow: nearest ? undefined : "Start here",
      title: "Overview",
      hint: <span className="font-mono">/</span>,
      name: "Overview",
      href: "/",
      label: "Open the Overview",
      line: { state: "up" as const, moving: !nearest },
    },
  ]

  return (
    <div
      data-slot="empty-state"
      data-kind="missing-page"
      className="mx-auto flex w-full max-w-3xl min-w-0 flex-col items-center gap-8 pt-2 text-center lg:pt-8"
    >
      <WireFlow
        // The page's words are centred; a node's own stand beside its mark.
        className="w-full lg:text-left"
        phone="marks"
        hubShare={0.7}
        start={{
          title: "The address asked for",
          nodes: [
            {
              id: "asked",
              mark: (size) => (
                <WirePlaceholder size={size}>
                  <Question aria-hidden />
                </WirePlaceholder>
              ),
              eyebrow: "Asked for",
              // One line, cut once by its width: cut to a count of letters
              // first, the tail still wrapped in the narrow column and was
              // cut again ("/this-page-does-not-" over "exi…"). The whole
              // address is in the sentence under the picture.
              title: (
                <span
                  title={pathname}
                  className="block truncate font-mono font-normal text-muted-foreground"
                >
                  {deeper ? "…" : ""}
                  {addressTail(pathname, Infinity)}
                </span>
              ),
              hint: "no page here",
              name: "The address asked for",
              placeholder: true,
              line: { state: "idle" },
            },
          ],
        }}
        hub={{ mark: <WireHost />, eyebrow: "This server" }}
        end={{ title: "Pages that are there", nodes: there }}
      />
      <div className="space-y-2">
        <p className="text-2xl leading-tight font-semibold tracking-tight">Page not found</p>
        <p className="mx-auto max-w-md text-sm leading-relaxed text-pretty text-muted-foreground">
          Nothing is at <span className="font-mono break-all text-foreground">{pathname}</span>. It
          may have moved in an update, or the link may be mistyped.
        </p>
      </div>
      <div className="flex flex-wrap items-center justify-center gap-3">
        {nearest && (
          <Button asChild>
            <Link href={nearest.href}>
              Open {nearest.title} <ArrowRight className="size-3.5" />
            </Link>
          </Button>
        )}
        <Button variant={nearest ? "outline" : "default"} asChild>
          <Link href="/">Back to the Overview</Link>
        </Button>
      </div>
    </div>
  )
}
