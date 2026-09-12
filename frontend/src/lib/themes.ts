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
 * Inlined in <head>, before anything paints.
 *
 * Two jobs: put `.dark` on the root so the `dark:` variants resolve, and tell
 * the browser the page is dark so its own form controls, scrollbars and
 * `color-scheme`-derived defaults match. It also drops the stored light/dark
 * choice from anyone upgrading, so the key does not sit in localStorage
 * forever meaning nothing.
 */
export function themeBootstrapScript(): string {
  return (
    `(function(){try{var d=document.documentElement;` +
    `d.classList.add("dark");d.style.colorScheme="dark";` +
    `localStorage.removeItem(${JSON.stringify(THEME_STORAGE_KEY)})}catch(e){}})()`
  )
}
