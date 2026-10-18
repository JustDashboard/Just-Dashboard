package dbx

import (
	"archive/tar"
	"bytes"
	"crypto"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	// The x/crypto OpenPGP package is frozen, and it is used for the one
	// thing it still has to do here: make a key in the format WAL-G encrypts
	// with. Nothing is signed or decrypted with it in this process.
	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/armor"
	"golang.org/x/crypto/openpgp/packet"
)

// The tool a server archives with.
//
// WAL-G's releases are linked against glibc, and most of the images this
// dashboard starts are Alpine's, which has none. So what goes into a server's
// container is WAL-G together with the dynamic loader and the libraries it was
// linked against, taken from this dashboard's own filesystem — the image it
// runs in carries the pinned release — and a wrapper that starts WAL-G
// through that loader. The image's own C library is never involved, which is
// what lets the same files run in an Alpine image and a Debian one.

// WALGBinary is where this dashboard keeps WAL-G. The image installs a pinned
// release there; a test points it at its own copy.
var WALGBinary = "/usr/local/bin/wal-g"

// walgCABundle is offered to an image that has no certificate bundle of its
// own, so that an object store reached over TLS can still be verified.
const walgCABundle = "/etc/ssl/certs/ca-certificates.crt"

// WALGTool is the tool as it is about to be handed to a server: its files and
// a digest of all of them, which names the build a server was given.
type WALGTool struct {
	files  []walgToolFile
	Digest string
}

type walgToolFile struct {
	name string
	mode int64
	data []byte
}

// LoadWALGTool reads WAL-G and what it needs to run out of this process's own
// filesystem.
func LoadWALGTool() (*WALGTool, error) {
	binary, err := os.ReadFile(WALGBinary)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("this dashboard has no WAL-G; it is installed in the dashboard's image, so this install predates it or was built without it")
		}
		return nil, err
	}
	tool := &WALGTool{}
	tool.files = append(tool.files, walgToolFile{"lib/wal-g", 0o755, binary})
	exe, err := elf.NewFile(bytes.NewReader(binary))
	if err != nil {
		return nil, fmt.Errorf("%s is not a program this dashboard can hand to a server: %w", WALGBinary, err)
	}
	defer exe.Close()
	interpreter := elfInterpreter(exe)
	wrapper := walgWrapper(interpreter != "")
	tool.files = append(tool.files, walgToolFile{"wal-g", 0o755, []byte(wrapper)})
	if interpreter != "" {
		loader, err := os.ReadFile(interpreter)
		if err != nil {
			return nil, fmt.Errorf("the loader WAL-G was linked with (%s) is not on this dashboard: %w", interpreter, err)
		}
		tool.files = append(tool.files, walgToolFile{"lib/ld.so", 0o755, loader})
		libraries, err := elfLibraries(exe, filepath.Dir(interpreter))
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(libraries))
		for name := range libraries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			data, err := os.ReadFile(libraries[name])
			if err != nil {
				return nil, err
			}
			tool.files = append(tool.files, walgToolFile{"lib/" + name, 0o755, data})
		}
	}
	if bundle, err := os.ReadFile(walgCABundle); err == nil {
		tool.files = append(tool.files, walgToolFile{"lib/ca-certificates.crt", 0o644, bundle})
	}
	sum := sha256.New()
	for _, f := range tool.files {
		fmt.Fprintf(sum, "%s\x00%d\x00", f.name, len(f.data))
		sum.Write(f.data)
	}
	tool.Digest = hex.EncodeToString(sum.Sum(nil))
	return tool, nil
}

// walgWrapper is the script the server runs as WAL-G. The configuration's
// path travels in the environment rather than as a flag, because WAL-G starts
// copies of itself to fetch ahead during a recovery and passes them its
// environment and not its flags; with the loader, --argv0 makes those copies
// come back through this script.
func walgWrapper(dynamic bool) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# WAL-G for point-in-time recovery, from Just Dashboard. It runs with the C library\n")
	b.WriteString("# it was built against, beside it, whatever the image itself has.\n")
	b.WriteString("export WALG_CONFIG_PATH=" + WALGMount + "/walg.json\n")
	b.WriteString("[ -r /etc/ssl/certs/ca-certificates.crt ] || [ -r /etc/ssl/cert.pem ] || export SSL_CERT_FILE=" + WALGMount + "/lib/ca-certificates.crt\n")
	if dynamic {
		b.WriteString("exec " + WALGMount + "/lib/ld.so --library-path " + WALGMount + "/lib --argv0 " + WALGCommand + " " + WALGMount + "/lib/wal-g \"$@\"\n")
	} else {
		b.WriteString("exec " + WALGMount + "/lib/wal-g \"$@\"\n")
	}
	return b.String()
}

func elfInterpreter(f *elf.File) string {
	for _, p := range f.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		data, err := io.ReadAll(p.Open())
		if err != nil {
			return ""
		}
		return strings.TrimRight(string(data), "\x00")
	}
	return ""
}

// elfLibraries is every shared library a program needs, by the name it asks
// for, resolved on this filesystem and followed through what each of those
// needs in turn.
func elfLibraries(f *elf.File, loaderDir string) (map[string]string, error) {
	dirs := []string{loaderDir}
	if real, err := filepath.EvalSymlinks(loaderDir); err == nil {
		dirs = append(dirs, real)
	}
	triplet := map[elf.Machine]string{elf.EM_X86_64: "x86_64-linux-gnu", elf.EM_AARCH64: "aarch64-linux-gnu"}[f.Machine]
	if triplet != "" {
		dirs = append(dirs, "/lib/"+triplet, "/usr/lib/"+triplet)
	}
	dirs = append(dirs, "/lib64", "/usr/lib64", "/lib", "/usr/lib")
	found := map[string]string{}
	var walk func(*elf.File) error
	walk = func(file *elf.File) error {
		needed, err := file.ImportedLibraries()
		if err != nil {
			return err
		}
		for _, name := range needed {
			if _, done := found[name]; done || strings.HasPrefix(name, "ld-linux") {
				continue
			}
			resolved := ""
			for _, dir := range dirs {
				if p, err := filepath.EvalSymlinks(filepath.Join(dir, name)); err == nil {
					resolved = p
					break
				}
			}
			if resolved == "" {
				return fmt.Errorf("WAL-G needs %s, which is not on this dashboard", name)
			}
			found[name] = resolved
			lib, err := elf.Open(resolved)
			if err != nil {
				return err
			}
			err = walk(lib)
			lib.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	return found, walk(f)
}

// WriteTar writes the tool as an archive to unpack at WALGMount. Every file
// belongs to root and none is writable by the server's account, which reads
// and runs them and may change none of them.
func (t *WALGTool) WriteTar(w io.Writer) error {
	tw := tar.NewWriter(w)
	now := time.Now().UTC()
	if err := tw.WriteHeader(&tar.Header{Name: "lib/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: now}); err != nil {
		return err
	}
	for _, f := range t.files {
		if err := tw.WriteHeader(&tar.Header{
			Name: f.name, Typeflag: tar.TypeReg, Mode: f.mode, Size: int64(len(f.data)), ModTime: now,
		}); err != nil {
			return err
		}
		if _, err := tw.Write(f.data); err != nil {
			return err
		}
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "lib/tool.sha256", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(t.Digest) + 1), ModTime: now,
	}); err != nil {
		return err
	}
	if _, err := io.WriteString(tw, t.Digest+"\n"); err != nil {
		return err
	}
	return tw.Close()
}

// WriteWALGConfigTar writes WAL-G's configuration as an archive of one file to
// unpack at WALGMount, readable by the server's account alone: it holds the
// destination's keys.
func WriteWALGConfigTar(w io.Writer, cfg map[string]string, uid, gid int) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	if err := tw.WriteHeader(&tar.Header{
		Name: "walg.json", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data)),
		Uid: uid, Gid: gid, ModTime: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	return tw.Close()
}

// NewWALKey makes the OpenPGP key an archive is encrypted with: the public
// half is all a server needs to write the archive, and the private half,
// which the API seals with the master key, is handed only to a copy being
// recovered from it. A server that is broken into therefore cannot read its
// own archive back.
func NewWALKey(name string) (public, private string, err error) {
	config := &packet.Config{RSABits: 3072, DefaultHash: crypto.SHA256}
	entity, err := openpgp.NewEntity(name, "point-in-time recovery", "", config)
	if err != nil {
		return "", "", err
	}
	var privateOut bytes.Buffer
	w, err := armor.Encode(&privateOut, openpgp.PrivateKeyType, nil)
	if err != nil {
		return "", "", err
	}
	if err := entity.SerializePrivate(w, config); err != nil {
		return "", "", err
	}
	if err := w.Close(); err != nil {
		return "", "", err
	}
	var publicOut bytes.Buffer
	w, err = armor.Encode(&publicOut, openpgp.PublicKeyType, nil)
	if err != nil {
		return "", "", err
	}
	if err := entity.Serialize(w); err != nil {
		return "", "", err
	}
	if err := w.Close(); err != nil {
		return "", "", err
	}
	return publicOut.String(), privateOut.String(), nil
}

// WALKeyFingerprint is how a key is named to an operator: its fingerprint, in
// the groups of four GnuPG prints it in.
func WALKeyFingerprint(public string) string {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(public))
	if err != nil || len(entities) == 0 {
		return ""
	}
	hexed := strings.ToUpper(hex.EncodeToString(entities[0].PrimaryKey.Fingerprint[:]))
	var groups []string
	for i := 0; i+4 <= len(hexed); i += 4 {
		groups = append(groups, hexed[i:i+4])
	}
	return strings.Join(groups, " ")
}
