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

const minioImage = "minio/minio:RELEASE.2024-03-05T04-48-44Z"

// Minio creates a local minio instance using docker.
func Minio() error {
	uid := os.Getuid()
	gid := os.Getgid()

	stateDir, err := internal.StateDir()
	if err != nil {
		return fmt.Errorf("get state directory path: %w", err)
	}

	instanceName := "local-minio"
	dataDir := filepath.Join(stateDir, instanceName)

	err = os.MkdirAll(dataDir, 0o700)
	if err != nil {
		return fmt.Errorf("create local state directory: %w", err)
	}

	err = internal.StopContainerIfExists(instanceName)
	if err != nil {
		return fmt.Errorf("stop existing container: %w", err)
	}

	err = sh.Run("docker", "run", "-d", "--rm",
		"--name", instanceName,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		"-v", fmt.Sprintf("%s:/data", dataDir),
		"-p", "9000:9000",
		"-p", "9001:9001",
		minioImage,
		"server", "/data",
		"--console-address", ":9001",
	)
	if err != nil {
		return fmt.Errorf("start postgres: %w", err)
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
			return fmt.Errorf("failed to ensure that minio is available: %w", err)
		} else if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}

		break
	}

	return nil
}

// Bucket creates a bucket in the local minio instance.
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

func minioClient() (*minio.Client, error) {
	endpoint := "localhost:9000"
	accessKeyID := "minioadmin"
	secretAccessKey := "minioadmin"
	useSSL := false

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}

	return client, nil
}
