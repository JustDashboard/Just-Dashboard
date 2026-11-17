"""What ci-plan.py starts for a change: the Go packages, the browser specs and their jobs, the live fixtures."""
import contextlib
import importlib.util
import io
import json
import pathlib
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("ci-plan.py")
spec = importlib.util.spec_from_file_location("ci_plan", SCRIPT)
ci_plan = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ci_plan)

MODULE = "example.test/dashboard/backend"

# A handler package over two it imports, a package only a test imports, one
# whose test reads the frontend's half of a contract, and a script no build
# includes.
BACKEND = {
    "go.mod": f"module {MODULE}\n",
    "go.sum": "",
    "internal/api/routes.go": f'package api\nimport "{MODULE}/internal/deploy"\nimport "{MODULE}/internal/shell"\n',
    "internal/api/testdata/drivers.json": "{}\n",
    "internal/deploy/engine.go": f'package deploy\nimport "{MODULE}/internal/store"\nimport "{MODULE}/internal/metrics"\n',
    "internal/metrics/metrics.go": "package metrics\n",
    "internal/deploy/engine_test.go": (
        "package deploy\n"
        "func TestListStaysWithinItsBudgetAtReferenceScale(t *testing.T) {}\n"
    ),
    "internal/store/store.go": "package store\n",
    "internal/store/store_test.go": f'package store\nimport "{MODULE}/internal/fixture"\n',
    "internal/fixture/fixture.go": "package fixture\n",
    "internal/version/version.go": "package version\n",
    "internal/version/version_test.go": (
        "package version\n"
        'const path = "../../../frontend/src/lib/version.ts"\n'
        # An attack a test expects to be refused, and a sample repository's
        # layout: neither is a file this package reads.
        'const attack = "../../../etc"\n'
        'var companions = []string{"../api"}\n'
    ),
    "internal/term/term.go": "package term\n",
    "internal/shell/shell.go": "package shell\n",
    "scripts/generate.go": "//go:build ignore\n\npackage main\n",
}

# A deployment page, a files page and a databases page inside the dashboard's
# layout, a card only the deployment page draws, a logo both the deployment
# and the files pages draw, a button the shell draws on every page, and the
# database engines the shell's rail reads as well as the databases page.
FRONTEND = {
    "package.json": "{}\n",
    "eslint.config.mjs": "export default []\n",
    "public/deploy-hero.png": "png\n",
    "public/logos/acme.svg": "<svg/>\n",
    "public/unnamed.svg": "<svg/>\n",
    "src/lib/version.ts": 'export const version = "1"\n',
    "src/lib/format.ts": "export const format = 1\n",
    "src/lib/format.test.ts": 'import { format } from "./format"\n',
    "src/app/globals.css": "",
    "src/app/layout.tsx": 'import "./globals.css"\n',
    "src/app/(dashboard)/layout.tsx": 'import { Shell } from "@/components/shell"\n',
    "src/app/(dashboard)/error.tsx": "export default function Error() {}\n",
    "src/app/(dashboard)/deploy/[id]/page.tsx": 'import { Card } from "@/components/deploy/card"\n',
    "src/app/(dashboard)/files/page.tsx": 'import { List } from "@/components/files"\n',
    "src/app/(dashboard)/databases/page.tsx": 'import { Engines } from "@/components/database/engines"\n',
    "src/components/shell.tsx": (
        'import { Button } from "@/components/ui/button"\nimport { Engines } from "@/components/database/engines"\n'
    ),
    "src/components/ui/button.tsx": "export const Button = 1\n",
    "src/components/product-logo.tsx": 'export const logos = { acme: "acme.svg" }\n',
    "src/components/database/engines.tsx": "export const Engines = 1\n",
    "src/components/deploy/card.tsx": 'import { Badge } from "./badge"\nimport { logos } from "@/components/product-logo"\n',
    "src/components/deploy/badge.tsx": 'export const Badge = "/deploy-hero.png"\n',
    "src/components/files/index.tsx": 'import { logos } from "@/components/product-logo"\nexport const List = 1\n',
    "tests/browser/design-system.spec.ts": 'test("every page", async () => {})\n',
    "tests/browser/navigation.spec.ts": 'test("the rail", async () => {})\n',
    "tests/browser/deploy.spec.ts": (
        'import { logos } from "@/components/product-logo"\n'
        'test("opens", async ({ page }) => { await page.goto(`/deploy/${id}`) })\n'
    ),
    "tests/browser/files.spec.ts": (
        'import { tree } from "./fixtures/files/tree"\n'
        'test("lists", async ({ page }) => { await page.goto("/files?path=/srv") })\n'
    ),
    "tests/browser/fixtures/files/tree.ts": "export const tree = 1\n",
    "tests/browser/database.spec.ts": (
        'import { drivers } from "./database-fixture"\n'
        'test("creates", async ({ page }) => { await page.goto("/databases") })\n'
    ),
    "tests/browser/database-fixture.ts": 'import catalogue from "../../../backend/internal/api/testdata/drivers.json"\n',
}

EVERY_SPEC = ["database", "deploy", "design-system", "files", "navigation"]


class PlanTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = pathlib.Path(directory.name)
        for top, tree in (("backend", BACKEND), ("frontend", FRONTEND)):
            for name, content in tree.items():
                path = self.root / top / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(content)

    def plan(self, *changed, pull_request=False):
        return ci_plan.plan(self.root, None if changed == (None,) else list(changed), pull_request)

    def go(self, *changed):
        out = self.plan(*changed)
        return out["go_packages"].split(), out["go_plain"].split(), json.loads(out["race"])

    def specs(self, *changed, pull_request=False):
        out = self.plan(*changed, pull_request=pull_request)
        jobs = json.loads(out["browser_specs"])
        self.assertEqual(json.loads(out["browser"]), list(range(1, len(jobs))))
        names = sorted(
            name.removeprefix("tests/browser/").removesuffix(".spec.ts") for job in jobs for name in job.split()
        )
        return "all" if names == EVERY_SPEC else names

    def pull_request_specs(self, *changed):
        return self.specs(*changed, pull_request=True)

    def write_specs(self, tests):
        """A spec of each name holding that many tests; their paths, in that order."""
        specs = []
        for name, count in tests.items():
            specs.append(f"frontend/tests/browser/{name}.spec.ts")
            (self.root / specs[-1]).write_text('test("one", async () => {})\n' * count)
        return specs

    def dealt(self, specs, per_shard=ci_plan.TESTS_PER_SHARD):
        return [
            [pathlib.Path(spec).name.removesuffix(".spec.ts") for spec in job]
            for job in ci_plan.shards(self.root, specs, per_shard)
        ]

    def test_a_package_is_checked_with_everything_that_imports_it(self):
        packages, plain, race = self.go("backend/internal/store/store.go")
        self.assertEqual(packages, ["./internal/api", "./internal/deploy", "./internal/store"])
        self.assertEqual(plain, [])
        self.assertEqual(race, ["api", "deploy"])

    def test_a_package_nothing_imports_is_checked_alone_and_plainly(self):
        self.assertEqual(self.go("backend/internal/term/term.go"), (["./internal/term"], ["./internal/term"], []))

    def test_a_test_import_reaches_that_package_and_not_its_importers(self):
        packages, _, _ = self.go("backend/internal/fixture/fixture.go")
        self.assertEqual(packages, ["./internal/fixture", "./internal/store"])

    def test_a_file_beside_no_go_file_belongs_to_the_package_above_it(self):
        packages, _, race = self.go("backend/internal/api/testdata/drivers.json")
        self.assertEqual((packages, race), (["./internal/api"], ["api"]))

    def test_the_module_file_reaches_every_package(self):
        self.assertEqual(self.plan("backend/go.sum")["go_packages"], "./...")

    def test_a_script_no_build_includes_is_not_a_package(self):
        self.assertEqual(self.plan("backend/scripts/generate.go")["backend"], "false")

    def test_a_file_a_test_reads_runs_that_package_plainly_and_nothing_importing_it(self):
        out = self.plan("frontend/src/lib/version.ts")
        self.assertEqual((out["go_packages"], out["go_plain"], out["race"], out["live"]), ("", "./internal/version", "[]", "[]"))

    def test_a_climbing_path_that_names_no_file_outside_the_backend_is_not_a_read(self):
        packages = ci_plan.go_packages(self.root)
        self.assertEqual(packages["backend/internal/version"]["reads"], {"frontend/src/lib/version.ts"})

    def test_the_budgets_run_plainly_for_a_raced_package_that_has_them(self):
        self.assertEqual(self.plan("backend/internal/deploy/engine.go")["go_budgets"], "./internal/deploy")
        self.assertEqual(self.plan("backend/internal/api/routes.go")["go_budgets"], "")

    def test_a_package_outside_the_deployment_six_builds_no_containers(self):
        out = self.plan("backend/internal/shell/shell.go")
        self.assertEqual((out["go_packages"], out["race"], out["live"]), ("./internal/api ./internal/shell", '["api"]', "[]"))

    def test_live_fixtures_follow_the_packages_being_checked(self):
        out = self.plan("backend/internal/api/routes.go")
        self.assertEqual(json.loads(out["live"]), ["fixtures"])
        fixtures = json.loads(out["live_jobs"])["fixtures"]
        self.assertEqual((fixtures["packages"], fixtures["docker"]), ("./internal/api", True))

        out = self.plan("backend/internal/deploy/engine.go")
        names, jobs = json.loads(out["live"]), json.loads(out["live_jobs"])
        self.assertEqual(names, ["fixtures", "frameworks-1", "frameworks-2", "frameworks-3", "frameworks-4"])
        self.assertEqual(jobs["fixtures"]["packages"], "./internal/api ./internal/deploy")
        # The last framework job runs whatever the others do not name.
        self.assertEqual(jobs["frameworks-4"]["run"], "^TestLiveDetectedFrameworkBuildAndServing$")
        for job in names[1:4]:
            for name in jobs[job]["run"].split("/^(")[1].removesuffix(")$").split("|"):
                self.assertIn(name, jobs["frameworks-4"]["skip"])

    def test_a_module_runs_the_specs_naming_the_pages_that_use_it(self):
        self.assertEqual(self.specs("frontend/src/components/deploy/badge.tsx"), ["deploy", "design-system"])
        self.assertEqual(self.specs("frontend/src/components/files/index.tsx"), ["design-system", "files"])

    def test_a_module_the_shell_draws_runs_everything(self):
        self.assertEqual(self.specs("frontend/src/components/ui/button.tsx"), "all")
        self.assertEqual(self.specs("frontend/src/app/globals.css"), "all")

    def test_what_no_import_leads_to_runs_everything(self):
        for name in ["frontend/package.json", "frontend/public/logo.svg", "frontend/src/app/(dashboard)/error.tsx"]:
            with self.subTest(name=name):
                self.assertEqual(self.specs(name), "all")

    def test_a_fixture_runs_the_specs_importing_it(self):
        self.assertEqual(self.specs("frontend/tests/browser/fixtures/files/tree.ts"), ["files"])

    def test_a_backend_file_a_fixture_imports_runs_its_specs_and_the_frontend_job(self):
        self.assertEqual(self.plan("backend/internal/api/testdata/drivers.json")["frontend"], "true")
        self.assertEqual(self.specs("backend/internal/api/testdata/drivers.json"), ["database"])

    def test_a_module_only_a_unit_test_uses_runs_no_browser(self):
        out = self.plan("frontend/src/lib/format.ts")
        self.assertEqual((out["frontend"], out["lint"], out["browser"]), ("true", "src/lib/format.ts", "[]"))

    def test_lint_covers_the_tree_when_its_rules_change(self):
        self.assertEqual(self.plan("frontend/eslint.config.mjs")["lint"], ".")

    def test_documentation_starts_nothing(self):
        out = self.plan("docs/internal/overview.md", "README.md")
        self.assertEqual(
            (out["backend"], out["frontend"], out["race"], out["live"], out["browser"]),
            ("false", "false", "[]", "[]", "[]"),
        )

    def test_the_ui_docs_run_the_unit_tests_that_hold_them_to_the_code(self):
        # `src/docs-catalogue.test.js` reads them, and it is one of the unit
        # tests the frontend job runs. Nothing is linted and no page is opened.
        for pull_request in (True, False):
            for changed in ["docs/internal/frontend/components/README.md", "docs/internal/frontend/foundations/tokens.md"]:
                with self.subTest(changed=changed, pull_request=pull_request):
                    out = self.plan(changed, "README.md", pull_request=pull_request)
                    self.assertEqual(
                        (out["frontend"], out["lint"], out["browser"], out["backend"], out["race"], out["live"]),
                        ("true", "", "[]", "false", "[]", "[]"),
                    )

    def test_no_base_or_a_change_to_the_workflow_runs_everything(self):
        for changed in [(None,), (".github/workflows/verify.yml",), ("scripts/ci-plan.py",)]:
            with self.subTest(changed=changed):
                out = self.plan(*changed)
                self.assertEqual((out["go_packages"], out["lint"], out["frontend"]), ("./...", ".", "true"))
                self.assertEqual(self.specs(*changed), "all")
                self.assertEqual(len(json.loads(out["live"])), 5)

    def test_a_pull_request_picks_a_one_section_module_as_the_merge_does(self):
        for changed in ["frontend/src/components/deploy/badge.tsx", "frontend/tests/browser/fixtures/files/tree.ts"]:
            with self.subTest(changed=changed):
                self.assertEqual(self.pull_request_specs(changed), self.specs(changed))

    def test_a_pull_request_runs_the_shell_specs_for_what_more_than_one_section_draws(self):
        # The merge's run follows each to every page it reaches.
        for changed, merge in [
            ("frontend/src/components/ui/button.tsx", "all"),
            ("frontend/src/components/product-logo.tsx", ["deploy", "design-system", "files"]),
            ("frontend/src/components/shell.tsx", "all"),
            ("frontend/src/app/(dashboard)/layout.tsx", "all"),
            ("frontend/src/app/(dashboard)/error.tsx", "all"),
            ("frontend/src/app/globals.css", "all"),
        ]:
            with self.subTest(changed=changed):
                self.assertEqual(self.pull_request_specs(changed), ["design-system", "navigation"])
                self.assertEqual(self.specs(changed), merge)

    def test_a_pull_request_keeps_the_home_section_of_a_module_the_shell_reads(self):
        self.assertEqual(
            self.pull_request_specs("frontend/src/components/database/engines.tsx"),
            ["database", "design-system", "navigation"],
        )

    def test_a_pull_request_follows_a_public_asset_from_the_modules_naming_it(self):
        self.assertEqual(self.pull_request_specs("frontend/public/deploy-hero.png"), ["deploy", "design-system"])
        self.assertEqual(self.pull_request_specs("frontend/public/logos/acme.svg"), ["design-system", "navigation"])
        self.assertEqual(self.pull_request_specs("frontend/public/unnamed.svg"), ["design-system", "navigation"])
        self.assertEqual(self.specs("frontend/public/deploy-hero.png"), "all")

    def test_a_pull_request_runs_everything_when_the_build_changes(self):
        for changed in ["frontend/package.json", "frontend/bun.lock", "frontend/next.config.ts"]:
            with self.subTest(changed=changed):
                self.assertEqual(self.pull_request_specs(changed), "all")

    def test_a_pull_request_deals_its_specs_into_smaller_jobs(self):
        specs = self.write_specs({name: 100 for name in "abcd"})
        self.assertEqual(len(ci_plan.shards(self.root, specs)), 2)
        self.assertEqual(len(ci_plan.shards(self.root, specs, ci_plan.PULL_REQUEST_TESTS_PER_SHARD)), 4)
        out = self.plan(*specs, pull_request=True)
        self.assertEqual(json.loads(out["browser"]), [1, 2, 3, 4])

    def test_a_pull_request_runs_the_fixtures_of_the_packages_built_on_the_one_that_changed(self):
        # The metrics deploy reads, beside a handler: the merge runs every
        # fixture the change reaches, the pull request only the handler's.
        changed = ("backend/internal/metrics/metrics.go", "backend/internal/api/routes.go")
        self.assertEqual(json.loads(self.plan(*changed)["live"])[:1], ["fixtures"])
        self.assertEqual(len(json.loads(self.plan(*changed)["live"])), 5)
        out = self.plan(*changed, pull_request=True)
        self.assertEqual(json.loads(out["live"]), ["fixtures"])
        self.assertEqual(json.loads(out["live_jobs"])["fixtures"]["packages"], "./internal/api")

        # The store both are built on runs both their fixtures, and still
        # builds no framework: those follow internal/deploy itself.
        out = self.plan("backend/internal/store/store.go", pull_request=True)
        self.assertEqual(json.loads(out["live"]), ["fixtures"])
        self.assertEqual(json.loads(out["live_jobs"])["fixtures"]["packages"], "./internal/api ./internal/deploy")

    def test_a_pull_request_builds_the_frameworks_when_the_deployment_package_changes(self):
        for changed in [("backend/internal/deploy/engine.go",), (None,), (".github/workflows/verify.yml",)]:
            with self.subTest(changed=changed):
                self.assertEqual(len(json.loads(self.plan(*changed, pull_request=True)["live"])), 5)

    def test_specs_are_dealt_into_jobs_by_how_many_tests_they_hold(self):
        # The heaviest first, each to the job holding the fewest so far.
        specs = self.write_specs({"a": 150, "b": 140, "c": 90, "d": 20})
        self.assertEqual(self.dealt(specs), [["a", "d"], ["b", "c"]])
        self.assertEqual(self.dealt(specs[3:]), [["d"]])
        # In name order the two large ones would have met in one job.
        specs = self.write_specs({"a": 20, "b": 90, "y": 140, "z": 150})
        self.assertEqual(self.dealt(specs), [["a", "z"], ["b", "y"]])

    def test_a_section_is_spread_over_the_jobs(self):
        # By weight alone the second database spec goes to the first job,
        # which is as light as the other and already holds the first.
        specs = self.write_specs({"database-a": 100, "deploy-a": 100, "database-b": 60, "deploy-b": 60})
        self.assertEqual(self.dealt(specs, per_shard=160), [["database-a", "deploy-b"], ["database-b", "deploy-a"]])

    def test_a_job_is_never_empty(self):
        # One spec heavier than a job: a job that named no spec would run
        # every spec there is.
        specs = self.write_specs({"a": 250})
        self.assertEqual(self.dealt(specs, per_shard=ci_plan.PULL_REQUEST_TESTS_PER_SHARD), [["a"]])
        out = self.plan(*specs, pull_request=True)
        self.assertEqual((out["browser"], json.loads(out["browser_specs"])), ("[1]", ["", "tests/browser/a.spec.ts"]))
        specs = self.write_specs({"a": 250, "b": 10, "c": 10})
        self.assertEqual(self.dealt(specs, per_shard=ci_plan.PULL_REQUEST_TESTS_PER_SHARD), [["a"], ["b"], ["c"]])

    def test_a_test_written_in_a_loop_counts_for_each_item_of_its_list(self):
        for source, tests in [
            ('test("one", async () => {})\ntest("two", async () => {})\n', 2),
            # A literal, with a trailing comma and without.
            ('for (const path of ["/a", "/b", "/c"]) {\n  test(`${path} opens`, async () => {})\n}\n', 3),
            ('for (const path of [\n  "/a",\n  "/b",\n]) {\n  test(`${path} opens`, async () => {})\n}\n', 2),
            # A constant of the spec, typed, cast, and spread into another.
            ('const PAGES = ["/a", "/b"] as const\nfor (const path of PAGES) test(path, async () => {})\n', 2),
            (
                'const PAGES: readonly string[] = ["/a", "/b"]\nconst MORE = [...PAGES, "/c"]\n'
                'for (const path of MORE) {\n  test(path, async () => {})\n  test(`${path} again`, async () => {})\n}\n',
                6,
            ),
            # Rows of several values, and the keys of an object.
            ('for (const [path, name] of [\n  ["/a", "A"],\n  ["/b", "B"],\n] as const) {\n  test(name, async () => {})\n}\n', 2),
            ('const WIDTHS = { phone: 390, desk: 1440 }\nfor (const [name, width] of Object.entries(WIDTHS)) {\n  test(name, async () => {})\n}\n', 2),
            # A loop in a loop, inside a describe, with a test outside the inner one.
            (
                'const PAGES = ["/a", "/b", "/c"]\ntest.describe("at each width", () => {\n'
                '  for (const width of [390, 1440]) {\n    for (const path of PAGES) {\n'
                '      test(`${path} at ${width}`, async () => {})\n    }\n'
                '    test(`dialogs at ${width}`, async () => {})\n  }\n})\n',
                8,
            ),
            # What keeps every item, what makes a list for each, and a run of a list.
            ('const PAGES = ["/a", "/b"]\nfor (const [path, at] of PAGES.map((path, at) => [path, at])) {\n  test(path, async () => {})\n}\n', 2),
            (
                'for (const [path, width] of (["/a", "/b", "/c"] as const).flatMap((path) =>\n'
                '  [390, 1440].map((width) => [path, width] as const),\n)) {\n  test(`${path} at ${width}`, async () => {})\n}\n',
                6,
            ),
            ('const PAGES = ["/a", "/b", "/c", "/d"]\nfor (const path of PAGES.slice(1, 3)) test(path, async () => {})\n', 2),
            ('const PAGES = ["/a", "/b", "/c", "/d"]\nfor (const path of PAGES.slice(1)) test(path, async () => {})\n', 3),
            ('const PAGES = ["/a", "/b", "/c", "/d"]\nfor (const path of PAGES.slice(2, 9)) test(path, async () => {})\n', 2),
            # A body of one statement that is no test, and tests after it.
            (
                'let pages = 0\nfor (const path of ["/a", "/b"]) pages += 1\n'
                'test("one", async () => {})\ntest("two", async () => {})\n',
                2,
            ),
            # A loop inside a test is the test's own business.
            ('test("every page", async ({ page }) => {\n  for (const path of ["/a", "/b"]) await page.goto(path)\n})\n', 1),
            # What only looks like code: a comment, a string, a template, a pattern.
            (
                '// for (const path of ["/a", "/b"]) {\n/* test("gone", async () => {}) */\n'
                'const said = "test(1) } ] )"\nconst shown = `${said} test(2) { ${`[`} }`\n'
                'const quoted = /["\'`(]test\\(/\nconst half = 4 / 2 / 1\n'
                'for (const path of ["/a", "/b"]) {\n  test(`${path} has "quotes" and a )`, async () => {\n'
                '    expect(quoted.test(path)).toBe(false)\n  })\n}\n',
                2,
            ),
        ]:
            with self.subTest(source=source):
                self.assertEqual(ci_plan.spec_tests(source), (tests, tests))

    def test_a_loop_over_an_imported_constant_is_counted_in_the_module_it_comes_from(self):
        source = 'import { PAGES as ROUTES, other } from "./pages-fixture"\nfor (const path of ROUTES) test(path, async () => {})\n'
        fixture = 'export const PAGES: string[] = [\n  "/a",\n  "/b",\n  "/c",\n]\n'
        self.assertEqual(ci_plan.spec_tests(source, {"./pages-fixture": fixture}.get), (3, 3))
        # Through the files, as the dealing reads them.
        (self.root / "frontend/tests/browser/pages-fixture.ts").write_text(fixture)
        (self.root / "frontend/tests/browser/looped.spec.ts").write_text(source * 50)
        self.assertEqual(len(ci_plan.shards(self.root, ["frontend/tests/browser/looped.spec.ts"] + self.write_specs({"a": 150}))), 2)

    def test_a_loop_whose_list_the_source_does_not_say_counts_once(self):
        for source in [
            'for (const path of PAGES.filter((path) => path !== "/a")) test(path, async () => {})\n',
            'for (const path of await pages()) test(path, async () => {})\n',
            'for (let part = 1; part <= 4; part++) {\n  test(`part ${part}`, async () => {})\n}\n',
            'const PAGES = ["/a", "/b", "/c"]\nfor (const path of PAGES.slice(-2)) test(path, async () => {})\n',
            'import { PAGES } from "@/lib/pages"\nfor (const path of PAGES) test(path, async () => {})\n',
            'const PAGES = [...PAGES]\nfor (const path of PAGES) test(path, async () => {})\n',
        ]:
            with self.subTest(source=source):
                self.assertEqual(ci_plan.spec_tests(source, {}.get), (1, 1))

    def test_a_test_an_unset_variable_switches_off_weighs_nothing(self):
        source = (
            'test("opens", async () => {})\n'
            'test.describe("screenshots", () => {\n'
            '  test.skip(!process.env.JD_SHOTS, "set JD_SHOTS to a directory")\n'
            '  for (const width of [390, 1280, 1720]) {\n    test(`at ${width}`, async () => {})\n  }\n})\n'
            'test("one picture", async ({ page }) => {\n  test.skip(!dir, "set JD_SHOTS")\n  await page.goto("/")\n})\n'
            # Declared and listed, and never run; and one that runs, to fail.
            'test.fixme("a known gap", async () => {})\n'
            'test.fail("a known break", async () => {})\n'
            # Decided by the browser, when it runs.
            'test("on a phone", async ({ isMobile }) => {\n  test.skip(isMobile, "desktop only")\n})\n'
        )
        self.assertEqual(ci_plan.spec_tests(source), (8, 3))
        self.assertEqual(ci_plan.spec_weight(source), 3)
        # A spec that is all screenshots still has a file to load.
        self.assertEqual(ci_plan.spec_weight('test.skip(!process.env.JD_SHOTS, "unset")\ntest("a", async () => {})\n'), 1)

    def test_a_spec_says_its_own_weight_in_a_comment(self):
        looped = 'for (const [name, mock] of ROUTES.filter(open)) {\n  test(name, async () => {})\n}\n'
        for marker, weight in [
            ("", 1),
            ("// ci-plan: 190 tests\n", 190),
            ("// ci-plan: each weighs 2.5\n", 2),
            ("// ci-plan: 190 tests, each weighs 2.5\n", 475),
            ("  //ci-plan: 1 test\n", 1),
            # Said in passing, or said wrong: the count stands.
            ("// see scripts/ci-plan.py: 190 tests\n", 1),
            ("// ci-plan: about 190\n", 1),
        ]:
            with self.subTest(marker=marker):
                self.assertEqual(ci_plan.spec_weight(marker + looped), weight)
        # The marker is what the dealing weighs.
        (self.root / "frontend/tests/browser/gate.spec.ts").write_text("// ci-plan: 190 tests, each weighs 2\n" + looped)
        specs = ["frontend/tests/browser/gate.spec.ts"] + self.write_specs({"a": 190, "b": 190})
        self.assertEqual(self.dealt(specs, per_shard=380), [["gate"], ["a", "b"]])

    def test_a_marker_the_planner_cannot_read_is_said_in_the_log(self):
        (self.root / "frontend/tests/browser/gate.spec.ts").write_text('// ci-plan: about 190\ntest("a", async () => {})\n')
        with contextlib.redirect_stderr(io.StringIO()) as log:
            ci_plan.shards(self.root, ["frontend/tests/browser/gate.spec.ts"] + self.write_specs({"a": 1}))
        self.assertIn("gate.spec.ts", log.getvalue())
        self.assertNotIn("a.spec.ts", log.getvalue())


if __name__ == "__main__":
    unittest.main()
