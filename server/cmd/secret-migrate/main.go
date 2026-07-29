// Command secret-migrate is the explicit, one-shot migration path for legacy
// plaintext SMTP, webhook, and A2A push credentials. The application runtime
// never invokes this command automatically.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/auth"
	"github.com/seaworld008/chronodesk/server/internal/config"
	"github.com/seaworld008/chronodesk/server/internal/security"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	var validateOnly bool
	var quarantineUnsupportedPasswords bool
	flag.BoolVar(
		&validateOnly,
		"validate-only",
		false,
		"Validate encrypted database secrets without modifying them",
	)
	flag.BoolVar(
		&quarantineUnsupportedPasswords,
		"quarantine-unsupported-passwords",
		false,
		"Replace unverifiable password hashes and suspend affected active accounts",
	)
	flag.Parse()

	// 与主服务使用完全相同的 .env、生产配置校验和 Agent 凭据 pepper
	// 解析路径，避免迁移工具与运行时派生出不同的数据加密密钥。
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Load configuration: %v", err)
	}

	dsn := firstEnvironmentValue(
		"DATABASE_URL_UNPOOLED",
		"POSTGRES_URL_NON_POOLING",
		"DATABASE_URL",
	)
	if dsn == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}
	protector, err := security.LoadDeploymentKeyring(
		[]byte(cfg.Agent.CredentialPepper),
	)
	if err != nil {
		log.Fatalf("Data-encryption keyring is invalid: %v", err)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		// Migration statements may contain a legacy plaintext value. Keep the
		// ORM fully silent and report only sanitized aggregate errors below.
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		log.Fatalf("Connect to database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Open database handle: %v", err)
	}
	defer sqlDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if quarantineUnsupportedPasswords {
		report, quarantineErr := auth.QuarantineUnsupportedPasswordHashes(ctx, db)
		if quarantineErr != nil {
			log.Fatalf("Unsupported password quarantine failed: %v", quarantineErr)
		}
		log.Printf(
			"Unsupported password quarantine completed: quarantined=%d active_suspended=%d inactive_sanitized=%d",
			report.Quarantined,
			report.ActiveSuspended,
			report.InactiveSanitized,
		)
	}
	if validateOnly {
		if err := security.ValidateDatabaseSecrets(ctx, db, protector); err != nil {
			log.Fatalf("Encrypted-secret validation failed: %v", err)
		}
		if err := auth.ValidateAuthCredentialStorage(ctx, db, protector); err != nil {
			log.Fatalf("Authentication credential validation failed: %v", err)
		}
		log.Println("Encrypted-secret and authentication credential validation completed successfully")
		return
	}

	var (
		secretReport security.LegacySecretMigrationReport
		authReport   auth.CredentialMigrationReport
	)
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var migrationErr error
		secretReport, migrationErr = security.MigrateLegacyDatabaseSecrets(
			ctx,
			tx,
			protector,
		)
		if migrationErr != nil {
			return migrationErr
		}
		authReport, migrationErr = auth.MigrateLegacyAuthCredentials(
			ctx,
			tx,
			protector,
		)
		return migrationErr
	})
	if err != nil {
		log.Fatalf("Legacy credential migration failed: %v", err)
	}
	if err := security.ValidateDatabaseSecrets(ctx, db, protector); err != nil {
		log.Fatalf("Post-migration validation failed: %v", err)
	}
	if err := auth.ValidateAuthCredentialStorage(ctx, db, protector); err != nil {
		log.Fatalf("Post-migration authentication validation failed: %v", err)
	}
	log.Printf(
		"Legacy credential migration completed: encrypted=%d rotated=%d verified=%d otp_encrypted=%d backup_codes_hashed=%d disabled_otp_cleared=%d",
		secretReport.Encrypted,
		secretReport.Rotated,
		secretReport.Verified,
		authReport.EncryptedOTPSecrets,
		authReport.HashedBackupCodes,
		authReport.ClearedDisabledOTP,
	)
}

func firstEnvironmentValue(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}
