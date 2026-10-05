package dockerx

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/docker/docker/api/types/container"
)

const n8nEditorCache = "/home/node/.cache/n8n/public"
const n8nUploadDirectory = "/tmp/n8nDataTableUploads"

// These exact shipped n8n 2.39.10 generators overwrite derived editor assets
// and type definitions on startup. Unknown versions keep the ordinary blocker.
var n8nGeneratorDigests = map[string]string{
	"/usr/local/lib/node_modules/n8n/dist/commands/start.js":                              "b5d3a43b425ad1d324e813f2b3179298bbefa2c3c373f6f7ff42aeab568bd4a4",
	"/usr/local/lib/node_modules/n8n/dist/services/frontend.service.js":                   "408fd11657eeb1c1e8d3beed36824ab7cd1ad34b0684843da7c3540cddb50e54",
	"/usr/local/lib/node_modules/n8n/dist/modules/data-table/multer-upload-middleware.js": "1674c17556eea1d8c0e442fb149ad8175d5b74efa2dd7a0bbc80658d432d778b",
}

func (c *Client) captureRegenerableN8nCache(ctx context.Context, capture *AdoptionContainer) {
	config := capture.Inspection.Config
	if !n8nDefaultStart(config) {
		return
	}
	hasCache := false
	for _, change := range capture.Changes {
		if strings.HasPrefix(change.Path, n8nEditorCache+"/") {
			hasCache = true
			break
		}
	}
	if !hasCache {
		return
	}
	cli, err := c.api()
	if err != nil {
		return
	}
	for file, want := range n8nGeneratorDigests {
		capture.RegenerableProofFailure = "generator_" + path.Base(file)
		stream, _, err := cli.CopyFromContainer(ctx, capture.Inspection.ID, file)
		if err != nil {
			return
		}
		content, err := readN8nGenerator(stream, path.Base(file))
		stream.Close()
		if err != nil {
			return
		}
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != want {
			return
		}
	}
	capture.RegenerableProofFailure = "editor_distribution_archive"
	stream, _, err := cli.CopyFromContainer(ctx, capture.Inspection.ID, "/usr/local/lib/node_modules/n8n/node_modules/n8n-editor-ui/dist")
	if err != nil {
		return
	}
	distribution, err := readN8nArchiveModes(stream, "dist")
	stream.Close()
	if err != nil {
		return
	}
	capture.RegenerableProofFailure = "cache_archive"
	stream, _, err = cli.CopyFromContainer(ctx, capture.Inspection.ID, n8nEditorCache)
	if err != nil {
		return
	}
	cache, err := readN8nArchiveModes(stream, "public")
	stream.Close()
	if err != nil {
		return
	}
	var uploads map[string]os.FileMode
	for _, change := range capture.Changes {
		if change.Path == n8nUploadDirectory || strings.HasPrefix(change.Path, n8nUploadDirectory+"/") {
			capture.RegenerableProofFailure = "upload_directory_archive"
			stream, _, err := cli.CopyFromContainer(ctx, capture.Inspection.ID, n8nUploadDirectory)
			if err != nil {
				return
			}
			uploads, err = readN8nArchiveModes(stream, "n8nDataTableUploads")
			stream.Close()
			if err != nil {
				return
			}
			break
		}
	}
	capture.RegenerableProofFailure = "changed_paths"
	verified, err := verifiedN8nRegenerableChanges(capture.Changes, capture.ChangeModes, distribution, cache, uploads)
	if err != nil {
		return
	}
	capture.RegenerableProofFailure = ""
	capture.RegenerablePaths = verified
	for name, mode := range cache {
		capture.ChangeModes[path.Join(n8nEditorCache, name)] = mode
	}
	if len(uploads) == 1 {
		capture.ChangeModes[n8nUploadDirectory] = os.ModeDir
	}
}

func n8nDefaultStart(config *container.Config) bool {
	return config != nil && slices.Equal(config.Entrypoint, []string{"tini", "--", "/docker-entrypoint.sh"}) && (len(config.Cmd) == 0 || slices.Equal(config.Cmd, []string{"start"}))
}

func readN8nGenerator(stream io.Reader, basename string) ([]byte, error) {
	reader := tar.NewReader(io.LimitReader(stream, 256<<10))
	header, err := reader.Next()
	if err != nil || header.Name != basename || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) || header.Size < 1 || header.Size > 128<<10 {
		return nil, errors.New("unknown n8n generator")
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if _, err = reader.Next(); err != io.EOF {
		return nil, errors.New("ambiguous n8n generator archive")
	}
	return content, nil
}

func readN8nArchiveModes(stream io.Reader, basename string) (map[string]os.FileMode, error) {
	reader := tar.NewReader(io.LimitReader(stream, (128<<20)+1))
	result := map[string]os.FileMode{}
	var bytes int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		name := path.Clean(header.Name)
		if name != basename && !strings.HasPrefix(name, basename+"/") {
			return nil, errors.New("n8n archive path is invalid")
		}
		name = strings.TrimPrefix(strings.TrimPrefix(name, basename), "/")
		mode := header.FileInfo().Mode()
		if (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA && header.Typeflag != tar.TypeDir) || header.Size < 0 || bytes > (128<<20)-header.Size || len(result) >= 4096 {
			return nil, errors.New("n8n archive exceeds safe bounds or has links")
		}
		if _, exists := result[name]; exists {
			return nil, errors.New("n8n archive has duplicate entries")
		}
		bytes += header.Size
		result[name] = mode
	}
}

func verifiedN8nRegenerableChanges(changes []container.FilesystemChange, modes, distribution, cache, uploads map[string]os.FileMode) ([]string, error) {
	allowedFiles := map[string]bool{}
	for name, mode := range distribution {
		if mode.IsRegular() && (name == "index.html" || strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".css")) {
			allowedFiles[path.Join(n8nEditorCache, name)] = true
		}
	}
	for _, name := range []string{"types/nodes.json", "types/node-versions.json", "types/credentials.json"} {
		allowedFiles[path.Join(n8nEditorCache, name)] = true
	}
	allowedDirectories := map[string]bool{n8nEditorCache: true}
	for filename := range allowedFiles {
		for directory := path.Dir(filename); strings.HasPrefix(directory, n8nEditorCache+"/"); directory = path.Dir(directory) {
			allowedDirectories[directory] = true
		}
	}
	for name, mode := range cache {
		fullPath := path.Join(n8nEditorCache, name)
		if (mode.IsDir() && !allowedDirectories[fullPath]) || (!mode.IsDir() && (!mode.IsRegular() || !allowedFiles[fullPath])) {
			return nil, errors.New("n8n cache contains an unknown file")
		}
	}
	if uploads != nil && (len(uploads) != 1 || !uploads[""].IsDir()) {
		return nil, errors.New("n8n upload directory contains application data")
	}
	var verified []string
	for _, change := range changes {
		if strings.HasPrefix(change.Path, n8nEditorCache+"/") || change.Path == n8nEditorCache {
			relative := strings.TrimPrefix(strings.TrimPrefix(change.Path, n8nEditorCache), "/")
			mode, found := cache[relative]
			if change.Kind != container.ChangeAdd || !found || (!mode.IsDir() && !allowedFiles[change.Path]) {
				return nil, errors.New("n8n cache is modified, deleted or unverified")
			}
			verified = append(verified, change.Path)
		} else if change.Path == n8nUploadDirectory {
			if change.Kind != container.ChangeAdd || len(uploads) != 1 || !uploads[""].IsDir() {
				return nil, errors.New("n8n upload directory is not newly created and empty")
			}
			verified = append(verified, change.Path)
		} else if strings.HasPrefix(change.Path, n8nUploadDirectory+"/") {
			return nil, errors.New("n8n upload files must be persisted")
		}
	}
	for _, change := range changes {
		if mode, known := modes[change.Path]; known && mode.IsDir() && (change.Kind == container.ChangeAdd || change.Kind == container.ChangeModify) {
			for _, generated := range verified {
				if strings.HasPrefix(generated, strings.TrimSuffix(change.Path, "/")+"/") {
					verified = append(verified, change.Path)
					break
				}
			}
		}
	}
	return verified, nil
}
