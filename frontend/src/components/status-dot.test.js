import { describe, expect, test } from "bun:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { Status, StatusDot, StatusWords, toneFor } from "./status-dot"
import { DOT_TONE, OUTCOME_FILL, TEXT_TONE, TONE_DOT, VERDICT_TONE } from "./tone"
import { OUTCOME_FILL as STRIP_FILL } from "./outcome-strip"
import { StateCorner, StateMark } from "./product-logo"
import { Tag } from "./tag"
import { LanguageMark } from "./language-icon"
import { DashboardMark } from "./logo"
import { DashboardMark as BackupsDashboardMark } from "./backups/marks"
import { Box } from "./icons"

const draw = (component, props = {}) => renderToStaticMarkup(createElement(component, props))

describe("toneFor", () => {
  // The whole mapping, so a state that moves between tones is a decision
  // someone made rather than a case that slid.
  const cases = {
    running: ["running", "active", "online", "enabled", "success", "open", "connected"],
    warning: [
      "paused",
      "restarting",
      "activating",
      "deactivating",
      "launching",
      "stopping",
      "connecting",
      "reconnecting",
    ],
    danger: ["failed", "errored", "dead", "unreachable"],
    stopped: ["exited", "stopped", "inactive", "created", "closed", "disconnected"],
  }
  for (const [tone, states] of Object.entries(cases)) {
    test(`${states.join(", ")} read as ${tone}`, () => {
      for (const state of states) expect(toneFor(state)).toBe(tone)
    })
  }

  test("a failed thing is red, not the amber of one that is starting", () => {
    expect(DOT_TONE[toneFor("failed")]).toBe("bg-destructive")
    expect(TEXT_TONE[toneFor("Failed")]).toBe("text-destructive")
    expect(toneFor("failed")).not.toBe(toneFor("activating"))
  })

  test("case does not matter, and a word it does not know is unknown", () => {
    expect(toneFor("RUNNING")).toBe("running")
    expect(toneFor("hibernating")).toBe("unknown")
    expect(toneFor(undefined)).toBe("unknown")
  })
})

describe("the fills", () => {
  test("unknown has a mark of its own, not stopped's grey dot", () => {
    expect(DOT_TONE.unknown).not.toBe(DOT_TONE.stopped)
    expect(DOT_TONE.unknown).toContain("ring-")
  })

  test("an outcome strip's answers are the status dot's fills", () => {
    expect(OUTCOME_FILL.success).toBe(DOT_TONE.running)
    expect(OUTCOME_FILL.danger).toBe(DOT_TONE.danger)
    expect(OUTCOME_FILL.warning).toBe(DOT_TONE.warning)
    expect(STRIP_FILL).toBe(OUTCOME_FILL)
  })

  test("a colour tone and a verdict land on the state vocabulary", () => {
    expect(TONE_DOT).toEqual({
      default: "stopped",
      success: "running",
      warning: "warning",
      danger: "danger",
    })
    expect(VERDICT_TONE.critical).toBe("danger")
    expect(VERDICT_TONE.ok).toBe("running")
  })
})

describe("StatusDot", () => {
  test("the plain dot is one hidden element in its tone", () => {
    const html = draw(StatusDot, { state: "dead" })
    expect(html).toBe(
      '<span data-slot="status-dot" aria-hidden="true" class="size-1.5 shrink-0 rounded-full bg-destructive"></span>',
    )
  })

  test("corner places it on the mark's bottom-right, in a ring of the ground", () => {
    const html = draw(StatusDot, { tone: "running", corner: true })
    expect(html).toContain("absolute -right-0.5 -bottom-0.5")
    expect(html).toContain("rounded-full bg-background size-2.5")
    expect(html).toContain("rounded-full bg-success size-1.5")
    expect(html).toMatch(/^<span data-slot="status-dot" aria-hidden="true"/)
    expect(html).not.toContain("sr-only")
  })

  test("a corner's ring and dot grow with the mark", () => {
    expect(draw(StatusDot, { tone: "running", corner: "md" })).toContain("size-3")
    const large = draw(StatusDot, { tone: "running", corner: "lg" })
    expect(large).toContain("size-4")
    expect(large).toContain("size-2")
  })

  test("label says the state in words a screen reader reads, and only then", () => {
    const html = draw(StatusDot, { tone: "warning", corner: true, label: "Unsaved" })
    expect(html).toContain('<span class="sr-only">Unsaved</span>')
    expect(html).toContain('aria-hidden="true"')
    expect(draw(StatusDot, { tone: "warning", label: "Unsaved" })).toContain(
      '<span class="sr-only">Unsaved</span>',
    )
  })

  test("live breathes around the dot, without dimming it", () => {
    const html = draw(StatusDot, { tone: "running", live: true })
    expect(html).toContain("animate-breathe")
    expect(html).not.toContain("animate-pulse")
  })

  test("spread props reach the element", () => {
    expect(draw(StatusDot, { tone: "running", title: "Up" })).toContain('title="Up"')
  })
})

describe("Status", () => {
  test("a failed unit reads red", () => {
    const html = draw(Status, { state: "failed" })
    expect(html).toContain("bg-destructive")
    expect(html).toContain('<span class="text-destructive">failed</span>')
    expect(html).toContain('data-slot="status"')
  })

  test("announce speaks the word in a polite live region, said once", () => {
    const html = draw(Status, { state: "running", announce: "Container state" })
    expect(html).toContain('aria-live="polite"')
    expect(html).toContain('aria-atomic="true"')
    expect(html).toContain("Container state: running")
    // The drawn word is hidden from the reader, which hears the live one.
    expect(html).toContain('<span class="text-foreground" aria-hidden="true">running</span>')
  })

  test("without announce there is no live region", () => {
    expect(draw(Status, { state: "running" })).not.toContain("aria-live")
  })

  test("StatusWords stands alone for a state that is not a Status", () => {
    const html = renderToStaticMarkup(
      createElement(StatusWords, { subject: "Deployment state" }, "Succeeded"),
    )
    expect(html).toBe(
      '<span class="sr-only" aria-live="polite" aria-atomic="true">Deployment state: Succeeded</span>',
    )
  })
})

describe("StateMark", () => {
  test("one ladder: a row's tile is 36px with the medium corner", () => {
    const html = draw(StateMark, { glyph: Box, tone: "danger" })
    expect(html).toContain("size-9")
    expect(html).toContain("size-3")
    expect(html).toContain("bg-destructive")
  })

  test("a rail's tile is 28px with the small corner, a header's 48px", () => {
    expect(draw(StateMark, { glyph: Box, tone: "running", size: "sm" })).toContain("size-7")
    expect(draw(StateMark, { glyph: Box, tone: "running", size: "lg" })).toContain("size-12")
  })

  test("StateCorner is the corner alone", () => {
    const html = draw(StateCorner, { tone: "stopped" })
    expect(html).toContain("absolute -right-0.5 -bottom-0.5")
    expect(html).toContain("bg-muted-foreground")
  })
})

describe("Tag", () => {
  test("the default face is the caps class in the faint ink, not a faded one", () => {
    const html = renderToStaticMarkup(createElement(Tag, null, "ed25519"))
    expect(html).toContain("caps")
    expect(html).toContain("text-faint")
    expect(html).not.toContain("/80")
  })

  test("hue colours the word, without a style object at the call site", () => {
    const html = renderToStaticMarkup(createElement(Tag, { hue: "var(--git-modified)" }, "edited"))
    expect(html).toContain('style="color:var(--git-modified)"')
  })

  test("mono keeps its literal face", () => {
    const html = renderToStaticMarkup(createElement(Tag, { mono: true }, "TLS_AES_128"))
    expect(html).toContain("font-mono")
    expect(html).not.toContain("caps")
  })
})

describe("LanguageMark", () => {
  test("a language with a bundled logo is drawn as it", () => {
    expect(draw(LanguageMark, { language: "TypeScript" })).toContain('src="/logos/typescript.svg"')
    expect(draw(LanguageMark, { language: "C++" })).toContain('src="/logos/cplusplus.svg"')
    expect(draw(LanguageMark, { language: "golang" })).toContain('src="/logos/go.svg"')
    expect(draw(LanguageMark, { language: "Markdown" })).toContain('src="/logos/markdown.svg"')
  })

  test("one without a logo keeps its name and no mark", () => {
    const html = draw(LanguageMark, { language: "Brainfuck" })
    expect(html).not.toContain("<img")
    expect(html).not.toContain("<svg")
    expect(html).toContain("Brainfuck")
  })
})

describe("DashboardMark", () => {
  test("the product tile with the mark in brand blue, at a product's sizes", () => {
    const html = draw(DashboardMark)
    expect(html).toContain("size-8")
    expect(html).toContain("text-brand")
    expect(draw(DashboardMark, { size: "md" })).toContain("size-10")
  })

  test("the backups name still draws it", () => {
    expect(BackupsDashboardMark).toBe(DashboardMark)
  })
})
