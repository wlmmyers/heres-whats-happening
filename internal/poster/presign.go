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

// Every poster key's required prefix and extension. posterKeyBase in the Lambda
// emits "posters/v<N>/u-<userId>/<performer>/<venue>-<date>-<digest>", to which
// the sink appends ".png".
const (
	keyPrefix = "posters/v"
	keySuffix = ".png"
)

var (
	ErrKeyOutsidePosterPrefix = errors.New("poster: key outside the posters/v prefix")
	ErrKeyNotPNG              = errors.New("poster: key is not a .png")
)

// ValidateKey guards the one place this service signs on another component's
// say-so. The key arrives in the Lambda's response body; the bucket never does.
//
// The extension check matters because PresignGetObject sets no
// ResponseContentType, so a browser following the URL sees whatever
// Content-Type the object was stored with. A key ending in .svg or .html would
// therefore be served as ACTIVE CONTENT from the S3 origin — which is the
// stored-XSS shape the SVG artifact was removed to close. Nothing writes those
// objects anymore, so this check is not load-bearing today; it is here so the
// property holds structurally rather than by the accident of what the current
// producer happens to emit. Go only ever signs the png: the .json provenance
// sidecar is read by the Lambda's find(), never by this service.
func ValidateKey(key string) (string, error) {
	if !strings.HasPrefix(key, keyPrefix) || strings.Contains(key, "..") {
		return "", fmt.Errorf("%w: %q", ErrKeyOutsidePosterPrefix, key)
	}
	if !strings.HasSuffix(key, keySuffix) {
		return "", fmt.Errorf("%w: %q", ErrKeyNotPNG, key)
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
