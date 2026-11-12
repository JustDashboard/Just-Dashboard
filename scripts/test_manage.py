import json
import os
import pathlib
import pty
import select
import shutil
import subprocess
import tempfile
import time
import unittest


REPO = pathlib.Path(__file__).resolve().parent.parent


class TerminalToolsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name) / "checkout with spaces"
        self.scripts = self.root / "scripts"
        self.scripts.mkdir(parents=True)
        for name in ("manage.sh", "create-user.sh", "reset-password.sh", "dotenv.sh", "password.sh",
                     "build-stamp.sh", "preflight.sh"):
            shutil.copy2(REPO / "scripts" / name, self.scripts / name)
        self.bin = pathlib.Path(self.temp.name) / "bin"
        self.bin.mkdir()
        self.calls = pathlib.Path(self.temp.name) / "calls.jsonl"
        self.env = {**os.environ, "PATH": f"{self.bin}:{os.environ['PATH']}",
                    "TEST_CALLS": str(self.calls), "NO_COLOR": "1"}
        self.tool("id", '#!/bin/bash\necho "${TEST_UID:-0}"\n')
        self.tool("docker", '''#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
if args == ["compose", "version"]:
    sys.exit(int(os.environ.get("TEST_COMPOSE_FAIL", "0")))
if args == ["--version"]:
    print("Docker version 29.8.1")
    sys.exit(0)
entry = {"args": args, "cwd": os.getcwd()}
admin_link = "--admin" in args and any(x in args for x in ["link", "unlink", "link-status", "hub-connect", "hub-disconnect", "hub-status"])
if "--admin" in args and any(x in args for x in ["create-user", "reset-password"]) or admin_link:
    entry["stdin"] = sys.stdin.read()
with pathlib.Path(os.environ["TEST_CALLS"]).open("a") as calls:
    calls.write(json.dumps(entry) + "\\n")
if admin_link:
    if os.environ.get("TEST_LINK_OUTPUT"):
        print(os.environ["TEST_LINK_OUTPUT"])
    sys.exit(int(os.environ.get("TEST_ADMIN_EXIT", "0")))
if args[:2] == ["compose", "build"]:
    sys.exit(int(os.environ.get("TEST_BUILD_FAIL", "0")))
if args[:1] == ["ps"] and os.environ.get("TEST_UPDATER_RUNNING") == "1":
    print("0123456789ab")
if args[:2] == ["image", "inspect"] and args[-1].endswith(":previous"):
    if os.environ.get("TEST_NO_PREVIOUS", "0") == "0" and "--format" in args:
        print(os.environ.get("TEST_PREVIOUS_ID", "sha256:kept"))
    sys.exit(int(os.environ.get("TEST_NO_PREVIOUS", "0")))
if args[:4] == ["run", "--rm", "--entrypoint", "/usr/local/bin/jd"]:
    print(os.environ.get("TEST_JD_SAYS", ""))
if args[:4] == ["run", "--rm", "--entrypoint", "cat"]:
    sys.stdout.write(os.environ.get("TEST_JD_BINARY", ""))
''')
        # Sourcing this as shell code would execute the marker command.
        (self.root / ".env").write_text('JD_MASTER_KEY=fixture\nUNTRUSTED=$(touch sourced-env)\n')

    def tool(self, name, script):
        path = self.bin / name
        path.write_text(script)
        path.chmod(0o755)

    def run_tool(self, name, *args, input="", env=None):
        return subprocess.run([str(self.scripts / name), *args], input=input,
                              capture_output=True, text=True, cwd=self.temp.name,
                              env={**self.env, **(env or {})})

    def read_calls(self):
        return [json.loads(line) for line in self.calls.read_text().splitlines()] if self.calls.exists() else []

    def test_password_wrappers_preserve_stdin_without_secrets_in_argv(self):
        password = "Temporary password $'`\\42!"
        for script, args, command in (
                ("create-user.sh", ("Alice", "limited"), "create-user"),
                ("reset-password.sh", ("admin",), "reset-password")):
            result = self.run_tool(script, *args, "--password-stdin", input=password + "\n")
            self.assertEqual(result.returncode, 0, result.stderr)
            call = self.read_calls()[-1]
            self.assertEqual(call["args"], ["compose", "run", "--rm", "--no-deps", "-T", "backend", "--admin", command, *args])
            self.assertEqual(call["stdin"], password + "\n")
            self.assertEqual(call["cwd"], str(self.root))
            self.assertNotIn(password, result.stdout + result.stderr + " ".join(call["args"]))
            self.assertFalse((self.root / "sourced-env").exists())

    def test_stack_commands_use_current_environment_and_named_services(self):
        for args, expected in (
                (("status",), ["ps", "--all"]),
                (("logs",), ["logs", "--tail", "100", "--follow", "backend"]),
                (("logs", "proxy"), ["logs", "--tail", "100", "--follow", "proxy"]),
                (("restart",), ["up", "-d", "--force-recreate"])):
            result = self.run_tool("manage.sh", *args)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(self.read_calls()[-1]["args"], ["compose", *expected])

    def test_help_needs_no_root_configuration_or_docker(self):
        (self.root / ".env").unlink()
        for script in ("manage.sh", "create-user.sh", "reset-password.sh"):
            result = self.run_tool(script, "--help", env={"TEST_UID": "1000"})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("Usage:", result.stdout)
        self.assertEqual(self.read_calls(), [])

    def test_bad_inputs_and_non_root_do_not_reach_docker(self):
        for args in (("create-user",), ("create-user", "user", "owner"),
                     ("reset-password", "user", "extra"), ("logs", "database"),
                     ("status", "extra"), ("unknown",), ("revoke-sessions",)):
            result = self.run_tool("manage.sh", *args)
            self.assertNotEqual(result.returncode, 0)
        result = self.run_tool("manage.sh", "users", env={"TEST_UID": "1000"})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sudo", result.stderr)
        result = self.run_tool("reset-password.sh", "admin", input="secret\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--password-stdin", result.stderr)
        self.assertEqual(self.read_calls(), [])

    def test_legacy_compose_fallback(self):
        self.tool("docker-compose", '#!/bin/bash\nexec docker compose "$@"\n')
        result = self.run_tool("manage.sh", "users", env={"TEST_COMPOSE_FAIL": "1"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.read_calls()[-1]["args"][-2:], ["--admin", "users"])

    def test_interactive_password_is_hidden_and_confirmed(self):
        master, slave = pty.openpty()
        process = subprocess.Popen([str(self.scripts / "reset-password.sh"), "admin"],
                                   stdin=slave, stdout=slave, stderr=slave, env=self.env)
        os.close(slave)
        output = b""
        answers = 0
        deadline = time.monotonic() + 5
        try:
            while time.monotonic() < deadline:
                ready, _, _ = select.select([master], [], [], 0.1)
                if ready:
                    try:
                        output += os.read(master, 4096)
                    except OSError:
                        break
                if answers == 0 and b"Password: " in output:
                    os.write(master, b"Hidden-Password-42\n")
                    answers = 1
                if answers == 1 and b"Confirm password: " in output:
                    os.write(master, b"Hidden-Password-42\n")
                    answers = 2
            self.assertEqual(process.wait(timeout=1), 0, output.decode())
            self.assertEqual(answers, 2)
            self.assertNotIn(b"Hidden-Password-42", output)
            self.assertEqual(self.read_calls()[-1]["stdin"], "Hidden-Password-42\n")
        finally:
            os.close(master)
            if process.poll() is None:
                process.kill()
                process.wait()

    def update_fixture(self, signer=None, sign=None):
        """The checkout on release 0.1.0 of an origin that has tagged 0.2.0 and moved on.

        Returns git for the checkout, the environment to run manage.sh in and
        the three commits. signer is an allowed-signers line committed into
        the first release; sign is the private key the 0.2.0 tag is signed
        with, "" for an annotated tag with no signature.
        """
        git, env = self.fixture_git()
        origin = pathlib.Path(self.temp.name) / "origin"
        origin.mkdir()

        def upstream(*args):
            return subprocess.run(
                ["git", "-C", str(origin), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                 "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "init.defaultBranch=main", *args],
                env={**self.env, **env}, capture_output=True, text=True, check=True).stdout.strip()

        upstream("init", "--quiet")
        commits = []
        for number, tag in enumerate(("v0.1.0", "v0.2.0", "")):
            (origin / "release").write_text(str(number))
            if signer and number == 0:
                keys = origin / "backend/internal/selfupdate"
                keys.mkdir(parents=True)
                (keys / "allowed_signers").write_text("# release keys\n" + signer + "\n")
            upstream("add", "-A")
            upstream("commit", "--quiet", "-m", f"commit {number}")
            commits.append(upstream("rev-parse", "HEAD"))
            if tag == "v0.2.0" and sign is not None:
                signing = ["-c", "gpg.format=ssh", "-c", f"user.signingkey={sign}", "tag", "-s"] if sign else ["tag", "-a"]
                upstream(*signing, "-m", "0.2.0", tag)
            elif tag:
                upstream("tag", tag)
        git("init", "--quiet")
        git("remote", "add", "origin", str(origin))
        git("fetch", "--quiet", "--no-tags", "origin")
        git("checkout", "--quiet", "-B", "main", commits[0])
        self.data = pathlib.Path(self.temp.name) / "data"
        self.data.mkdir()
        # manage.sh runs git as the checkout's owner; here that is this user.
        self.tool("runuser", '#!/bin/bash\n[ "$1" = -u ] && shift 2\n[ "$1" = -- ] && shift\nexec "$@"\n')
        self.tool("getent", f'#!/bin/bash\necho "fixture:x:1:1::{self.temp.name}:/bin/sh"\n')
        return git, {**env, "JD_HOST_DATA_DIR": str(self.data)}, commits

    def docker_calls(self):
        return [call["args"] for call in self.read_calls()]

    def test_update_installs_the_newest_release_tag_and_keeps_what_ran(self):
        git, env, (first, second, _head) = self.update_fixture()
        result = self.run_tool("manage.sh", "update", "--yes", env=env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        # The tag, not the branch head past it.
        self.assertEqual(git("rev-parse", "HEAD"), second)
        calls = self.docker_calls()
        kept = [["image", "tag", f"just-dashboard-{name}:latest", f"just-dashboard-{name}:previous"]
                for name in ("backend", "frontend")]
        build = next(i for i, call in enumerate(calls) if call[:2] == ["compose", "build"])
        up = calls.index(["compose", "up", "-d", "--remove-orphans"])
        self.assertLess(calls.index(kept[0]), build)
        self.assertLess(calls.index(kept[1]), build)
        self.assertLess(build, up)
        self.assertIn(f"go back with: sudo ./scripts/manage.sh rollback {first[:12]}", result.stdout)
        self.assertIn("The database is not copied first", result.stdout)
        self.assertEqual((self.data / "manage-update.last").read_text(), f"{first} {second} sha256:kept,sha256:kept\n")

    def test_update_changes_nothing_without_a_yes(self):
        git, env, (first, _second, _head) = self.update_fixture()
        result = self.run_tool("manage.sh", "update", env=env)
        self.assertEqual(result.returncode, 1)
        self.assertIn("append --yes", result.stderr)
        self.assertIn("fast-forward the checkout from", result.stdout)
        self.assertEqual(git("rev-parse", "HEAD"), first)
        self.assertFalse([call for call in self.docker_calls() if call[:2] in (["image", "tag"], ["compose", "build"], ["compose", "up"])])

    def test_update_refuses_while_the_dashboards_own_update_runs(self):
        git, env, (first, _second, _head) = self.update_fixture()
        result = self.run_tool("manage.sh", "update", "--yes", env={**env, "TEST_UPDATER_RUNNING": "1"})
        self.assertEqual(result.returncode, 1)
        self.assertIn("docker logs -f just-dashboard-updater", result.stderr)
        self.assertEqual(git("rev-parse", "HEAD"), first)

    def test_update_on_the_newest_release_does_nothing(self):
        git, env, (first, second, _head) = self.update_fixture()
        git("merge", "--quiet", "--ff-only", second)
        result = self.run_tool("manage.sh", "update", "--yes", env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("already on v0.2.0", result.stdout)
        self.assertFalse([call for call in self.docker_calls() if call[:2] in (["image", "tag"], ["compose", "build"])])
        # A version that has no tag is not installed from anywhere else.
        result = self.run_tool("manage.sh", "update", "0.3", "--yes", env=env)
        self.assertEqual(result.returncode, 1)
        self.assertIn("There is no release tag v0.3", result.stderr)

    def test_update_that_cannot_fast_forward_changes_nothing(self):
        git, env, _commits = self.update_fixture()
        git("commit", "--quiet", "--allow-empty", "-m", "a commit of the operator's own")
        mine = git("rev-parse", "HEAD")
        result = self.run_tool("manage.sh", "update", "--yes", env=env)
        self.assertEqual(result.returncode, 1)
        self.assertIn("cannot be fast-forwarded", result.stderr)
        self.assertEqual(git("rev-parse", "HEAD"), mine)
        self.assertFalse([call for call in self.docker_calls() if call[:2] == ["image", "tag"]])

    def test_failed_update_build_names_the_way_back_and_rollback_takes_it(self):
        git, env, (first, second, _head) = self.update_fixture()
        result = self.run_tool("manage.sh", "update", "--yes", env={**env, "TEST_BUILD_FAIL": "1"})
        self.assertEqual(result.returncode, 1)
        self.assertIn(f"sudo ./scripts/manage.sh rollback {first[:12]}", result.stderr)
        self.assertNotIn(["compose", "up", "-d", "--remove-orphans"], self.docker_calls())
        self.assertEqual(git("rev-parse", "HEAD"), second)

        self.calls.unlink()
        result = self.run_tool("manage.sh", "rollback", "--yes", env=env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(git("rev-parse", "HEAD"), first)
        calls = self.docker_calls()
        for name in ("backend", "frontend"):
            self.assertIn(["image", "tag", f"just-dashboard-{name}:previous", f"just-dashboard-{name}:latest"], calls)
        self.assertEqual(calls[-1], ["compose", "up", "-d", "--no-build", "--remove-orphans"])
        self.assertFalse([call for call in calls if call[:2] == ["compose", "build"]])

    def write_record(self, **fields):
        record = {"id": "20261110T053000Z", "status": "failed", "phase": "build", "fromVersion": "0.1.0",
                  "toVersion": "0.2.0", "savedImages": [{"current": "x:latest", "previous": "x:previous"}], **fields}
        (self.data / "self-update.json").write_text(json.dumps(record, indent=2))

    def test_rollback_reads_the_commit_the_dashboards_update_started_from(self):
        git, env, (first, second, head) = self.update_fixture()
        git("merge", "--quiet", "--ff-only", second)
        (self.root / "docker-compose.override.yml").write_text("# the operator's own\n")
        self.write_record(fromCommit=first, toCommit=second, databaseCopy="/var/lib/just-dashboard/pre-update/vpsd-0.1.0-x.db")
        result = self.run_tool("manage.sh", "rollback", "--yes", env=env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(git("rev-parse", "HEAD"), first)
        self.assertTrue((self.root / "docker-compose.override.yml").exists())
        self.assertIn("pre-update/vpsd-0.1.0-x.db", result.stdout)

        # A checkout that is no longer where that update left it is not moved on a guess.
        git("merge", "--quiet", "--ff-only", head)
        result = self.run_tool("manage.sh", "rollback", "--yes", env=env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("leave the checkout where it is", result.stdout)
        self.assertEqual(git("rev-parse", "HEAD"), head)
        # Named, it is.
        result = self.run_tool("manage.sh", "rollback", first[:10], "--yes", env=env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(git("rev-parse", "HEAD"), first)

    # Both records say which commit the :previous images ran at, and each is
    # true only until something keeps the images again. A stale one moved the
    # checkout two versions back under images one version back.
    def test_rollback_drops_its_own_record_once_the_images_were_kept_again(self):
        git, env, (_first, second, head) = self.update_fixture()
        result = self.run_tool("manage.sh", "update", "--yes", env=env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        # A later update from the dashboard kept the images again, failed and
        # went back by itself: the checkout is where this script left it, and
        # :previous is now the version that is running.
        self.write_record(fromCommit=second, toCommit=head)
        result = self.run_tool("manage.sh", "rollback", "--yes", env={**env, "TEST_PREVIOUS_ID": "sha256:kept-again"})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("leave the checkout where it is", result.stdout)
        self.assertEqual(git("rev-parse", "HEAD"), second)

    def test_rollback_drops_the_dashboards_record_once_this_script_kept_the_images_after_it(self):
        git, env, (first, second, head) = self.update_fixture()
        git("merge", "--quiet", "--ff-only", second)
        self.write_record(fromCommit=first, toCommit=second)
        # This script then updated past it and was rolled back: the images kept
        # are those of `second`, which is where the checkout is again.
        last = self.data / "manage-update.last"
        last.write_text(f"{second} {head} sha256:kept,sha256:kept\n")
        later = (self.data / "self-update.json").stat().st_mtime + 60
        os.utime(last, (later, later))
        result = self.run_tool("manage.sh", "rollback", "--yes", env=env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("leave the checkout where it is", result.stdout)
        self.assertEqual(git("rev-parse", "HEAD"), second)

    def test_update_records_what_it_kept_before_the_checkout_moves(self):
        git, env, (first, second, _head) = self.update_fixture()
        (self.root / "release").write_text("an edit of the operator's own, in the release's way\n")
        result = self.run_tool("manage.sh", "update", "--yes", env=env)
        self.assertEqual(result.returncode, 1)
        self.assertIn("would not fast-forward", result.stderr)
        self.assertEqual(git("rev-parse", "HEAD"), first)
        # The images were kept before git refused, so the record of that is there.
        self.assertEqual((self.data / "manage-update.last").read_text(), f"{first} {second} sha256:kept,sha256:kept\n")

    def test_rollback_with_nothing_kept_changes_nothing(self):
        git, env, (first, second, _head) = self.update_fixture()
        git("merge", "--quiet", "--ff-only", second)
        self.write_record(fromCommit=first, toCommit=second)
        result = self.run_tool("manage.sh", "rollback", "--yes", env={**env, "TEST_NO_PREVIOUS": "1"})
        self.assertEqual(result.returncode, 1)
        self.assertIn("No :previous image is kept", result.stderr)
        self.assertEqual(git("rev-parse", "HEAD"), second)
        self.assertFalse([call for call in self.docker_calls() if call[:2] in (["image", "tag"], ["compose", "up"])])
        for bad in (("rollback", "v0.1.0"), ("rollback", "a", "b"), ("update", "main")):
            self.assertEqual(self.run_tool("manage.sh", *bad, env=env).returncode, 1)

    def test_version_says_what_is_checked_out_built_and_last_updated(self):
        git, env, (first, _second, _head) = self.update_fixture()
        version_dir = self.root / "backend/internal/version"
        version_dir.mkdir(parents=True)
        (version_dir / "version.go").write_text('package version\n\nconst Version = "0.1.0"\n')
        self.write_record(status="success", fromCommit=first, toCommit=first)
        result = self.run_tool("manage.sh", "version", env={**env, "TEST_JD_SAYS": "jd 0.1.0+abc1234"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Just Dashboard 0.1.0\n", result.stdout)
        self.assertIn(f"at {git('rev-parse', '--short', 'HEAD')} on main (not the 0.1.0 release)", result.stdout)
        self.assertIn("just-dashboard-backend:latest calls itself jd 0.1.0+abc1234", result.stdout)
        self.assertIn("just-dashboard-backend:previous calls itself jd 0.1.0+abc1234", result.stdout)
        self.assertIn("Last update  0.1.0 to 0.2.0, success", result.stdout)
        self.assertIn(str(self.data / "self-update.log"), result.stdout)
        # On the release's own commit it says so, and it changes nothing either way.
        git("fetch", "--quiet", "--tags", "origin")
        result = self.run_tool("manage.sh", "version", env=env)
        self.assertIn("(the 0.1.0 release)", result.stdout)
        self.assertFalse([call for call in self.docker_calls() if call[:2] in (["image", "tag"], ["compose", "up"])])

    @unittest.skipUnless(shutil.which("ssh-keygen"), "ssh-keygen signs the fixture's tags")
    def test_update_installs_only_a_signed_tag_once_a_release_key_is_committed(self):
        keys = pathlib.Path(self.temp.name) / "keys"
        keys.mkdir()
        for name in ("release", "stranger"):
            subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", name, "-f", str(keys / name)], check=True)
        public = (keys / "release.pub").read_text().split()
        signer = f'release@just-dashboard namespaces="git" {public[0]} {public[1]}'
        for sign, verdict in ((None, "is a lightweight tag"), ("", "is not signed by a Just Dashboard release key"),
                              (str(keys / "stranger"), "is not signed by a Just Dashboard release key"),
                              (str(keys / "release"), "")):
            with self.subTest(sign=sign):
                for stale in ("origin", "data"):
                    shutil.rmtree(pathlib.Path(self.temp.name) / stale, ignore_errors=True)
                shutil.rmtree(self.root / ".git", ignore_errors=True)
                for stale in ("release", "backend"):
                    target = self.root / stale
                    shutil.rmtree(target, ignore_errors=True) if target.is_dir() else target.unlink(missing_ok=True)
                git, env, (first, second, _head) = self.update_fixture(signer=signer, sign=sign)
                result = self.run_tool("manage.sh", "update", "--yes", env=env)
                if verdict:
                    self.assertEqual(result.returncode, 1, result.stdout)
                    self.assertIn(verdict, result.stderr)
                    self.assertEqual(git("rev-parse", "HEAD"), first)
                    self.assertFalse([call for call in self.docker_calls() if call[:2] == ["image", "tag"]])
                else:
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual(git("rev-parse", "HEAD"), second)

    def run_installer(self, build_fail=False, env_file=True, answers="\n", args=(), env=None, env_body=None,
                      machine=None):
        script = (REPO / "install.sh").read_text()
        # Every writable absolute host path is confined to this disposable fixture.
        for prefix in ("/var/lib/", "/var/backups/", "/tmp/just-dashboard", "/usr/local/bin/jd"):
            script = script.replace(prefix, str(pathlib.Path(self.temp.name) / "host") + prefix)
        installer = self.root / "install.sh"
        installer.write_text(script)
        installer.chmod(0o755)
        (self.root / "docker-compose.yml").write_text("services: {}\n")
        self.env_body = env_body or """JD_MASTER_KEY=fixture
JD_SITE=localhost
JD_BIND=
JD_TLS=off
JD_REQUIRE_2FA=false
JD_TERMINAL_ENABLED=false
JD_PORT=8443
JD_BACKEND_PORT=42001
JD_FRONTEND_PORT=42002
"""
        if env_file:
            (self.root / ".env").write_text(self.env_body)
        else:
            (self.root / ".env").unlink(missing_ok=True)
        version_dir = self.root / "backend/internal/version"
        version_dir.mkdir(parents=True, exist_ok=True)
        shutil.copy2(REPO / "backend/internal/version/version.go", version_dir / "version.go")
        (self.scripts / "install-dependencies.sh").write_text("jd_install_dependencies() { :; }\njd_install_host_tools() { :; }\n")
        self.tool("curl", '#!/bin/bash\n[[ "${@: -1}" == https://api.ipify.org ]] && echo TEST_SERVER\nexit 0\n')
        self.tool("dpkg", '#!/bin/bash\nexit 0\n')
        self.tool("chown", '#!/bin/bash\nexit 0\n')
        self.tool("logname", '#!/bin/bash\necho operator\n')
        (pathlib.Path(self.temp.name) / "host/tmp").mkdir(parents=True, exist_ok=True)
        return subprocess.run([str(installer), *args], input=answers, capture_output=True, text=True,
                              cwd=self.temp.name, env={**self.env, **self.machine(**(machine or {})),
                                                       "TEST_BUILD_FAIL": "1" if build_fail else "0", **(env or {})})

    def machine(self, arch="x86_64", memory_gb=4, swap_gb=0, free_mb=40000, systemd=True):
        """The server the installer's preflight sees, whatever machine the tests run on."""
        host = pathlib.Path(self.temp.name) / "machine"
        (host / "systemd").mkdir(parents=True, exist_ok=True)
        (host / "meminfo").write_text(
            f"MemTotal: {int(memory_gb * 1024 * 1024)} kB\nSwapTotal: {int(swap_gb * 1024 * 1024)} kB\n")
        self.tool("uname", f'#!/bin/bash\n[ "$1" = -m ] && echo {arch} && exit 0\nexec /usr/bin/uname "$@"\n')
        self.tool("df", '#!/bin/bash\necho "Filesystem 1048576-blocks Used Available Capacity Mounted on"\n'
                        f'echo "/dev/vda1 80000 1 {free_mb} 1% /"\n')
        return {"JD_PREFLIGHT_MEMINFO": str(host / "meminfo"),
                "JD_PREFLIGHT_SYSTEMD": str(host / ("systemd" if systemd else "none"))}

    def test_installer_stops_before_changing_anything_on_a_server_it_cannot_run_on(self):
        for machine, reason in ((dict(arch="armv7l"), "This server is armv7l."),
                                (dict(systemd=False), "does not run systemd")):
            with self.subTest(reason=reason):
                self.calls.unlink(missing_ok=True)
                result = self.run_installer(env_file=False, machine=machine)
                self.assertEqual(result.returncode, 1, result.stdout)
                self.assertIn(reason, result.stderr)
                self.assertIn("Nothing was installed or changed.", result.stderr)
                self.assertNotIn("Required host tools", result.stdout)
                # Asking Docker where it keeps images is the only thing it ran.
                self.assertFalse([call["args"] for call in self.read_calls() if call["args"][:1] != ["info"]])
                self.assertFalse((self.root / ".env").exists())
                self.assertFalse((pathlib.Path(self.temp.name) / "host/var").exists())

    def test_installer_warns_with_the_numbers_and_says_them_again_when_the_build_fails(self):
        small = dict(memory_gb=0.95, free_mb=4200)
        result = self.run_installer(machine=small)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("[!] 0.9 GB of memory and 0.0 GB of swap.", result.stdout)
        self.assertIn("[!] 4.1 GB free under /var/lib/docker.", result.stdout)
        self.assertIn("Setup complete", result.stdout)

        result = self.run_installer(build_fail=True, machine=small)
        self.assertEqual(result.returncode, 1)
        self.assertIn("The build failed, and this server is below what building needs: 0.9 GB of memory", result.stderr)
        self.assertNotIn("Setup complete", result.stdout)

        # With enough of everything a failed build says only that it failed.
        result = self.run_installer(build_fail=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn("The build failed; the reason is above.", result.stderr)
        self.assertIn("[ok] 4.0 GB of memory and 0.0 GB of swap", result.stdout)

    def fixture_git(self):
        """git in the fixture checkout, reading no configuration but the arguments here."""
        env = {"GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
               "GIT_CEILING_DIRECTORIES": self.temp.name, "HOME": self.temp.name}

        def git(*args):
            return subprocess.run(
                ["git", "-C", str(self.root), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                 "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", *args],
                env={**self.env, **env}, capture_output=True, text=True, check=True).stdout.strip()
        return git, env

    def test_installer_stamps_a_build_that_is_not_a_release(self):
        git, env = self.fixture_git()
        git("init", "--quiet")
        git("commit", "--quiet", "--allow-empty", "-m", "a commit of the operator's own")
        short = git("rev-parse", "--short", "HEAD")
        result = self.run_installer(env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(["compose", "build", "--build-arg", "JD_COMMIT=" + short],
                      [call["args"] for call in self.read_calls()])
        self.assertIn("stamped with commit " + short, result.stdout)

    def test_installer_does_not_stamp_a_release(self):
        git, env = self.fixture_git()
        git("init", "--quiet")
        git("commit", "--quiet", "--allow-empty", "-m", "the release")
        version = (REPO / "backend/internal/version/version.go").read_text().split('const Version = "')[1].split('"')[0]
        git("tag", "v" + version)
        result = self.run_installer(env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(["compose", "build"], [call["args"] for call in self.read_calls()])
        self.assertNotIn("stamped with commit", result.stdout)

    def test_installer_completion_shows_stages_access_and_terminal_commands(self):
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stderr)
        for text in ("[1/4] Prepare the host", "[2/4] Configure the dashboard",
                     "[3/4] Build and start", "[4/4] Verify startup", "Setup complete",
                     "http://localhost:8443", "scripts/reset-password.sh USER",
                     "scripts/create-user.sh USER limited", "scripts/manage.sh status",
                     "scripts/manage.sh --help"):
            self.assertIn(text, result.stdout)
        self.assertNotIn("\x1b", result.stdout)
        self.assertIn("./scripts/manage.sh restart", result.stdout)
        self.assertNotIn("compose restart", result.stdout)

    def test_installer_installs_jd_from_the_built_image_and_says_how_to_use_it(self):
        fake_jd = "#!/bin/sh\necho jd 0.8.2\n"
        result = self.run_installer(env={"TEST_JD_BINARY": fake_jd})
        self.assertEqual(result.returncode, 0, result.stderr)
        installed = pathlib.Path(self.temp.name) / "host/usr/local/bin/jd"
        self.assertEqual(installed.read_text(), fake_jd)
        self.assertEqual(installed.stat().st_mode & 0o777, 0o755)
        # The fixture moves every host path under its own directory, so the
        # message names the moved one.
        self.assertIn(f"jd is at {installed}", result.stdout)
        self.assertIn("jd login", result.stdout)
        self.assertIn(["run", "--rm", "--entrypoint", "cat", "just-dashboard-backend:latest"],
                      [call["args"][:5] for call in self.read_calls()])

    def test_installer_finishes_when_the_image_has_no_jd(self):
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("jd was not installed; sudo ./scripts/manage.sh install-cli tries again", result.stdout)
        self.assertFalse((pathlib.Path(self.temp.name) / "host/usr/local/bin/jd").exists())
        self.assertIn("Setup complete", result.stdout)

    def test_install_cli_copies_jd_out_of_the_image(self):
        target = pathlib.Path(self.temp.name) / "bin-target" / "jd"
        target.parent.mkdir()
        fake_jd = "#!/bin/sh\necho jd 0.8.2\n"
        result = self.run_tool("manage.sh", "install-cli", env={"JD_CLI_PATH": str(target), "TEST_JD_BINARY": fake_jd})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(target.read_text(), fake_jd)
        self.assertEqual(target.stat().st_mode & 0o777, 0o755)
        self.assertIn(f"Installed {target}: jd 0.8.2", result.stdout)
        self.assertEqual(self.read_calls()[-1]["args"],
                         ["run", "--rm", "--entrypoint", "cat", "just-dashboard-backend:latest", "/usr/local/bin/jd"])
        self.assertEqual([p.name for p in target.parent.iterdir()], ["jd"])

    def test_install_cli_refuses_an_image_without_jd_and_leaves_nothing(self):
        target = pathlib.Path(self.temp.name) / "bin-target" / "jd"
        target.parent.mkdir()
        result = self.run_tool("manage.sh", "install-cli", env={"JD_CLI_PATH": str(target)})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("The backend image has no jd", result.stderr)
        self.assertEqual(list(target.parent.iterdir()), [])
        result = self.run_tool("manage.sh", "install-cli", "extra")
        self.assertNotEqual(result.returncode, 0)
        result = self.run_tool("manage.sh", "install-cli", env={"TEST_UID": "1000", "JD_CLI_PATH": str(target)})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sudo", result.stderr)

    def test_fresh_install_forgets_the_first_password_once_healthy(self):
        # What a port conflict during startup leaves beside .env.
        port_backup = self.root / ".env.jd-port-previous-fixture"
        port_backup.write_text('JD_MASTER_KEY=fixture\nJD_BOOTSTRAP_PASSWORD="Abcd-1234-efgh"\n')
        result = self.run_installer(env_file=False, answers="2\nad$min\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        written = (self.root / ".env").read_text()
        self.assertNotIn("JD_BOOTSTRAP_PASSWORD=", written)
        self.assertNotIn("JD_BOOTSTRAP_PASSWORD=", port_backup.read_text())
        self.assertIn('JD_BOOTSTRAP_USER="ad$$min"\n', written)
        self.assertEqual((self.root / ".env").stat().st_mode & 0o777, 0o600)
        # The container is created again, so the value leaves its environment too.
        self.assertIn(["compose", "up", "-d", "--no-deps", "--no-build", "backend"],
                      [call["args"] for call in self.read_calls()])
        # Still shown once: the summary reads it from memory, not from the file.
        self.assertIn("Save that password now", result.stdout)

    def test_unhealthy_fresh_install_keeps_the_first_password(self):
        self.run_installer(env_file=False, answers="2\n")
        self.tool("curl", '#!/bin/bash\nexit 1\n')
        self.tool("seq", '#!/bin/bash\necho 1\n')
        self.tool("sleep", '#!/bin/bash\nexit 0\n')
        (self.root / ".env").unlink()
        script = (self.root / "install.sh")
        result = subprocess.run([str(script)], input="2\n", capture_output=True, text=True,
                                cwd=self.temp.name, env=self.env)
        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn("the backend did not answer", result.stdout)
        self.assertIn("JD_BOOTSTRAP_PASSWORD=", (self.root / ".env").read_text())

    def test_kept_env_keeps_its_bootstrap_password_and_is_not_recreated_for_it(self):
        body = ('JD_MASTER_KEY=fixture\nJD_SITE=localhost\nJD_BIND=\nJD_TLS=off\nJD_REQUIRE_2FA=false\n'
                'JD_TERMINAL_ENABLED=false\nJD_BOOTSTRAP_PASSWORD="Abcd-1234-efgh"\n')
        result = self.run_installer(env_body=body)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('JD_BOOTSTRAP_PASSWORD="Abcd-1234-efgh"\n', (self.root / ".env").read_text())
        self.assertNotIn(["compose", "up", "-d", "--no-deps", "--no-build", "backend"],
                         [call["args"] for call in self.read_calls()])

    def test_kept_env_without_a_site_finishes_on_the_compose_default(self):
        body = "JD_MASTER_KEY=fixture\nJD_TLS=off\nJD_REQUIRE_2FA=false\nJD_TERMINAL_ENABLED=false\n"
        result = self.run_installer(env_body=body)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Setup complete", result.stdout)
        self.assertIn("http://localhost:8443", result.stdout)

    def test_installer_asks_again_for_a_password_the_backend_would_refuse(self):
        answers = "2\nadmin\nn\n" + "shortAa1!\n" * 2 + "alllowercaseletters\n" * 2 + "Abcd-1234-efgh\n" * 2
        result = self.run_installer(env_file=False, answers=answers)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("at least 12 characters", result.stdout + result.stderr)
        self.assertIn("mix at least three of", result.stdout + result.stderr)
        self.assertIn("(the one you chose)", result.stdout)

    def test_installer_restores_the_env_a_kept_uninstall_left(self):
        kept = pathlib.Path(self.temp.name) / "host/var/lib/just-dashboard/install.env"
        kept.parent.mkdir(parents=True)
        kept.write_text("JD_MASTER_KEY=kept\nJD_SITE=localhost\nJD_TLS=off\nJD_PORT=8443\n")
        result = self.run_installer(env_file=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Settings kept by an earlier uninstall", result.stdout)
        # Restored, and kept rather than asked about a second time.
        self.assertNotIn("An .env already exists", result.stdout)
        # The kept key, not a new one; settings it predates are added as on any re-run.
        restored = (self.root / ".env").read_text()
        self.assertTrue(restored.startswith(kept.read_text()), restored)
        self.assertEqual(restored.count("JD_MASTER_KEY="), 1)
        self.assertEqual((self.root / ".env").stat().st_mode & 0o777, 0o600)

    def leave_earlier_store(self):
        store = pathlib.Path(self.temp.name) / "host/var/lib/just-dashboard/vpsd.db"
        store.parent.mkdir(parents=True)
        store.write_bytes(b"SQLite format 3\x00")

    def test_installer_takes_the_key_of_data_an_earlier_install_left(self):
        self.leave_earlier_store()
        key = "ab" * 32
        result = self.run_installer(env_file=False, answers=f"y\nJD_MASTER_KEY={key}\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Data from an earlier install", result.stdout)
        self.assertNotIn(key, result.stdout)
        self.assertIn(f"JD_MASTER_KEY={key}\n", (self.root / ".env").read_text())

    def test_installer_says_what_a_new_key_leaves_unreadable(self):
        self.leave_earlier_store()
        result = self.run_installer(env_file=False, answers="n\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Continuing with a new key", result.stdout)
        written = (self.root / ".env").read_text()
        self.assertRegex(written, r"JD_MASTER_KEY=[0-9a-f]{64}\n")

    def test_installer_does_not_ask_for_a_key_on_a_fresh_server(self):
        result = self.run_installer(env_file=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("Data from an earlier install", result.stdout)

    def test_installer_build_failure_never_claims_success(self):
        result = self.run_installer(build_fail=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("Setup complete", result.stdout)


    # ---- linking to a just-dashboard.com account -------------------------------

    def test_link_commands_forward_to_the_admin_cli_with_the_scope(self):
        for args, expected in (
                (("link",), ["--admin", "link", "--scope", "observe"]),
                (("link", "operate"), ["--admin", "link", "--scope", "operate"]),
                (("link", "maintain"), ["--admin", "link", "--scope", "maintain"]),
                (("unlink",), ["--admin", "unlink"]),
                (("link-status",), ["--admin", "link-status"])):
            result = self.run_tool("manage.sh", *args)
            self.assertEqual(result.returncode, 0, result.stderr)
            call = self.read_calls()[-1]
            self.assertEqual(call["args"], ["compose", "run", "--rm", "--no-deps", "-T", "backend", *expected])
            self.assertEqual(call["cwd"], str(self.root))

    def test_link_keeps_the_commands_exit_status_so_a_decline_is_not_a_failure_to_run(self):
        for code in (2, 1):
            result = self.run_tool("manage.sh", "link", env={"TEST_ADMIN_EXIT": str(code)})
            self.assertEqual(result.returncode, code, result.stderr)

    def test_link_arguments_are_validated_before_docker_and_root_is_needed(self):
        for args in (("link", "root"), ("link", "OBSERVE"), ("link", "observe", "extra"), ("link", "--scope", "observe"),
                     ("unlink", "now"), ("link-status", "-v")):
            result = self.run_tool("manage.sh", *args)
            self.assertNotEqual(result.returncode, 0, args)
        result = self.run_tool("manage.sh", "link", env={"TEST_UID": "1000"})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sudo", result.stderr)
        self.assertEqual(self.read_calls(), [])

    def test_help_lists_the_linking_commands(self):
        result = self.run_tool("manage.sh", "--help", env={"TEST_UID": "1000"})
        self.assertEqual(result.returncode, 0, result.stderr)
        for text in ("link [SCOPE]", "unlink", "link-status", "observe (default), operate or", "JD_ACCOUNT_LINK=false"):
            self.assertIn(text, result.stdout)

    # ---- connecting to a Just Hub ----------------------------------------------

    def test_hub_commands_forward_to_the_admin_cli(self):
        for args, expected in (
                (("hub", "connect", "hub.example.ts.net"), ["--admin", "hub-connect", "hub.example.ts.net"]),
                (("hub", "connect", "https://hub.example.ts.net", "--scope", "observe", "--wait=10m"),
                 ["--admin", "hub-connect", "https://hub.example.ts.net", "--scope", "observe", "--wait=10m"]),
                (("hub", "disconnect"), ["--admin", "hub-disconnect"]),
                (("hub", "status"), ["--admin", "hub-status"])):
            result = self.run_tool("manage.sh", *args)
            self.assertEqual(result.returncode, 0, result.stderr)
            call = self.read_calls()[-1]
            self.assertEqual(call["args"], ["compose", "run", "--rm", "--no-deps", "-T", "backend", *expected])
            self.assertEqual(call["cwd"], str(self.root))

    def test_hub_connect_passes_a_token_on_stdin_and_never_in_argv(self):
        token = "jht_" + "A" * 43
        result = self.run_tool("manage.sh", "hub", "connect", "hub.example.ts.net", "--token-stdin", input=token + "\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        call = self.read_calls()[-1]
        self.assertEqual(call["args"][-3:], ["hub-connect", "hub.example.ts.net", "--token-stdin"])
        self.assertEqual(call["stdin"], token + "\n")
        self.assertNotIn(token, " ".join(call["args"]) + result.stdout + result.stderr)

    def test_hub_connect_refuses_a_token_on_the_command_line(self):
        token = "jht_" + "A" * 43
        for args in (("--token", token), ("--token=" + token,), ("--token-stdin", "--token", token)):
            result = self.run_tool("manage.sh", "hub", "connect", "hub.example.ts.net", *args, input=token + "\n")
            self.assertEqual(result.returncode, 1, args)
            self.assertIn("pass the token on standard input with --token-stdin", result.stderr)
        self.assertEqual(self.read_calls(), [])

    def test_hub_connect_needs_a_token_on_stdin_when_asked_for_one(self):
        result = self.run_tool("manage.sh", "hub", "connect", "hub.example.ts.net", "--token-stdin", input="")
        self.assertEqual(result.returncode, 1)
        self.assertIn("no token", result.stderr)
        self.assertEqual(self.read_calls(), [])

    def test_hub_connect_asks_for_the_token_with_echo_off_at_a_terminal(self):
        token = "jht_" + "B" * 43
        master, slave = pty.openpty()
        process = subprocess.Popen([str(self.scripts / "manage.sh"), "hub", "connect", "hub.example.ts.net", "--token-stdin"],
                                   stdin=slave, stdout=slave, stderr=slave, env=self.env, cwd=self.temp.name)
        os.close(slave)
        output = b""
        answered = False
        deadline = time.monotonic() + 5
        try:
            while time.monotonic() < deadline:
                ready, _, _ = select.select([master], [], [], 0.1)
                if ready:
                    try:
                        output += os.read(master, 4096)
                    except OSError:
                        break
                if not answered and b"Link token: " in output:
                    os.write(master, token.encode() + b"\n")
                    answered = True
                if process.poll() is not None:
                    break
            self.assertEqual(process.wait(timeout=1), 0, output.decode())
            self.assertTrue(answered, output.decode())
            self.assertNotIn(token.encode(), output)
            call = self.read_calls()[-1]
            self.assertEqual(call["args"][-3:], ["hub-connect", "hub.example.ts.net", "--token-stdin"])
            self.assertEqual(call["stdin"], token + "\n")
            self.assertNotIn(token, " ".join(call["args"]))
        finally:
            os.close(master)
            if process.poll() is None:
                process.kill()

    def test_hub_connect_keeps_the_commands_exit_status(self):
        for code in (2, 1):
            result = self.run_tool("manage.sh", "hub", "connect", "hub.example.ts.net", env={"TEST_ADMIN_EXIT": str(code)})
            self.assertEqual(result.returncode, code, result.stderr)

    def test_hub_arguments_are_validated_before_docker_and_root_is_needed(self):
        for args in (("hub",), ("hub", "join"), ("hub", "connect"), ("hub", "connect", "--scope", "observe"),
                     ("hub", "connect", "hub.example.ts.net", "--scope", "root"),
                     ("hub", "connect", "hub.example.ts.net", "--scope"),
                     ("hub", "connect", "hub.example.ts.net", "--force"), ("hub", "disconnect", "now"),
                     ("hub", "status", "-v")):
            result = self.run_tool("manage.sh", *args)
            self.assertNotEqual(result.returncode, 0, args)
        result = self.run_tool("manage.sh", "hub", "status", env={"TEST_UID": "1000"})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("sudo", result.stderr)
        self.assertEqual(self.read_calls(), [])

    def test_help_lists_the_hub_commands(self):
        result = self.run_tool("manage.sh", "--help", env={"TEST_UID": "1000"})
        self.assertEqual(result.returncode, 0, result.stderr)
        for text in ("hub connect URL", "hub disconnect", "hub status", "--token-stdin", "default maintain", "JD_HUB_LINK=false"):
            self.assertIn(text, result.stdout)

    # The installer's own prompts are read from a pipe, where bash does not
    # print them, so these tests prove what was asked by what the answers did.
    #
    # Fresh install answers, in order: how to reach it (2, the tunnel, so no
    # Tailscale is touched), the username, a generated password, the web
    # terminal, two-factor, then the question under test.
    FRESH = "2\n\ny\nn\nn\n"
    LINKED = 'To link this server, open   https://just-dashboard.com/link\nand enter the code          ABCD-2345\nLinked to ion@example.com as "box" (observe).'

    def link_calls(self):
        return [c for c in self.read_calls() if "--admin" in c["args"]]

    def test_installer_does_not_link_unless_told_to_and_says_how_to_later(self):
        for answers in (self.FRESH + "n\n", self.FRESH, self.FRESH + "\n"):
            with self.subTest(answers=answers):
                self.calls.unlink(missing_ok=True)
                result = self.run_installer(env_file=False, answers=answers)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.link_calls(), [])
                self.assertIn("Setup complete", result.stdout)
                self.assertIn("Not linked. Later: sudo ./scripts/manage.sh link, or Settings → Connect", result.stdout)
                self.assertNotIn("Linking to just-dashboard.com", result.stdout)
                for line in ("manage.sh link [scope]", "manage.sh unlink", "manage.sh link-status"):
                    self.assertIn(line, result.stdout)

    def test_installer_links_after_the_health_check_and_reports_the_owner(self):
        result = self.run_installer(env_file=False, answers=self.FRESH + "y\n", env={"TEST_LINK_OUTPUT": self.LINKED})
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.link_calls()
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["args"], ["compose", "run", "--rm", "--no-deps", "-T", "backend", "--admin", "link",
                                            "--scope", "observe", "--wait", "15m"])
        self.assertEqual(calls[0]["cwd"], str(self.root))
        # Nothing of the installer's own input reaches the link command.
        self.assertEqual(calls[0]["stdin"], "")
        out = result.stdout
        self.assertLess(out.index("backend is healthy"), out.index("Linking to just-dashboard.com"))
        self.assertLess(out.index("Linking to just-dashboard.com"), out.index("Setup complete"))
        self.assertIn("and enter the code          ABCD-2345", out)
        self.assertIn('Linked to ion@example.com as "box" (observe).', out.split("Setup complete")[1])
        self.assertNotIn("Not linked", out)
        # The scope is not in .env; nothing about the link is.
        self.assertNotIn("JD_ACCOUNT", (self.root / ".env").read_text())

    def test_installer_flags_answer_for_an_unattended_run_and_name_the_scope(self):
        # "y" sits where the question would be read: it must not be.
        result = self.run_installer(env_file=False, answers=self.FRESH + "y\n", args=("--no-link",))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.link_calls(), [])
        self.assertIn("Not linked. Later", result.stdout)

        self.calls.unlink(missing_ok=True)
        result = self.run_installer(env_file=False, answers=self.FRESH + "n\n", args=("--link", "--link-scope", "maintain"),
                                    env={"TEST_LINK_OUTPUT": self.LINKED})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.link_calls()[0]["args"][-4:], ["--scope", "maintain", "--wait", "15m"])

        self.calls.unlink(missing_ok=True)
        result = self.run_installer(env_file=False, answers=self.FRESH, args=("--link", "--link-scope=operate"),
                                    env={"TEST_LINK_OUTPUT": self.LINKED})
        self.assertEqual(self.link_calls()[0]["args"][-4:], ["--scope", "operate", "--wait", "15m"])

    def test_installer_finishes_whatever_becomes_of_the_link(self):
        for name, env in (
                ("declined", {"TEST_ADMIN_EXIT": "2", "TEST_LINK_OUTPUT": "The request was declined on the site. Nothing was linked."}),
                ("failed", {"TEST_ADMIN_EXIT": "1", "TEST_LINK_OUTPUT": "account command failed: the site is unreachable"}),
                ("no network", {"TEST_ADMIN_EXIT": "1"}),
                ("exit zero without a link", {"TEST_LINK_OUTPUT": "something else entirely"})):
            with self.subTest(name):
                self.calls.unlink(missing_ok=True)
                result = self.run_installer(env_file=False, answers=self.FRESH + "y\n", env=env)
                self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
                out = result.stdout
                self.assertIn("Setup complete", out)
                self.assertIn("This server was not linked (the install itself is finished)", out)
                self.assertIn("sudo ./scripts/manage.sh link", out)
                self.assertTrue(out.rstrip().split("Setup complete")[1].count("Not linked. Later: sudo ./scripts/manage.sh link") == 1)
                self.assertNotIn("Linked to", out)
                # The stack was started before the link was tried.
                self.assertTrue(any(c["args"][:2] == ["compose", "build"] or "-start-stack" in c["args"] for c in self.read_calls()))

    def test_installer_skips_the_question_and_says_nothing_of_linking_when_it_is_switched_off(self):
        result = self.run_installer(env_file=False, answers=self.FRESH + "y\n", env={"JD_ACCOUNT_LINK": "false"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.link_calls(), [])
        self.assertNotIn("Not linked", result.stdout)
        self.assertNotIn("manage.sh link", result.stdout)
        env_text = (self.root / ".env").read_text()
        self.assertIn("\nJD_ACCOUNT_LINK=false\n", env_text)

        # --link does not get round it.
        result = self.run_installer(env_file=False, answers=self.FRESH, args=("--link",), env={"JD_ACCOUNT_LINK": "false"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.link_calls(), [])
        self.assertIn("--link ignored: JD_ACCOUNT_LINK=false", result.stdout)

        # A kept .env that says false is read the same way, quoted or not and
        # with a comment after it, as Compose reads it.
        for spelling in ('"false"', "false", "false # switched off", "'false'   # off", "0", "F"):
            with self.subTest(spelling=spelling):
                self.calls.unlink(missing_ok=True)
                result = self.run_installer(args=("--link",), env_body="JD_MASTER_KEY=fixture\nJD_SITE=localhost\nJD_TLS=off\nJD_REQUIRE_2FA=false\n"
                                            f"JD_TERMINAL_ENABLED=false\nJD_ACCOUNT_LINK={spelling}\nJD_PORT=8443\nJD_BACKEND_PORT=42001\nJD_FRONTEND_PORT=42002\n")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.link_calls(), [])
                self.assertIn("--link ignored", result.stdout)
                self.assertNotIn("manage.sh link", result.stdout)

        # And a value that is not false does not switch it off: a hash with no
        # space before it is part of the value, not a comment.
        self.calls.unlink(missing_ok=True)
        result = self.run_installer(args=("--link",), env={"TEST_LINK_OUTPUT": self.LINKED},
                                    env_body="JD_MASTER_KEY=fixture\nJD_SITE=localhost\nJD_TLS=off\nJD_REQUIRE_2FA=false\n"
                                    "JD_TERMINAL_ENABLED=false\nJD_ACCOUNT_LINK=true # on\nJD_PORT=8443\nJD_BACKEND_PORT=42001\nJD_FRONTEND_PORT=42002\n")
        self.assertEqual(len(self.link_calls()), 1)

    def test_installer_rerun_with_a_kept_env_only_links_when_asked_to(self):
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.link_calls(), [])
        self.assertIn("Linking this server to a just-dashboard.com account is optional", result.stdout)
        self.assertIn("sudo ./scripts/manage.sh link, or Settings → Connect", result.stdout)
        # It does not claim the install is unlinked: it may well be linked.
        self.assertNotIn("Not linked", result.stdout)

        self.calls.unlink(missing_ok=True)
        result = self.run_installer(args=("--link",), env={"TEST_LINK_OUTPUT": self.LINKED})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.link_calls()), 1)
        self.assertIn('Linked to ion@example.com as "box" (observe).', result.stdout.split("Setup complete")[1])
        # .env is kept exactly as it was.
        self.assertEqual((self.root / ".env").read_text(), self.env_body)

        self.calls.unlink(missing_ok=True)
        result = self.run_installer(args=("--no-link",))
        self.assertEqual(self.link_calls(), [])
        self.assertNotIn("is optional", result.stdout)

    def test_installer_says_so_when_a_rerun_with_link_finds_the_server_already_linked(self):
        said = "account command failed: this server is already linked to ion@example.com; run unlink first to link it to another account"
        result = self.run_installer(args=("--link",), env={"TEST_ADMIN_EXIT": "1", "TEST_LINK_OUTPUT": said})
        self.assertEqual(result.returncode, 0, result.stderr)
        out = result.stdout
        self.assertIn("Setup complete", out)
        self.assertIn("This server is already linked to ion@example.com.", out)
        self.assertIn("sudo ./scripts/manage.sh unlink", out)
        self.assertNotIn("Not linked", out)
        self.assertNotIn("was not linked", out)

    def test_installer_refuses_contradictory_or_unknown_link_flags_before_doing_anything(self):
        for args in (("--link", "--no-link"), ("--no-link", "--link"), ("--link-scope", "root"), ("--link-scope",),
                     ("--link-scope=OBSERVE",), ("--linked",), ("link",)):
            with self.subTest(args=args):
                result = self.run_installer(args=args)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("[1/4]", result.stdout)
                self.assertEqual(self.read_calls(), [])
        result = self.run_installer(args=("--help",))
        self.assertEqual(result.returncode, 0)
        for text in ("--link", "--no-link", "--link-scope", "optional"):
            self.assertIn(text, result.stdout)


if __name__ == "__main__":
    unittest.main()
