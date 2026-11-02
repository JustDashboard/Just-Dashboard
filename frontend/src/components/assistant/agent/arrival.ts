"use client"

/**
 * Arriving where a shortcut sent you: the section it named is scrolled to and lit
 * for a moment, so the reader sees what changed rather than looks for it. The pages
 * read their data when they mount, so the section may not exist yet; it is looked
 * for a little while, and if it never appears the page itself was the answer.
 */

let wanted: string | null = null

const TRIES = 40
const EVERY = 100

/** A shortcut was pressed: remember the section it names. */
export function arriveAt(href: string) {
  const at = href.indexOf("#")
  wanted = at >= 0 ? href.slice(at + 1) : null
  if (wanted) window.setTimeout(look, EVERY, TRIES)
}

function look(left: number) {
  if (!wanted) return
  const section = document.getElementById(wanted)
  if (!section) {
    if (left > 0) window.setTimeout(look, EVERY, left - 1)
    return
  }
  wanted = null
  const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches
  section.scrollIntoView({ block: "start", behavior: reduced ? "instant" : "smooth" })
  section.setAttribute("data-arrived", "true")
  window.setTimeout(() => section.removeAttribute("data-arrived"), 2400)
}
