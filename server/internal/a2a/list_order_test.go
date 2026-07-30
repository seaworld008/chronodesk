package a2a

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMemoryStoreListsTasksByStatusTimestamp(t *testing.T) {
	assertTaskStatusTimestampOrder(t, NewMemoryStore())
}

func TestGormStoreListsTasksByStatusTimestamp(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	store := NewGormStoreWithProtector(db, nil)
	if err := store.AutoMigrate(); err != nil {
		t.Fatalf("migrate A2A models: %v", err)
	}
	assertTaskStatusTimestampOrder(t, store)
}

func assertTaskStatusTimestampOrder(t *testing.T, store Store) {
	t.Helper()
	base := time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)
	tasks := []Task{
		{
			ID:        "created-newest",
			ContextID: "status-order",
			Status: TaskStatus{
				State:     TaskStateSubmitted,
				Timestamp: base,
			},
			CreatedAt:    base.Add(2 * time.Hour),
			LastModified: base,
			Version:      1,
		},
		{
			ID:        "middle",
			ContextID: "status-order",
			Status: TaskStatus{
				State:     TaskStateWorking,
				Timestamp: base.Add(time.Hour),
			},
			CreatedAt:    base.Add(time.Hour),
			LastModified: base.Add(time.Hour),
			Version:      1,
		},
		{
			ID:        "status-newest",
			ContextID: "status-order",
			Status: TaskStatus{
				State:     TaskStateInputRequired,
				Timestamp: base.Add(2 * time.Hour),
			},
			CreatedAt:    base,
			LastModified: base.Add(2 * time.Hour),
			Version:      1,
		},
	}
	for _, task := range tasks {
		task.StatusHistory = []TaskStatus{task.Status}
		if err := store.CreateTask(a2aTestContext(t), task); err != nil {
			t.Fatalf("create task %q: %v", task.ID, err)
		}
	}

	first, err := store.ListTasks(
		a2aTestContext(t),
		ListTasksParams{ContextID: "status-order", PageSize: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tasks) != 2 ||
		first.Tasks[0].ID != "status-newest" ||
		first.Tasks[1].ID != "middle" ||
		first.NextPageToken == "" {
		t.Fatalf("first status-ordered page: %#v", first)
	}

	second, err := store.ListTasks(a2aTestContext(t), ListTasksParams{
		ContextID: "status-order",
		PageSize:  2,
		PageToken: first.NextPageToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Tasks) != 1 ||
		second.Tasks[0].ID != "created-newest" ||
		second.NextPageToken != "" {
		t.Fatalf("second status-ordered page: %#v", second)
	}
}
