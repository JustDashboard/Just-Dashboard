/**
 * Dark. Only dark.
 *
 * This was a registry of twelve named palettes, then one palette in two modes,
 * and is now one palette. The light mode was removed rather than left to rot:
 * every tinted surface, every status hue, every chart colour and every terminal
 * ANSI slot had to be chosen twice and checked twice, and the second set was
 * seen by almost nobody — this is a console you keep open beside the thing you
 * are fixing, not a document you print.
 *
 * What is left is the one line of bootstrap the document still needs. It is
 * kept as a script rather than hard-coded into the markup because the class is
 * also what the generated shadcn primitives' `dark:` variants key off, and the
 * root element has to carry it before anything paints either way.
 */

/** Where the removed preference used to be kept. Cleared on first load. */
export const THEME_STORAGE_KEY = "just-dashboard.theme"

/**
 * Where "Pause animations" is kept: this browser's, like the reduced-motion
 * setting it stands beside, and not the account's. `MOTION_PAUSED` is the one
 * value it holds; anything else, or nothing, is motion as designed.
 */
export const MOTION_STORAGE_KEY = "just-dashboard.motion"
export const MOTION_PAUSED = "paused"

/**
 * Inlined in <head>, before anything paints.
 *
 * Three jobs. It puts `.dark` on the root so the `dark:` variants resolve, and
 * tells the browser the page is dark so its own form controls, scrollbars and
 * `color-scheme`-derived defaults match. It puts `data-motion="paused"` on the
 * root for a browser that asked for still pages (the shell's "Pause
 * animations", `components/shell/motion-pause.ts`), so a paused page never
 * starts moving and then stops: the stylesheet reads the attribute the way it
 * reads `prefers-reduced-motion`. And it drops the stored light/dark choice
 * from anyone upgrading, so the key does not sit in localStorage forever
 * meaning nothing.
 *
 * Storage is read last and inside the `try`: a browser that refuses it (a
 * private window, site data blocked) still gets the class, and is simply not
 * paused.
 */
export function themeBootstrapScript(): string {
  return (
    `(function(){var d=document.documentElement;` +
    `d.classList.add("dark");d.style.colorScheme="dark";` +
    `try{if(localStorage.getItem(${JSON.stringify(MOTION_STORAGE_KEY)})===${JSON.stringify(MOTION_PAUSED)})` +
    `d.setAttribute("data-motion",${JSON.stringify(MOTION_PAUSED)});` +
    `localStorage.removeItem(${JSON.stringify(THEME_STORAGE_KEY)})}catch(e){}})()`
  )
}
