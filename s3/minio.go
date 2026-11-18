package s3

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/magefile/mage/sh"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/ttab/mage/internal"
)

// RustFSImage is the S3-compatible server that [Minio] runs. MinIO no
// longer publishes container images, so the target runs RustFS in its place.
const RustFSImage = "rustfs/rustfs:1.0.6"

// legacyMinioInstance is the container name the target used while it ran
// MinIO. A container left running under that name holds port 9000.
const legacyMinioInstance = "local-minio"

// Minio creates a local S3-compatible instance using docker. The name is kept
// from when it ran MinIO; it now runs RustFS, see [RustFSImage].
func Minio() error {
	uid := os.Getuid()
	gid := os.Getgid()

	stateDir, err := internal.StateDir()
	if err != nil {
		return fmt.Errorf("get state directory path: %w", err)
	}

	instanceName := "local-rustfs"
	dataDir := filepath.Join(stateDir, instanceName)

	err = os.MkdirAll(dataDir, 0o700)
	if err != nil {
		return fmt.Errorf("create local state directory: %w", err)
	}

	for _, name := range []string{instanceName, legacyMinioInstance} {
		err = internal.StopContainerIfExists(name)
		if err != nil {
			return fmt.Errorf("stop existing container %q: %w", name, err)
		}
	}

	err = sh.Run("docker", "run", "-d", "--rm",
		"--name", instanceName,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		"-v", fmt.Sprintf("%s:/data", dataDir),
		"-p", "9000:9000",
		"-p", "9001:9001",
		"-e", "RUSTFS_ACCESS_KEY="+accessKeyID,
		"-e", "RUSTFS_SECRET_KEY="+secretAccessKey,
		RustFSImage,
		"/data",
	)
	if err != nil {
		return fmt.Errorf("start rustfs: %w", err)
	}

	client, err := minioClient()
	if err != nil {
		return err
	}

	ctx := context.Background()

	deadline := time.Now().Add(20 * time.Second)

	for {
		_, err := client.BucketExists(ctx, "randomname")
		if err != nil && time.Now().After(deadline) {
			return fmt.Errorf("ensure that rustfs is available: %w", err)
		} else if err != nil {
			time.Sleep(300 * time.Millisecond)

			continue
		}

		break
	}

	return nil
}

// Bucket creates a bucket in the local S3 instance started by [Minio].
func Bucket(name string) error {
	client, err := minioClient()
	if err != nil {
		return err
	}

	ctx := context.Background()

	exists, err := client.BucketExists(ctx, name)
	if err != nil {
		return fmt.Errorf("check if bucket exists: %w", err)
	}

	if exists {
		return nil
	}

	err = client.MakeBucket(ctx, name,
		minio.MakeBucketOptions{})
	if err != nil {
		return fmt.Errorf("create bucket: %w", err)
	}

	return nil
}

// The credentials keep MinIO's defaults so that projects configured against
// the old instance keep working.
const (
	endpoint        = "localhost:9000"
	accessKeyID     = "minioadmin"
	secretAccessKey = "minioadmin"
)

func minioClient() (*minio.Client, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: false,
	})
	if err != nil {
		return nil, fmt.Errorf("create s3 client: %w", err)
	}

	return client, nil
}
