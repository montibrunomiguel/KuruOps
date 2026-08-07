package secrets

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// AWSKMSStore backs Store by encrypting a secret value with an AWS KMS key
// and using the resulting ciphertext itself (base64, tagged with a ref
// prefix) as the "ref" -- there's no separate storage layer to write a
// value to (unlike blobstore.S3Store's actual bucket), and KMS's
// Encrypt/Decrypt API is enough for values this small (API keys, bind
// passwords), so this doesn't reach for the added cost/complexity of
// Secrets Manager just to hold a reference.
type AWSKMSStore struct {
	client *kms.Client
	keyID  string
}

// NewAWSKMSStore builds a client from explicit credentials/region -- no
// ambient AWS config file, environment variables, or IAM instance role are
// consulted, matching blobstore.NewS3Store's reasoning: ArgusOps is
// self-hosted and may not be running inside AWS at all.
func NewAWSKMSStore(region, accessKeyID, secretAccessKey, keyID string) *AWSKMSStore {
	cfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
	}
	return &AWSKMSStore{client: kms.NewFromConfig(cfg), keyID: keyID}
}

const kmsRefPrefix = "kms:"

func (s *AWSKMSStore) Put(ctx context.Context, tenantID, purpose, value string) (string, error) {
	out, err := s.client.Encrypt(ctx, &kms.EncryptInput{
		KeyId:     aws.String(s.keyID),
		Plaintext: []byte(value),
	})
	if err != nil {
		return "", fmt.Errorf("kms encrypt: %w", err)
	}
	return kmsRefPrefix + base64.RawURLEncoding.EncodeToString(out.CiphertextBlob), nil
}

// Resolve decrypts ref back to its plaintext value. An empty ref (nothing
// stored yet) resolves to "", matching EnvStore's behavior for an unknown
// ref -- callers already treat "" as "no value configured", not a fetch
// error.
func (s *AWSKMSStore) Resolve(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	encoded, ok := strings.CutPrefix(ref, kmsRefPrefix)
	if !ok {
		return "", fmt.Errorf("not a kms secret ref: %q", ref)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode kms ciphertext ref: %w", err)
	}

	out, err := s.client.Decrypt(ctx, &kms.DecryptInput{
		KeyId:          aws.String(s.keyID),
		CiphertextBlob: ciphertext,
	})
	if err != nil {
		return "", fmt.Errorf("kms decrypt: %w", err)
	}
	return string(out.Plaintext), nil
}
