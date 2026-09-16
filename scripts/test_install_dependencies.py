import os
import pathlib
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("install-dependencies.sh").resolve()


class DependenciesTest(unittest.TestCase):
    def run_setup(self, present, manager=True, fail=False):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            tool_script = '#!/bin/bash\nif [[ "$1" == plugins ]]; then echo "standalone webroot"; fi\n'
            for name in present:
                path = root / name
                path.write_text(tool_script)
                path.chmod(0o755)
            if manager:
                path = root / 'apt-get'
                path.write_text('#!/bin/bash\nprintf "%s\\n" "$*" >> "$TEST_ROOT/calls"\n' + (
                    'exit 1\n' if fail else
                    '''if [[ "$1" == install ]]; then
for tool in curl openssl certbot; do
printf '#!/bin/bash\\nif [[ "$1" == plugins ]]; then echo "standalone webroot"; fi\\n' > "$TEST_ROOT/$tool"
/bin/chmod 755 "$TEST_ROOT/$tool"
done
fi
'''))
                path.chmod(0o755)
            result = subprocess.run(['/bin/bash', '-c', 'source "$1"; jd_install_dependencies', 'test', str(SCRIPT)],
                                    env={**os.environ, 'PATH': directory, 'TEST_ROOT': directory}, capture_output=True, text=True)
            calls = (root / 'calls').read_text() if (root / 'calls').exists() else ''
            return result, calls

    def test_installs_only_missing_tools(self):
        result, calls = self.run_setup(['curl', 'openssl'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, 'update\ninstall -y --no-install-recommends certbot\n')

    def test_existing_install_does_not_reinstall(self):
        result, calls = self.run_setup(['curl', 'openssl', 'certbot'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, '')

    def test_package_failure_stops_setup(self):
        result, calls = self.run_setup([], fail=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, 'update\n')

    def test_unsupported_host_reports_missing_tools(self):
        result, _ = self.run_setup([], manager=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('No supported package manager', result.stderr)

    # The terminal extras are installed through the same manager, into a fake
    # /usr/share so the plugin probe can be satisfied without touching the host.
    def run_extras(self, present, manager=True, fail=False, updated=False):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            share = root / 'share'
            for name in present:
                if name == 'zsh':
                    path = root / 'zsh'
                    path.write_text('#!/bin/bash\n')
                    path.chmod(0o755)
                else:
                    (share / name).mkdir(parents=True)
                    (share / name / (name + '.zsh')).write_text('')
            if manager:
                path = root / 'apt-get'
                path.write_text('#!/bin/bash\nprintf "%s\\n" "$*" >> "$TEST_ROOT/calls"\n' + (
                    'exit 1\n' if fail else
                    '''if [[ "$1" == install ]]; then
shift; while [[ $1 == -* ]]; do shift; done
for pkg in "$@"; do
if [[ $pkg == zsh ]]; then printf '#!/bin/bash\\n' > "$TEST_ROOT/zsh"; /bin/chmod 755 "$TEST_ROOT/zsh"
else /bin/mkdir -p "$TEST_ROOT/share/$pkg"; : > "$TEST_ROOT/share/$pkg/$pkg.zsh"; fi
done
fi
'''))
                path.chmod(0o755)
            # The plugin probe reads /usr/share; point it at the fake tree by
            # running the function with that path prefix substituted.
            script = SCRIPT.read_text().replace('/usr/share/', str(share) + '/')
            env = {**os.environ, 'PATH': directory, 'TEST_ROOT': directory}
            if updated:
                env['JD_APT_UPDATED'] = '1'
            result = subprocess.run(['/bin/bash', '-c', 'eval "$1"; jd_install_terminal_extras', 'test', script],
                                    env=env, capture_output=True, text=True)
            calls = (root / 'calls').read_text() if (root / 'calls').exists() else ''
            return result, calls

    def test_extras_install_only_what_is_missing(self):
        result, calls = self.run_extras(['zsh', 'zsh-syntax-highlighting'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, 'update\ninstall -y --no-install-recommends zsh-autosuggestions\n')

    def test_extras_present_do_not_touch_the_manager(self):
        result, calls = self.run_extras(['zsh', 'zsh-autosuggestions', 'zsh-syntax-highlighting'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, '')

    def test_extras_skip_a_second_apt_update(self):
        result, calls = self.run_extras([], updated=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, 'install -y --no-install-recommends zsh zsh-autosuggestions zsh-syntax-highlighting\n')

    def test_extras_failure_is_reported_not_fatal_to_the_shell(self):
        result, calls = self.run_extras([], fail=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, 'update\n')

    def test_extras_without_a_manager_report_it(self):
        result, _ = self.run_extras([], manager=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('No supported package manager', result.stderr)


if __name__ == '__main__':
    unittest.main()
