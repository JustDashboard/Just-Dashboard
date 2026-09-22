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


    # Host tools go through the same manager into a fake root: /etc/apt is
    # substituted like /usr/share above, and the two coreutils the gh
    # repository setup needs are linked in beside the fake commands.
    def run_host_tools(self, present, fail=()):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            etc = root / 'etc-apt'
            for name in present:
                path = root / name
                path.write_text('#!/bin/bash\n')
                path.chmod(0o755)
            for tool in ('mkdir', 'chmod'):
                real = next(p for p in (pathlib.Path('/usr/bin') / tool, pathlib.Path('/bin') / tool) if p.exists())
                (root / tool).symlink_to(real)
            fakes = {
                'dpkg': '#!/bin/bash\necho amd64\n',
                'curl': '#!/bin/bash\n'
                        'for arg in "$@"; do [[ $arg == https://* ]] && printf "curl %s\\n" "$arg" >> "$TEST_ROOT/calls"; done\n'
                        'while [[ $# -gt 0 ]]; do [[ $1 == -o ]] && : > "$2"; shift; done\n',
                'apt-get': '#!/bin/bash\n'
                           'printf "%s\\n" "$*" >> "$TEST_ROOT/calls"\n'
                           'if [[ "$1" == install ]]; then\n'
                           'shift; while [[ $1 == -* ]]; do shift; done\n'
                           'for pkg in "$@"; do\n'
                           'for bad in $TEST_FAIL; do [[ $pkg == "$bad" ]] && exit 100; done\n'
                           'printf "#!/bin/bash\\n" > "$TEST_ROOT/$pkg"; /bin/chmod 755 "$TEST_ROOT/$pkg"\n'
                           'done\n'
                           'fi\n',
            }
            for name, body in fakes.items():
                (root / name).write_text(body)
                (root / name).chmod(0o755)
            script = SCRIPT.read_text().replace('/etc/apt/', str(etc) + '/')
            env = {**os.environ, 'PATH': directory, 'TEST_ROOT': directory, 'TEST_FAIL': ' '.join(fail)}
            result = subprocess.run(['/bin/bash', '-c', 'eval "$1"; jd_install_host_tools', 'test', script],
                                    env=env, capture_output=True, text=True)
            calls = (root / 'calls').read_text() if (root / 'calls').exists() else ''
            source = etc / 'sources.list.d' / 'github-cli.list'
            return result, calls, source.read_text() if source.exists() else ''

    def test_host_tools_install_what_is_missing_and_gh_from_github(self):
        result, calls, source = self.run_host_tools(['git', 'whois'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, 'update\n'
                         'install -y --no-install-recommends git-lfs\n'
                         'install -y --no-install-recommends traceroute\n'
                         'curl https://cli.github.com/packages/githubcli-archive-keyring.gpg\n'
                         'update\n'
                         'install -y --no-install-recommends gh\n')
        self.assertIn('arch=amd64 signed-by=', source)
        self.assertIn('https://cli.github.com/packages stable main', source)

    def test_host_tools_present_do_not_touch_the_manager(self):
        result, calls, source = self.run_host_tools(['git', 'git-lfs', 'whois', 'traceroute', 'gh'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, '')
        self.assertEqual(source, '')

    def test_one_unavailable_host_tool_does_not_cost_the_others(self):
        result, calls, _ = self.run_host_tools(['git'], fail=('whois',))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Could not install: whois', result.stderr)
        self.assertIn('install -y --no-install-recommends traceroute\n', calls)
        self.assertIn('install -y --no-install-recommends gh\n', calls)

if __name__ == '__main__':
    unittest.main()
