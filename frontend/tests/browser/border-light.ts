import type { Locator } from "@playwright/test"

/**
 * How far past its window's edge the page reaches at any point of a border
 * light's lap, with the lights found under `scope`.
 *
 * A light is moved round its frame by one animation (`ui/border-beam.tsx`),
 * which is stepped through a lap here rather than waited for: at four or five
 * seconds a lap, a check that looked once saw the light on the right edge one
 * time in ten. `unclipped` takes the light's own clip away first, which is
 * how a test shows it is looking at the thing the clip is for.
 */
export function widestDuringLap(scope: Locator, unclipped = false) {
  return scope.evaluate(async (el, bare) => {
    if (bare)
      for (const box of el.querySelectorAll("[data-slot=border-beam]"))
        box.classList.remove("overflow-hidden")
    const laps = el
      .getAnimations({ subtree: true })
      .filter((animation) =>
        (animation.effect as KeyframeEffect | null)
          ?.getKeyframes()
          .some((frame) => String(frame.transform ?? "").includes("translate(")),
      )
    const scrolls = [
      document.documentElement,
      ...document.querySelectorAll("[data-slot=page]"),
    ].map((node) => node.parentElement ?? node)
    let most = 0
    for (const lap of laps) {
      const duration = Number(lap.effect?.getComputedTiming().duration ?? 0)
      lap.pause()
      for (let at = 0; at < 1; at += 0.02) {
        lap.currentTime = at * duration
        await new Promise(requestAnimationFrame)
        most = Math.max(most, ...scrolls.map((node) => node.scrollWidth - node.clientWidth))
      }
      lap.play()
    }
    return { laps: laps.length, most }
  }, unclipped)
}
