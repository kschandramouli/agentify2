package secrets

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// fakeAPI is a minimal in-memory stand-in for secretsManagerAPI.
type fakeAPI struct {
	secrets map[string]string // name/ARN -> value
	arns    map[string]string // name -> arn
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{secrets: map[string]string{}, arns: map[string]string{}}
}

func (f *fakeAPI) CreateSecret(_ context.Context, in *secretsmanager.CreateSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.CreateSecretOutput, error) {
	name := aws.ToString(in.Name)
	if _, exists := f.arns[name]; exists {
		return nil, &types.ResourceExistsException{Message: aws.String("already exists")}
	}
	arn := "arn:aws:secretsmanager:test:000000000000:secret:" + name
	f.arns[name] = arn
	f.secrets[arn] = aws.ToString(in.SecretString)
	return &secretsmanager.CreateSecretOutput{ARN: aws.String(arn), Name: aws.String(name)}, nil
}

func (f *fakeAPI) PutSecretValue(_ context.Context, in *secretsmanager.PutSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error) {
	name := aws.ToString(in.SecretId)
	arn, exists := f.arns[name]
	if !exists {
		return nil, errors.New("secret not found")
	}
	f.secrets[arn] = aws.ToString(in.SecretString)
	return &secretsmanager.PutSecretValueOutput{ARN: aws.String(arn)}, nil
}

func (f *fakeAPI) GetSecretValue(_ context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	arn := aws.ToString(in.SecretId)
	value, exists := f.secrets[arn]
	if !exists {
		return nil, errors.New("secret not found")
	}
	return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(value)}, nil
}

func (f *fakeAPI) DeleteSecret(_ context.Context, in *secretsmanager.DeleteSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.DeleteSecretOutput, error) {
	arn := aws.ToString(in.SecretId)
	if _, exists := f.secrets[arn]; !exists {
		return nil, errors.New("secret not found")
	}
	delete(f.secrets, arn)
	return &secretsmanager.DeleteSecretOutput{}, nil
}

func TestAWSManager_StoreCreatesNewSecret(t *testing.T) {
	m := &AWSManager{client: newFakeAPI()}
	arn, err := m.Store(context.Background(), "agentify/dev/integrations/int-1", "s3cr3t")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if arn == "" {
		t.Fatal("expected non-empty ARN")
	}

	got, err := m.Fetch(context.Background(), arn)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != "s3cr3t" {
		t.Fatalf("Fetch: got %q, want %q", got, "s3cr3t")
	}
}

func TestAWSManager_StoreUpdatesExistingSecret(t *testing.T) {
	m := &AWSManager{client: newFakeAPI()}
	name := "agentify/dev/integrations/int-2"

	arn1, err := m.Store(context.Background(), name, "first")
	if err != nil {
		t.Fatalf("first Store: %v", err)
	}

	arn2, err := m.Store(context.Background(), name, "second")
	if err != nil {
		t.Fatalf("second Store (update path): %v", err)
	}
	if arn1 != arn2 {
		t.Fatalf("expected same ARN on update, got %q then %q", arn1, arn2)
	}

	got, err := m.Fetch(context.Background(), arn2)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != "second" {
		t.Fatalf("Fetch after update: got %q, want %q", got, "second")
	}
}

func TestAWSManager_Delete(t *testing.T) {
	m := &AWSManager{client: newFakeAPI()}
	arn, err := m.Store(context.Background(), "agentify/dev/integrations/int-3", "value")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	if err := m.Delete(context.Background(), arn); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := m.Fetch(context.Background(), arn); err == nil {
		t.Fatal("expected Fetch to fail after Delete")
	}
}
