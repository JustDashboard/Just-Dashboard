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
        for name in ("manage.sh", "create-user.sh", "reset-password.sh", "dotenv.sh", "password.sh"):
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
admin_link = "--admin" in args and any(x in args for x in ["link", "unlink", "link-status"])
if "--admin" in args and any(x in args for x in ["create-user", "reset-password"]) or admin_link:
    entry["stdin"] = sys.stdin.read()
with pathlib.Path(os.environ["TEST_CALLS"]).open("a") as calls:
    calls.write(json.dumps(entry) + "\\n")
if admin_link:
    if os.environ.get("TEST_LINK_OUTPUT"):
        print(os.environ["TEST_LINK_OUTPUT"])
    sys.exit(int(os.environ.get("TEST_ADMIN_EXIT", "0")))
if args == ["compose", "build"]:
    sys.exit(int(os.environ.get("TEST_BUILD_FAIL", "0")))
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

    def run_installer(self, build_fail=False, env_file=True, answers="\n", args=(), env=None, env_body=None):
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
                              cwd=self.temp.name, env={**self.env, "TEST_BUILD_FAIL": "1" if build_fail else "0", **(env or {})})

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
