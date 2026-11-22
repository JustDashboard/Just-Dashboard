package deploy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// A recipe hands every build value to its RUN steps as a BuildKit secret
// mount, and BuildKit leaves a secret's value out of the step's cache key on
// purpose. On its own that means a redeploy of the same commit with a
// changed value — a NEXT_PUBLIC_ address, a VITE_ key — reuses the cached
// build and ships the bundle compiled with the old one.
//
// The build-value guard closes that. One pass over every rendered recipe
// (withBuildValueGuards, called by renderRecipeDockerfile for every recipe
// kind, so a language's renderer never has to know about it) finds each RUN
// that mounts a secret and declares a build argument just before it:
//
//	ARG JD_BUILD_VALUES_1
//	RUN --mount=type=secret,id=SITE_URL,env=SITE_URL,required=true bun run build
//
// A declared argument is part of the cache key of every RUN after it in
// its stage, so when its value changes that RUN and everything built on it
// rerun. Each distinct set of mounted values gets an argument of its own,
// declared only where that set is first mounted in a stage, so the install
// (mounting only install-mapped values) keeps its layer when only a build
// step's value changes.
//
// The argument's value is a keyed digest of the values that RUN mounts
// (buildValueMarker): HMAC-SHA256 under a key derived from the install's
// master key, never a plain hash, because a declared argument's value is
// written into the image's history for the stage it runs in, and a plain
// hash of a low-entropy value (a port, a short password) could be guessed
// from it. The Dockerfile holds only the argument's name; the value
// reaches buildx as `--build-arg NAME` with the digest in its process
// environment, the way a Dockerfile's own build arguments do, so it is in
// no argv, no stored preview and no generated file.

// buildValueGuardPrefix names the guard arguments. A user's build argument
// can never take the prefix (reservedBuildArgName).
const buildValueGuardPrefix = "JD_BUILD_VALUES_"

// BuildValueGuard is one guard argument and the build values, by variable
// name, whose keyed digest it carries.
type BuildValueGuard struct {
	Arg string   `json:"arg"`
	IDs []string `json:"ids"`
}

// withBuildValueGuards declares the guard argument before each RUN that
// mounts secrets, once per stage and set, and returns the guards in the
// order they were first named. Lines are Dockerfile instructions, one per
// element; a RUN continued over lines carries its flags on its first.
func withBuildValueGuards(lines []string) ([]string, []BuildValueGuard) {
	var guards []BuildValueGuard
	names := map[string]string{}
	declared := map[string]bool{}
	result := make([]string, 0, len(lines))
	continued := false
	for _, line := range lines {
		instruction := !continued
		continued = strings.HasSuffix(strings.TrimRight(line, " \t"), "\\")
		if !instruction {
			result = append(result, line)
			continue
		}
		if strings.HasPrefix(line, "FROM ") {
			clear(declared)
		}
		ids := runSecretIDs(line)
		if len(ids) == 0 {
			result = append(result, line)
			continue
		}
		key := strings.Join(ids, ",")
		name, ok := names[key]
		if !ok {
			name = buildValueGuardPrefix + strconv.Itoa(len(guards)+1)
			names[key] = name
			guards = append(guards, BuildValueGuard{Arg: name, IDs: ids})
		}
		if !declared[name] {
			declared[name] = true
			result = append(result, "ARG "+name)
		}
		result = append(result, line)
	}
	return result, guards
}

// runSecretIDs lists, sorted, the secrets a RUN instruction mounts, read
// from its leading flags only: a command's own text that happens to say
// --mount is not a flag.
func runSecretIDs(line string) []string {
	rest, ok := strings.CutPrefix(line, "RUN ")
	if !ok {
		return nil
	}
	var ids []string
	for _, field := range strings.Fields(rest) {
		if !strings.HasPrefix(field, "--") {
			break
		}
		options, ok := strings.CutPrefix(field, "--mount=")
		if !ok {
			continue
		}
		parts := strings.Split(options, ",")
		if !slices.Contains(parts, "type=secret") {
			continue
		}
		for _, part := range parts {
			if id, ok := strings.CutPrefix(part, "id="); ok && id != "" && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

// buildValueMarker is a guard argument's value: HMAC-SHA256 under key over
// the values the guard names, encoded so no two different sets of values
// share an input. A value the run does not have is encoded as absent, so
// it still differs from an empty one.
func buildValueMarker(key []byte, guard BuildValueGuard, secrets []BuildSecretValue) string {
	values := map[string]string{}
	for _, secret := range secrets {
		values[secret.ID] = secret.Value
	}
	type entry struct {
		ID    string  `json:"id"`
		Value *string `json:"value"`
	}
	entries := make([]entry, 0, len(guard.IDs))
	for _, id := range guard.IDs {
		item := entry{ID: id}
		if value, ok := values[id]; ok {
			item.Value = &value
		}
		entries = append(entries, item)
	}
	encoded, _ := json.Marshal(entries)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("v1\x00"))
	mac.Write(encoded)
	return hex.EncodeToString(mac.Sum(nil))
}

// buildValueGuardArgs are the guard arguments a prepared recipe declares,
// with their digests, for buildx.
func (b *ArtifactBuilder) buildValueGuardArgs(prepared PreparedBuild, secrets []BuildSecretValue) []BuildArgValue {
	if len(prepared.BuildValueGuards) == 0 {
		return nil
	}
	key := b.buildValueKey
	if len(key) == 0 {
		key = processBuildValueKey()
	}
	args := make([]BuildArgValue, 0, len(prepared.BuildValueGuards))
	for _, guard := range prepared.BuildValueGuards {
		args = append(args, BuildArgValue{Name: guard.Arg, Value: buildValueMarker(key, guard, secrets)})
	}
	return args
}

// processBuildValueKey is the key of a builder that was given none — a test
// or a tool without the master key. It is random for the life of the
// process, so a value change still rebuilds; a restart rebuilds once more.
var processBuildValueKey = sync.OnceValue(func() []byte {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return key
})

// WithBuildValueKey gives the builder the key its build-value guards are
// keyed with: a value derived from the install's master key
// (auth.Sealer.DeriveHMAC), stable across restarts so an unchanged value
// keeps its cache.
func (b *ArtifactBuilder) WithBuildValueKey(key []byte) *ArtifactBuilder {
	if b != nil {
		b.buildValueKey = append([]byte(nil), key...)
	}
	return b
}

// BuildValueKeyLabel is the DeriveHMAC label the server derives the guard
// key with, which separates it from every other use of the install's key.
const BuildValueKeyLabel = "deploy.build-values"
