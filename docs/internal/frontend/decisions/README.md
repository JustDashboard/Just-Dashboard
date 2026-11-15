# Decisions

Why each page looks as it does. Until 0.9.0 these stories ran inside `design-system.md` §14 and
§15, so a rule and the story of one page were read as one text; they are moved here verbatim, one
file per section, under a heading per page.

They are history. A rule is in [`../standard.md`](../standard.md), a foundation page, a component
page or a recipe; where a story here and the standard disagree, the standard wins. A name a story
uses may no longer exist in the code: that is what history is, and it is why reference pages do not
carry these stories.

| File | Section |
| --- | --- |
| [`shared.md`](shared.md) | Across the product |
| [`deploy.md`](deploy.md) | Deploy |
| [`databases.md`](databases.md) | Databases |
| [`network.md`](network.md) | Network |
| [`proxy-security.md`](proxy-security.md) | Proxy and Security |
| [`host.md`](host.md) | Overview, Docker, Metrics, Processes, Packages, System users, Backups |
| [`workspace.md`](workspace.md) | Account, settings, audit, Files, Git, terminal, logs |

A page pass adds its story to its section's file, dated, rather than to a rule page.
