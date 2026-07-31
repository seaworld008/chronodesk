package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestCleanupServiceListCleanupLogPageIsBoundedAndStable(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&models.CleanupLog{}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	logs := make([]models.CleanupLog, 0, 152)
	for index := 0; index < 151; index++ {
		logs = append(logs, models.CleanupLog{
			CreatedAt:      base.Add(time.Duration(index) * time.Second),
			TaskType:       "login_history",
			Status:         "completed",
			StartTime:      base,
			RecordsDeleted: index,
			ErrorMessage:   strings.Repeat("故障", 400),
			RetentionDays:  30,
			CutoffDate:     base.AddDate(0, 0, -30),
			TriggerType:    "scheduled",
		})
	}
	logs = append(logs, models.CleanupLog{
		CreatedAt:     base,
		TaskType:      "other",
		Status:        "failed",
		StartTime:     base,
		RetentionDays: 30,
		CutoffDate:    base.AddDate(0, 0, -30),
		TriggerType:   "manual",
	})
	if err := db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	service := NewCleanupService(db)
	request := DirectoryPageRequest{
		Page:      1,
		PageSize:  100,
		SortBy:    "created_at",
		SortOrder: "desc",
	}
	first, err := service.ListCleanupLogPage(
		context.Background(),
		"login_history",
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Page = 2
	second, err := service.ListCleanupLogPage(
		context.Background(),
		"login_history",
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 151 || first.TotalPages != 2 ||
		len(first.Items) != 100 || len(second.Items) != 51 {
		t.Fatalf("unexpected pages: first=%+v second=%+v", first, second)
	}
	seen := make(map[uint]struct{}, 151)
	for _, page := range []*DirectoryPage[*models.CleanupLogResponse]{
		first,
		second,
	} {
		for _, log := range page.Items {
			if log.TaskType != "login_history" {
				t.Fatalf("task filter leaked row: %+v", log)
			}
			if _, duplicate := seen[log.ID]; duplicate {
				t.Fatalf("cleanup log %d appears on multiple pages", log.ID)
			}
			seen[log.ID] = struct{}{}
			if len([]rune(log.ErrorMessage)) > 501 {
				t.Fatalf("cleanup error was not bounded: %d runes", len([]rune(log.ErrorMessage)))
			}
		}
	}
	if _, err := service.ListCleanupLogPage(
		context.Background(),
		" invalid ",
		DirectoryPageRequest{
			Page:      1,
			PageSize:  25,
			SortBy:    "created_at",
			SortOrder: "desc",
		},
	); !errors.Is(err, ErrDirectoryListQuery) {
		t.Fatalf("invalid task filter error = %v", err)
	}
}
