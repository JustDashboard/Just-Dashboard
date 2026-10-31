import type { Page } from "@playwright/test"

/** Where the page rests: `boundingBox` during its four-pixel rise reads a place it is leaving. */
export const risen = (page: Page) =>
  page.evaluate(() =>
    Promise.allSettled(
      document
        .getAnimations()
        .filter((a) => (a as CSSAnimation).animationName === "jd-rise")
        .map((a) => a.finished),
    ),
  )
