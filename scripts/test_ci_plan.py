"""What ci-plan.py starts for a change: the Go packages, the browser specs, the live fixtures."""
import importlib.util
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
        for name in ("a", "b"):
            (self.root / f"frontend/tests/browser/{name}.spec.ts").write_text('test("one", async () => {})\n' * 200)
        specs = ["frontend/tests/browser/a.spec.ts", "frontend/tests/browser/b.spec.ts"]
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
        # In name order, each to the job holding the fewest so far.
        tests = {"a": 150, "b": 140, "c": 90, "d": 20}
        for name, count in tests.items():
            (self.root / f"frontend/tests/browser/{name}.spec.ts").write_text('test("one", async () => {})\n' * count)
        jobs = ci_plan.shards(self.root, [f"frontend/tests/browser/{name}.spec.ts" for name in tests])
        self.assertEqual(
            [[pathlib.Path(spec).name.removesuffix(".spec.ts") for spec in job] for job in jobs], [["a", "d"], ["b", "c"]]
        )
        self.assertEqual(len(ci_plan.shards(self.root, ["frontend/tests/browser/d.spec.ts"])), 1)
        (self.root / "frontend/tests/browser/a.spec.ts").write_text('test("one", async () => {})\n' * 5000)
        self.assertEqual(len(ci_plan.shards(self.root, ["frontend/tests/browser/a.spec.ts"])), ci_plan.MOST_SHARDS)


if __name__ == "__main__":
    unittest.main()
