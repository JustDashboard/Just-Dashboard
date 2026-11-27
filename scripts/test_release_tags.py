import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
HELPER = HERE / "release-tags.sh"
RELEASE = HERE / "release.sh"
MANIFEST = "backend/internal/selfupdate/changelog.json"


class Fixture(unittest.TestCase):
    """A clone of an origin made here, for release-tags.sh and release.sh.

    origin is a bare repository in the temporary directory, so nothing reaches
    the network, and git runs with no configuration but the fixture's own.
    """

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        base = pathlib.Path(self.temp.name)
        self.origin = base / "origin.git"
        self.root = base / "checkout with spaces"
        self.env = {
            "PATH": os.environ["PATH"], "HOME": self.temp.name, "LC_ALL": "C",
            "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CEILING_DIRECTORIES": self.temp.name,
        }
        self.git_at(base, "init", "--quiet", "--bare", str(self.origin))
        self.git_at(base, "clone", "--quiet", str(self.origin), str(self.root))

    def git_at(self, where, *args):
        return subprocess.run(
            ["git", "-C", str(where), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
             "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "init.defaultBranch=main", *args],
            env=self.env, capture_output=True, text=True, check=True).stdout.strip()

    def git(self, *args):
        return self.git_at(self.root, *args)

    def release(self, *versions, branch="main"):
        """Commits a changelog whose releases are the versions given, newest first, and pushes it."""
        path = self.root / MANIFEST
        path.parent.mkdir(parents=True, exist_ok=True)
        manifest = {"latest": versions[0], "releases": [{"version": v, "title": v} for v in versions]}
        path.write_text(json.dumps(manifest, indent=2) + "\n")
        self.git("add", "-A")
        self.git("commit", "--quiet", "-m", f"Release {versions[0]}")
        self.git("push", "--quiet", "origin", f"HEAD:refs/heads/{branch}")

    def tag(self, *versions, annotated=False):
        for v in versions:
            if annotated:
                self.git("tag", "-a", "-m", v, f"v{v}")
            else:
                self.git("tag", f"v{v}")
            self.git("push", "--quiet", "origin", f"refs/tags/v{v}")

    def untagged(self, cutting="9.9", where=None):
        result = subprocess.run(
            ["/bin/bash", "-c", 'set -euo pipefail; source "$1"; jd_untagged_releases "$2" "$3"', "test",
             str(HELPER), str(where or self.root), cutting],
            env=self.env, capture_output=True, text=True)
        return result

    def assertUntagged(self, expected, cutting="9.9", where=None):
        result = self.untagged(cutting, where)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.split(), expected)


class UntaggedReleasesTest(Fixture):
    """jd_untagged_releases: which versions on origin's main have no tag there."""

    def test_every_release_tagged_passes(self):
        self.release("0.9.0", "0.8.6", "0.8.4")
        self.tag("0.9.0", "0.8.6", "0.8.4")
        self.assertUntagged([])

    def test_a_release_on_main_without_its_tag_is_named(self):
        self.release("0.9.1", "0.9.0", "0.8.6")
        self.tag("0.9.0", "0.8.6")
        self.assertUntagged(["0.9.1"], cutting="0.9.2")

    def test_every_untagged_release_is_named(self):
        self.release("0.9.3", "0.9.2", "0.9.1")
        self.tag("0.9.2")
        self.assertUntagged(["0.9.3", "0.9.1"], cutting="0.9.4")

    def test_an_annotated_tag_counts(self):
        self.release("0.9.1", "0.9.0")
        self.tag("0.9.1", "0.9.0", annotated=True)
        self.assertUntagged([])

    def test_a_tag_only_in_the_clone_does_not_count(self):
        self.release("0.9.1", "0.9.0")
        self.tag("0.9.0")
        self.git("tag", "v0.9.1")
        self.assertUntagged(["0.9.1"], cutting="0.9.2")

    def test_the_version_being_cut_needs_no_tag(self):
        # Run again on a branch whose release already reached main, to
        # regenerate CHANGELOG.md, it is the version this run is about.
        self.release("0.9.1", "0.9.0")
        self.tag("0.9.0")
        self.assertUntagged([], cutting="0.9.1")

    def test_a_release_only_on_the_branch_needs_no_tag(self):
        self.release("0.9.0")
        self.tag("0.9.0")
        path = self.root / MANIFEST
        path.write_text(json.dumps({"latest": "0.9.1", "releases": [{"version": "0.9.1"}, {"version": "0.9.0"}]}))
        self.git("commit", "--quiet", "-am", "Release 0.9.1 on its patch branch")
        self.assertUntagged([], cutting="0.9.2")

    def test_releases_before_the_workflow_need_no_tag(self):
        self.release("0.7.2", "0.7.1", "0.7.0", "0.6")
        self.tag("0.7.2")
        self.assertUntagged([])

    def test_the_first_release_the_workflow_tagged_needs_its_tag(self):
        self.release("0.7.3", "0.7.2", "0.7.1")
        self.tag("0.7.3")
        self.assertUntagged(["0.7.2"])

    def test_the_known_untagged_releases_are_left_out(self):
        self.release("0.9.0", "0.8.7", "0.8.6", "0.8.5", "0.8.4")
        self.tag("0.9.0", "0.8.6", "0.8.4")
        self.assertUntagged([])

    def test_each_known_untagged_release_says_why(self):
        result = subprocess.run(
            ["/bin/bash", "-c", 'set -euo pipefail; source "$1"; jd_known_untagged_releases', "test", str(HELPER)],
            env=self.env, capture_output=True, text=True, check=True)
        rows = [line.split("\t") for line in result.stdout.splitlines()]
        self.assertEqual([row[0] for row in rows], ["0.8.5", "0.8.7"])
        for row in rows:
            self.assertEqual(len(row), 2, row)
            self.assertGreater(len(row[1]), 40, row)

    def test_a_main_the_clone_has_not_fetched_is_read_from_origin(self):
        self.release("0.9.0")
        self.tag("0.9.0")
        other = pathlib.Path(self.temp.name) / "other"
        self.git_at(self.temp.name, "clone", "--quiet", str(self.origin), str(other))
        self.release("0.9.1", "0.9.0")
        self.assertUntagged(["0.9.1"], cutting="0.9.2", where=other)

    def test_an_origin_that_cannot_be_reached_fails_and_says_so(self):
        self.release("0.9.0")
        shutil.rmtree(self.origin)
        result = self.untagged()
        self.assertEqual(result.returncode, 2)
        self.assertEqual(result.stdout, "")
        self.assertIn("could not ask origin which releases are tagged", result.stderr)

    def test_a_checkout_with_no_origin_fails_and_says_so(self):
        self.git("remote", "remove", "origin")
        result = self.untagged()
        self.assertEqual(result.returncode, 2)
        self.assertEqual(result.stdout, "")
        self.assertIn("could not ask origin", result.stderr)

    def test_an_origin_without_main_fails_and_says_so(self):
        self.release("0.9.0", branch="patch/0.9.0")
        result = self.untagged()
        self.assertEqual(result.returncode, 2)
        self.assertEqual(result.stdout, "")
        self.assertIn("origin has no branch main", result.stderr)


class ReleaseRefusesTest(Fixture):
    """release.sh itself, copied into the fixture so it runs on the fixture's tree.

    `go` is a stand-in that only records it was called: the check comes before
    the changelog step, so a refusal must leave the tree as it found it.
    """

    def setUp(self):
        super().setUp()
        (self.root / "scripts").mkdir()
        shutil.copy(RELEASE, self.root / "scripts" / "release.sh")
        shutil.copy(HELPER, self.root / "scripts" / "release-tags.sh")
        version = self.root / "backend/internal/version/version.go"
        version.parent.mkdir(parents=True)
        version.write_text('package version\n\nconst Version = "0.9.0"\n')
        bin_dir = pathlib.Path(self.temp.name) / "bin"
        bin_dir.mkdir()
        self.go_calls = pathlib.Path(self.temp.name) / "go-calls"
        go = bin_dir / "go"
        go.write_text(f'#!/bin/sh\necho "$*" >> "{self.go_calls}"\nexit 1\n')
        go.chmod(0o755)
        self.env["PATH"] = f"{bin_dir}{os.pathsep}{self.env['PATH']}"

    def run_release(self, version):
        return subprocess.run(
            [str(self.root / "scripts" / "release.sh"), version], env=self.env, capture_output=True, text=True)

    def test_a_release_is_refused_while_one_on_main_is_untagged(self):
        self.release("0.9.0", "0.8.6")
        self.tag("0.8.6")
        result = self.run_release("0.9.1")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("never published: 0.9.0", result.stderr)
        self.assertIn("once 0.9.1 reaches main", result.stderr)
        self.assertIn("jd_known_untagged_releases", result.stderr)
        self.assertFalse(self.go_calls.exists(), "release.sh went on past the check")
        self.assertEqual(self.git("status", "--porcelain"), "")

    def test_a_release_with_every_earlier_one_tagged_goes_on(self):
        self.release("0.9.0", "0.8.7", "0.8.6", "0.8.5")
        self.tag("0.9.0", "0.8.6")
        result = self.run_release("0.9.1")
        self.assertIn("every release on main is tagged but those known not to be: 0.8.5 0.8.7", result.stdout)
        # The stand-in go fails the changelog step, which is the next one.
        self.assertEqual(result.returncode, 1)
        self.assertIn("the changelog does not describe 0.9.1", result.stderr)
        self.assertIn("gen-changelog.go -expect 0.9.1", self.go_calls.read_text())

    def test_a_release_is_refused_when_origin_cannot_be_asked(self):
        self.release("0.9.0")
        shutil.rmtree(self.origin)
        result = self.run_release("0.9.1")
        self.assertEqual(result.returncode, 1)
        self.assertIn("could not check that every release on main is tagged", result.stderr)
        self.assertFalse(self.go_calls.exists(), "release.sh went on past the check")



if __name__ == "__main__":
    unittest.main()
