// Command migrate-integration-tokens is a one-time operational script (ADR
// 0025): it moves every Integration row's plaintext outbound token into AWS
// Secrets Manager and clears the plaintext column. Run it once, after
// setting INTEGRATION_SECRETS_PREFIX, before any further admin edits to
// existing integrations (see the ADR's rollout note on why ordering matters).
//
// Usage:
//
//	INTEGRATION_SECRETS_PREFIX=agentify/dev/integrations \
//	  go run ./cmd/migrate-integration-tokens
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	apicfg "github.com/chan/agentify/backend/internal/config"
	"github.com/chan/agentify/backend/internal/secrets"
	"github.com/chan/agentify/backend/internal/storage/postgres"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := apicfg.LoadFromEnv()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}
	if cfg.IntegrationSecretsPrefix == "" {
		logger.Error("INTEGRATION_SECRETS_PREFIX is not set — nothing to migrate to, aborting")
		os.Exit(1)
	}

	sslMode := "require"
	if cfg.DBHost == "localhost" || cfg.DBHost == "127.0.0.1" {
		sslMode = "disable"
	}
	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, sslMode,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pg, err := postgres.NewClient(ctx, connStr, logger)
	if err != nil {
		logger.Error("connect to postgres", "error", err)
		os.Exit(1)
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.AWSRegion))
	if err != nil {
		logger.Error("load AWS config", "error", err)
		os.Exit(1)
	}
	mgr := secrets.NewAWSManager(secretsmanager.NewFromConfig(awsCfg))

	rows, err := pg.ListIntegrations(ctx)
	if err != nil {
		logger.Error("list integrations", "error", err)
		os.Exit(1)
	}

	var migrated, skipped int
	for _, row := range rows {
		if row.Token == "" || row.TokenSecretARN != "" {
			fmt.Printf("SKIP  id=%s name=%q (already migrated or no plaintext token)\n", row.ID, row.Name)
			skipped++
			continue
		}
		name := cfg.IntegrationSecretsPrefix + "/" + row.ID
		arn, err := mgr.Store(ctx, name, row.Token)
		if err != nil {
			fmt.Printf("FAIL  id=%s name=%q: store secret: %v\n", row.ID, row.Name, err)
			continue
		}
		row.Token = ""
		row.TokenSecretARN = arn
		if err := pg.UpdateIntegration(ctx, &row); err != nil {
			fmt.Printf("FAIL  id=%s name=%q: update row after storing secret %s: %v\n", row.ID, row.Name, arn, err)
			continue
		}
		fmt.Printf("OK    id=%s name=%q -> %s\n", row.ID, row.Name, arn)
		migrated++
	}

	fmt.Printf("\nDone: %d migrated, %d skipped, %d total\n", migrated, skipped, len(rows))
}
