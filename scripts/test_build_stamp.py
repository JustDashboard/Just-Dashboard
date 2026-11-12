import os
import pathlib
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("build-stamp.sh").resolve()


class BuildStampTest(unittest.TestCase):
    """jd_build_commit, against checkouts made here.

    git runs with no configuration but the fixture's own: nothing of the
    machine's or the person's is read, and nothing outside the temporary
    directory is written.
    """

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name) / "checkout with spaces"
        self.root.mkdir()
        self.env = {
            "PATH": os.environ["PATH"], "HOME": self.temp.name,
            "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CEILING_DIRECTORIES": self.temp.name,
        }

    def git(self, *args):
        return subprocess.run(
            ["git", "-C", str(self.root), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
             "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "init.defaultBranch=main", *args],
            env=self.env, capture_output=True, text=True, check=True).stdout.strip()

    def checkout(self, version="0.8.5"):
        self.git("init", "--quiet")
        path = self.root / "backend/internal/version"
        path.mkdir(parents=True)
        (path / "version.go").write_text(f'package version\n\nconst Version = "{version}"\n')
        self.git("add", "-A")
        self.git("commit", "--quiet", "-m", "release")

    def commit(self, name):
        (self.root / name).write_text(name)
        self.git("add", "-A")
        self.git("commit", "--quiet", "-m", name)

    def stamp(self, path=None):
        result = subprocess.run(
            ["/bin/bash", "-c", 'set -euo pipefail; source "$1"; jd_build_commit "$2"', "test", str(SCRIPT),
             str(path or self.root)],
            env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        return result.stdout.strip()

    def test_a_commit_that_is_not_the_release_is_named(self):
        self.checkout()
        self.assertEqual(self.stamp(), self.git("rev-parse", "--short", "HEAD"))

    def test_the_release_tag_is_not_stamped(self):
        self.checkout()
        self.git("tag", "v0.8.5")
        self.assertEqual(self.stamp(), "")

    def test_an_annotated_release_tag_is_not_stamped(self):
        self.checkout()
        self.git("tag", "-a", "-m", "0.8.5", "v0.8.5")
        self.assertEqual(self.stamp(), "")

    def test_a_commit_after_the_release_is_stamped(self):
        self.checkout()
        self.git("tag", "v0.8.5")
        self.commit("patch")
        self.assertEqual(self.stamp(), self.git("rev-parse", "--short", "HEAD"))

    def test_a_tag_for_another_version_does_not_make_a_release(self):
        self.checkout()
        self.git("tag", "v0.8.4")
        self.assertEqual(self.stamp(), self.git("rev-parse", "--short", "HEAD"))

    def test_the_ref_an_update_writes_counts_as_the_release(self):
        self.checkout()
        self.git("update-ref", "refs/just-dashboard/release/v0.8.5", "HEAD")
        self.assertEqual(self.stamp(), "")
        self.commit("patch")
        self.assertEqual(self.stamp(), self.git("rev-parse", "--short", "HEAD"))

    def test_a_directory_that_is_not_a_checkout_is_not_stamped(self):
        self.assertEqual(self.stamp(), "")

    def test_a_checkout_with_no_commit_is_not_stamped(self):
        self.git("init", "--quiet")
        self.assertEqual(self.stamp(), "")

    def test_a_host_without_git_is_not_stamped(self):
        self.checkout()
        empty = pathlib.Path(self.temp.name) / "empty"
        empty.mkdir()
        result = subprocess.run(
            ["/bin/bash", "-c", 'set -euo pipefail; source "$1"; jd_build_commit "$2"', "test", str(SCRIPT),
             str(self.root)],
            env={**self.env, "PATH": str(empty)}, capture_output=True, text=True)
        self.assertEqual((result.returncode, result.stdout, result.stderr), (0, "", ""))


if __name__ == "__main__":
    unittest.main()
