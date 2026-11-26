"""The proxy's entrypoint, run with a stand-in for Caddy that prints what it was given."""
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest

ENTRYPOINT = pathlib.Path(__file__).resolve().parent.parent / "deploy" / "proxy-entrypoint.sh"
HSTS = "header Strict-Transport-Security max-age=31536000"


class ProxyEntrypointTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.bin = pathlib.Path(directory.name)
        caddy = self.bin / "caddy"
        caddy.write_text('#!/bin/sh\nprintf "%s\\n%s\\n" "$JD_HSTS_DIRECTIVE" "$JD_BIND"\n')
        caddy.chmod(0o755)

    def run_entrypoint(self, **env):
        environment = {"PATH": f"{self.bin}{os.pathsep}{os.environ['PATH']}", "JD_CERT_DIR": str(self.bin)}
        environment.update(env)
        result = subprocess.run(["sh", str(ENTRYPOINT)], env=environment, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        hsts, bind = result.stdout.split("\n")[:2]
        return hsts, bind

    def test_hsts_is_sent_for_a_real_name_in_any_case(self):
        for site in ["dash.example.com", "Dash.Example.COM", "box.tailnet.ts.net"]:
            with self.subTest(site=site):
                self.assertEqual(self.run_entrypoint(JD_SITE=site)[0], HSTS)

    def test_loopback_names_and_addresses_get_no_hsts_in_any_case(self):
        # A browser keeps the policy per host, so LOCALHOST would pin every
        # localhost port on the operator's machine as surely as localhost.
        for site in ["localhost", "LOCALHOST", "LocalHost", "app.localhost", "app.LocalHost", "DEV.LOCALHOST", "10.0.0.1", "::1"]:
            with self.subTest(site=site):
                self.assertEqual(self.run_entrypoint(JD_SITE=site)[0], "")
        self.assertEqual(self.run_entrypoint()[0], "")

    def test_the_site_is_handed_on_as_it_was_written(self):
        self.assertEqual(self.run_entrypoint(JD_SITE="Dash.Example.COM")[1], "Dash.Example.COM")

    def test_plain_http_sends_no_hsts(self):
        self.assertEqual(self.run_entrypoint(JD_SITE="dash.example.com", JD_TLS="off")[0], "")

    @unittest.skipUnless(shutil.which("busybox"), "busybox unavailable")
    def test_the_images_shell_reads_it_the_same_way(self):
        # The Caddy image is Alpine's: its sh and tr are busybox's.
        for site, want in [("app.LocalHost", ""), ("Dash.Example.COM", HSTS)]:
            with self.subTest(site=site):
                environment = {"PATH": f"{self.bin}{os.pathsep}{os.environ['PATH']}", "JD_SITE": site}
                result = subprocess.run(["busybox", "sh", str(ENTRYPOINT)], env=environment, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.split("\n")[0], want)


if __name__ == "__main__":
    unittest.main()
