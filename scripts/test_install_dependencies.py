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
        # The first try and the zsh-alone retry each ask for the index; nothing installs.
        self.assertEqual(calls, 'update\nupdate\n')

    # Amazon Linux and openSUSE package zsh and neither plugin: zsh comes from
    # the manager and each plugin from its release tag at github.com/zsh-users.
    def test_unpackaged_plugins_are_cloned_at_their_release_tag(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            share = root / 'share'
            for tool in ('rm', 'mkdir'):
                real = next(p for p in (pathlib.Path('/usr/bin') / tool, pathlib.Path('/bin') / tool) if p.exists())
                (root / tool).symlink_to(real)
            fakes = {
                'apt-get': '#!/bin/bash\nprintf "%s\\n" "$*" >> "$TEST_ROOT/calls"\n'
                           'if [[ "$1" == install ]]; then shift; while [[ $1 == -* ]]; do shift; done\n'
                           'for pkg in "$@"; do [[ $pkg == zsh-* ]] && exit 100; done\n'
                           'printf "#!/bin/bash\\n" > "$TEST_ROOT/zsh"; /bin/chmod 755 "$TEST_ROOT/zsh"; fi\n',
                'git': '#!/bin/bash\nprintf "git %s\\n" "$*" >> "$TEST_ROOT/calls"\n'
                       'dest="${@: -1}"; /bin/mkdir -p "$dest/.git"; : > "$dest/${dest##*/}.zsh"\n',
            }
            for name, body in fakes.items():
                (root / name).write_text(body)
                (root / name).chmod(0o755)
            script = SCRIPT.read_text().replace('/usr/share/', str(share) + '/')
            env = {**os.environ, 'PATH': directory, 'TEST_ROOT': directory}
            result = subprocess.run(['/bin/bash', '-c', 'eval "$1"; jd_install_terminal_extras', 'test', script],
                                    env=env, capture_output=True, text=True)
            calls = (root / 'calls').read_text()
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('install -y --no-install-recommends zsh zsh-autosuggestions zsh-syntax-highlighting\n', calls)
            self.assertIn('install -y --no-install-recommends zsh\n', calls)
            self.assertIn('--branch v0.7.1 https://github.com/zsh-users/zsh-autosuggestions.git', calls)
            self.assertIn('--branch 0.8.0 https://github.com/zsh-users/zsh-syntax-highlighting.git', calls)
            self.assertTrue((share / 'zsh-autosuggestions' / 'zsh-autosuggestions.zsh').exists())
            self.assertFalse((share / 'zsh-autosuggestions' / '.git').exists())

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
                           'bins=$pkg; [[ $pkg == iproute2 ]] && bins="ip ss tc"; [[ $pkg == wtmpdb ]] && bins=last\n'
                           'for bin in $bins; do printf "#!/bin/bash\\n" > "$TEST_ROOT/$bin"; /bin/chmod 755 "$TEST_ROOT/$bin"; done\n'
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
        result, calls, source = self.run_host_tools(['git', 'whois', 'ip', 'ss', 'tc', 'tcpdump', 'last'])
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
        result, calls, source = self.run_host_tools(['git', 'git-lfs', 'whois', 'traceroute', 'ip', 'ss', 'tc', 'tcpdump', 'last', 'gh'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, '')
        self.assertEqual(source, '')

    def test_ip_ss_and_tc_come_from_one_iproute2_package(self):
        result, calls, _ = self.run_host_tools(['git', 'git-lfs', 'whois', 'traceroute', 'last', 'gh'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, 'update\n'
                         'install -y --no-install-recommends iproute2\n'
                         'install -y --no-install-recommends tcpdump\n')

    # Debian 13 and Ubuntu 26.04 dropped util-linux's last; wtmpdb carries it.
    def test_a_missing_last_comes_from_wtmpdb(self):
        result, calls, _ = self.run_host_tools(['git', 'git-lfs', 'whois', 'traceroute', 'ip', 'ss', 'tc', 'tcpdump', 'gh'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, 'update\ninstall -y --no-install-recommends wtmpdb\n')

    def test_the_red_hat_family_names_its_own_packages(self):
        script = SCRIPT.read_text()
        with tempfile.TemporaryDirectory() as directory:
            (pathlib.Path(directory) / 'dnf').write_text('#!/bin/bash\n')
            (pathlib.Path(directory) / 'dnf').chmod(0o755)
            out = subprocess.run(['/bin/bash', '-c', 'eval "$1"; for t in ip ss tc last git; do jd_host_tool_package $t; done', 'test', script],
                                 env={**os.environ, 'PATH': directory}, capture_output=True, text=True).stdout.split()
        self.assertEqual(out, ['iproute', 'iproute', 'iproute-tc', 'wtmpdb', 'git'])

    def test_one_unavailable_host_tool_does_not_cost_the_others(self):
        result, calls, _ = self.run_host_tools(['git', 'ip', 'ss', 'tc', 'tcpdump', 'last'], fail=('whois',))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Could not install: whois', result.stderr)
        self.assertIn('install -y --no-install-recommends traceroute\n', calls)
        self.assertIn('install -y --no-install-recommends gh\n', calls)

# os-release as each distribution ships it, cut to the fields the installer
# reads. scripts/verify-distros/run.sh installs on the real ones.
OS_RELEASE = {
    'ubuntu': ('ID=ubuntu\nID_LIKE=debian\nVERSION_CODENAME=noble\nUBUNTU_CODENAME=noble\n', 'apt-get', None),
    'linuxmint': ('ID=linuxmint\nID_LIKE="ubuntu debian"\nVERSION_CODENAME=zena\nUBUNTU_CODENAME=noble\n', 'apt-get', 'trixie/sid'),
    'pop': ('ID=pop\nID_LIKE="ubuntu debian"\nVERSION_CODENAME=noble\nUBUNTU_CODENAME=noble\n', 'apt-get', 'trixie/sid'),
    'kali': ('ID=kali\nID_LIKE=debian\nVERSION_CODENAME=kali-rolling\n', 'apt-get', 'kali-rolling'),
    'parrot': ('ID=parrot\nID_LIKE=debian\nVERSION_CODENAME=echo\n', 'apt-get', '13.6'),
    'deepin': ('ID=deepin\nVERSION_CODENAME=crimson\n', 'apt-get', 'bookworm/sid'),
    'lmde': ('ID=linuxmint\nID_LIKE=debian\nVERSION_CODENAME=gigi\nDEBIAN_CODENAME=trixie\n', 'apt-get', '13.1'),
    'arch': ('ID=arch\n', 'pacman', None),
    'manjaro': ('ID=manjaro\nID_LIKE=arch\n', 'pacman', None),
    'tumbleweed': ('ID="opensuse-tumbleweed"\nID_LIKE="opensuse suse"\n', 'zypper', None),
    'amzn': ('ID="amzn"\nID_LIKE="fedora"\nVERSION_ID="2023"\n', 'dnf', None),
    'rocky': ('ID="rocky"\nID_LIKE="rhel centos fedora"\nVERSION_ID="9.6"\n', 'dnf', None),
    'fedora': ('ID=fedora\nVERSION_ID=45\n', 'dnf', None),
    'eurolinux': ('ID="eurolinux"\nID_LIKE="rhel fedora centos"\nVERSION_ID="9.4"\n', 'dnf', None),
    'ultramarine': ('ID=ultramarine\nID_LIKE="fedora"\nVERSION_ID=42\n', 'dnf', None),
    'void': ('ID="void"\n', None, None),
}


class DockerPlanTest(unittest.TestCase):
    def run_function(self, distro, function):
        text, manager, debian_version = OS_RELEASE[distro]
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / 'os-release').write_text(text)
            version = root / 'debian_version'
            if debian_version:
                version.write_text(debian_version + '\n')
            if manager:
                (root / manager).write_text('#!/bin/bash\n')
                (root / manager).chmod(0o755)
            env = {**os.environ, 'PATH': directory, 'JD_OS_RELEASE': str(root / 'os-release'),
                   'JD_DEBIAN_VERSION': str(version)}
            return subprocess.run(['/bin/bash', '-c', 'source "$1"; ' + function, 'test', str(SCRIPT)],
                                  env=env, capture_output=True, text=True)

    def plan(self, distro):
        result = self.run_function(distro, 'jd_docker_plan')
        return result.stdout.strip() if result.returncode == 0 else None

    def test_distributions_docker_names_take_its_script(self):
        for distro in ('ubuntu', 'rocky', 'fedora'):
            self.assertEqual(self.plan(distro), 'get-docker', distro)

    def test_ubuntu_derivatives_take_the_ubuntu_release_they_are_built_on(self):
        for distro in ('linuxmint', 'pop'):
            self.assertEqual(self.plan(distro), 'apt-repo ubuntu noble', distro)

    def test_debian_derivatives_take_the_debian_release_they_are_built_on(self):
        self.assertEqual(self.plan('parrot'), 'apt-repo debian trixie')
        self.assertEqual(self.plan('deepin'), 'apt-repo debian bookworm')
        self.assertEqual(self.plan('lmde'), 'apt-repo debian trixie')

    def test_a_rolling_debian_derivative_takes_its_own_packages(self):
        self.assertEqual(self.plan('kali'), 'packages docker.io docker-compose docker-buildx')

    def test_arch_and_opensuse_take_their_own_packages(self):
        for distro in ('arch', 'manjaro', 'tumbleweed'):
            self.assertEqual(self.plan(distro), 'packages docker docker-compose docker-buildx', distro)

    def test_amazon_linux_takes_its_engine_and_dockers_plugins(self):
        self.assertEqual(self.plan('amzn'), 'plugins docker')

    def test_other_rpm_rebuilds_take_the_repository_they_follow(self):
        self.assertEqual(self.plan('eurolinux'), 'rpm-repo centos')
        self.assertEqual(self.plan('ultramarine'), 'rpm-repo fedora')

    def test_no_known_way_says_so(self):
        self.assertIsNone(self.plan('void'))
        result = self.run_function('void', 'jd_install_docker')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('No known way to install Docker', result.stderr)

    def test_epel_is_for_the_red_hat_family_only(self):
        for distro, expected in (('rocky', 0), ('eurolinux', 0), ('fedora', 1), ('amzn', 1), ('ubuntu', 1)):
            self.assertEqual(self.run_function(distro, 'jd_rhel_family').returncode, expected, distro)


if __name__ == '__main__':
    unittest.main()
