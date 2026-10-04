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
    "internal/api/routes.go": f'package api\nimport "{MODULE}/internal/deploy"\n',
    "internal/api/testdata/drivers.json": "{}\n",
    "internal/deploy/engine.go": f'package deploy\nimport "{MODULE}/internal/store"\n',
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
    "scripts/generate.go": "//go:build ignore\n\npackage main\n",
}

# A deployment page and a files page inside the dashboard's layout, a card
# only the deployment page draws, and a button the shell draws on every page.
FRONTEND = {
    "package.json": "{}\n",
    "eslint.config.mjs": "export default []\n",
    "src/lib/version.ts": 'export const version = "1"\n',
    "src/lib/format.ts": "export const format = 1\n",
    "src/lib/format.test.ts": 'import { format } from "./format"\n',
    "src/app/globals.css": "",
    "src/app/layout.tsx": 'import "./globals.css"\n',
    "src/app/(dashboard)/layout.tsx": 'import { Shell } from "@/components/shell"\n',
    "src/app/(dashboard)/error.tsx": "export default function Error() {}\n",
    "src/app/(dashboard)/deploy/[id]/page.tsx": 'import { Card } from "@/components/deploy/card"\n',
    "src/app/(dashboard)/files/page.tsx": 'import { List } from "@/components/files"\n',
    "src/components/shell.tsx": 'import { Button } from "@/components/ui/button"\n',
    "src/components/ui/button.tsx": "export const Button = 1\n",
    "src/components/deploy/card.tsx": 'import { Badge } from "./badge"\n',
    "src/components/deploy/badge.tsx": "export const Badge = 1\n",
    "src/components/files/index.tsx": "export const List = 1\n",
    "tests/browser/design-system.spec.ts": 'test("every page", async () => {})\n',
    "tests/browser/deploy.spec.ts": 'test("opens", async ({ page }) => { await page.goto(`/deploy/${id}`) })\n',
    "tests/browser/files.spec.ts": (
        'import { tree } from "./fixtures/files/tree"\n'
        'test("lists", async ({ page }) => { await page.goto("/files?path=/srv") })\n'
    ),
    "tests/browser/fixtures/files/tree.ts": "export const tree = 1\n",
    "tests/browser/database.spec.ts": 'import { drivers } from "./database-fixture"\ntest("creates", async () => {})\n',
    "tests/browser/database-fixture.ts": 'import catalogue from "../../../backend/internal/api/testdata/drivers.json"\n',
}


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

    def plan(self, *changed):
        return ci_plan.plan(self.root, None if changed == (None,) else list(changed))

    def go(self, *changed):
        out = self.plan(*changed)
        return out["go_packages"].split(), out["go_plain"].split(), [job["name"] for job in json.loads(out["race"])]

    def specs(self, *changed):
        jobs = json.loads(self.plan(*changed)["browser"])
        names = sorted(
            name.removeprefix("tests/browser/").removesuffix(".spec.ts") for job in jobs for name in job["specs"].split()
        )
        return "all" if names == ["database", "deploy", "design-system", "files"] else names

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

    def test_live_fixtures_follow_the_packages_being_checked(self):
        live = json.loads(self.plan("backend/internal/api/routes.go")["live"])
        self.assertEqual([job["name"] for job in live], ["fixtures"])
        self.assertEqual((live[0]["packages"], live[0]["docker"]), ("./internal/api", True))

        live = json.loads(self.plan("backend/internal/deploy/engine.go")["live"])
        self.assertEqual(
            [job["name"] for job in live],
            ["fixtures", "frameworks-1", "frameworks-2", "frameworks-3", "frameworks-4"],
        )
        self.assertEqual(live[0]["packages"], "./internal/api ./internal/deploy")
        # The last framework job runs whatever the others do not name.
        self.assertEqual(live[4]["run"], "^TestLiveDetectedFrameworkBuildAndServing$")
        for job in live[1:4]:
            for name in job["run"].split("/^(")[1].removesuffix(")$").split("|"):
                self.assertIn(name, live[4]["skip"])

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
