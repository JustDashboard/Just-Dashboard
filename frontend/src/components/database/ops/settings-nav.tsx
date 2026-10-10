"use client"

import { useEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"
import { tabClasses } from "@/components/tabs"

export type SettingsSection = { id: string; title: string }

/**
 * What a section carries so that it lands under the strip rather than behind
 * it: the strip's own height, so the hairline over the section is the strip's.
 */
export const UNDER_STRIP = "scroll-mt-10"

/** How far under the strip a section's head may be and still be the one being read. */
const READING_LINE = 96

/** The element whose scroll moves the page: the shell's page region, not the window. */
function scrollerOf(node: HTMLElement | null): HTMLElement | null {
  for (let el = node?.parentElement; el; el = el.parentElement) {
    const overflow = getComputedStyle(el).overflowY
    if (overflow === "auto" || overflow === "scroll") return el
  }
  return null
}

/**
 * The sections of Settings as a strip that stays at the top of the page: the
 * way from Connection to the Danger zone without scrolling the six screens
 * between them, and the mark of which one is being read.
 *
 * They are anchors, not views — every section is on the page at once — so a
 * press moves the address's `#section` and Back returns to where the reader
 * was. The underline is the brand's "where you are", the one a view strip
 * draws.
 */
export function SectionJump({ sections }: { sections: SettingsSection[] }) {
  const nav = useRef<HTMLElement>(null)
  const [current, setCurrent] = useState(sections[0]?.id ?? "")
  const ids = sections.map((section) => section.id).join(" ")

  useEffect(() => {
    const scroller = scrollerOf(nav.current)
    if (!scroller) return
    const list = ids.split(" ")
    let frame = 0
    const read = () => {
      frame = 0
      const top = scroller.getBoundingClientRect().top
      let found = list[0]
      for (const id of list) {
        const head = document.getElementById(id)
        if (head && head.getBoundingClientRect().top - top <= READING_LINE) found = id
      }
      // The last section is too short to reach the line: at the foot of the page it is the one.
      const foot = scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 2
      if (foot && scroller.scrollTop > 0) found = list[list.length - 1]
      setCurrent(found)
    }
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(read)
    }
    read()
    scroller.addEventListener("scroll", onScroll, { passive: true })
    // The sections above grow as their reads land, which moves every head.
    const sized = new ResizeObserver(onScroll)
    if (nav.current?.parentElement) sized.observe(nav.current.parentElement)
    return () => {
      scroller.removeEventListener("scroll", onScroll)
      sized.disconnect()
      cancelAnimationFrame(frame)
    }
  }, [ids])

  // On a phone the strip scrolls sideways: the current section's word is kept in it.
  useEffect(() => {
    const strip = nav.current?.firstElementChild
    const link = strip?.querySelector<HTMLElement>('[aria-current="location"]')
    if (!strip || !link) return
    const [box, at] = [strip.getBoundingClientRect(), link.getBoundingClientRect()]
    if (at.left < box.left || at.right > box.right) {
      strip.scrollTo({ left: strip.scrollLeft + at.left - box.left - 16 })
    }
  }, [current])

  return (
    <nav
      ref={nav}
      aria-label="Sections of Settings"
      data-slot="settings-sections"
      // Flush under the database's strip, and over whatever scrolls beneath it.
      className="sticky top-0 z-10 -mx-4 -mt-6 bg-background px-4 md:-mt-8"
    >
      <div className="flex min-w-0 [scrollbar-width:none] gap-5 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden">
        {sections.map((section) => (
          <a
            key={section.id}
            href={`#${section.id}`}
            aria-current={current === section.id ? "location" : undefined}
            className={cn(tabClasses(current === section.id, "h-10"), "shrink-0")}
          >
            {section.title}
          </a>
        ))}
      </div>
    </nav>
  )
}

/**
 * Lands the page on the section its address names: `#danger`, or the
 * parameters when the address carries a search for one.
 *
 * The browser's own scroll to an anchor happens before this page exists — the
 * shell draws it once the connection has answered — and the sections above
 * the target keep growing as their own reads land. So the section is scrolled
 * to when the page arrives and again whenever the page's height changes,
 * until the reader moves it themselves or a few seconds have passed.
 */
export function useSectionAnchor(fallback: string) {
  const wanted = useRef(fallback)
  useEffect(() => {
    let sized: ResizeObserver | undefined
    let timer = 0
    const release = () => {
      sized?.disconnect()
      sized = undefined
      window.clearTimeout(timer)
      for (const type of MOVES) window.removeEventListener(type, release)
    }
    const land = (id: string, hold: boolean) => {
      release()
      const target = id ? document.getElementById(id) : null
      if (!target) return
      const go = () => target.scrollIntoView({ block: "start" })
      go()
      if (!hold || !target.parentElement) return
      sized = new ResizeObserver(go)
      sized.observe(target.parentElement)
      timer = window.setTimeout(release, 5000)
      for (const type of MOVES) window.addEventListener(type, release, { passive: true })
    }
    const named = () => decodeURIComponent(window.location.hash.slice(1))
    land(named() || wanted.current, true)
    const onHash = () => land(named(), false)
    window.addEventListener("hashchange", onHash)
    return () => {
      release()
      window.removeEventListener("hashchange", onHash)
    }
  }, [])
}

/** What a reader does to move the page themselves. */
const MOVES = ["wheel", "touchstart", "keydown", "pointerdown"] as const
