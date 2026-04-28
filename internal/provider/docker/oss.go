package docker

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"mp_sched/internal/config"
)

func newMinioClient(o *config.DockerOSS) (*minio.Client, error) {
	if o == nil || !o.Enable {
		return nil, fmt.Errorf("oss: not enabled")
	}
	ep := strings.TrimSpace(o.Endpoint)
	if ep == "" || strings.TrimSpace(o.Bucket) == "" || strings.TrimSpace(o.AccessKey) == "" || strings.TrimSpace(o.SecretKey) == "" {
		return nil, fmt.Errorf("oss: endpoint, bucket, access_key, secret_key required")
	}
	return minio.New(ep, &minio.Options{
		Creds:  credentials.NewStaticV4(o.AccessKey, o.SecretKey, ""),
		Secure: o.UseSSL,
		Region: strings.TrimSpace(o.Region),
	})
}

func (c *Client) downloadFromOSS(ctx context.Context, tID, objectKey string) (string, error) {
	if c.cfg == nil {
		return "", fmt.Errorf("oss: nil docker config")
	}
	mc, err := newMinioClient(&c.cfg.OSS)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(objectKey)
	dir := c.taskStageDir(tID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	local := filepath.Join(dir, safeLocalName(key))
	if err = mc.FGetObject(ctx, c.cfg.OSS.Bucket, key, local, minio.GetObjectOptions{}); err != nil {
		return "", err
	}
	return local, nil
}

func safeLocalName(ossKey string) string {
	s := strings.ReplaceAll(ossKey, "\\", "/")
	base := path.Base(s)
	if base == "" || base == "." {
		return "object"
	}
	return base
}
