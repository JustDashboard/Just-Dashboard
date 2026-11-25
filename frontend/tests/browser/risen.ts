import type { Page } from "@playwright/test"

/**
 * Where the page rests: `boundingBox` during its four-pixel rise reads a place
 * it is leaving. A `BlurFade` block arrives the same way, and later: its slide
 * is drawn frame by frame by motion, so it has landed when its own transform
 * is gone. Two boxes read one after the other while it lands are a hair
 * apart, which `toBe` reads as a misalignment.
 *
 * Its fade is not waited for. It moves no box, and under a spec's installed
 * clock (the deploy showcase's) motion starts it at a time the page's own
 * timeline has not reached, so its end can be minutes away.
 */
export const risen = (page: Page) =>
  page.evaluate(async () => {
    await Promise.allSettled(
      document
        .getAnimations()
        .filter((animation) => (animation as CSSAnimation).animationName === "jd-rise")
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
