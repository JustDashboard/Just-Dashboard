package deploy

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The process flags of a release's container beside its limits and its
// access switches: the user it runs as, a root filesystem it cannot write
// with the tmpfs paths it can, the kernel's per-process limits (ulimits),
// the kernel parameters a container has a namespace of its own for
// (sysctls), and the server's GPUs. They are what `docker run` and Coolify's
// custom Docker options carry and the plan did not. The runtime owner hands
// them to the creation of every container of a release — each replica, a
// canary's candidate, a container woken from sleep (the same container
// started again) — and the release comparison and preflight say them.
//
// A Compose stack declares them per service in its own file (user:,
// read_only: and tmpfs:, ulimits:, sysctls:, a device reservation), so a
// Compose plan refuses them here rather than holding a second answer the
// file would contradict.

// UlimitName is a resource limit Docker sets with --ulimit; the names are
// the ones the daemon accepts.
type UlimitName string

const (
	UlimitCore       UlimitName = "core"
	UlimitCPU        UlimitName = "cpu"
	UlimitData       UlimitName = "data"
	UlimitFsize      UlimitName = "fsize"
	UlimitLocks      UlimitName = "locks"
	UlimitMemlock    UlimitName = "memlock"
	UlimitMsgqueue   UlimitName = "msgqueue"
	UlimitNice       UlimitName = "nice"
	UlimitNofile     UlimitName = "nofile"
	UlimitNproc      UlimitName = "nproc"
	UlimitRss        UlimitName = "rss"
	UlimitRtprio     UlimitName = "rtprio"
	UlimitRttime     UlimitName = "rttime"
	UlimitSigpending UlimitName = "sigpending"
	UlimitStack      UlimitName = "stack"
)

// UlimitNames is every name a plan may set, in Docker's order of the alphabet.
var UlimitNames = []UlimitName{
	UlimitCore, UlimitCPU, UlimitData, UlimitFsize, UlimitLocks, UlimitMemlock, UlimitMsgqueue,
	UlimitNice, UlimitNofile, UlimitNproc, UlimitRss, UlimitRtprio, UlimitRttime, UlimitSigpending, UlimitStack,
}

// RuntimeUlimit is one limit: the soft value the process starts with and the
// hard ceiling it may raise it to. -1 is unlimited where the kernel allows
// it, as `--ulimit memlock=-1:-1` writes it.
type RuntimeUlimit struct {
	Name UlimitName `json:"name"`
	Soft int64      `json:"soft"`
	Hard int64      `json:"hard"`
}

// RuntimeTmpfs is a path the container may write in memory: on a read-only
// root filesystem, the only places outside its volumes it can. SizeMB caps
// it; zero is DefaultTmpfsSizeMB. Its pages count against the container's
// memory limit, as the kernel charges them to the container.
type RuntimeTmpfs struct {
	Path   string `json:"path"`
	SizeMB int64  `json:"sizeMb,omitempty"`
}

// RuntimeSysctl is one kernel parameter set in the container's own
// namespace.
type RuntimeSysctl struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// GPUMode is how many of the server's GPUs a container is given.
type GPUMode string

const (
	GPUAll     GPUMode = "all"
	GPUCount   GPUMode = "count"
	GPUDevices GPUMode = "devices"
)

// RuntimeGPUs asks Docker for NVIDIA GPUs (`--gpus`): every one, a number of
// them, or the ones named by index or UUID. Previews says a pull request
// preview of the environment gets them too; without it a preview's copy of
// the plan has none, because a preview runs code nobody has deployed yet.
type RuntimeGPUs struct {
	Mode      GPUMode  `json:"mode"`
	Count     int      `json:"count,omitempty"`
	DeviceIDs []string `json:"deviceIds,omitempty"`
	Previews  bool     `json:"previews,omitempty"`
}

// Bounds of the flags. Each list is short in practice; the caps keep a plan
// from carrying a pasted file. nofile stops at the kernel's default
// fs.nr_open, past which a container cannot start; nice and rtprio are the
// kernel's own ranges.
const (
	UlimitUnlimited        int64 = -1
	MinUlimitNofile              = 16
	MaxUlimitNofile              = 1 << 20
	MaxUlimitNice                = 40
	MaxUlimitRtprio              = 99
	maxUlimitValue               = 1 << 62
	MaxRuntimeTmpfs              = 16
	DefaultTmpfsSizeMB           = 64
	MaxTmpfsSizeMB               = 16 << 10
	MaxRuntimeSysctls            = 32
	MaxRuntimeGPUs               = 64
	MaxRuntimeGPUDeviceIDs       = 16
	maxRuntimeUserLength         = 65
	maxSysctlNameLength          = 128
	maxSysctlValueLength         = 256
	gpuDriver                    = "nvidia"
)

// namespacedKernelSysctls are the kernel.* parameters of the IPC namespace a
// container has its own of; with net.* (its network namespace) and
// fs.mqueue.* they are the set Docker lets a container set.
var namespacedKernelSysctls = []string{
	"kernel.msgmax", "kernel.msgmnb", "kernel.msgmni", "kernel.sem",
	"kernel.shmall", "kernel.shmmax", "kernel.shmmni", "kernel.shm_rmid_forced",
}

var (
	runtimeUserPartRE = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_.-]{0,31}|[0-9]{1,10})$`)
	sysctlNameRE      = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[A-Za-z0-9_][A-Za-z0-9_/-]*)+$`)
	sysctlValueRE     = regexp.MustCompile(`^[A-Za-z0-9._:,/+=-]+(?:[ \t]+[A-Za-z0-9._:,/+=-]+)*$`)
	gpuDeviceIDRE     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

// staticRootWrites are the places nginx writes when it serves a static site:
// its temporary files' directories under /var/cache/nginx, made at start,
// and its PID file in /run (/var/run is a link to it in the image).
var staticRootWrites = []string{"/var/cache/nginx", "/run"}

// EffectiveTmpfs is what the container is given: the plan's paths, each at
// its size, or /tmp alone for a read-only root that names none, because a
// process with nowhere to write a temporary file is broken in ways its
// output rarely says.
func (c RuntimePlanConfig) EffectiveTmpfs() []RuntimeTmpfs {
	if len(c.Tmpfs) == 0 {
		if c.ReadOnlyRootfs {
			return []RuntimeTmpfs{{Path: "/tmp", SizeMB: DefaultTmpfsSizeMB}}
		}
		return nil
	}
	out := make([]RuntimeTmpfs, 0, len(c.Tmpfs))
	for _, entry := range c.Tmpfs {
		if entry.SizeMB == 0 {
			entry.SizeMB = DefaultTmpfsSizeMB
		}
		out = append(out, entry)
	}
	return out
}

// hasProcessFlags says the plan sets any of the five.
func (c RuntimePlanConfig) hasProcessFlags() bool {
	return c.User != "" || c.ReadOnlyRootfs || len(c.Tmpfs) > 0 || len(c.Ulimits) > 0 ||
		len(c.Sysctls) > 0 || c.GPUs != nil
}

// canonicalRuntimeFlags spells the flags one way, as copies so the caller's
// plan is not written through: the user trimmed, tmpfs paths cleaned and in
// path order, limits and parameters in name order, a GPU request with no
// mode and nothing else in it dropped. Empty lists stay absent, so a plan
// that never set one keeps the bytes, and the digest, it had.
func canonicalRuntimeFlags(c *RuntimePlanConfig) {
	c.User = strings.TrimSpace(c.User)
	if len(c.Tmpfs) == 0 {
		c.Tmpfs = nil
	} else {
		tmpfs := make([]RuntimeTmpfs, 0, len(c.Tmpfs))
		for _, entry := range c.Tmpfs {
			cleaned := strings.TrimSpace(entry.Path)
			if cleaned != "" {
				cleaned = path.Clean(cleaned)
			}
			tmpfs = append(tmpfs, RuntimeTmpfs{Path: cleaned, SizeMB: entry.SizeMB})
		}
		slices.SortStableFunc(tmpfs, func(a, b RuntimeTmpfs) int { return strings.Compare(a.Path, b.Path) })
		c.Tmpfs = tmpfs
	}
	if len(c.Ulimits) == 0 {
		c.Ulimits = nil
	} else {
		ulimits := make([]RuntimeUlimit, 0, len(c.Ulimits))
		for _, limit := range c.Ulimits {
			limit.Name = UlimitName(strings.ToLower(strings.TrimSpace(string(limit.Name))))
			ulimits = append(ulimits, limit)
		}
		slices.SortStableFunc(ulimits, func(a, b RuntimeUlimit) int { return strings.Compare(string(a.Name), string(b.Name)) })
		c.Ulimits = ulimits
	}
	if len(c.Sysctls) == 0 {
		c.Sysctls = nil
	} else {
		sysctls := make([]RuntimeSysctl, 0, len(c.Sysctls))
		for _, parameter := range c.Sysctls {
			sysctls = append(sysctls, RuntimeSysctl{Name: strings.TrimSpace(parameter.Name), Value: strings.TrimSpace(parameter.Value)})
		}
		slices.SortStableFunc(sysctls, func(a, b RuntimeSysctl) int { return strings.Compare(a.Name, b.Name) })
		c.Sysctls = sysctls
	}
	if gpus := c.GPUs; gpus != nil {
		request := RuntimeGPUs{
			Mode: GPUMode(strings.ToLower(strings.TrimSpace(string(gpus.Mode)))), Count: gpus.Count, Previews: gpus.Previews,
		}
		for _, id := range gpus.DeviceIDs {
			request.DeviceIDs = append(request.DeviceIDs, strings.TrimSpace(id))
		}
		if request.Mode == "" && request.Count == 0 && len(request.DeviceIDs) == 0 {
			c.GPUs = nil
		} else {
			c.GPUs = &request
		}
	}
}

// validateRuntimeFlags is the plan's own rule for the five, so a draft, a
// settings save, `jd config set` and a blueprint are refused alike, each
// naming the field a control attaches to.
func validateRuntimeFlags(c PlanConfiguration) error {
	runtime := c.Runtime
	if c.Build.Method == BuildCompose || c.Build.Method == BuildLegacyCompose {
		if field, key := composeRuntimeFlag(runtime); field != "" {
			return invalidField(field, "a Compose stack sets this per service in its own file (%s); set it there instead", key)
		}
		return nil
	}
	if err := validateRuntimeUser(runtime.User); err != nil {
		return err
	}
	if err := validateRuntimeTmpfs(c); err != nil {
		return err
	}
	if err := validateRuntimeUlimits(runtime.Ulimits); err != nil {
		return err
	}
	if err := validateRuntimeSysctls(runtime); err != nil {
		return err
	}
	return validateRuntimeGPUs(runtime.GPUs)
}

// composeRuntimeFlag names the first flag a Compose plan carries, with the
// key its file has for it.
func composeRuntimeFlag(runtime RuntimePlanConfig) (string, string) {
	switch {
	case runtime.User != "":
		return "runtime.user", "user:"
	case runtime.ReadOnlyRootfs:
		return "runtime.readOnlyRootfs", "read_only:"
	case len(runtime.Tmpfs) > 0:
		return "runtime.tmpfs", "tmpfs:"
	case len(runtime.Ulimits) > 0:
		return "runtime.ulimits", "ulimits:"
	case len(runtime.Sysctls) > 0:
		return "runtime.sysctls", "sysctls:"
	case runtime.GPUs != nil:
		return "runtime.gpus", "deploy.resources.reservations.devices"
	}
	return "", ""
}

// validateRuntimeUser checks the shape Docker takes — a user and optionally
// a group, each a name or a number. Whether the image knows a name is the
// image's to say, at start.
func validateRuntimeUser(user string) error {
	if user == "" {
		return nil
	}
	parts := strings.Split(user, ":")
	if len(user) > maxRuntimeUserLength || len(parts) > 2 {
		return invalidField("runtime.user", "the user is a name or a number, optionally with a group after a colon, such as 1000:1000 or node")
	}
	for _, part := range parts {
		if !runtimeUserPartRE.MatchString(part) {
			return invalidField("runtime.user", "the user is a name or a number, optionally with a group after a colon, such as 1000:1000 or node")
		}
		if part[0] >= '0' && part[0] <= '9' {
			if id, err := strconv.ParseUint(part, 10, 32); err != nil || id >= 1<<32-1 {
				return invalidField("runtime.user", "a user or group number is at most 4294967294")
			}
		}
	}
	return nil
}

// validateRuntimeTmpfs checks the writable paths and the read-only root's
// one conflict this plan can show: a static site's nginx, which writes its
// cache and PID file under the root, with nowhere writable for them.
func validateRuntimeTmpfs(c PlanConfiguration) error {
	runtime := c.Runtime
	if len(runtime.Tmpfs) > MaxRuntimeTmpfs {
		return invalidField("runtime.tmpfs", "at most %d writable paths", MaxRuntimeTmpfs)
	}
	mountTargets := map[string]bool{}
	for _, mount := range runtime.Mounts {
		mountTargets[path.Clean(mount.Target)] = true
	}
	seen := map[string]bool{}
	for index, entry := range runtime.Tmpfs {
		field := fmt.Sprintf("runtime.tmpfs[%d]", index)
		raw := strings.TrimSpace(entry.Path)
		cleaned := path.Clean(raw)
		switch {
		case raw == "" || !path.IsAbs(cleaned) || strings.ContainsAny(raw, "\x00\r\n\t ,:"):
			return invalidField(field, "a writable path is an absolute path inside the container, such as /tmp")
		case cleaned == "/":
			return invalidField(field, "the root itself cannot be a writable path; turn the read-only root filesystem off instead")
		case underPath(cleaned, "/proc") || underPath(cleaned, "/sys"):
			return invalidField(field, "%s belongs to the kernel and cannot be a writable path", cleaned)
		case seen[cleaned]:
			return invalidField(field, "%s is listed twice", cleaned)
		case mountTargets[cleaned]:
			return invalidField(field, "%s is already where a volume is mounted, which is writable unless it is mounted read-only", cleaned)
		case entry.SizeMB < 0 || entry.SizeMB > MaxTmpfsSizeMB:
			return invalidField(field, "a writable path holds 1 to %d MiB, or zero for %d MiB", MaxTmpfsSizeMB, DefaultTmpfsSizeMB)
		}
		seen[cleaned] = true
	}
	if !runtime.ReadOnlyRootfs || !servedByNginx(c.Build) {
		return nil
	}
	missing := []string{}
	for _, needed := range staticRootWrites {
		if !writableAt(runtime, needed) {
			missing = append(missing, needed)
		}
	}
	if len(missing) > 0 {
		return invalidField("runtime.tmpfs", "this site is served by nginx, which writes its temporary files under /var/cache/nginx and its PID file in /run; add %s as writable paths, or leave the root filesystem writable", strings.Join(missing, " and "))
	}
	return nil
}

// servedByNginx says the release's image ends in the catalogue's nginx: a
// static site, or a recipe that serves its build's output directory.
func servedByNginx(build BuildPlanConfig) bool {
	return build.Method == BuildStatic || (build.Method == BuildRecipe && strings.TrimSpace(build.OutputDirectory) != "")
}

// writableAt says a path is writable on a read-only root: a tmpfs or a
// writable mount at it or above it. /var/run is /run in every image the
// plans here start from.
func writableAt(runtime RuntimePlanConfig, target string) bool {
	places := []string{}
	for _, entry := range runtime.EffectiveTmpfs() {
		places = append(places, path.Clean(entry.Path))
	}
	for _, mount := range runtime.Mounts {
		if !mount.ReadOnly {
			places = append(places, path.Clean(mount.Target))
		}
	}
	for _, place := range places {
		if place == "/var/run" {
			place = "/run"
		}
		if underPath(target, place) {
			return true
		}
	}
	return false
}

// underPath says target is base or inside it.
func underPath(target, base string) bool {
	return target == base || strings.HasPrefix(target, strings.TrimSuffix(base, "/")+"/")
}

func validateRuntimeUlimits(ulimits []RuntimeUlimit) error {
	if len(ulimits) > len(UlimitNames) {
		return invalidField("runtime.ulimits", "at most one limit of each of the %d names", len(UlimitNames))
	}
	seen := map[UlimitName]bool{}
	for index, limit := range ulimits {
		field := fmt.Sprintf("runtime.ulimits[%d]", index)
		if !slices.Contains(UlimitNames, limit.Name) {
			return invalidField(field, "%q is not a limit Docker sets; use one of %s", limit.Name, ulimitNameList())
		}
		if seen[limit.Name] {
			return invalidField(field, "%s is set twice", limit.Name)
		}
		seen[limit.Name] = true
		low, high, unlimited := ulimitRange(limit.Name)
		for _, value := range []int64{limit.Soft, limit.Hard} {
			if value == UlimitUnlimited && unlimited {
				continue
			}
			if value < low || value > high {
				if unlimited {
					return invalidField(field, "%s is between %d and %d, or -1 for unlimited", limit.Name, low, high)
				}
				return invalidField(field, "%s is between %d and %d", limit.Name, low, high)
			}
		}
		if limit.Hard != UlimitUnlimited && (limit.Soft == UlimitUnlimited || limit.Soft > limit.Hard) {
			return invalidField(field, "the soft %s limit cannot be above the hard one", limit.Name)
		}
	}
	return nil
}

// ulimitRange is the values a limit may take, and whether -1 (unlimited) is
// one of them.
func ulimitRange(name UlimitName) (int64, int64, bool) {
	switch name {
	case UlimitNofile:
		return MinUlimitNofile, MaxUlimitNofile, false
	case UlimitNice:
		return 0, MaxUlimitNice, false
	case UlimitRtprio:
		return 0, MaxUlimitRtprio, false
	}
	return 0, maxUlimitValue, true
}

func ulimitNameList() string {
	names := make([]string, 0, len(UlimitNames))
	for _, name := range UlimitNames {
		names = append(names, string(name))
	}
	return strings.Join(names, ", ")
}

// validateRuntimeSysctls holds parameters to the namespaced set: a
// container may set its own network stack (net.*), its POSIX message queues
// (fs.mqueue.*) and its System V IPC limits, and nothing that is the
// server's. On the host's network the network stack is the server's.
func validateRuntimeSysctls(runtime RuntimePlanConfig) error {
	if len(runtime.Sysctls) > MaxRuntimeSysctls {
		return invalidField("runtime.sysctls", "at most %d kernel parameters", MaxRuntimeSysctls)
	}
	seen := map[string]bool{}
	for index, parameter := range runtime.Sysctls {
		field := fmt.Sprintf("runtime.sysctls[%d]", index)
		name := strings.TrimSpace(parameter.Name)
		if len(name) > maxSysctlNameLength || !sysctlNameRE.MatchString(name) {
			return invalidField(field, "a kernel parameter is written as its dotted name, such as net.core.somaxconn")
		}
		if !namespacedSysctl(name) {
			return invalidField(field, "%s is the server's, not the container's; a container may set net.*, fs.mqueue.* and %s", name, strings.Join(namespacedKernelSysctls, ", "))
		}
		if runtime.HostNetwork && strings.HasPrefix(name, "net.") {
			return invalidField(field, "%s would change the server's own network stack: on the host network the container has none of its own", name)
		}
		if seen[name] {
			return invalidField(field, "%s is set twice", name)
		}
		seen[name] = true
		value := strings.TrimSpace(parameter.Value)
		if len(value) > maxSysctlValueLength || !sysctlValueRE.MatchString(value) {
			return invalidField(field, "the value of %s is a number, a word or numbers separated by spaces, such as 1024 or 250 32000 32 128", name)
		}
	}
	return nil
}

func namespacedSysctl(name string) bool {
	return strings.HasPrefix(name, "net.") || strings.HasPrefix(name, "fs.mqueue.") || slices.Contains(namespacedKernelSysctls, name)
}

func validateRuntimeGPUs(gpus *RuntimeGPUs) error {
	if gpus == nil {
		return nil
	}
	switch gpus.Mode {
	case GPUAll:
		if gpus.Count != 0 || len(gpus.DeviceIDs) > 0 {
			return invalidField("runtime.gpus", "every GPU names no count and no devices")
		}
	case GPUCount:
		if gpus.Count < 1 || gpus.Count > MaxRuntimeGPUs || len(gpus.DeviceIDs) > 0 {
			return invalidField("runtime.gpus.count", "a number of GPUs is 1 to %d, and names no devices", MaxRuntimeGPUs)
		}
	case GPUDevices:
		if gpus.Count != 0 || len(gpus.DeviceIDs) == 0 || len(gpus.DeviceIDs) > MaxRuntimeGPUDeviceIDs {
			return invalidField("runtime.gpus.deviceIds", "name 1 to %d GPUs by index or UUID, such as 0 or GPU-3a1b…", MaxRuntimeGPUDeviceIDs)
		}
		seen := map[string]bool{}
		for index, id := range gpus.DeviceIDs {
			if !gpuDeviceIDRE.MatchString(id) || seen[id] {
				return invalidField(fmt.Sprintf("runtime.gpus.deviceIds[%d]", index), "a GPU is named once, by its index or its UUID")
			}
			seen[id] = true
		}
	default:
		return invalidField("runtime.gpus.mode", "GPUs are every one, a number of them, or the devices named")
	}
	return nil
}

// dockerUlimits, dockerSysctls and dockerGPUs are the flags as container
// creation takes them.
func (c RuntimePlanConfig) dockerUlimits() []dockerx.Ulimit {
	if len(c.Ulimits) == 0 {
		return nil
	}
	out := make([]dockerx.Ulimit, 0, len(c.Ulimits))
	for _, limit := range c.Ulimits {
		out = append(out, dockerx.Ulimit{Name: string(limit.Name), Soft: limit.Soft, Hard: limit.Hard})
	}
	return out
}

func (c RuntimePlanConfig) dockerSysctls() map[string]string {
	if len(c.Sysctls) == 0 {
		return nil
	}
	out := make(map[string]string, len(c.Sysctls))
	for _, parameter := range c.Sysctls {
		out[parameter.Name] = parameter.Value
	}
	return out
}

func (c RuntimePlanConfig) dockerGPUs() *dockerx.GPURequest {
	gpus := c.GPUs
	if gpus == nil {
		return nil
	}
	switch gpus.Mode {
	case GPUAll:
		return &dockerx.GPURequest{Driver: gpuDriver, Count: -1}
	case GPUCount:
		return &dockerx.GPURequest{Driver: gpuDriver, Count: gpus.Count}
	case GPUDevices:
		return &dockerx.GPURequest{Driver: gpuDriver, DeviceIDs: slices.Clone(gpus.DeviceIDs)}
	}
	return nil
}

// tmpfsMountSpecs are the writable paths as mounts.
func (c RuntimePlanConfig) tmpfsMountSpecs() []dockerx.MountSpec {
	tmpfs := c.EffectiveTmpfs()
	mounts := make([]dockerx.MountSpec, 0, len(tmpfs))
	for _, entry := range tmpfs {
		mounts = append(mounts, dockerx.MountSpec{Type: "tmpfs", Target: entry.Path, SizeMB: entry.SizeMB})
	}
	return mounts
}

// Labels for the comparison and preflight, each empty when the flag is off,
// so a release before the flags existed compares equal to one that does not
// set them.

func readOnlyRootLabel(c RuntimePlanConfig) string {
	if !c.ReadOnlyRootfs {
		return ""
	}
	return "on · writable " + tmpfsLabel(c.EffectiveTmpfs())
}

func tmpfsLabel(tmpfs []RuntimeTmpfs) string {
	parts := make([]string, 0, len(tmpfs))
	for _, entry := range tmpfs {
		size := entry.SizeMB
		if size == 0 {
			size = DefaultTmpfsSizeMB
		}
		parts = append(parts, fmt.Sprintf("%s (%d MiB)", entry.Path, size))
	}
	return strings.Join(parts, ", ")
}

// writablePathsLabel is the tmpfs paths a writable root also has, which the
// read-only label says otherwise.
func writablePathsLabel(c RuntimePlanConfig) string {
	if c.ReadOnlyRootfs {
		return ""
	}
	return tmpfsLabel(c.Tmpfs)
}

func ulimitsLabel(ulimits []RuntimeUlimit) string {
	parts := make([]string, 0, len(ulimits))
	for _, limit := range ulimits {
		parts = append(parts, string(limit.Name)+"="+ulimitValueLabel(limit.Soft)+":"+ulimitValueLabel(limit.Hard))
	}
	return strings.Join(parts, ", ")
}

func ulimitValueLabel(value int64) string {
	if value == UlimitUnlimited {
		return "unlimited"
	}
	return strconv.FormatInt(value, 10)
}

func sysctlsLabel(sysctls []RuntimeSysctl) string {
	parts := make([]string, 0, len(sysctls))
	for _, parameter := range sysctls {
		parts = append(parts, parameter.Name+"="+parameter.Value)
	}
	return strings.Join(parts, ", ")
}

func gpusLabel(gpus *RuntimeGPUs) string {
	if gpus == nil {
		return ""
	}
	label := ""
	switch gpus.Mode {
	case GPUAll:
		label = "all"
	case GPUCount:
		label = strconv.Itoa(gpus.Count)
	case GPUDevices:
		label = strings.Join(gpus.DeviceIDs, ", ")
	}
	if gpus.Previews {
		label += " · previews too"
	}
	return label
}

// runtimeFlagFindings say the flags back before Deploy, with what each means
// for the application, and warn where the server may not give what the plan
// asks: tmpfs paths that could hold more than the memory limit allows, and
// GPUs on a Docker that lists no NVIDIA runtime.
func runtimeFlagFindings(configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	runtime := configuration.Runtime
	findings := []PreflightFinding{}
	if runtime.User != "" {
		findings = append(findings, finding("runtime_user", PreflightPass,
			"Runs as "+runtime.User, runtime.User,
			"The application's processes run as this user instead of the image's own. A name must exist in the image, and what it writes into a volume belongs to it.",
			"", "docker", "runtime.user"))
	}
	tmpfs := runtime.EffectiveTmpfs()
	switch {
	case runtime.ReadOnlyRootfs:
		findings = append(findings, finding("runtime_read_only_root", PreflightPass,
			"The root filesystem is read-only", "writable: "+tmpfsLabel(tmpfs),
			"The application can write only to these paths and to its volumes; a write anywhere else fails with \"Read-only file system\". What is written to these paths is kept in memory and lost when the container stops.",
			"", "docker", "runtime.readOnlyRootfs"))
	case len(tmpfs) > 0:
		findings = append(findings, finding("runtime_tmpfs", PreflightPass,
			"Writable paths are kept in memory", tmpfsLabel(tmpfs),
			"What is written to these paths is kept in memory and lost when the container stops.",
			"", "docker", "runtime.tmpfs"))
	}
	var tmpfsTotal int64
	for _, entry := range tmpfs {
		tmpfsTotal += entry.SizeMB
	}
	if runtime.MemoryMB > 0 && tmpfsTotal > runtime.MemoryMB {
		findings = append(findings, finding("runtime_tmpfs_exceeds_memory", PreflightWarning,
			"The writable paths can hold more than the memory limit",
			fmt.Sprintf("%d MiB of writable paths, %d MiB memory limit", tmpfsTotal, runtime.MemoryMB),
			"What is written to them is memory the container is charged for, so filling them reaches the memory limit first and the kernel stops the container.",
			"Lower their sizes, or raise the memory limit.", "docker", "runtime.tmpfs"))
	}
	if len(runtime.Ulimits) > 0 {
		findings = append(findings, finding("runtime_ulimits", PreflightPass,
			"Process limits are set", ulimitsLabel(runtime.Ulimits),
			"Each process starts at the soft limit and may raise itself to the hard one, in place of the limits Docker's daemon gives every container.",
			"", "docker", "runtime.ulimits"))
	}
	if len(runtime.Sysctls) > 0 {
		findings = append(findings, finding("runtime_sysctls", PreflightPass,
			"Kernel parameters are set", sysctlsLabel(runtime.Sysctls),
			"They are set in the container's own namespaces when it starts and change nothing for the rest of the server.",
			"", "docker", "runtime.sysctls"))
	}
	if runtime.GPUs != nil {
		requested := gpusLabel(runtime.GPUs)
		observed := observation.GPURuntime
		switch {
		case observed == nil || !observed.Observed:
			detail := "not observed"
			if observed != nil && observed.Detail != "" {
				detail = observed.Detail
			}
			findings = append(findings, finding("runtime_gpu_runtime_unknown", PreflightWarning,
				"Whether Docker can give the GPUs is unknown", detail,
				"The release asks for GPUs ("+requested+"). If Docker cannot give them, the candidate does not start and the run says runtime_gpu_unavailable.",
				"Check that the NVIDIA driver and the NVIDIA Container Toolkit are installed on this server.", "docker", "runtime.gpus"))
		case !observed.NVIDIA:
			findings = append(findings, finding("runtime_gpu_runtime_missing", PreflightWarning,
				"Docker on this server has no NVIDIA runtime", "runtimes: "+strings.Join(observed.Runtimes, ", "),
				"The release asks for GPUs ("+requested+"), and docker info lists no nvidia runtime, so Docker will most likely refuse to start the container; the run then says runtime_gpu_unavailable.",
				"Install the NVIDIA driver and the NVIDIA Container Toolkit, run nvidia-ctk runtime configure --runtime=docker and restart Docker, or remove the GPU request.",
				"docker", "runtime.gpus"))
		default:
			findings = append(findings, finding("runtime_gpus", PreflightPass,
				"The container is given GPUs", requested,
				"Docker's NVIDIA runtime is installed; the container sees the GPUs it asks for, through the driver on this server.",
				"", "docker", "runtime.gpus"))
		}
	}
	return findings
}
