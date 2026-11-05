package minio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Client struct {
	client *minio.Client
	bucket string
}

type Entry struct {
	Key     string    `json:"key"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

func NewClient(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Client, error) {
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}
	return &Client{client: mc, bucket: bucket}, nil
}

func (c *Client) EnsureBucket(ctx context.Context) error {
	exists, err := c.client.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		if err := c.client.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("failed to create bucket: %w", err)
		}
	}
	return nil
}

func (c *Client) Upload(localPath, objectKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_, err := c.client.FPutObject(ctx, c.bucket, objectKey, localPath, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	return nil
}

func (c *Client) Stat(objectKey string) (size int64, modTime time.Time, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := c.client.StatObject(ctx, c.bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		return 0, time.Time{}, err
	}
	return info.Size, info.LastModified, nil
}

func (c *Client) Delete(objectKey string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.client.RemoveObject(ctx, c.bucket, objectKey, minio.RemoveObjectOptions{})
}

func (c *Client) List(prefix string) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prefix = strings.Trim(prefix, "/")
	var entries []Entry
	objCh := c.client.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	for obj := range objCh {
		if obj.Err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", obj.Err)
		}
		if strings.HasSuffix(obj.Key, "/") {
			continue
		}
		entries = append(entries, Entry{Key: obj.Key, Size: obj.Size, ModTime: obj.LastModified})
	}
	return entries, nil
}

type GetResult struct {
	Body       io.ReadCloser
	TotalSize  int64
	Start, End int64
}

func (c *Client) Get(ctx context.Context, objectKey, rangeHeader string) (*GetResult, error) {
	size, _, err := c.Stat(objectKey)
	if err != nil {
		return nil, err
	}

	opts := minio.GetObjectOptions{}
	start, end := int64(0), size-1
	if rangeHeader != "" {
		s, e, ok := parseRange(rangeHeader, size)
		if ok {
			if err := opts.SetRange(s, e); err != nil {
				return nil, fmt.Errorf("failed to parse Range: %w", err)
			}
			start, end = s, e
		}
	}

	rc, err := c.client.GetObject(ctx, c.bucket, objectKey, opts)
	if err != nil {
		return nil, err
	}
	return &GetResult{Body: rc, TotalSize: size, Start: start, End: end}, nil
}

func parseRange(rangeHeader string, total int64) (start, end int64, ok bool) {
	v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rangeHeader), "bytes="))
	if v == "" {
		return 0, 0, false
	}
	i := strings.Index(v, "-")
	if i < 0 {
		return 0, 0, false
	}
	a, b := v[:i], strings.TrimSpace(v[i+1:])
	switch {
	case a == "" && b == "":
		return 0, 0, false
	case a == "":
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > total {
			n = total
		}
		return total - n, total - 1, true
	default:
		s, err := strconv.ParseInt(a, 10, 64)
		if err != nil || s < 0 || s >= total {
			return 0, 0, false
		}
		if b == "" {
			return s, total - 1, true
		}
		e, err := strconv.ParseInt(b, 10, 64)
		if err != nil || e < s || e >= total {
			return 0, 0, false
		}
		return s, e, true
	}
}

func (c *Client) TestAndUpload(basePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := c.EnsureBucket(ctx); err != nil {
		return err
	}

	key := strings.Trim(strings.TrimSpace(basePath), "/")
	if key != "" {
		key += "/"
	}
	key += ".connect_test"

	payload := []byte(fmt.Sprintf("surveillance-system minio connect test %d", time.Now().Unix()))
	if _, err := c.client.PutObject(ctx, c.bucket, key, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("failed to write test object: %w", err)
	}
	defer c.client.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})

	rc, err := c.client.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to read test object: %w", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil || string(got) != string(payload) {
		return fmt.Errorf("test object read verification failed: %v", err)
	}
	return nil
}
