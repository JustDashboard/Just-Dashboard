import hashlib
import json
import os
import pathlib
import stat
import subprocess
import time

ROOT = pathlib.Path('/home/ubuntu/Just-Dashboard-network-dns-decision-restart-endpoint')
ARTIFACTS = pathlib.Path('/home/ubuntu/Just-Dashboard-network-dns-decision-example-artifacts')
SOURCE = '8301171bc48c0ce9c3bae605ad72ed56e494bc7b'
BASE = '17aeaae3'
TMP = pathlib.Path('/home/ubuntu/jd-de-t')
HELPER = ARTIFACTS / 'native-dns-example-helper'
BINARY = ARTIFACTS / 'native-example-race.test'
OUTPUT = ARTIFACTS / 'decision-example-compile-result.json'


def sha(data):
    return hashlib.sha256(data).hexdigest()


def hashes():
    assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT).decode().strip() == SOURCE
    assert subprocess.check_output(['git', 'status', '--porcelain'], cwd=ROOT) == b''
    go = {str(p.relative_to(ROOT)): sha(p.read_bytes()) for p in sorted((ROOT / 'backend').rglob('*.go'))}
    modules = {p: sha((ROOT / p).read_bytes()) for p in ('backend/go.mod', 'backend/go.sum')}
    assert len(go) == 1869
    tree = subprocess.check_output(['git', 'ls-tree', '-rz', SOURCE, 'backend'], cwd=ROOT)
    objects = {}
    for record in tree.split(b'\0'):
        if not record:
            continue
        entry, path = record.split(b'\t', 1)
        mode, kind, oid = entry.split()
        name = path.decode()
        if name in go or name in modules:
            assert mode == b'100644' and kind == b'blob'
            objects[name] = oid
    assert set(objects) == set(go) | set(modules)
    names = sorted(objects)
    batch = subprocess.check_output(['git', 'cat-file', '--batch'], cwd=ROOT,
                                    input=b'\n'.join(objects[n] for n in names) + b'\n')
    position = 0
    for name in names:
        end = batch.index(b'\n', position)
        oid, kind, size = batch[position:end].split()
        assert oid == objects[name] and kind == b'blob'
        position = end + 1
        data = batch[position:position + int(size)]
        assert sha(data) == (go | modules)[name]
        position += int(size)
        assert batch[position:position + 1] == b'\n'
        position += 1
    assert position == len(batch)
    return go, modules


env = os.environ.copy()
for key in list(env):
    if key.startswith('JD_DNS_'):
        del env[key]
env.update(GOTOOLCHAIN='go1.26.8', GOMAXPROCS='2', GOFLAGS='-p=2', TMPDIR=str(TMP),
           JD_DNS_DOMAIN_DECISIONS_LIVE='0', JD_DNS_SERVICES_LIVE_ENGINE='')
assert not OUTPUT.exists() and not HELPER.exists() and not BINARY.exists()
for path in (ARTIFACTS, TMP):
    info = path.lstat()
    assert stat.S_ISDIR(info.st_mode) and info.st_uid == 1000 and stat.S_IMODE(info.st_mode) == 0o700
assert not list(TMP.iterdir())
before = hashes()
version = subprocess.check_output(['go', 'version'], cwd=ROOT / 'backend', env=env).decode().strip()
assert version == 'go version go1.26.8 linux/amd64'
tool = pathlib.Path(subprocess.check_output(['go', 'env', 'GOTOOLDIR'], cwd=ROOT / 'backend', env=env).decode().strip())
compiler = {'tool_directory': str(tool), **{t + '_sha256': sha((tool / t).read_bytes()) for t in ('compile', 'link')}}
steps = []
for name, argv, extra in (
    ('helper-compile', ['go', 'build', '-trimpath', '-buildmode=exe', '-o', str(HELPER), './internal/dnsservice/testdata/native-dns-decisions'], {'CGO_ENABLED': '0', 'GOOS': 'linux', 'GOARCH': 'amd64'}),
    ('race-compile', ['go', 'test', '-c', '-race', '-o', str(BINARY), './internal/dnsservice'], {'CGO_ENABLED': '1', 'GOOS': 'linux', 'GOARCH': 'amd64'}),
):
    child_env = env | extra
    log = ARTIFACTS / ('example-' + name + '.log')
    start = time.monotonic()
    with log.open('xb') as capture:
        child = subprocess.run(argv, cwd=ROOT / 'backend', env=child_env, stdout=capture, stderr=subprocess.STDOUT)
    step = {'name': name, 'argv': argv, 'cwd': str(ROOT / 'backend'),
            'environment': {k: child_env[k] for k in ('GOTOOLCHAIN', 'GOMAXPROCS', 'GOFLAGS', 'TMPDIR', 'JD_DNS_DOMAIN_DECISIONS_LIVE', 'JD_DNS_SERVICES_LIVE_ENGINE', 'CGO_ENABLED', 'GOOS', 'GOARCH')},
            'exit_code': child.returncode, 'elapsed_seconds': round(time.monotonic() - start, 3),
            'raw_sha256': sha(log.read_bytes()), 'native_dispatch': False}
    (ARTIFACTS / ('example-' + name + '.result.json')).write_text(json.dumps(step, indent=2) + '\n')
    steps.append(step)
    print(json.dumps(step), flush=True)
    assert child.returncode == 0

assets = {}
for key, path in (('native-dns-decisions-phase-helper', HELPER), ('native-domain-decisions-phase-race.test', BINARY)):
    os.chmod(path, 0o555)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        first = os.fstat(fd)
        assert stat.S_ISREG(first.st_mode) and first.st_uid == 1000 and first.st_nlink == 1
        assert stat.S_IMODE(first.st_mode) == 0o555 and 0 < first.st_size <= 64 << 20
        data = bytearray()
        while True:
            part = os.read(fd, 1 << 20)
            if not part:
                break
            data.extend(part)
            assert len(data) <= 64 << 20
        fields = {'dev': 'st_dev', 'inode': 'st_ino', 'uid': 'st_uid', 'gid': 'st_gid', 'nlink': 'st_nlink', 'size': 'st_size', 'mtime_ns': 'st_mtime_ns', 'ctime_ns': 'st_ctime_ns'}
        receipt = {k: getattr(first, field) for k, field in fields.items()}
        for observed in (os.fstat(fd), path.lstat()):
            assert stat.S_ISREG(observed.st_mode) and stat.S_IMODE(observed.st_mode) == 0o555
            assert all(getattr(observed, field) == receipt[k] for k, field in fields.items())
        assert len(data) == first.st_size
        receipt.update(path=str(path), sha256=sha(data), mode=0o555,
                       go_version_m=subprocess.check_output(['go', 'version', '-m', str(path)], cwd=ROOT / 'backend', env=env).decode())
        assert 'go1.26.8' in receipt['go_version_m']
        assets[key] = receipt
    finally:
        os.close(fd)

elf = subprocess.check_output(['readelf', '-h', '-l', str(HELPER)])
assert b'ELF64' in elf and b'EXEC' in elf and b'INTERP' not in elf
(ARTIFACTS / 'example-helper-static-elf.txt').write_bytes(elf)
assert hashes() == before
assert compiler == {'tool_directory': str(tool), **{t + '_sha256': sha((tool / t).read_bytes()) for t in ('compile', 'link')}}
assert not list(TMP.iterdir())
parent_budget = pathlib.Path('/home/ubuntu/Just-Dashboard-network-dns-decision-restart-endpoint-artifacts/decision-phase-query-matrix-budget.json')
budget = json.loads(parent_budget.read_bytes())
budget['source_commit'] = SOURCE
budget['history_evidence'] = {'adguard_client_proto': 'required present empty string, plain encryption marker',
                            'selected_history_completeness': 'A+AAAA for selected allow and deny in each current phase',
                            'udp_tcp_basis': 'independent full wire matrices for both engines',
                            'adguard_transport_basis_log': 'wire_only_native_plain_encryption_marker',
                            'pihole_transport_basis_log': 'wire_only_native_transport_unreported'}
assert budget['total_question_ceiling'] == 256 and budget['maximum_questions_per_second'] == 8
(ARTIFACTS / 'decision-phase-query-matrix-budget.json').write_text(json.dumps(budget, indent=2) + '\n')
checks = {name: json.loads((ARTIFACTS / ('example-' + name + '.result.json')).read_bytes()) for name in ('required', 'focused-race')}
assert all(r['exit_code'] == 0 and not r['native_dispatch'] for r in checks.values())
record = {'phase': 'example_names_and_restart_transition_frozen_preparation_no_native_dispatch',
          'source_commit': SOURCE, 'base_commit': BASE, 'backend_source_count': len(before[0]),
          'backend_source_sha256': before[0], 'dependency_files_sha256': before[1],
          'clean_source': True, 'git_content_matches_current': True,
          'go_version': version, 'compiler': compiler, 'assets': assets, 'steps': steps,
          'checks': checks, 'static_elf_sha256': sha(elf), 'history_evidence': budget['history_evidence'],
          'history_phase': budget['history_phase'], 'query_matrix_budget_sha256': sha((ARTIFACTS / 'decision-phase-query-matrix-budget.json').read_bytes()),
          'preparer_sha256': sha(pathlib.Path(__file__).read_bytes()),
          'historical_actual_failures': [
              {'source': '16501fb23a9c972299b1060eb1623b4234bb3931', 'engine': 'adguard', 'elapsed_seconds': 17.372, 'cause': 'empty pinned persistent clients:null refused before questions', 'raw_path': '/home/ubuntu/Just-Dashboard-network-dns-domain-decisions-artifacts/native-decision-root-adguard.log', 'raw_sha256': 'fa486f57dddc398fe682bb997e61d02c92e75a47a15c2f812d37731d72f3d180', 'pihole_dispatched': False},
              {'source': '757e9d248def2ca3676cb6b8f13854e3adb75ea3', 'engine': 'adguard', 'elapsed_seconds': 53.792, 'successful_controlled_questions': 108, 'cause': 'pinned plain encryption marker incorrectly expected UDP/TCP', 'raw_path': '/home/ubuntu/Just-Dashboard-network-dns-decision-client-scope-artifacts/native-decision-client-scope-adguard.log', 'raw_sha256': '02d1d1c5d716ca49a8189d2818d4f787f294dc1c677e150b2740e438d7a3f698', 'pihole_dispatched': False},
          ], 'native_dispatch': False}
record['restart_transition'] = {'endpoint_rebind': 'exactly once after the owned restart: engine PID/endpoint ID/MAC only', 'per_query_guard_relaxation': False, 'query_budget_or_deadline_change': False, 'pre_effect_receipt': 'original verified engine/sidecar/bridge', 'failure_receipt': 'same original guard reads, no diagnostic reread', 'native_error': 'original wrapped error with closed stage/code/digest', 'helper_error': 'original exec state and exact whitelisted stderr code/digest'}
record['historical_actual_failures'].append({'source':'438830d790445aca63ee9f6dca6f777e18d9717d','engine':'adguard','elapsed_seconds':56.302,'successful_controlled_questions':108,'failed_post_restart_question_attempts':16,'first_native_history_corroboration':True,'actual_post_restart_cause':'unreported; original error/endpoint receipt absent','raw_path':'/home/ubuntu/Just-Dashboard-network-dns-decision-history-artifacts/native-decision-history-adguard.log','raw_sha256':'312111a9230ecd090e3b9084d04f55049587546a3f3645b4357f9a5d416a5359','pihole_dispatched':False})
record['historical_actual_failures'].append({'source':'053f095116ba282ee965e3616843544ec0fe9a9b','engine':'adguard','elapsed_seconds':55.894,'successful_controlled_questions':108,'failed_post_restart_question_attempts':16,'first_native_history_corroboration':True,'actual_post_restart_cause':'bridge_identity/engine_endpoint_changed: fresh engine endpoint ID and MAC after restart, same container/config/IPv4','raw_path':'/home/ubuntu/Just-Dashboard-network-dns-decision-restart-artifacts/native-decision-restart-adguard.log','raw_sha256':'622323e624f0c3b18cfabfce25496a6815c5225a61ca2a6221b3836289390b39','pihole_dispatched':False})
record['historical_actual_failures'].append({'source':'17aeaae33447e022df14ce1e78f46a5afb72fd3a','engine':'pihole','elapsed_seconds':26.997,'successful_controlled_questions':0,'actual_cause':'FTL generated server=/invalid/ answered .invalid locally; helper dns_response_shape_changed','raw_path':'/home/ubuntu/Just-Dashboard-network-dns-decision-restart-endpoint-artifacts/native-decision-endpoint-pihole.log','raw_sha256':'12da44ddb0de273c123847b17a4b99dcc89ddf01e6dd9500a4f58b24e9025da9'})
record['superseded_passes'] = [{'source':'17aeaae33447e022df14ce1e78f46a5afb72fd3a','engine':'adguard','elapsed_seconds':80.656,'questions':184,'raw_path':'/home/ubuntu/Just-Dashboard-network-dns-decision-restart-endpoint-artifacts/native-decision-endpoint-adguard.log','raw_sha256':'3f65d25108e13424108481df9f2eee50e87cf65956f3e6e8729c2756f6d18b6a'}]
with OUTPUT.open('x') as f:
    json.dump(record, f, indent=2)
    f.write('\n')
print(json.dumps({'compile_receipt': str(OUTPUT), 'sha256': sha(OUTPUT.read_bytes()), 'source': SOURCE,
                  'backend_source_count': len(before[0]), 'assets': {k: {'path': v['path'], 'sha256': v['sha256'], 'size': v['size'], 'mode': v['mode']} for k, v in assets.items()}, 'native_dispatch': False}), flush=True)
