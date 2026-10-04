# Shared dropdown audit — 2026-10-04

The audit searched all frontend TSX sources for shared select, dropdown, context-menu and popover
imports, native `<select>` elements and menu options carrying manually aligned metadata. The table
below records the 122 consumer files in the audited tree; static trigger counts do
not count the number of rendered items in a mapped list. Feature wrappers such as `VerbMenu`,
`CredentialSelect` and `OwnershipSelect` extend coverage to their callers.

## Findings and changes

| Finding | Coverage | Result |
| --- | --- | --- |
| Ownership button repeats the field name; option hints make a wide second column | Domains, mounts, volume dependencies | Button shows the value; menu holds plain option names. Selected removal consequence remains visible beside the control and is its accessible description. |
| Select opens over its selected row by default; some callers override to popper | 143 static select triggers in 76 consumer files after native migrations | Shared default opens below, start aligned with a 4px gap; viewport collision handling can flip it above. Trigger composes `Button`. |
| Select hints occupy the right-hand column | Credentials, releases, containers, backup jobs, volumes, schedules, Redis DBs, aggregation stages, proxy addresses/certificates, log parsers | All hints use `MenuItemText` beneath the name, outside the value Radix copies into the trigger. |
| Manually aligned secondary columns recreate the same layout | 19 rows across 17 consumers: terminal snippets, file places, database/schema switchers, query history, saved pipelines, column menus, export menus, cell values and explain actions | Moved to `MenuItemText`; text can wrap within the available viewport. Keyboard shortcut hints keep their conventional end alignment. |
| Two native selectors draw and open differently | Command search scope; database activity history | Both now use the shared select. Search returns focus to its input after the picker closes. |
| Two options mix metadata into their selected value | PM2 daemon account; database import match key | Home directory and constraint name now use `SelectItem.hint`. |
| Menus copy styling between primitives; right-click labels/items have different type ranks | Select, dropdown, context menu, submenu and form popover | Shared `menu-styles.ts` defines surfaces, motion, rows, labels, danger states and indicators. Options/actions are at least 32px on desktop and 44px on mobile. Reduced motion removes the opening animation. |

Selects remain listboxes, action menus remain menus, and context menus retain pointer-positioned
opening. Those roles need different keyboard semantics, so the Radix wrappers share the drawing
rather than being replaced with one generic role. Portals continue to use the fullscreen container.
The account card retains its identity layout and navigation state; shortcut key hints and selection
marks retain their place beside an action. Rich form popovers retain their form content and padding.

## Validation and visual evidence

Screenshots use production frontend pages with the repository's mocked deployment API, rather than
a live server's resources. `shared-dropdowns.spec.ts` checks the real ownership trigger/list at
1280, 1720 and 390px, saving ownership changes, mount ownership, keyboard/typeahead, Escape,
outside dismissal, option metadata placement, long-list scrolling, read-only disabling, narrow-screen bounds and
reduced-motion action menus.
The local changed-file gate covers the affected pages plus design-system and navigation checks.
Final check results and screenshot links are recorded in the pull request.

## Screenshots

The before image is the operator's supplied screenshot. After captures are rendered from the final
production build with synthetic project and credential fixtures.

- [Before: ownership menu](screenshots/ownership-before.png)
- [After: ownership button and menu](screenshots/ownership-detail.png)
- [Domains at 1280px](screenshots/domains-1280.png), [1720px](screenshots/domains-1720.png)
  and [390px](screenshots/domains-390.png)
- [Credential options on desktop](screenshots/credentials-desktop.png) and
  [phone](screenshots/credentials-mobile.png)

## Consumer inventory

| Source under `frontend/src/` | Shared primitives | Select triggers | Menu triggers |
| --- | --- | ---: | ---: |
| [app/(dashboard)/files/page.tsx](../../../frontend/src/app/(dashboard)/files/page.tsx) | dropdown-menu | 0 | 3 |
| [app/(dashboard)/processes/services/page.tsx](../../../frontend/src/app/(dashboard)/processes/services/page.tsx) | select | 1 | 0 |
| [app/(dashboard)/terminal/page.tsx](../../../frontend/src/app/(dashboard)/terminal/page.tsx) | dropdown-menu | 0 | 2 |
| [components/account/dashboard-users.tsx](../../../frontend/src/components/account/dashboard-users.tsx) | select | 1 | 0 |
| [components/app-sidebar.tsx](../../../frontend/src/components/app-sidebar.tsx) | dropdown-menu | 0 | 1 |
| [components/backups/job-detail.tsx](../../../frontend/src/components/backups/job-detail.tsx) | select | 2 | 0 |
| [components/backups/job-form.tsx](../../../frontend/src/components/backups/job-form.tsx) | select | 1 | 0 |
| [components/command-palette.tsx](../../../frontend/src/components/command-palette.tsx) | select | 1 | 0 |
| [components/database/data/field.tsx](../../../frontend/src/components/database/data/field.tsx) | select, dropdown-menu | 1 | 1 |
| [components/database/data/filter-bar.tsx](../../../frontend/src/components/database/data/filter-bar.tsx) | select, dropdown-menu, popover | 3 | 1 |
| [components/database/data/fk-peek.tsx](../../../frontend/src/components/database/data/fk-peek.tsx) | popover | 0 | 0 |
| [components/database/data/fk-picker.tsx](../../../frontend/src/components/database/data/fk-picker.tsx) | popover | 0 | 0 |
| [components/database/data/foot.tsx](../../../frontend/src/components/database/data/foot.tsx) | select | 1 | 0 |
| [components/database/data/import-dialog.tsx](../../../frontend/src/components/database/data/import-dialog.tsx) | select | 3 | 0 |
| [components/database/data/rail.tsx](../../../frontend/src/components/database/data/rail.tsx) | dropdown-menu | 0 | 1 |
| [components/database/data/workbench.tsx](../../../frontend/src/components/database/data/workbench.tsx) | dropdown-menu | 0 | 1 |
| [components/database/diagram/chrome.tsx](../../../frontend/src/components/database/diagram/chrome.tsx) | dropdown-menu | 0 | 1 |
| [components/database/diagram/er-diagram.tsx](../../../frontend/src/components/database/diagram/er-diagram.tsx) | dropdown-menu | 0 | 4 |
| [components/database/generate/index.tsx](../../../frontend/src/components/database/generate/index.tsx) | dropdown-menu | 0 | 1 |
| [components/database/generate/table-picker.tsx](../../../frontend/src/components/database/generate/table-picker.tsx) | popover | 0 | 0 |
| [components/database/grid/cell-editor.tsx](../../../frontend/src/components/database/grid/cell-editor.tsx) | popover | 0 | 0 |
| [components/database/grid/data-grid.tsx](../../../frontend/src/components/database/grid/data-grid.tsx) | context-menu | 0 | 0 |
| [components/database/grid/grid-header.tsx](../../../frontend/src/components/database/grid/grid-header.tsx) | dropdown-menu | 0 | 1 |
| [components/database/grid/grid-menus.tsx](../../../frontend/src/components/database/grid/grid-menus.tsx) | dropdown-menu, context-menu | 0 | 1 |
| [components/database/home/activity.tsx](../../../frontend/src/components/database/home/activity.tsx) | select | 1 | 0 |
| [components/database/mongo/aggregations/console.tsx](../../../frontend/src/components/database/mongo/aggregations/console.tsx) | select | 1 | 0 |
| [components/database/mongo/aggregations/pipeline-view.tsx](../../../frontend/src/components/database/mongo/aggregations/pipeline-view.tsx) | dropdown-menu | 0 | 2 |
| [components/database/mongo/aggregations/stage-card.tsx](../../../frontend/src/components/database/mongo/aggregations/stage-card.tsx) | select | 1 | 0 |
| [components/database/mongo/bson-tree.tsx](../../../frontend/src/components/database/mongo/bson-tree.tsx) | select | 2 | 0 |
| [components/database/mongo/collection-dialogs.tsx](../../../frontend/src/components/database/mongo/collection-dialogs.tsx) | select | 2 | 0 |
| [components/database/mongo/documents/index.tsx](../../../frontend/src/components/database/mongo/documents/index.tsx) | select | 1 | 0 |
| [components/database/mongo/documents/query-bar.tsx](../../../frontend/src/components/database/mongo/documents/query-bar.tsx) | dropdown-menu | 0 | 1 |
| [components/database/mongo/performance/profiler.tsx](../../../frontend/src/components/database/mongo/performance/profiler.tsx) | select | 1 | 0 |
| [components/database/mongo/rail.tsx](../../../frontend/src/components/database/mongo/rail.tsx) | dropdown-menu | 0 | 1 |
| [components/database/mongo/schema/index-dialog.tsx](../../../frontend/src/components/database/mongo/schema/index-dialog.tsx) | select | 1 | 0 |
| [components/database/ops/access-mongo.tsx](../../../frontend/src/components/database/ops/access-mongo.tsx) | select | 3 | 0 |
| [components/database/ops/access-new.tsx](../../../frontend/src/components/database/ops/access-new.tsx) | select | 1 | 0 |
| [components/database/query/command-strip.tsx](../../../frontend/src/components/database/query/command-strip.tsx) | dropdown-menu | 0 | 2 |
| [components/database/query/rail.tsx](../../../frontend/src/components/database/query/rail.tsx) | dropdown-menu | 0 | 1 |
| [components/database/query/results.tsx](../../../frontend/src/components/database/query/results.tsx) | dropdown-menu | 0 | 2 |
| [components/database/query/tab-strip.tsx](../../../frontend/src/components/database/query/tab-strip.tsx) | dropdown-menu | 0 | 1 |
| [components/database/redis/db-picker.tsx](../../../frontend/src/components/database/redis/db-picker.tsx) | select | 1 | 0 |
| [components/database/redis/keys/key-dialogs.tsx](../../../frontend/src/components/database/redis/keys/key-dialogs.tsx) | select | 1 | 0 |
| [components/database/redis/keys/ttl-editor.tsx](../../../frontend/src/components/database/redis/keys/ttl-editor.tsx) | popover | 0 | 0 |
| [components/database/schema/fields.tsx](../../../frontend/src/components/database/schema/fields.tsx) | dropdown-menu | 0 | 1 |
| [components/database/schema/object-forms.tsx](../../../frontend/src/components/database/schema/object-forms.tsx) | select | 2 | 0 |
| [components/database/schema/rail.tsx](../../../frontend/src/components/database/schema/rail.tsx) | dropdown-menu | 0 | 1 |
| [components/database/schema/statistics.tsx](../../../frontend/src/components/database/schema/statistics.tsx) | dropdown-menu | 0 | 1 |
| [components/database/schema/table-forms.tsx](../../../frontend/src/components/database/schema/table-forms.tsx) | select | 6 | 0 |
| [components/database/search/index.tsx](../../../frontend/src/components/database/search/index.tsx) | dropdown-menu | 0 | 1 |
| [components/database/shell/connect-popover.tsx](../../../frontend/src/components/database/shell/connect-popover.tsx) | popover | 0 | 0 |
| [components/database/shell/connection-switcher.tsx](../../../frontend/src/components/database/shell/connection-switcher.tsx) | popover | 0 | 0 |
| [components/deploy/build-console.tsx](../../../frontend/src/components/deploy/build-console.tsx) | select | 1 | 0 |
| [components/deploy/credentials-page.tsx](../../../frontend/src/components/deploy/credentials-page.tsx) | select | 1 | 0 |
| [components/deploy/game/settings.tsx](../../../frontend/src/components/deploy/game/settings.tsx) | select | 1 | 0 |
| [components/deploy/insights.tsx](../../../frontend/src/components/deploy/insights.tsx) | select | 1 | 0 |
| [components/deploy/new-project/configure-advanced.tsx](../../../frontend/src/components/deploy/new-project/configure-advanced.tsx) | select | 6 | 0 |
| [components/deploy/new-project/source-git.tsx](../../../frontend/src/components/deploy/new-project/source-git.tsx) | select | 1 | 0 |
| [components/deploy/new-project/source-template.tsx](../../../frontend/src/components/deploy/new-project/source-template.tsx) | select | 2 | 0 |
| [components/deploy/new-project/step-project.tsx](../../../frontend/src/components/deploy/new-project/step-project.tsx) | select | 8 | 0 |
| [components/deploy/new-project.tsx](../../../frontend/src/components/deploy/new-project.tsx) | popover | 0 | 0 |
| [components/deploy/project-builds.tsx](../../../frontend/src/components/deploy/project-builds.tsx) | select | 1 | 0 |
| [components/deploy/project-console.tsx](../../../frontend/src/components/deploy/project-console.tsx) | select | 3 | 0 |
| [components/deploy/project-deployments.tsx](../../../frontend/src/components/deploy/project-deployments.tsx) | select | 1 | 0 |
| [components/deploy/project-runtime.tsx](../../../frontend/src/components/deploy/project-runtime.tsx) | select | 1 | 0 |
| [components/deploy/requests-workspace.tsx](../../../frontend/src/components/deploy/requests-workspace.tsx) | select, popover | 1 | 0 |
| [components/deploy/settings/automation/schedules.tsx](../../../frontend/src/components/deploy/settings/automation/schedules.tsx) | select, popover | 2 | 0 |
| [components/deploy/settings/databases.tsx](../../../frontend/src/components/deploy/settings/databases.tsx) | select | 2 | 0 |
| [components/deploy/settings/mounts.tsx](../../../frontend/src/components/deploy/settings/mounts.tsx) | select | 1 | 0 |
| [components/deploy/settings/runtime.tsx](../../../frontend/src/components/deploy/settings/runtime.tsx) | select | 2 | 0 |
| [components/deploy/settings/variables.tsx](../../../frontend/src/components/deploy/settings/variables.tsx) | select | 1 | 0 |
| [components/docker/container-actions.tsx](../../../frontend/src/components/docker/container-actions.tsx) | dropdown-menu | 0 | 1 |
| [components/docker/networks-tab.tsx](../../../frontend/src/components/docker/networks-tab.tsx) | select | 1 | 0 |
| [components/docker/stacks-tab.tsx](../../../frontend/src/components/docker/stacks-tab.tsx) | dropdown-menu | 0 | 1 |
| [components/files/file-actions.tsx](../../../frontend/src/components/files/file-actions.tsx) | context-menu | 0 | 0 |
| [components/files/file-editor.tsx](../../../frontend/src/components/files/file-editor.tsx) | select | 2 | 0 |
| [components/files/file-tree.tsx](../../../frontend/src/components/files/file-tree.tsx) | dropdown-menu | 0 | 1 |
| [components/files/folder-colour.tsx](../../../frontend/src/components/files/folder-colour.tsx) | dropdown-menu | 0 | 1 |
| [components/files/image-editor.tsx](../../../frontend/src/components/files/image-editor.tsx) | select | 3 | 0 |
| [components/files/path-bar.tsx](../../../frontend/src/components/files/path-bar.tsx) | dropdown-menu | 0 | 1 |
| [components/files/places-menu.tsx](../../../frontend/src/components/files/places-menu.tsx) | dropdown-menu | 0 | 1 |
| [components/git/branches-panel.tsx](../../../frontend/src/components/git/branches-panel.tsx) | select | 1 | 0 |
| [components/git/changes-panel.tsx](../../../frontend/src/components/git/changes-panel.tsx) | dropdown-menu | 0 | 1 |
| [components/git/clone-dialog.tsx](../../../frontend/src/components/git/clone-dialog.tsx) | select | 1 | 0 |
| [components/git/extras-preview.tsx](../../../frontend/src/components/git/extras-preview.tsx) | select | 1 | 0 |
| [components/git/forge-preview.tsx](../../../frontend/src/components/git/forge-preview.tsx) | select | 3 | 0 |
| [components/git/github-account.tsx](../../../frontend/src/components/git/github-account.tsx) | dropdown-menu | 0 | 1 |
| [components/git/github-panel.tsx](../../../frontend/src/components/git/github-panel.tsx) | select | 1 | 0 |
| [components/git/github-review.tsx](../../../frontend/src/components/git/github-review.tsx) | select | 2 | 0 |
| [components/git/graph-panel.tsx](../../../frontend/src/components/git/graph-panel.tsx) | select | 1 | 0 |
| [components/git/help.tsx](../../../frontend/src/components/git/help.tsx) | popover | 0 | 0 |
| [components/git/merge-pull-dialog.tsx](../../../frontend/src/components/git/merge-pull-dialog.tsx) | select | 1 | 0 |
| [components/git/rebase-preview.tsx](../../../frontend/src/components/git/rebase-preview.tsx) | select | 1 | 0 |
| [components/logs/export-dialog.tsx](../../../frontend/src/components/logs/export-dialog.tsx) | select | 1 | 0 |
| [components/logs/filter-bar.tsx](../../../frontend/src/components/logs/filter-bar.tsx) | select, popover | 2 | 0 |
| [components/logs/lens-bar.tsx](../../../frontend/src/components/logs/lens-bar.tsx) | popover | 0 | 0 |
| [components/logs/log-console.tsx](../../../frontend/src/components/logs/log-console.tsx) | dropdown-menu | 0 | 2 |
| [components/logs/service-logs.tsx](../../../frontend/src/components/logs/service-logs.tsx) | select | 1 | 0 |
| [components/procs/cron-jobs.tsx](../../../frontend/src/components/procs/cron-jobs.tsx) | select | 3 | 0 |
| [components/procs/pm2-start-dialog.tsx](../../../frontend/src/components/procs/pm2-start-dialog.tsx) | select | 3 | 0 |
| [components/procs/process-table.tsx](../../../frontend/src/components/procs/process-table.tsx) | select, dropdown-menu | 2 | 1 |
| [components/proxy/access-lists-panel.tsx](../../../frontend/src/components/proxy/access-lists-panel.tsx) | select | 1 | 0 |
| [components/proxy/certbot-panel.tsx](../../../frontend/src/components/proxy/certbot-panel.tsx) | select | 5 | 0 |
| [components/proxy/ports-panel.tsx](../../../frontend/src/components/proxy/ports-panel.tsx) | select | 1 | 0 |
| [components/proxy/ports-proxy.tsx](../../../frontend/src/components/proxy/ports-proxy.tsx) | popover | 0 | 0 |
| [components/proxy/request-tester.tsx](../../../frontend/src/components/proxy/request-tester.tsx) | select | 1 | 0 |
| [components/proxy/site-form.tsx](../../../frontend/src/components/proxy/site-form.tsx) | select | 12 | 0 |
| [components/proxy/site-traffic-view.tsx](../../../frontend/src/components/proxy/site-traffic-view.tsx) | select | 1 | 0 |
| [components/proxy/stream-listen.tsx](../../../frontend/src/components/proxy/stream-listen.tsx) | select | 1 | 0 |
| [components/proxy/stream-tls.tsx](../../../frontend/src/components/proxy/stream-tls.tsx) | select | 1 | 0 |
| [components/proxy/streams-panel.tsx](../../../frontend/src/components/proxy/streams-panel.tsx) | select, popover | 2 | 0 |
| [components/proxy/tls-report.tsx](../../../frontend/src/components/proxy/tls-report.tsx) | select | 2 | 0 |
| [components/proxy/upstream-picker.tsx](../../../frontend/src/components/proxy/upstream-picker.tsx) | popover | 0 | 0 |
| [components/proxy/vhosts-panel.tsx](../../../frontend/src/components/proxy/vhosts-panel.tsx) | select | 1 | 0 |
| [components/proxy/watched-domains.tsx](../../../frontend/src/components/proxy/watched-domains.tsx) | select | 1 | 0 |
| [components/schedule-builder.tsx](../../../frontend/src/components/schedule-builder.tsx) | select | 2 | 0 |
| [components/security/firewall-panel.tsx](../../../frontend/src/components/security/firewall-panel.tsx) | select | 1 | 0 |
| [components/security/rule-form.tsx](../../../frontend/src/components/security/rule-form.tsx) | select, popover | 4 | 0 |
| [components/security/ssh-panel.tsx](../../../frontend/src/components/security/ssh-panel.tsx) | select | 1 | 0 |
| [components/security/tools/tool-panel.tsx](../../../frontend/src/components/security/tools/tool-panel.tsx) | select | 2 | 0 |
| [components/verbs.tsx](../../../frontend/src/components/verbs.tsx) | dropdown-menu | 0 | 1 |
| [components/xterm-pane.tsx](../../../frontend/src/components/xterm-pane.tsx) | dropdown-menu, popover | 0 | 2 |
