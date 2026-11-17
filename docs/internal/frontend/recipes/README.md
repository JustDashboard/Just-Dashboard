# Recipes

One recipe per page shape. Pick the shape (the test is in
[`../patterns/registers.md`](../patterns/registers.md)), open its recipe, and build the page in
the order it gives.

Every recipe has the same parts: the reference route and its shot, the regions in order, a
skeleton on the catalogue's components, the four states (loading, empty, unavailable, failed), the
phone, a checklist and the spec to add. The identity line's rules are the same on every shape:
[`../components/page-header.md`](../components/page-header.md#the-identity-lines-rules).

| Recipe | Shape | Reference |
| --- | --- | --- |
| [`reading-page.md`](reading-page.md) | A page that reports (§15) | a project's Overview, `/deploy/[id]` |
| [`detail-with-rail.md`](detail-with-rail.md) | An item's own page with a rail (D9) | a process, `/processes/[pid]` |
| [`settings-page.md`](settings-page.md) | Settings in one column (D8) | `/deploy/[id]/settings/general` |
| [`flow-page.md`](flow-page.md) | A sequence the reader decides (§17) | `/deploy/new` |
| [`workbench.md`](workbench.md) | One frame held to the window | a database's Data, `/terminal` |
| [`list-page.md`](list-page.md) | A list of things you open | the fleet, `/deploy` |
