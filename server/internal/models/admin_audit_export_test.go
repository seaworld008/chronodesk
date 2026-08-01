package models

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAdminAuditExportPublicIDRequiresCanonicalUUIDv7(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&AdminAuditExportJob{}); err != nil {
		t.Fatal(err)
	}
	validID := uuid.Must(uuid.NewV7()).String()
	for _, test := range []struct {
		name     string
		publicID string
		valid    bool
	}{
		{name: "generated", valid: true},
		{name: "canonical v7", publicID: validID, valid: true},
		{name: "uuid v4", publicID: uuid.NewString()},
		{name: "uppercase v7", publicID: strings.ToUpper(validID)},
		{name: "padded v7", publicID: " " + validID},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC()
			job := AdminAuditExportJob{
				PublicID:        test.publicID,
				RequesterUserID: 1,
				RequesterRole:   PlatformRolePlatformAdmin,
				FilterSnapshot:  "{}",
				FilterHash:      strings.Repeat("a", 64),
				StartTime:       now.Add(-time.Hour),
				EndTime:         now,
				AnchorCreatedAt: now,
				AnchorID:        1,
				State:           AdminAuditExportQueued,
				RequestedAt:     now,
			}
			err := db.Create(&job).Error
			if test.valid && err != nil {
				t.Fatalf("valid audit export rejected: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("non-canonical audit export public id was accepted")
			}
			if test.valid {
				parsed, parseErr := uuid.Parse(job.PublicID)
				if parseErr != nil ||
					parsed.Version() != 7 ||
					parsed.String() != job.PublicID {
					t.Fatalf("stored public id = %q", job.PublicID)
				}
			}
		})
	}
}
