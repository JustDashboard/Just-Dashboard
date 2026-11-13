"""What deps-check.sh makes of what the tools report."""
import os
import pathlib
import stat
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("deps-check.sh").resolve()

ADVISORY = (
    "=== Symbol Results ===\n\n"
    "Vulnerability #1: {id}\n"
    "    Something in a module\n"
    "    Found in: example.com/module@v1.0.0\n"
    "    Fixed in: {fixed}\n\n"
)


class DependencyCheckTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.bin = pathlib.Path(directory.name)

    def tool(self, name, body):
        path = self.bin / name
        path.write_text("#!/bin/sh\n" + body)
        path.chmod(path.stat().st_mode | stat.S_IXUSR)

    def check(self, govulncheck, status=3, audit="exit 0"):
        """Run the script with a go that reports this and a bun that audits so."""
        (self.bin / "report").write_text(govulncheck)
        self.tool(
            "go",
            'case "$1" in\n'
            f'run) cat "{self.bin}/report"; exit {status} ;;\n'
            "list) echo example.com/module v1.0.0 '->' v1.1.0 ;;\n"
            "esac\n",
        )
        self.tool("bun", f'case "$1" in audit) {audit} ;; esac\n')
        env = dict(os.environ, PATH=f"{self.bin}:/usr/bin:/bin")
        return subprocess.run(["bash", str(SCRIPT)], env=env, capture_output=True, text=True)

    def test_an_advisory_with_no_fix_that_is_listed_passes(self):
        result = self.check(
            ADVISORY.format(id="GO-2026-4887", fixed="N/A") + ADVISORY.format(id="GO-2026-4883", fixed="N/A")
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("example.com/module v1.0.0 -> v1.1.0", result.stdout)

    def test_an_advisory_that_is_not_listed_fails_and_is_named(self):
        result = self.check(
            ADVISORY.format(id="GO-2026-4887", fixed="N/A")
            + ADVISORY.format(id="GO-2099-0001", fixed="example.com/module@v1.0.1")
        )
        self.assertEqual(result.returncode, 1)
        self.assertIn("not accepted in scripts/deps-check.sh: GO-2099-0001", result.stderr)
        self.assertNotIn("GO-2026-4887", result.stderr)

    def test_a_clean_report_passes(self):
        result = self.check("No vulnerabilities found.\n", status=0)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_a_scan_that_could_not_run_is_not_a_clean_one(self):
        result = self.check("go: golang.org/x/vuln: dial tcp: no route to host\n", status=1)
        self.assertEqual(result.returncode, 1)
        self.assertIn("govulncheck did not run", result.stderr)

    def test_an_advisory_in_a_frontend_package_fails(self):
        result = self.check("No vulnerabilities found.\n", status=0, audit="echo 1 vulnerability; exit 1")
        self.assertEqual(result.returncode, 1)
        self.assertIn("1 vulnerability", result.stdout)


if __name__ == "__main__":
    unittest.main()
