import pathlib,subprocess,json,hashlib,os,time,tempfile,sys,stat
root=pathlib.Path('/home/ubuntu/Just-Dashboard-network-dns-query-history-shapes')
artifacts=pathlib.Path('/home/ubuntu/jd-network-validation-tmp/dns-query-history-shapes')
binary=artifacts/'dns-query-native.test'
engine=sys.argv[1]
assert engine in ('adguard','pihole','technitium')
prefix=artifacts/('native-'+engine)
assert not prefix.with_suffix('.log').exists()
assert subprocess.check_output(['git','status','--porcelain'],cwd=root)==b''
sha=lambda b:hashlib.sha256(b).hexdigest()
prepared=json.loads((artifacts/'compile.result.json').read_text())
assert prepared['source']=='4ce967f1f4c8e604ab2f39ecfe02fc4e756a6be3'
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=root).decode().strip()==prepared['source']
assert sha(binary.read_bytes())==prepared['binary_sha256']=='b262c01738792ca6c964056a2f3b578c5ed394a44d1db6f1ed8f5a6375dd592c'
assert {str(p.relative_to(root)):sha(p.read_bytes()) for p in sorted((root/'backend').rglob('*.go'))}==prepared['backend_source_sha256']
assert {f:sha((root/f).read_bytes()) for f in ['backend/go.mod','backend/go.sum']}==prepared['dependency_files_sha256']
docker_endpoint='unix:///var/run/docker.sock'
docker_env=os.environ.copy()
for key in ('DOCKER_HOST','DOCKER_CONTEXT','DOCKER_TLS_VERIFY','DOCKER_CERT_PATH','DOCKER_API_VERSION'):
 docker_env.pop(key,None)
def docker_socket():
 st=pathlib.Path('/var/run/docker.sock').stat()
 assert stat.S_ISSOCK(st.st_mode)
 return {'device':st.st_dev,'inode':st.st_ino,'uid':st.st_uid,'gid':st.st_gid,'mode':st.st_mode}
def inventory():
 return {kind:subprocess.check_output(['docker','--host',docker_endpoint,kind,'ls','-q','--filter','label=io.justdashboard.dns.provision'],env=docker_env).decode().splitlines() for kind in ('container','network','volume')}
def host():
 return {'resolver_sha256':sha(pathlib.Path('/etc/resolv.conf').read_bytes()),'networkd883_start':pathlib.Path('/proc/883/stat').read_text().rsplit(')',1)[1].split()[19],'nm_paths':{p:os.path.lexists(p) for p in ['/etc/NetworkManager','/var/lib/NetworkManager']}}
before=host();socket_before=docker_socket();prior=inventory();assert not any(prior.values()),prior
task=pathlib.Path(tempfile.mkdtemp(prefix='n-'+engine[:3]+'-',dir='/home/ubuntu/jd-df-t'))
env=os.environ.copy()
for key in tuple(env):
 if key.startswith('JD_DNS_SERVICES_'):del env[key]
env.update({'JD_DNS_SERVICES_LIVE_ENGINE':engine,'JD_DNS_SERVICES_DOCKER_HOST':docker_endpoint,'TMPDIR':str(task),'GOMAXPROCS':'2','GOFLAGS':'-p=2'})
argv=['timeout','--kill-after=5s','240s',str(binary),'-test.run','^TestDNSServiceNativeOwnedEngine$','-test.count=1','-test.timeout=235s','-test.v']
manifest={'source':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root).decode().strip(),'binary_sha256':sha(binary.read_bytes()),'runner_sha256':sha(pathlib.Path(__file__).read_bytes()),'backend_source_sha256':{str(p.relative_to(root)):sha(p.read_bytes()) for p in sorted((root/'backend').rglob('*.go'))},'dependency_files_sha256':{f:sha((root/f).read_bytes()) for f in ['backend/go.mod','backend/go.sum']},'argv':argv,'cwd':str(root/'backend/internal/dnsservice'),'environment':{k:env[k] for k in ['JD_DNS_SERVICES_LIVE_ENGINE','JD_DNS_SERVICES_DOCKER_HOST','TMPDIR','GOMAXPROCS','GOFLAGS']},'host_before':before,'owned_docker_before':prior,'docker_endpoint':docker_endpoint,'docker_socket_before':socket_before}
prefix.with_suffix('.source-binary.json').write_text(json.dumps(manifest,indent=2)+'\n')
started=time.monotonic()
with prefix.with_suffix('.log').open('xb') as log:
 completed=subprocess.run(argv,cwd=root/'backend/internal/dnsservice',env=env,stdout=log,stderr=subprocess.STDOUT)
wall=time.monotonic()-started
raw=prefix.with_suffix('.log').read_bytes()
processes=[]
for entry in pathlib.Path('/proc').iterdir():
 if not entry.name.isdigit():continue
 try:args=(entry/'cmdline').read_bytes().split(b'\0')
 except (FileNotFoundError,PermissionError,ProcessLookupError):continue
 if str(binary).encode() in args:processes.append({'pid':int(entry.name),'argv':[a.decode(errors='replace') for a in args if a]})
after=host();owned=inventory();remaining=[p.name for p in task.iterdir()]
socket_after=docker_socket()
source_after={str(p.relative_to(root)):sha(p.read_bytes()) for p in sorted((root/'backend').rglob('*.go'))}
modules_after={f:sha((root/f).read_bytes()) for f in ['backend/go.mod','backend/go.sum']}
binary_after=sha(binary.read_bytes())
frozen_source_unchanged=source_after==prepared['backend_source_sha256'] and modules_after==prepared['dependency_files_sha256'] and binary_after==prepared['binary_sha256'] and subprocess.check_output(['git','rev-parse','HEAD'],cwd=root).decode().strip()==prepared['source'] and sha(pathlib.Path(__file__).read_bytes())==manifest['runner_sha256']
if not remaining:task.rmdir()
result={'source':manifest['source'],'engine':engine,'exit_code':completed.returncode,'wall_seconds':round(wall,3),'raw_sha256':sha(raw),'skipped':b'--- SKIP:' in raw,'owned_processes_remaining':processes,'owned_docker_remaining':owned,'owned_tmp_entries_remaining':remaining,'host_after':after,'host_unchanged':before==after,'frozen_source_unchanged':frozen_source_unchanged,'binary_sha256_after':binary_after,'docker_socket_after':socket_after,'docker_socket_unchanged':socket_before==socket_after}
prefix.with_suffix('.result.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result,indent=2),flush=True)
print(raw.decode()[-6000:],flush=True)
if completed.returncode or processes or any(owned.values()) or remaining or before!=after or not frozen_source_unchanged or socket_before!=socket_after or result['skipped']:raise SystemExit(1)
