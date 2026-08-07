package secrets

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAWSKMSStore_PutAndResolve lives in-package (not secrets_test) so it
// can point the SDK's kms.Client at a local httptest.Server via
// kms.Options.BaseEndpoint instead of the real AWS KMS endpoint --
// AWSKMSStore itself has no seam for this, matching S3Store's convention
// of always dialing whatever endpoint the aws.Config says to.
func TestAWSKMSStore_PutAndResolve(t *testing.T) {
	// A fake KMS server implementing just enough of the AWS JSON 1.1 wire
	// protocol for Encrypt/Decrypt: "encryption" is a no-op passthrough
	// (ciphertext == the base64 plaintext the SDK sent), which is enough to
	// prove Put/Resolve round-trip through the real SDK request/response
	// marshaling without needing real KMS crypto.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		var reqBody map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")

		switch target {
		case "TrentService.Encrypt":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"CiphertextBlob": reqBody["Plaintext"], // passthrough "encryption"
				"KeyId":          reqBody["KeyId"],
			})
		case "TrentService.Decrypt":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"Plaintext": reqBody["CiphertextBlob"], // passthrough "decryption"
				"KeyId":     reqBody["KeyId"],
			})
		default:
			t.Fatalf("unexpected KMS action %q", target)
		}
	}))
	defer srv.Close()

	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKIAFAKE", "secretfake", ""),
	}
	client := kms.NewFromConfig(cfg, func(o *kms.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
	})
	store := &AWSKMSStore{client: client, keyID: "arn:aws:kms:us-east-1:000000000000:key/fake-key-id"}

	ref, err := store.Put(t.Context(), "tenant-1", "llm:openai", "sk-secret-value")
	require.NoError(t, err)
	assert.NotContains(t, ref, "sk-secret-value", "the ref must not embed the raw secret value")
	assert.Contains(t, ref, kmsRefPrefix)

	value, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err)
	assert.Equal(t, "sk-secret-value", value)
}

func TestAWSKMSStore_Resolve_EmptyRef(t *testing.T) {
	store := &AWSKMSStore{}
	value, err := store.Resolve(t.Context(), "")
	require.NoError(t, err)
	assert.Empty(t, value)
}

func TestAWSKMSStore_Resolve_NotAKMSRef(t *testing.T) {
	store := &AWSKMSStore{}
	_, err := store.Resolve(t.Context(), "not-a-kms-ref")
	assert.ErrorContains(t, err, "not a kms secret ref")
}

func TestAWSKMSStore_Resolve_MalformedCiphertext(t *testing.T) {
	store := &AWSKMSStore{}
	_, err := store.Resolve(t.Context(), kmsRefPrefix+"not valid base64!!!")
	assert.Error(t, err)
}
