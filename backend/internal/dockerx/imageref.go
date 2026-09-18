package dockerx

import (
	"strings"
)

// How an image was named, and what that means for what can be done with it.
//
// Docker overloads one string. `nginx:1.27`, `nginx@sha256:…`, a bare
// `sha256:…` image id and `<none>:<none>` all arrive through the same field
// and all mean something different: only the first can be checked for updates,
// only the first two can be pulled, the third is not a reference at all, and
// the fourth is a dangling layer with no name to speak of.
//
// Before this existed each caller guessed. The image detail route handed
// whatever it was given straight to the Engine, so opening an image by id
// produced `invalid reference format: repository name (library/sha256…) must
// be lowercase` — the daemon parsing an id as a repository, prepending the
// implicit `library/`, and complaining about the result.

// ImageRefKind classifies an image string.
type ImageRefKind string

const (
	// RefTag is repository:tag, the only form that can be checked against a
	// registry for a newer image.
	RefTag ImageRefKind = "tag"
	// RefDigest is repository@sha256:… — pinned, reproducible, and by
	// definition never out of date.
	RefDigest ImageRefKind = "digest"
	// RefID is a bare sha256:… image id. It identifies a local image and is
	// not a reference: it can be inspected and removed, never pulled.
	RefID ImageRefKind = "id"
	// RefDangling is Docker's placeholder for an image with no tag left.
	RefDangling ImageRefKind = "dangling"
	// RefUnknown is an empty or unparseable string.
	RefUnknown ImageRefKind = "unknown"
)

const digestPrefix = "sha256:"

// danglingRef is what the Engine reports for an image whose tags were all
// removed. It is a literal, not a reference, and treating it as one produces a
// pull attempt for a repository called "<none>".
const danglingRef = "<none>:<none>"

// IsImageID reports whether a string is a bare image id rather than a name.
//
// An id is `sha256:` followed by hex — anything else with a colon is a
// repository and a tag. The hex check matters: `sha256:latest` would otherwise
// be read as an id, and it is a (legal, if perverse) repository name and tag.
func IsImageID(ref string) bool {
	rest, found := strings.CutPrefix(strings.TrimSpace(ref), digestPrefix)
	if !found || len(rest) < 12 {
		return false
	}
	return isHex(rest)
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// ClassifyImageRef says what shape an image string is.
func ClassifyImageRef(ref string) ImageRefKind {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "":
		return RefUnknown
	case ref == danglingRef, strings.HasPrefix(ref, "<none>"):
		return RefDangling
	case IsImageID(ref):
		return RefID
	case strings.Contains(ref, "@"):
		return RefDigest
	default:
		return RefTag
	}
}

// NormalizeImageRef prepares a user-supplied string for the Engine.
//
// A bare repository gets the `:latest` the CLI would add. An image id, a
// digest reference and a dangling placeholder are returned untouched: an id is
// already exact, a digest already pins, and appending a tag to either produces
// the malformed reference this function exists to stop being sent.
func NormalizeImageRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ref
	}
	switch ClassifyImageRef(ref) {
	case RefID, RefDigest, RefDangling:
		return ref
	}
	// A colon after the last slash is a tag; one before it is a registry port
	// (`registry.example.com:5000/app`), which leaves the image untagged.
	if lastColon, lastSlash := strings.LastIndex(ref, ":"), strings.LastIndex(ref, "/"); lastColon <= lastSlash {
		return ref + ":latest"
	}
	return ref
}

// Pullable reports whether a reference names something a registry could serve.
// Ids and dangling placeholders never can, and offering "pull" for them is
// offering a button whose only outcome is an error.
func Pullable(ref string) bool {
	switch ClassifyImageRef(ref) {
	case RefTag, RefDigest:
		return true
	default:
		return false
	}
}

// Checkable reports whether "is there a newer one" is a question with an
// answer. Only a moving reference — a tag — has one: a digest is pinned by
// construction and an id is local.
func Checkable(ref string) bool { return ClassifyImageRef(ref) == RefTag }

// MovingTag reports whether a reference will silently mean something else
// after the next pull. `latest` and a bare repository are the usual ones, but
// so is any tag that a publisher rewrites — which cannot be known from here,
// so this reports only the cases that are certain.
func MovingTag(ref string) bool {
	if ClassifyImageRef(ref) != RefTag {
		return false
	}
	_, tag := SplitImageRef(ref)
	return tag == "latest" || tag == ""
}

// SplitImageRef separates a reference into its repository and its tag.
// A digest reference keeps the digest as the "tag" half, because that is the
// part that identifies the version. An id has no repository.
func SplitImageRef(ref string) (repository, tag string) {
	ref = strings.TrimSpace(ref)
	switch ClassifyImageRef(ref) {
	case RefID, RefUnknown:
		return "", ref
	case RefDangling:
		return "", ""
	}
	if repo, digest, found := strings.Cut(ref, "@"); found {
		return repo, digest
	}
	lastColon, lastSlash := strings.LastIndex(ref, ":"), strings.LastIndex(ref, "/")
	if lastColon > lastSlash {
		return ref[:lastColon], ref[lastColon+1:]
	}
	return ref, ""
}

// PreferredRef picks the best name for an image out of everything the Engine
// knows about it, for a UI that wants one line rather than three lists.
//
// A real tag beats a digest, a digest beats an id, and an id beats nothing. A
// multi-tag image keeps its first tag as the headline; the rest belong beside
// it rather than in place of it.
func PreferredRef(id string, repoTags, repoDigests []string) string {
	for _, t := range repoTags {
		if ClassifyImageRef(t) == RefTag {
			return t
		}
	}
	for _, d := range repoDigests {
		if ClassifyImageRef(d) == RefDigest {
			return d
		}
	}
	return id
}
