package backups

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// s3Client builds a client for AWS S3 or any S3-compatible service.
// Backblaze B2 exposes an S3 API, so the only difference between the two
// targets is the endpoint and the need for path-style addressing.
func s3Client(ctx context.Context, job *Job, secrets *TargetSecrets) (*s3.Client, error) {
	region := job.Target.Region
	if region == "" {
		region = "us-east-1"
	}
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if secrets != nil && secrets.AccessKeyID != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(secrets.AccessKeyID, secrets.SecretAccessKey, ""),
		))
	}
	// Without explicit credentials the SDK's default chain applies, which
	// picks up an instance role — the right behaviour on an EC2 host.
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if job.Target.Endpoint != "" {
			endpoint := job.Target.Endpoint
			if len(endpoint) < 8 || (endpoint[:7] != "http://" && endpoint[:8] != "https://") {
				endpoint = "https://" + endpoint
			}
			o.BaseEndpoint = &endpoint
			o.UsePathStyle = true
		}
	}), nil
}

func s3Upload(ctx context.Context, job *Job, secrets *TargetSecrets, key, localPath string) error {
	client, err := s3Client(ctx, job, secrets)
	if err != nil {
		return err
	}
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// The manager splits large archives into concurrent multipart uploads,
	// which is what makes a multi-gigabyte backup finish in reasonable time.
	uploader := manager.NewUploader(client, func(u *manager.Uploader) {
		u.PartSize = 16 * 1024 * 1024
		u.Concurrency = 3
	})
	ctx, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()
	_, err = uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: &job.Target.Bucket,
		Key:    &key,
		Body:   f,
	})
	if err != nil {
		return fmt.Errorf("upload to %s/%s failed: %w", job.Target.Bucket, key, err)
	}
	return nil
}

func s3Delete(ctx context.Context, job *Job, secrets *TargetSecrets, key string) error {
	client, err := s3Client(ctx, job, secrets)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &job.Target.Bucket, Key: &key})
	return err
}

// s3Download streams an artifact back for a restore.
func s3Download(ctx context.Context, job *Job, secrets *TargetSecrets, key, destPath string) error {
	client, err := s3Client(ctx, job, secrets)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()
	obj, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: &job.Target.Bucket, Key: &key})
	if err != nil {
		return fmt.Errorf("download of %s/%s failed: %w", job.Target.Bucket, key, err)
	}
	defer obj.Body.Close()

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, obj.Body)
	return err
}

// TestTarget verifies a destination is reachable and writable before an
// operator trusts a schedule to it. A backup configuration that only fails at
// 3am is worse than one that fails while somebody is looking at it. An SFTP
// server reached for the first time is pinned through pin.
func TestTarget(ctx context.Context, job *Job, secrets *TargetSecrets, pin HostKeyPinner) error {
	switch job.TargetKind {
	case TargetSFTP:
		return sftpTest(ctx, job, secrets, pin)
	case TargetRclone:
		return rcloneTest(ctx, job, secrets)
	}
	if job.TargetKind == TargetLocal {
		if err := os.MkdirAll(job.Target.Path, 0o700); err != nil {
			return err
		}
		probe, err := os.CreateTemp(job.Target.Path, ".vpsd-probe-*")
		if err != nil {
			return fmt.Errorf("%s is not writable: %w", job.Target.Path, err)
		}
		probe.Close()
		return os.Remove(probe.Name())
	}
	client, err := s3Client(ctx, job, secrets)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &job.Target.Bucket})
	if err != nil {
		return fmt.Errorf("cannot reach bucket %s: %w", job.Target.Bucket, err)
	}
	return nil
}

// remoteKey is where an archive named name lands at a remote destination:
// under the bucket's prefix, or in the directory on an SFTP server or in an
// rclone remote.
func remoteKey(job *Job, name string) string {
	switch job.TargetKind {
	case TargetSFTP, TargetRclone:
		return path.Join(job.Target.Path, name)
	}
	return strings.TrimPrefix(filepath.Join(job.Target.Prefix, name), "/")
}

// remoteLabel is the destination as a run's log names it.
func remoteLabel(job *Job, key string) string {
	switch job.TargetKind {
	case TargetSFTP:
		return "sftp://" + job.Target.Username + "@" + job.Target.sftpAddress() + "/" + strings.TrimPrefix(key, "/")
	case TargetRclone:
		return "rclone " + job.Target.RemoteType + ":" + key
	}
	return job.Target.Bucket + "/" + key
}

func uploadArtifact(ctx context.Context, job *Job, secrets *TargetSecrets, pin HostKeyPinner, key, localPath string) error {
	switch job.TargetKind {
	case TargetSFTP:
		return sftpUpload(ctx, job, secrets, pin, key, localPath)
	case TargetRclone:
		return rcloneUpload(ctx, secrets, key, localPath)
	}
	return s3Upload(ctx, job, secrets, key, localPath)
}

func downloadArtifact(ctx context.Context, job *Job, secrets *TargetSecrets, pin HostKeyPinner, key, destPath string) error {
	switch job.TargetKind {
	case TargetSFTP:
		return sftpDownload(ctx, job, secrets, pin, key, destPath)
	case TargetRclone:
		return rcloneDownload(ctx, secrets, key, destPath)
	}
	return s3Download(ctx, job, secrets, key, destPath)
}

func deleteArtifact(ctx context.Context, job *Job, secrets *TargetSecrets, pin HostKeyPinner, key string) error {
	switch job.TargetKind {
	case TargetSFTP:
		return sftpDelete(ctx, job, secrets, pin, key)
	case TargetRclone:
		return rcloneDelete(ctx, secrets, key)
	}
	return s3Delete(ctx, job, secrets, key)
}

// withPinnedKey gives an archive's own destination the host key pinned for
// its server since: an archive written by the run that first reached the
// server recorded no key, and must not be fetched from whoever answers now.
func withPinnedKey(archived, current *Job) *Job {
	if archived.TargetKind != TargetSFTP || archived.Target.HostKey != "" || current == nil ||
		current.TargetKind != TargetSFTP || current.Target.HostKey == "" || !sameSFTPServer(archived.Target, current.Target) {
		return archived
	}
	copy := *archived
	copy.Target.HostKey, copy.Target.HostKeyFingerprint = current.Target.HostKey, current.Target.HostKeyFingerprint
	return &copy
}

// transferJob is the destination an existing archive is read or removed at:
// the target its run recorded, with the host key since pinned for the same
// server. Only that server may be pinned on the job's behalf; an archive on
// a server the job no longer names, never pinned, is not fetched from
// whoever answers there now.
func (s *Store) transferJob(current *Job, run *Run) (*Job, HostKeyPinner) {
	archived := withPinnedKey(archiveJob(current, run), current)
	if archived.TargetKind == TargetSFTP && current.TargetKind == TargetSFTP && sameSFTPServer(archived.Target, current.Target) {
		return archived, s.Pinner(current.ID)
	}
	return archived, nil
}
