package deploy

import (
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Which CPython a project can build on is often decided by its oldest pin: a
// pip freeze from 2024 pins numpy 1.26 and pydantic-core 2.14, which publish
// no wheel for Python 3.13, and on an image with no compiler and no Rust pip
// falls back to building them and fails. Locks answer this exactly — every
// wheel they may install is listed by file name — and for a requirement file
// the table below answers it for the compiled distributions freezes commonly
// pin. A few pure-Python releases instead import a module a newer Python
// removed.

// pythonCatalogueMinors are the CPython families the recipe builds on.
var pythonCatalogueMinors = []int{10, 11, 12, 13, 14}

// pythonWheelFirsts is the first release of a distribution that published a
// manylinux wheel for each catalogue family (3.10 to 3.14), on x86_64 and,
// where it differs, aarch64. An empty entry is a family it never published
// for. Generated from PyPI's JSON API: a release counts for 3.N when one of
// its manylinux wheels is tagged cp3N-cp3N, or abi3 from an earlier cp3M.
type pythonWheelFirsts struct {
	amd64, arm64 [5]string
}

func (f pythonWheelFirsts) first(arch string, minor int) (string, bool) {
	index := minor - pythonCatalogueMinors[0]
	if index < 0 || index >= len(f.amd64) {
		return "", false
	}
	if arch == "arm64" && f.arm64[index] != "" {
		return f.arm64[index], true
	}
	return f.amd64[index], true
}

var pythonWheelReleases = map[string]pythonWheelFirsts{
	"asyncpg":          {amd64: [5]string{"0.24.0", "0.27.0", "0.29.0", "0.30.0", "0.31.0"}, arm64: [5]string{"0.27.0", "0.27.0", "0.29.0", "0.30.0", "0.31.0"}},
	"av":               {amd64: [5]string{"12.0.0", "12.0.0", "12.0.0", "13.0.0", "15.1.0"}},
	"blis":             {amd64: [5]string{"0.7.5", "0.7.9", "0.7.11", "1.0.2", "1.3.2"}, arm64: [5]string{"0.7.8", "0.7.9", "0.7.11", "1.3.0", "1.3.2"}},
	"bottleneck":       {amd64: [5]string{"1.3.3", "1.3.7", "1.3.8", "1.4.2", "1.6.0"}, arm64: [5]string{"1.5.0", "1.5.0", "1.5.0", "1.5.0", "1.6.0"}},
	"cffi":             {amd64: [5]string{"1.15.0", "1.15.1", "1.16.0", "1.17.0", "2.0.0"}},
	"confluent-kafka":  {amd64: [5]string{"1.9.0", "2.0.2", "2.3.0", "2.6.0", "2.12.1"}, arm64: [5]string{"2.1.0", "2.1.0", "2.3.0", "2.6.0", "2.12.1"}},
	"contourpy":        {amd64: [5]string{"0.0.4", "1.0.5", "1.1.1", "1.3.0", "1.3.3"}, arm64: [5]string{"0.0.5", "1.0.5", "1.1.1", "1.3.0", "1.3.3"}},
	"cymem":            {amd64: [5]string{"2.0.6", "2.0.7", "2.0.8", "2.0.10", "2.0.12"}, arm64: [5]string{"2.0.6", "2.0.7", "2.0.8", "2.0.11", "2.0.12"}},
	"faiss-cpu":        {amd64: [5]string{"1.7.1.post3", "1.7.3", "1.8.0", "1.9.0.post1", "1.12.0"}},
	"gevent":           {amd64: [5]string{"21.12.0", "22.10.1", "23.7.0", "24.10.1", "25.8.1"}},
	"greenlet":         {amd64: [5]string{"1.1.0", "1.1.3", "3.0.0", "3.1.0", "3.2.3"}},
	"grpcio":           {amd64: [5]string{"1.41.0", "1.49.1", "1.59.0", "1.66.2", "1.75.1"}, arm64: [5]string{"1.41.0", "1.53.0", "1.59.0", "1.66.2", "1.75.1"}},
	"h5py":             {amd64: [5]string{"3.6.0", "3.8.0", "3.10.0", "3.12.1", "3.15.0"}, arm64: [5]string{"3.7.0", "3.8.0", "3.12.1", "3.12.1", "3.15.0"}},
	"httptools":        {amd64: [5]string{"0.3.0", "0.5.0", "0.6.1", "0.6.2", "0.7.1"}},
	"jiter":            {amd64: [5]string{"0.1.0", "0.1.0", "0.1.0", "0.6.0", "0.10.0"}},
	"kiwisolver":       {amd64: [5]string{"1.3.2", "1.4.4", "1.4.5", "1.4.7", "1.4.9"}},
	"levenshtein":      {amd64: [5]string{"0.16.0", "0.20.3", "0.22.0", "0.26.0", "0.27.3"}},
	"llvmlite":         {amd64: [5]string{"0.38.0", "0.40.0", "0.42.0", "0.44.0", "0.46.0"}},
	"lxml":             {amd64: [5]string{"4.6.3", "4.9.1", "4.9.3", "5.3.0", "6.0.1"}, arm64: [5]string{"4.6.4", "4.9.2", "4.9.3", "5.3.0", "6.0.1"}},
	"lz4":              {amd64: [5]string{"4.0.0", "4.1.0", "4.3.3", "4.4.3", "4.4.5"}, arm64: [5]string{"4.3.0", "4.3.0", "4.3.3", "4.4.4", "4.4.5"}},
	"matplotlib":       {amd64: [5]string{"3.5.0", "3.6.0", "3.7.3", "3.9.2", "3.10.5"}},
	"mmh3":             {amd64: [5]string{"3.1.0", "3.1.0", "4.1.0", "5.0.0", "5.2.0"}},
	"murmurhash":       {amd64: [5]string{"1.0.6", "1.0.9", "1.0.10", "1.0.11", "1.0.14"}, arm64: [5]string{"1.0.7", "1.0.8", "1.0.10", "1.0.12", "1.0.14"}},
	"numba":            {amd64: [5]string{"0.55.0", "0.57.0", "0.59.0", "0.61.0", "0.63.0"}},
	"numexpr":          {amd64: [5]string{"2.8.0", "2.8.4", "2.8.7", "2.10.2", "2.12.0"}, arm64: [5]string{"2.8.3", "2.8.4", "2.8.7", "2.10.2", "2.12.0"}},
	"numpy":            {amd64: [5]string{"1.21.2", "1.23.2", "1.26.0", "2.1.0", "2.3.2"}},
	"onnxruntime":      {amd64: [5]string{"1.12.0", "1.15.0", "1.17.0", "1.20.0", "1.24.1"}},
	"orjson":           {amd64: [5]string{"3.4.7", "3.7.7", "3.9.3", "3.10.7", "3.11.1"}, arm64: [5]string{"3.4.7", "3.8.1", "3.9.5", "3.10.10", "3.11.1"}},
	"pandas":           {amd64: [5]string{"1.3.3", "1.5.0", "2.1.1", "2.2.3", "2.3.3"}},
	"pillow":           {amd64: [5]string{"8.3.2", "9.2.0", "10.0.0", "10.4.0", "11.3.0"}},
	"preshed":          {amd64: [5]string{"3.0.6", "3.0.8", "3.0.9", "3.0.10", "3.0.11"}, arm64: [5]string{"3.0.6", "3.0.7", "3.0.9", "3.0.10", "3.0.11"}},
	"psutil":           {amd64: [5]string{"5.9.0", "5.9.4", "5.9.4", "5.9.4", "5.9.4"}, arm64: [5]string{"6.0.0", "6.0.0", "6.0.0", "6.0.0", "6.0.0"}},
	"psycopg-binary":   {amd64: [5]string{"3.0", "3.1.4", "3.1.12", "3.2.2", "3.2.10"}},
	"psycopg2-binary":  {amd64: [5]string{"2.9.1", "2.9.5", "2.9.9", "2.9.10", "2.9.11"}},
	"pyarrow":          {amd64: [5]string{"6.0.0", "10.0.1", "14.0.0", "18.0.0", "22.0.0"}},
	"pycurl":           {amd64: [5]string{"7.45.3", "7.45.3", "7.45.3", "7.45.4", "7.45.7"}},
	"pydantic-core":    {amd64: [5]string{"0.1.0", "0.7.0", "2.1.2", "2.20.0", "2.35.0"}, arm64: [5]string{"0.3.0", "0.7.0", "2.1.2", "2.20.0", "2.35.0"}},
	"pymssql":          {amd64: [5]string{"2.2.4", "2.2.6", "2.2.10", "2.3.1", "2.3.8"}, arm64: [5]string{"2.2.11", "2.2.11", "2.2.11", "2.3.1", "2.3.8"}},
	"pyodbc":           {amd64: [5]string{"4.0.34", "4.0.35", "5.0.0", "5.2.0", "5.3.0"}, arm64: [5]string{"4.0.39", "4.0.39", "5.0.0", "5.2.0", "5.3.0"}},
	"pyproj":           {amd64: [5]string{"3.3.0", "3.4.0", "3.6.1", "3.7.0", "3.7.2"}},
	"pyzmq":            {amd64: [5]string{"22.2.1", "23.2.1", "25.1.1", "26.1.0", "27.0.0"}},
	"rapidfuzz":        {amd64: [5]string{"1.8.0", "2.5.0", "2.15.2", "3.9.6", "3.14.1"}},
	"regex":            {amd64: [5]string{"2021.8.21", "2022.9.11", "2023.10.3", "2024.9.11", "2025.7.29"}, arm64: [5]string{"2021.8.21", "2022.9.11", "2023.10.3", "2024.9.11", "2025.7.31"}},
	"ruamel-yaml-clib": {amd64: [5]string{"0.2.6", "0.2.7", "0.2.8", "0.2.11", "0.2.15"}},
	"safetensors":      {amd64: [5]string{"0.0.1", "0.2.6", "0.4.0", "0.4.2", "0.5.0"}},
	"scikit-image":     {amd64: [5]string{"0.19.0", "0.20.0", "0.22.0", "0.25.0", "0.26.0"}},
	"scikit-learn":     {amd64: [5]string{"1.0.2", "1.1.3", "1.3.1", "1.5.2", "1.7.2"}},
	"scipy":            {amd64: [5]string{"1.7.2", "1.9.2", "1.11.2", "1.14.1", "1.16.1"}},
	"sentencepiece":    {amd64: [5]string{"0.1.96", "0.1.98", "0.2.0", "0.2.1", "0.2.1"}},
	"shapely":          {amd64: [5]string{"1.8.0", "1.8.5", "2.0.2", "2.0.6", "2.1.2"}, arm64: [5]string{"1.8.0", "2.0.0", "2.0.2", "2.0.6", "2.1.2"}},
	"spacy":            {amd64: [5]string{"2.3.8", "2.3.8", "3.7.0", "3.8.7", "3.8.10"}},
	"srsly":            {amd64: [5]string{"1.0.6", "1.0.6", "2.4.8", "2.5.0", "2.5.2"}, arm64: [5]string{"1.0.6", "1.0.6", "2.4.8", "2.5.1", "2.5.2"}},
	"statsmodels":      {amd64: [5]string{"0.13.1", "0.13.3", "0.14.0", "0.14.3", "0.14.5"}},
	"thinc":            {amd64: [5]string{"7.4.6", "7.4.6", "8.2.1", "8.3.6", "8.3.9"}},
	"tiktoken":         {amd64: [5]string{"0.1.1", "0.1.1", "0.5.2", "0.8.0", "0.12.0"}, arm64: [5]string{"0.3.1", "0.3.1", "0.5.2", "0.8.0", "0.12.0"}},
	"tokenizers":       {amd64: [5]string{"0.11.5", "0.13.2", "0.14.0", "0.15.1", "0.21.0"}},
	"torch":            {amd64: [5]string{"1.11.0", "1.13.0", "2.2.0", "2.5.0", "2.9.0"}, arm64: [5]string{"1.10.2", "2.0.0", "2.2.0", "2.6.0", "2.9.0"}},
	"torchaudio":       {amd64: [5]string{"0.11.0", "2.0.1", "2.2.0", "2.6.0", "2.9.0"}, arm64: [5]string{"0.10.2", "2.0.1", "2.2.0", "2.6.0", "2.9.0"}},
	"torchvision":      {amd64: [5]string{"0.12.0", "0.15.1", "0.17.0", "0.21.0", "0.24.0"}, arm64: [5]string{"0.11.3", "0.15.1", "0.17.0", "0.21.0", "0.24.0"}},
	"ujson":            {amd64: [5]string{"4.2.0", "5.5.0", "5.8.0", "5.10.0", "5.11.0"}},
	"uvloop":           {amd64: [5]string{"0.16.0", "0.17.0", "0.18.0", "0.21.0", "0.22.1"}},
	"xxhash":           {amd64: [5]string{"3.0.0", "3.2.0", "3.4.1", "3.5.0", "3.6.0"}},
	"zstandard":        {amd64: [5]string{"0.16.0", "0.19.0", "0.22.0", "0.23.0", "0.24.0"}},
	// pydantic 2 pins one pydantic-core release, so its own version decides.
	"pydantic": {amd64: [5]string{"2.0", "2.0", "2.0.2", "2.8.0", "2.12.0"}},
}

// pythonWheelFloors are the releases below which a table entry does not
// apply: pydantic 1 is pure Python.
var pythonWheelFloors = map[string]string{"pydantic": "2.0"}

// pythonStdlibCeilings are releases that import a standard library module a
// later Python removed; the build succeeds and the first import fails.
var pythonStdlibCeilings = []struct {
	name, below string
	newest      int
	unless      string
	reason      string
}{
	{"python-telegram-bot", "20.0", 12, "", "imports imghdr, which Python 3.13 removed"},
	{"django", "4.1", 12, "", "imports cgi, which Python 3.13 removed"},
	{"pydub", "", 12, "audioop-lts", "imports audioop, which Python 3.13 removed (audioop-lts restores it)"},
}

// pythonVersionBlockers says, for each catalogue family, which of the
// project's pins cannot be installed or imported on it. arch is the Go
// architecture the image is built for.
func (p pythonProject) versionBlockers(arch string) map[int][]string {
	blockers := map[int][]string{}
	block := func(minor int, what string) {
		for _, existing := range blockers[minor] {
			if existing == what {
				return
			}
		}
		blockers[minor] = append(blockers[minor], what)
	}
	if p.lock != nil && !p.lock.unreadable {
		main := p.lock.mainPackages()
		for _, pkg := range p.lock.packages {
			if len(main) > 0 && !main[pkg.name] {
				continue
			}
			support := pythonWheelSupport(pkg.wheels, arch)
			if !support.binary {
				continue
			}
			for _, minor := range pythonCatalogueMinors {
				if !support.supports(minor) {
					block(minor, pkg.name+"=="+pkg.version)
				}
			}
		}
	} else {
		for _, requirement := range p.installedRequirements() {
			firsts, known := pythonWheelReleases[requirement.name]
			if !known || requirement.url != "" {
				continue
			}
			if floor := pythonWheelFloors[requirement.name]; floor != "" {
				if version := requirement.pinnedVersion(); version != "" && comparePythonVersions(version, floor) < 0 {
					continue
				}
			}
			for _, minor := range pythonCatalogueMinors {
				first, _ := firsts.first(arch, minor)
				if !pythonRequirementReaches(requirement, first) {
					block(minor, requirement.text)
				}
			}
		}
	}
	declared := map[string]bool{}
	for _, requirement := range p.declared() {
		declared[requirement.name] = true
	}
	for name := range p.deps.names {
		declared[name] = true
	}
	for _, rule := range pythonStdlibCeilings {
		if !declared[rule.name] || (rule.unless != "" && declared[rule.unless]) {
			continue
		}
		version := p.resolvedVersion(rule.name)
		if rule.below != "" && (version == "" || comparePythonVersions(version, rule.below) >= 0) {
			if upper := p.upperBound(rule.name); upper == "" || comparePythonVersions(upper, rule.below) > 0 {
				continue
			}
		}
		label := rule.name
		if version != "" {
			label += "==" + version
		}
		for _, minor := range pythonCatalogueMinors {
			if minor > rule.newest {
				block(minor, label+" "+rule.reason)
			}
		}
	}
	for minor := range blockers {
		sort.Strings(blockers[minor])
	}
	return blockers
}

// resolvedVersion is the release a lock or an exact pin installs.
func (p pythonProject) resolvedVersion(name string) string {
	if pkg := p.lock.find(name); pkg != nil {
		return pkg.version
	}
	if version := p.pipfileLock.packages[name]; version != "" {
		return strings.TrimLeft(version, "=")
	}
	for _, requirement := range p.installedRequirements() {
		if requirement.name == name {
			return requirement.pinnedVersion()
		}
	}
	return ""
}

// upperBound is the exclusive ceiling a requirement's specifier puts on a
// distribution, when it has one: `<4`, `~=3.2` (below 4), `==3.2.*`.
func (p pythonProject) upperBound(name string) string {
	for _, requirement := range p.installedRequirements() {
		if requirement.name == name {
			return specifierUpperBound(requirement.specifier)
		}
	}
	return ""
}

func specifierUpperBound(specifier string) string {
	bound := ""
	for _, clause := range strings.Split(strings.ReplaceAll(specifier, " ", ""), ",") {
		var candidate string
		switch {
		case strings.HasPrefix(clause, "<="):
			version := strings.TrimPrefix(clause, "<=")
			candidate = nextPythonRelease(version, strings.Count(version, "."))
		case strings.HasPrefix(clause, "<"):
			candidate = strings.TrimPrefix(clause, "<")
		case strings.HasPrefix(clause, "~="):
			parts := strings.Split(strings.TrimPrefix(clause, "~="), ".")
			if len(parts) >= 2 {
				candidate = nextPythonRelease(strings.Join(parts[:len(parts)-1], "."), len(parts)-2)
			}
		case strings.HasPrefix(clause, "==") && strings.HasSuffix(clause, ".*"):
			parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(clause, "=="), ".*"), ".")
			candidate = nextPythonRelease(strings.Join(parts, "."), len(parts)-1)
		}
		if candidate != "" && (bound == "" || comparePythonVersions(candidate, bound) < 0) {
			bound = candidate
		}
	}
	return bound
}

// nextPythonRelease increments a release at one of its components, dropping
// what follows: ("3.2", 0) is "4", ("1.26.4", 2) is "1.26.5".
func nextPythonRelease(version string, component int) string {
	parts := strings.Split(version, ".")
	if component >= len(parts) {
		component = len(parts) - 1
	}
	if component < 0 {
		return ""
	}
	value, err := strconv.Atoi(parts[component])
	if err != nil {
		return ""
	}
	parts = append(parts[:component], strconv.Itoa(value+1))
	return strings.Join(parts, ".")
}

// pythonRequirementReaches says whether a requirement can resolve to a
// release at or after first: an exact pin at or after it, or a range whose
// ceiling lies above it. An empty first is a family never published for.
func pythonRequirementReaches(requirement pythonRequirement, first string) bool {
	if first == "" {
		return false
	}
	if version := requirement.pinnedVersion(); version != "" {
		return comparePythonVersions(version, first) >= 0
	}
	if upper := specifierUpperBound(requirement.specifier); upper != "" {
		return comparePythonVersions(first, upper) < 0
	}
	return true
}

// comparePythonVersions orders two releases by their numeric components;
// suffixes (rc1, .post1) are ignored, which is precise enough for a table of
// first releases.
func comparePythonVersions(a, b string) int {
	parse := func(version string) []int {
		var numbers []int
		for _, part := range strings.Split(version, ".") {
			digits := part
			for index, r := range part {
				if r < '0' || r > '9' {
					digits = part[:index]
					break
				}
			}
			if digits == "" {
				break
			}
			value, _ := strconv.Atoi(digits)
			numbers = append(numbers, value)
			if len(digits) != len(part) {
				break
			}
		}
		return numbers
	}
	left, right := parse(a), parse(b)
	for index := 0; index < len(left) || index < len(right); index++ {
		var l, r int
		if index < len(left) {
			l = left[index]
		}
		if index < len(right) {
			r = right[index]
		}
		if l != r {
			if l < r {
				return -1
			}
			return 1
		}
	}
	return 0
}

// pythonWheels is what a locked package's wheel file names say: whether it
// is compiled at all for Linux, and for which CPython families a Linux build
// of the given architecture has a wheel.
type pythonWheels struct {
	binary bool
	pure   bool
	abi3   int
	minors map[int]bool
}

func (w pythonWheels) supports(minor int) bool {
	return w.pure || (w.abi3 > 0 && w.abi3 <= minor) || w.minors[minor]
}

// pythonWheelSupport reads wheel file names: name-version(-build)-python-abi-platform.whl.
func pythonWheelSupport(wheels []string, arch string) pythonWheels {
	machine := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[arch]
	support := pythonWheels{minors: map[int]bool{}}
	for _, wheel := range wheels {
		parts := strings.Split(strings.TrimSuffix(wheel, ".whl"), "-")
		if len(parts) < 5 {
			continue
		}
		python, abi, platform := parts[len(parts)-3], parts[len(parts)-2], parts[len(parts)-1]
		if platform == "any" {
			support.pure = true
			continue
		}
		if !strings.Contains(platform, "linux") {
			continue
		}
		support.binary = true
		if machine == "" || !strings.Contains(platform, machine) {
			continue
		}
		for _, tag := range strings.Split(python, ".") {
			if !strings.HasPrefix(tag, "cp3") {
				continue
			}
			minor, err := strconv.Atoi(strings.TrimPrefix(tag, "cp3"))
			if err != nil {
				continue
			}
			switch {
			case abi == "abi3":
				if support.abi3 == 0 || minor < support.abi3 {
					support.abi3 = minor
				}
			case abi == "cp3"+strconv.Itoa(minor):
				support.minors[minor] = true
			}
		}
	}
	return support
}

// pythonBuildArch is the architecture a recipe image is built for: the
// plan's target platform, else this host's.
func pythonBuildArch(targetPlatform string) string {
	if _, arch, found := strings.Cut(strings.ToLower(targetPlatform), "/"); found {
		arch, _, _ = strings.Cut(arch, "/")
		return arch
	}
	return runtime.GOARCH
}
