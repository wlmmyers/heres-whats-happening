// Package secrets builds a database password provider backed by AWS Secrets
// Manager. It exists so running tasks pick up an RDS-rotated password on the
// next connection instead of failing auth until they're restarted.
package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// parseDBPassword extracts the password field from an RDS-managed master user
// secret, which stores credentials as a JSON document.
func parseDBPassword(raw []byte) (string, error) {
	var doc struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("decode db secret: %w", err)
	}
	if doc.Password == "" {
		return "", errors.New("db secret has no password field")
	}
	return doc.Password, nil
}

// NewDBPasswordProvider returns a function that fetches the current password
// from the given Secrets Manager secret. Pass the result to
// db.NewPoolWithPassword; pgx calls it before each new connection, so a rotated
// password is picked up as connections recycle — no restart required.
func NewDBPasswordProvider(ctx context.Context, region, secretARN string) (func(context.Context) (string, error), error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := secretsmanager.NewFromConfig(cfg)
	return func(ctx context.Context) (string, error) {
		out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
			SecretId: &secretARN,
		})
		if err != nil {
			return "", fmt.Errorf("get secret value: %w", err)
		}
		if out.SecretString == nil {
			return "", errors.New("db secret has no string value")
		}
		return parseDBPassword([]byte(*out.SecretString))
	}, nil
}
