package backups

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// What a destination holds, read the way a fresh server has to read it: by
// listing it, reading the small files beside the archives, and fetching an
// archive whole to prove its digest. Each transport answers the same three
// questions with the connection it already has — the S3 client, one SFTP
// session for the whole catalogue, rclone's lsjson and cat — and the
// listing looks only where the runner writes: the directory, the bucket's
// prefix, the folder inside the remote, and nothing under them.

// maxListedObjects bounds a listing; a destination holding more than this
// many objects where archives are written is read in part, and says so.
const maxListedObjects = 20000

type remoteObject struct {
	Key      string
	Size     int64
	Modified time.Time
}

type destinationReader interface {
	list(ctx context.Context) ([]remoteObject, bool, error)
	// read answers a small object whole, refusing one longer than limit.
	read(ctx context.Context, key string, limit int64) ([]byte, error)
	// fetch streams an object into out and answers how many bytes it was.
	fetch(ctx context.Context, key string, out io.Writer) (int64, error)
	close()
}

// openDestination connects to a destination for the length of a catalogue or
// an import. A local directory has been resolved by the caller.
func (s *Store) openDestination(ctx context.Context, job *Job, secrets *TargetSecrets, pin HostKeyPinner) (destinationReader, error) {
	switch job.TargetKind {
	case TargetLocal:
		return &localDestination{dir: job.Target.Path, store: s}, nil
	case TargetS3, TargetB2:
		client, err := s3Client(ctx, job, secrets)
		if err != nil {
			return nil, err
		}
		return &s3Destination{client: client, bucket: job.Target.Bucket, prefix: s3ListPrefix(job.Target.Prefix)}, nil
	case TargetSFTP:
		session, err := dialSFTP(ctx, job, secrets, pin)
		if err != nil {
			return nil, err
		}
		return &sftpDestination{session: session, dir: job.Target.Path}, nil
	case TargetRclone:
		return &rcloneDestination{secrets: secrets, dir: job.Target.Path}, nil
	}
	return nil, fmt.Errorf("unknown target kind %q", job.TargetKind)
}

// s3ListPrefix is the prefix remoteKey writes under, as a listing asks for it.
func s3ListPrefix(prefix string) string {
	clean := strings.Trim(filepath.ToSlash(filepath.Clean("/"+prefix)), "/")
	if clean == "" || clean == "." {
		return ""
	}
	return clean + "/"
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("the object is longer than %d bytes", limit)
	}
	return raw, nil
}

// --- a directory on this server -------------------------------------------

type localDestination struct {
	dir   string
	store *Store
}

func (d *localDestination) list(ctx context.Context) ([]remoteObject, bool, error) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, false, err
	}
	var out []remoteObject
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if len(out) == maxListedObjects {
			return out, true, nil
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		out = append(out, remoteObject{Key: filepath.Join(d.dir, entry.Name()), Size: info.Size(), Modified: info.ModTime().UTC()})
	}
	return out, false, nil
}

// open reads a listed file through the file service again, and only a
// regular file: a link swapped in after the listing is not followed.
func (d *localDestination) open(key string) (*os.File, error) {
	if filepath.Dir(key) != filepath.Clean(d.dir) {
		return nil, errors.New("the archive is not in the destination directory")
	}
	resolved, err := d.store.paths.ResolveEntry(key)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("the archive is not a regular file")
	}
	return os.Open(resolved)
}

func (d *localDestination) read(_ context.Context, key string, limit int64) ([]byte, error) {
	f, err := d.open(key)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readLimited(f, limit)
}

func (d *localDestination) fetch(ctx context.Context, key string, out io.Writer) (int64, error) {
	f, err := d.open(key)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(out, &recoveryContextReader{ctx: ctx, reader: f})
}

func (d *localDestination) close() {}

// --- S3 and Backblaze B2 ----------------------------------------------------

type s3Destination struct {
	client *s3.Client
	bucket string
	prefix string
}

func (d *s3Destination) list(ctx context.Context) ([]remoteObject, bool, error) {
	input := &s3.ListObjectsV2Input{Bucket: aws.String(d.bucket), Delimiter: aws.String("/")}
	if d.prefix != "" {
		input.Prefix = aws.String(d.prefix)
	}
	var out []remoteObject
	pages := s3.NewListObjectsV2Paginator(d.client, input)
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("cannot list bucket %s: %w", d.bucket, err)
		}
		for _, object := range page.Contents {
			if len(out) == maxListedObjects {
				return out, true, nil
			}
			item := remoteObject{Key: aws.ToString(object.Key), Size: aws.ToInt64(object.Size)}
			if object.LastModified != nil {
				item.Modified = object.LastModified.UTC()
			}
			out = append(out, item)
		}
	}
	return out, false, nil
}

func (d *s3Destination) get(ctx context.Context, key string) (io.ReadCloser, error) {
	object, err := d.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(d.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, fmt.Errorf("download of %s/%s failed: %w", d.bucket, key, err)
	}
	return object.Body, nil
}

func (d *s3Destination) read(ctx context.Context, key string, limit int64) ([]byte, error) {
	body, err := d.get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return readLimited(body, limit)
}

func (d *s3Destination) fetch(ctx context.Context, key string, out io.Writer) (int64, error) {
	body, err := d.get(ctx, key)
	if err != nil {
		return 0, err
	}
	defer body.Close()
	return io.Copy(out, body)
}

func (d *s3Destination) close() {}

// --- SFTP -------------------------------------------------------------------

type sftpDestination struct {
	session *sftpSession
	dir     string
}

func (d *sftpDestination) list(context.Context) ([]remoteObject, bool, error) {
	entries, err := d.session.ReadDir(d.dir)
	if err != nil {
		return nil, false, fmt.Errorf("cannot list %s on the server: %w", d.dir, err)
	}
	var out []remoteObject
	for _, entry := range entries {
		if !entry.Mode().IsRegular() {
			continue
		}
		if len(out) == maxListedObjects {
			return out, true, nil
		}
		out = append(out, remoteObject{Key: path.Join(d.dir, entry.Name()), Size: entry.Size(), Modified: entry.ModTime().UTC()})
	}
	return out, false, nil
}

func (d *sftpDestination) read(_ context.Context, key string, limit int64) ([]byte, error) {
	f, err := d.session.Open(key)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s on the server: %w", key, err)
	}
	defer f.Close()
	return readLimited(f, limit)
}

func (d *sftpDestination) fetch(_ context.Context, key string, out io.Writer) (int64, error) {
	f, err := d.session.Open(key)
	if err != nil {
		return 0, fmt.Errorf("download of %s failed: %w", key, err)
	}
	defer f.Close()
	return f.WriteTo(out)
}

func (d *sftpDestination) close() { d.session.Close() }

// --- rclone -----------------------------------------------------------------

type rcloneDestination struct {
	secrets *TargetSecrets
	dir     string
}

type rcloneListing struct {
	Path    string    `json:"Path"`
	Name    string    `json:"Name"`
	Size    int64     `json:"Size"`
	ModTime time.Time `json:"ModTime"`
	IsDir   bool      `json:"IsDir"`
}

func (d *rcloneDestination) list(ctx context.Context) ([]remoteObject, bool, error) {
	var stdout bytes.Buffer
	if err := rcloneStream(ctx, d.secrets, nil, &limitedBuffer{buf: &stdout, limit: 32 << 20}, "lsjson", "--files-only", "--no-mimetype", rcloneTarget(d.dir)); err != nil {
		return nil, false, err
	}
	var listed []rcloneListing
	if err := json.Unmarshal(stdout.Bytes(), &listed); err != nil {
		return nil, false, errors.New("rclone listed the folder in a form the dashboard could not read")
	}
	var out []remoteObject
	for _, entry := range listed {
		if entry.IsDir || strings.Contains(entry.Path, "/") {
			continue
		}
		if len(out) == maxListedObjects {
			return out, true, nil
		}
		out = append(out, remoteObject{Key: path.Join(d.dir, entry.Name), Size: entry.Size, Modified: entry.ModTime.UTC()})
	}
	return out, false, nil
}

func (d *rcloneDestination) read(ctx context.Context, key string, limit int64) ([]byte, error) {
	var stdout bytes.Buffer
	if err := rcloneStream(ctx, d.secrets, nil, &stdout, "cat", "--count", fmt.Sprint(limit+1), rcloneTarget(key)); err != nil {
		return nil, err
	}
	return readLimited(&stdout, limit)
}

type countingWriter struct {
	out io.Writer
	n   int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	w.n += int64(n)
	return n, err
}

func (d *rcloneDestination) fetch(ctx context.Context, key string, out io.Writer) (int64, error) {
	counted := &countingWriter{out: out}
	err := rcloneStream(ctx, d.secrets, nil, counted, "cat", rcloneTarget(key))
	return counted.n, err
}

func (d *rcloneDestination) close() {}
