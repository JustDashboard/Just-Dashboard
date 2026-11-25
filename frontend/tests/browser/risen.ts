import type { Page } from "@playwright/test"

/**
 * Where the page rests: `boundingBox` during its four-pixel rise reads a place
 * it is leaving. A `BlurFade` block arrives the same way, and later: its fade
 * is the browser's, but its slide is drawn frame by frame by motion, so it
 * has landed when its own transform is gone. Two boxes read one after the
 * other while it lands are a hair apart, which `toBe` reads as a misalignment.
 */
export const risen = (page: Page) =>
  page.evaluate(async () => {
    const arriving = (animation: Animation) => {
      if ((animation as CSSAnimation).animationName === "jd-rise") return true
      const target = (animation.effect as KeyframeEffect | null)?.target
      return target instanceof Element && target.matches("[data-slot=blur-fade]")
    }
    await Promise.allSettled(
      document
        .getAnimations()
        .filter(arriving)
        .map((animation) => animation.finished),
    )
    const sliding = () =>
      [...document.querySelectorAll<HTMLElement>("[data-slot=blur-fade]")].some(
        (block) => block.style.transform !== "" && block.style.transform !== "none",
      )
    for (let frame = 0; frame < 120 && sliding(); frame++) {
      await new Promise((landed) => requestAnimationFrame(landed))
    }
  })
