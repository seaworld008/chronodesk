// Command credential-maintain validates current credential storage, rotates
// authenticated envelopes, or quarantines unsupported password hashes. It
// never imports or rewrites plaintext secrets.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/auth"
	"github.com/seaworld008/chronodesk/server/internal/config"
	"github.com/seaworld008/chronodesk/server/internal/database"
	"github.com/seaworld008/chronodesk/server/internal/security"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type maintenanceMode string

const (
	modeValidate   maintenanceMode = "validate-only"
	modeRotate     maintenanceMode = "rotate"
	modeQuarantine maintenanceMode = "quarantine"
)

func main() {
	var validateOnly bool
	var rotate bool
	var quarantine bool
	flag.BoolVar(
		&validateOnly,
		"validate-only",
		false,
		"Validate current credential storage without modifying it",
	)
	flag.BoolVar(
		&rotate,
		"rotate",
		false,
		"Rewrap authenticated database-secret envelopes with the primary key",
	)
	flag.BoolVar(
		&quarantine,
		"quarantine",
		false,
		"Replace unsupported password hashes and suspend affected active accounts",
	)
	flag.Parse()
	mode, err := selectMaintenanceMode(validateOnly, rotate, quarantine)
	if err != nil {
		log.Fatal(err)
	}

	// 与主服务使用完全相同的配置和 keyring 解析路径，避免维护工具与
	// 运行时派生出不同的数据加密密钥。
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
	if err := database.ValidatePostgresTransport(
		dsn,
		os.Getenv("POSTGRES_ALLOW_INSECURE") == "true",
	); err != nil {
		log.Fatalf("PostgreSQL transport validation failed: %v", err)
	}
	protector, err := security.LoadDeploymentKeyring(
		[]byte(cfg.Agent.CredentialPepper),
	)
	if err != nil {
		log.Fatalf("Data-encryption keyring is invalid: %v", err)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		// Credential maintenance logs only aggregate results. SQL logging stays
		// disabled so stored envelopes and password hashes never reach output.
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

	switch mode {
	case modeValidate:
		if err := validateCredentialStorage(ctx, db, protector); err != nil {
			log.Fatalf("Credential storage validation failed: %v", err)
		}
		log.Println("Credential storage validation completed successfully")
	case modeRotate:
		if err := validateCredentialStorage(ctx, db, protector); err != nil {
			log.Fatalf("Pre-rotation credential validation failed: %v", err)
		}
		report, rotateErr := security.RotateDatabaseSecrets(ctx, db, protector)
		if rotateErr != nil {
			log.Fatalf("Database-secret rotation failed: %v", rotateErr)
		}
		if err := validateCredentialStorage(ctx, db, protector); err != nil {
			log.Fatalf("Post-rotation credential validation failed: %v", err)
		}
		log.Printf(
			"Database-secret rotation completed: rotated=%d verified=%d",
			report.Rotated,
			report.Verified,
		)
	case modeQuarantine:
		if err := security.ValidateDatabaseSecrets(ctx, db, protector); err != nil {
			log.Fatalf("Database-secret validation before quarantine failed: %v", err)
		}
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
		if err := validateCredentialStorage(ctx, db, protector); err != nil {
			log.Fatalf("Post-quarantine credential validation failed: %v", err)
		}
	}
}

func selectMaintenanceMode(validateOnly, rotate, quarantine bool) (maintenanceMode, error) {
	selected := 0
	var mode maintenanceMode
	for candidate, enabled := range map[maintenanceMode]bool{
		modeValidate:   validateOnly,
		modeRotate:     rotate,
		modeQuarantine: quarantine,
	} {
		if enabled {
			selected++
			mode = candidate
		}
	}
	if selected != 1 {
		return "", errors.New("exactly one of -validate-only, -rotate, or -quarantine is required")
	}
	return mode, nil
}

func validateCredentialStorage(
	ctx context.Context,
	db *gorm.DB,
	protector security.Protector,
) error {
	if err := security.ValidateDatabaseSecrets(ctx, db, protector); err != nil {
		return err
	}
	return auth.ValidateAuthCredentialStorage(ctx, db, protector)
}

func firstEnvironmentValue(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}
