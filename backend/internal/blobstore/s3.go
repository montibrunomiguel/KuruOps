package blobstore

import (
	"context"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Store backs Store with an AWS S3 bucket, using long-lived static
// credentials entered by the admin in Settings (see StorageConfigService)
// rather than an IAM instance role -- ArgusOps is self-hosted and may not
// be running inside AWS at all, so there is no role to assume by default.
type S3Store struct {
	client *s3.Client
	bucket string
}

// NewS3Store builds a client from explicit credentials/region -- no
// ambient AWS config file or environment variables are consulted, so
// behavior doesn't depend on whatever happens to be on the host running
// cmd/api.
func NewS3Store(region, accessKeyID, secretAccessKey, bucket string) *S3Store {
	cfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
	}
	return &S3Store{client: s3.NewFromConfig(cfg), bucket: bucket}
}

func (s *S3Store) Put(ctx context.Context, key string, content io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          content,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	return err
}

func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	contentType := "application/octet-stream"
	if out.ContentType != nil {
		contentType = *out.ContentType
	}
	return out.Body, contentType, nil
}
