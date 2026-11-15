# Audits and redesign records

Each folder is the record of one audit, redesign or verification pass, named by its date. A record
describes the code as it was that day: file names, counts and findings in it are history, and the
present behaviour is in [`../internal/`](../internal/README.md). Nothing here is read by the build,
the installer or a test.

The pictures, recordings and traces these records show are not in the tree. They are on the orphan
branch `pr-assets/audits-evidence`, at the paths they had here, and every link names the commit
(`397e711c0c5c`), so it cannot move. New evidence follows the same rule: a `pr-assets/*` branch, linked
by commit, never a file under this directory ([`../../AGENTS.md`](../../AGENTS.md)).

## Records with a written report

| Date | Record | What it is | Evidence |
| --- | --- | --- | --- |
| 2026-08-28 | [`2026-08-28-design-system-audit/`](2026-08-28-design-system-audit/report.html) | Frontend Design System Audit | — |
| 2026-09-15 | [`0.6.7/`](0.6.7/remediation-report.md) | 0.6.7 audit remediation | — |
| 2026-09-16 | [`2026-09-16-deployments/`](2026-09-16-deployments/README.md) | Deployment audit and competitor comparison | — |
| 2026-09-16 | [`2026-09-16-deployments-second-pass/`](2026-09-16-deployments-second-pass/report.html) | Deployments Second Pass | — |
| 2026-09-17 | [`2026-09-17-proxy/`](2026-09-17-proxy/README.md) | Proxy & TLS audit and competitor comparison | — |
| 2026-09-19 | [`2026-09-19-resource-footprint/`](2026-09-19-resource-footprint/report.html) | Just Dashboard — Resource Footprint | — |
| 2026-09-22 | [`2026-09-22-deploy-new/`](2026-09-22-deploy-new/README.md) | Deployment creation audit and implementation plan | — |
| 2026-09-29 | [`2026-09-29-deployment-header/`](2026-09-29-deployment-header/README.md) | Deployment header layout | [4 files][2026-09-29-deployment-header] |
| 2026-09-29 | [`2026-09-29-deployment-history/`](2026-09-29-deployment-history/README.md) | Deployment history layout | [6 files][2026-09-29-deployment-history] |
| 2026-09-29 | [`2026-09-29-source-sans-3/`](2026-09-29-source-sans-3/README.md) | Source Sans 3 visual comparison | [4 files][2026-09-29-source-sans-3] |
| 2026-09-29 | [`2026-09-29-terminal-tools/`](2026-09-29-terminal-tools/README.md) | Terminal tools and installer verification | [2 files][2026-09-29-terminal-tools] |
| 2026-10-02 | [`2026-10-02-compact-deploy-header/`](2026-10-02-compact-deploy-header/README.md) | Compact project header | [8 files][2026-10-02-compact-deploy-header] |
| 2026-10-02 | [`2026-10-02-database-creation/`](2026-10-02-database-creation/README.md) | Shared database creation | [7 files][2026-10-02-database-creation] |
| 2026-10-02 | [`2026-10-02-files/`](2026-10-02-files/README.md) | File browser spacing and interactions | [5 files][2026-10-02-files] |
| 2026-10-02 | [`2026-10-02-files-search/`](2026-10-02-files-search/README.md) | Files search palette verification | [11 files][2026-10-02-files-search] |
| 2026-10-02 | [`2026-10-02-files-workspace/`](2026-10-02-files-workspace/README.md) | Files workspace review | [10 files][2026-10-02-files-workspace] |
| 2026-10-02 | [`2026-10-02-terminal-persistence/`](2026-10-02-terminal-persistence/README.md) | Terminal lifetime verification | [2 files][2026-10-02-terminal-persistence] |
| 2026-10-03 | [`2026-10-03-files-native/`](2026-10-03-files-native/README.md) | Files navigation and native interactions | [4 files][2026-10-03-files-native] |
| 2026-10-03 | [`2026-10-03-workspace-interactions/`](2026-10-03-workspace-interactions/README.md) | Workspace interactions across the dashboard | [7 files][2026-10-03-workspace-interactions] |
| 2026-10-04 | [`2026-10-04-command-search/`](2026-10-04-command-search/README.md) | Command search | [5 files][2026-10-04-command-search] |
| 2026-10-04 | [`2026-10-04-deploy-console-toolbar/`](2026-10-04-deploy-console-toolbar/README.md) | Deployment console toolbar | [3 files][2026-10-04-deploy-console-toolbar] |
| 2026-10-04 | [`2026-10-04-dropdowns/`](2026-10-04-dropdowns/README.md) | Shared dropdown audit | [7 files][2026-10-04-dropdowns] |
| 2026-10-04 | [`2026-10-04-overview-activity/`](2026-10-04-overview-activity/README.md) | Overview activity comparison | [5 files][2026-10-04-overview-activity] |
| 2026-10-04 | [`2026-10-04-terminal-splits/`](2026-10-04-terminal-splits/README.md) | Terminal splits and agent launch proof | [11 files][2026-10-04-terminal-splits] |
| 2026-10-04 | [`2026-10-04-terminal-window-docking/`](2026-10-04-terminal-window-docking/README.md) | Terminal pane detachment and window docking proof | [9 files][2026-10-04-terminal-window-docking] |
| 2026-10-05 | [`2026-10-05-revert-deployment-import/`](2026-10-05-revert-deployment-import/README.md) | Remove existing-workload deployment import | [1 files][2026-10-05-revert-deployment-import] |
| 2026-10-05 | [`2026-10-05-stop-run-page/`](2026-10-05-stop-run-page/README.md) | A Stop's run page | [3 files][2026-10-05-stop-run-page] |
| 2026-10-05 | [`2026-10-05-table-scroll-reveal/`](2026-10-05-table-scroll-reveal/README.md) | Table scroll reveal | [2 files][2026-10-05-table-scroll-reveal] |
| 2026-10-06 | [`2026-10-06-command-palette-overhaul/`](2026-10-06-command-palette-overhaul/README.md) | Command palette overhaul | [11 files][2026-10-06-command-palette-overhaul] |
| 2026-10-07 | [`2026-10-07-network-section/`](2026-10-07-network-section/README.md) | The Network section: plan | [25 files][2026-10-07-network-section] |
| 2026-10-07 | [`2026-10-07-packages-overhaul/`](2026-10-07-packages-overhaul/README.md) | Packages overhaul | [24 files][2026-10-07-packages-overhaul] |
| 2026-10-07 | [`2026-10-07-pm2-overhaul/`](2026-10-07-pm2-overhaul/README.md) | PM2 overhaul and merge verification | [21 files][2026-10-07-pm2-overhaul] |
| 2026-10-07 | [`2026-10-07-process-refresh/`](2026-10-07-process-refresh/README.md) | Process refresh continuity | [3 files][2026-10-07-process-refresh] |
| 2026-10-08 | [`2026-10-08-container-page-overhaul/`](2026-10-08-container-page-overhaul/) | Capture scripts only (`capture.ts`) | [30 files][2026-10-08-container-page-overhaul] |
| 2026-10-08 | [`2026-10-08-docker-images-overhaul/`](2026-10-08-docker-images-overhaul/README.md) | Docker Images overhaul | [25 files][2026-10-08-docker-images-overhaul] |
| 2026-10-08 | [`2026-10-08-docker-networks-overhaul/`](2026-10-08-docker-networks-overhaul/README.md) | Docker Networks overhaul | [15 files][2026-10-08-docker-networks-overhaul] |
| 2026-10-08 | [`2026-10-08-docker-overview/`](2026-10-08-docker-overview/README.md) | Docker overview overhaul | [13 files][2026-10-08-docker-overview] |
| 2026-10-08 | [`2026-10-08-docker-volumes-overhaul/`](2026-10-08-docker-volumes-overhaul/) | Capture scripts only (`capture.ts`) | [26 files][2026-10-08-docker-volumes-overhaul] |
| 2026-10-08 | [`2026-10-08-network-audit/`](2026-10-08-network-audit/README.md) | Networking inventory, fixes and compatibility audit | [4 files][2026-10-08-network-audit] |
| 2026-10-08 | [`2026-10-08-network-capability-report/`](2026-10-08-network-capability-report/README.md) | Networking capability, quality and competitor report | [146 files][2026-10-08-network-capability-report] |
| 2026-10-11 | [`2026-10-11-log-filters-insights/`](2026-10-11-log-filters-insights/README.md) | Log filters, Insights and per-page log sources | [30 files][2026-10-11-log-filters-insights] |
| 2026-10-12 | [`2026-10-12-actionable-findings/`](2026-10-12-actionable-findings/README.md) | Actionable findings across the dashboard | [14 files][2026-10-12-actionable-findings] |
| 2026-10-12 | [`2026-10-12-onboarding/`](2026-10-12-onboarding/README.md) | The first-run question | [26 files][2026-10-12-onboarding] |
| 2026-10-13 | [`2026-10-13-remote-desktop/`](2026-10-13-remote-desktop/) | Capture scripts only (`capture-cards.ts`, `capture.ts`) | [18 files][2026-10-13-remote-desktop] |
| 2026-10-14 | [`2026-10-14-current-non-network-fixes/`](2026-10-14-current-non-network-fixes/README.md) | Current non-network audit repairs | [14 files][2026-10-14-current-non-network-fixes] |
| 2026-10-14 | [`2026-10-14-non-network-fixes/`](2026-10-14-non-network-fixes/README.md) | Non-network audit repairs | — |
| 2026-10-14 | [`2026-10-14-operation-progress/`](2026-10-14-operation-progress/README.md) | Operation progress feedback | [43 files][2026-10-14-operation-progress] |
| 2026-10-15 | [`2026-10-15-api-keys/`](2026-10-15-api-keys/README.md) | API key lifecycle and discovery | [11 files][2026-10-15-api-keys] |
| 2026-11-05 | [`2026-11-05-hub-signin/`](2026-11-05-hub-signin/security-review.md) | Hub sign-in: security review against invariant 2 | — |
| 2026-11-13 | [`2026-11-13-ui-overhaul-map/`](2026-11-13-ui-overhaul-map/README.md) | UI overhaul map: every page against the deploy pages, and what is left | [3 files][2026-11-13-ui-overhaul-map] |

## Records that were pictures only

These folders held nothing but before-and-after screenshots or recordings of one change, so they
exist only on the evidence commit now. The change itself is in the changelog and the git history
under the same name.

| Date | Record | Files |
| --- | --- | --- |
| 2026-10-02 | [`2026-10-02-deploy-credentials`][2026-10-02-deploy-credentials] | 6 |
| 2026-10-02 | [`2026-10-02-deploy-notifications`][2026-10-02-deploy-notifications] | 4 |
| 2026-10-03 | [`2026-10-03-account-menu`][2026-10-03-account-menu] | 1 |
| 2026-10-03 | [`2026-10-03-overview-usefulness`][2026-10-03-overview-usefulness] | 4 |
| 2026-10-04 | [`2026-10-04-build-image-section`][2026-10-04-build-image-section] | 1 |
| 2026-10-05 | [`2026-10-05-git-workspace`][2026-10-05-git-workspace] | 2 |
| 2026-10-05 | [`2026-10-05-runtime-chart-gutter`][2026-10-05-runtime-chart-gutter] | 2 |
| 2026-10-06 | [`2026-10-06-backups-overhaul`][2026-10-06-backups-overhaul] | 2 |
| 2026-10-06 | [`2026-10-06-command-palette`][2026-10-06-command-palette] | 3 |
| 2026-10-06 | [`2026-10-06-configuration-overhaul`][2026-10-06-configuration-overhaul] | 2 |
| 2026-10-06 | [`2026-10-06-deploy-logs`][2026-10-06-deploy-logs] | 3 |
| 2026-10-06 | [`2026-10-06-git-repo-cards`][2026-10-06-git-repo-cards] | 2 |
| 2026-10-06 | [`2026-10-06-logs-overhaul`][2026-10-06-logs-overhaul] | 2 |
| 2026-10-06 | [`2026-10-06-metrics-overhaul`][2026-10-06-metrics-overhaul] | 2 |
| 2026-10-06 | [`2026-10-06-processes-overhaul`][2026-10-06-processes-overhaul] | 6 |
| 2026-10-07 | [`2026-10-07-scheduled-overhaul`][2026-10-07-scheduled-overhaul] | 6 |
| 2026-10-07 | [`2026-10-07-services-overhaul`][2026-10-07-services-overhaul] | 7 |
| 2026-10-08 | [`2026-10-08-containers-overhaul`][2026-10-08-containers-overhaul] | 8 |
| 2026-10-08 | [`2026-10-08-docker-events-overhaul`][2026-10-08-docker-events-overhaul] | 8 |
| 2026-10-08 | [`2026-10-08-stack-detail-overhaul`][2026-10-08-stack-detail-overhaul] | 9 |
| 2026-10-08 | [`2026-10-08-stacks-overhaul`][2026-10-08-stacks-overhaul] | 8 |
| 2026-10-16 | [`2026-10-16-dialog-progress`][2026-10-16-dialog-progress] | 2 |
| 2026-10-17 | [`2026-10-17-database-alerts`][2026-10-17-database-alerts] | 6 |
| 2026-10-17 | [`2026-10-17-database-backup-encryption`][2026-10-17-database-backup-encryption] | 7 |
| 2026-10-17 | [`2026-10-17-database-db-allowlist`][2026-10-17-database-db-allowlist] | 6 |
| 2026-10-17 | [`2026-10-17-database-db-server-tls`][2026-10-17-database-db-server-tls] | 8 |
| 2026-10-17 | [`2026-10-17-database-extension-manager`][2026-10-17-database-extension-manager] | 8 |
| 2026-10-17 | [`2026-10-17-database-index-advice`][2026-10-17-database-index-advice] | 8 |
| 2026-10-17 | [`2026-10-17-database-major-upgrade`][2026-10-17-database-major-upgrade] | 7 |
| 2026-10-17 | [`2026-10-17-database-metrics-export`][2026-10-17-database-metrics-export] | 8 |
| 2026-10-17 | [`2026-10-17-database-pg-stat-statements`][2026-10-17-database-pg-stat-statements] | 9 |
| 2026-10-17 | [`2026-10-17-database-pgbouncer`][2026-10-17-database-pgbouncer] | 8 |
| 2026-10-17 | [`2026-10-17-database-pitr-postgres`][2026-10-17-database-pitr-postgres] | 7 |
| 2026-10-17 | [`2026-10-17-database-provisioning-catalogue`][2026-10-17-database-provisioning-catalogue] | 7 |
| 2026-10-17 | [`2026-10-17-database-query-insights`][2026-10-17-database-query-insights] | 7 |
| 2026-10-17 | [`2026-10-17-database-redis-modules`][2026-10-17-database-redis-modules] | 5 |
| 2026-10-17 | [`2026-10-17-database-report-fixes`][2026-10-17-database-report-fixes] | 4 |
| 2026-10-17 | [`2026-10-17-database-resource-limits`][2026-10-17-database-resource-limits] | 7 |
| 2026-10-17 | [`2026-10-17-database-routine-editor`][2026-10-17-database-routine-editor] | 7 |
| 2026-10-17 | [`2026-10-17-database-script-splitter`][2026-10-17-database-script-splitter] | 7 |
| 2026-10-17 | [`2026-10-17-database-statement-masking`][2026-10-17-database-statement-masking] | 6 |
| 2026-10-18 | [`2026-10-18-database-explain-view`][2026-10-18-database-explain-view] | 6 |
| 2026-10-19 | [`2026-10-19-metrics-interfaces`][2026-10-19-metrics-interfaces] | 4 |
| 2026-10-19 | [`2026-10-19-overview`][2026-10-19-overview] | 2 |
| 2026-10-19 | [`2026-10-19-remote-session`][2026-10-19-remote-session] | 2 |

[2026-09-29-deployment-header]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-09-29-deployment-header
[2026-09-29-deployment-history]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-09-29-deployment-history
[2026-09-29-source-sans-3]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-09-29-source-sans-3
[2026-09-29-terminal-tools]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-09-29-terminal-tools
[2026-10-02-compact-deploy-header]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-02-compact-deploy-header
[2026-10-02-database-creation]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-02-database-creation
[2026-10-02-deploy-credentials]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-02-deploy-credentials
[2026-10-02-deploy-notifications]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-02-deploy-notifications
[2026-10-02-files]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-02-files
[2026-10-02-files-search]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-02-files-search
[2026-10-02-files-workspace]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-02-files-workspace
[2026-10-02-terminal-persistence]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-02-terminal-persistence
[2026-10-03-account-menu]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-03-account-menu
[2026-10-03-files-native]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-03-files-native
[2026-10-03-overview-usefulness]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-03-overview-usefulness
[2026-10-03-workspace-interactions]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-03-workspace-interactions
[2026-10-04-build-image-section]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-04-build-image-section
[2026-10-04-command-search]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-04-command-search
[2026-10-04-deploy-console-toolbar]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-04-deploy-console-toolbar
[2026-10-04-dropdowns]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-04-dropdowns
[2026-10-04-overview-activity]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-04-overview-activity
[2026-10-04-terminal-splits]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-04-terminal-splits
[2026-10-04-terminal-window-docking]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-04-terminal-window-docking
[2026-10-05-git-workspace]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-05-git-workspace
[2026-10-05-revert-deployment-import]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-05-revert-deployment-import
[2026-10-05-runtime-chart-gutter]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-05-runtime-chart-gutter
[2026-10-05-stop-run-page]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-05-stop-run-page
[2026-10-05-table-scroll-reveal]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-05-table-scroll-reveal
[2026-10-06-backups-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-06-backups-overhaul
[2026-10-06-command-palette]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-06-command-palette
[2026-10-06-command-palette-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-06-command-palette-overhaul
[2026-10-06-configuration-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-06-configuration-overhaul
[2026-10-06-deploy-logs]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-06-deploy-logs
[2026-10-06-git-repo-cards]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-06-git-repo-cards
[2026-10-06-logs-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-06-logs-overhaul
[2026-10-06-metrics-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-06-metrics-overhaul
[2026-10-06-processes-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-06-processes-overhaul
[2026-10-07-network-section]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-07-network-section
[2026-10-07-packages-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-07-packages-overhaul
[2026-10-07-pm2-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-07-pm2-overhaul
[2026-10-07-process-refresh]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-07-process-refresh
[2026-10-07-scheduled-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-07-scheduled-overhaul
[2026-10-07-services-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-07-services-overhaul
[2026-10-08-container-page-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-08-container-page-overhaul
[2026-10-08-containers-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-08-containers-overhaul
[2026-10-08-docker-events-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-08-docker-events-overhaul
[2026-10-08-docker-images-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-08-docker-images-overhaul
[2026-10-08-docker-networks-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-08-docker-networks-overhaul
[2026-10-08-docker-overview]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-08-docker-overview
[2026-10-08-docker-volumes-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-08-docker-volumes-overhaul
[2026-10-08-network-audit]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-08-network-audit
[2026-10-08-network-capability-report]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-08-network-capability-report
[2026-10-08-stack-detail-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-08-stack-detail-overhaul
[2026-10-08-stacks-overhaul]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-08-stacks-overhaul
[2026-10-11-log-filters-insights]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-11-log-filters-insights
[2026-10-12-actionable-findings]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-12-actionable-findings
[2026-10-12-onboarding]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-12-onboarding
[2026-10-13-remote-desktop]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-13-remote-desktop
[2026-10-14-current-non-network-fixes]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-14-current-non-network-fixes
[2026-10-14-operation-progress]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-14-operation-progress
[2026-10-15-api-keys]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-15-api-keys
[2026-10-16-dialog-progress]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-16-dialog-progress
[2026-10-17-database-alerts]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-alerts
[2026-10-17-database-backup-encryption]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-backup-encryption
[2026-10-17-database-db-allowlist]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-db-allowlist
[2026-10-17-database-db-server-tls]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-db-server-tls
[2026-10-17-database-extension-manager]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-extension-manager
[2026-10-17-database-index-advice]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-index-advice
[2026-10-17-database-major-upgrade]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-major-upgrade
[2026-10-17-database-metrics-export]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-metrics-export
[2026-10-17-database-pg-stat-statements]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-pg-stat-statements
[2026-10-17-database-pgbouncer]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-pgbouncer
[2026-10-17-database-pitr-postgres]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-pitr-postgres
[2026-10-17-database-provisioning-catalogue]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-provisioning-catalogue
[2026-10-17-database-query-insights]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-query-insights
[2026-10-17-database-redis-modules]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-redis-modules
[2026-10-17-database-report-fixes]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-report-fixes
[2026-10-17-database-resource-limits]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-resource-limits
[2026-10-17-database-routine-editor]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-routine-editor
[2026-10-17-database-script-splitter]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-script-splitter
[2026-10-17-database-statement-masking]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-17-database-statement-masking
[2026-10-18-database-explain-view]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-18-database-explain-view
[2026-10-19-metrics-interfaces]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-19-metrics-interfaces
[2026-10-19-overview]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-19-overview
[2026-10-19-remote-session]: https://github.com/JustDashboard/Just-Dashboard/tree/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-10-19-remote-session
[2026-11-13-ui-overhaul-map]: https://github.com/JustDashboard/Just-Dashboard/tree/0db4e0622e7916e77de2f4efcadb0f4ab0c23811
