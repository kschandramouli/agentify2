// Package secrets stores and retrieves per-row credentials in AWS Secrets
// Manager, so callers never hold a value like Integration.Token in plaintext
// Postgres — see ADR 0025.
package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// Manager stores, retrieves, and removes single string secret values. Callers
// key each secret by an opaque name (e.g. "{prefix}/{integration_id}") and get
// back an ARN to persist alongside their own row; Fetch/Delete take that ARN.
type Manager interface {
	// Store creates the named secret if it doesn't exist, or updates its
	// value if it does. Returns the secret's ARN.
	Store(ctx context.Context, name, value string) (arn string, err error)
	// Fetch returns the plaintext value for a previously stored secret ARN.
	Fetch(ctx context.Context, arn string) (value string, err error)
	// Delete removes a secret by ARN. Recovery window follows the account's
	// default Secrets Manager policy (not force-deleted, so an accidental
	// delete is still recoverable for the standard window).
	Delete(ctx context.Context, arn string) error
}

// secretsManagerAPI is the subset of *secretsmanager.Client this package
// calls — narrowed to an interface so tests can inject a fake without
// standing up real AWS calls.
type secretsManagerAPI interface {
	CreateSecret(ctx context.Context, in *secretsmanager.CreateSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error)
	PutSecretValue(ctx context.Context, in *secretsmanager.PutSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error)
	GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
	DeleteSecret(ctx context.Context, in *secretsmanager.DeleteSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.DeleteSecretOutput, error)
}

// AWSManager is the production Manager, backed by AWS Secrets Manager.
type AWSManager struct {
	client secretsManagerAPI
}

// NewAWSManager wraps a real Secrets Manager client.
func NewAWSManager(client *secretsmanager.Client) *AWSManager {
	return &AWSManager{client: client}
}

// Store creates the secret on first write; on a subsequent write to the same
// name (e.g. an admin editing an Integration's token) it falls back to
// updating the existing secret's value instead of erroring, since the name is
// deterministic per-row (`{prefix}/{integration_id}`) and reused across edits.
func (m *AWSManager) Store(ctx context.Context, name, value string) (string, error) {
	created, err := m.client.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(name),
		SecretString: aws.String(value),
	})
	if err == nil {
		return aws.ToString(created.ARN), nil
	}
	var exists *types.ResourceExistsException
	if !errors.As(err, &exists) {
		return "", fmt.Errorf("create secret %q: %w", name, err)
	}
	updated, err := m.client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(name),
		SecretString: aws.String(value),
	})
	if err != nil {
		return "", fmt.Errorf("update secret %q: %w", name, err)
	}
	return aws.ToString(updated.ARN), nil
}

// Fetch retrieves the plaintext value for a stored secret ARN.
func (m *AWSManager) Fetch(ctx context.Context, arn string) (string, error) {
	out, err := m.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(arn),
	})
	if err != nil {
		return "", fmt.Errorf("fetch secret %q: %w", arn, err)
	}
	return aws.ToString(out.SecretString), nil
}

// Delete removes a secret by ARN, honoring the account's default recovery
// window rather than force-deleting.
func (m *AWSManager) Delete(ctx context.Context, arn string) error {
	_, err := m.client.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
		SecretId: aws.String(arn),
	})
	if err != nil {
		return fmt.Errorf("delete secret %q: %w", arn, err)
	}
	return nil
}
