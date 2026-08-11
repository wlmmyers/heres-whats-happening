package poster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Presigned GET lifetime. Matches what the Lambda used to mint.
const presignTTL = 3600 * time.Second

// keyPrefix is every poster key's required prefix. posterKeyBase in the Lambda
// emits "posters/v<N>/<performer>/<venue>-<date>.<ext>".
const keyPrefix = "posters/v"

var ErrKeyOutsidePosterPrefix = errors.New("poster: key outside the posters/v prefix")

// ValidateKey guards the one place this service signs on another component's
// say-so. The key arrives in the Lambda's response body; the bucket never does.
func ValidateKey(key string) (string, error) {
	if !strings.HasPrefix(key, keyPrefix) || strings.Contains(key, "..") {
		return "", fmt.Errorf("%w: %q", ErrKeyOutsidePosterPrefix, key)
	}
	return key, nil
}

type Presigner interface {
	PresignGet(ctx context.Context, key string) (string, error)
}

// S3Presigner mints short-lived GET URLs for poster artifacts. The bucket is
// configuration, never taken from a response.
type S3Presigner struct {
	client *s3.PresignClient
	bucket string
}

func NewPresigner(api *s3.Client, bucket string) *S3Presigner {
	return &S3Presigner{client: s3.NewPresignClient(api), bucket: bucket}
}

func (p *S3Presigner) PresignGet(ctx context.Context, key string) (string, error) {
	safe, err := ValidateKey(key)
	if err != nil {
		return "", err
	}
	out, err := p.client.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(safe),
	}, s3.WithPresignExpires(presignTTL))
	if err != nil {
		return "", fmt.Errorf("presign %s: %w", safe, err)
	}
	return out.URL, nil
}

// normalize makes the job key insensitive to case and surrounding whitespace,
// so "La Luz " and "la luz" are one job rather than two.
func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
