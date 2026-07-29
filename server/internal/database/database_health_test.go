package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type healthRedis struct {
	pingErr error
}

func (r *healthRedis) Ping(context.Context) error { return r.pingErr }
func (r *healthRedis) Set(context.Context, string, interface{}, time.Duration) error {
	return nil
}
func (r *healthRedis) Get(context.Context, string) (string, error) { return "", nil }
func (r *healthRedis) Del(context.Context, ...string) error        { return nil }
func (r *healthRedis) Exists(context.Context, ...string) (int64, error) {
	return 0, nil
}
func (r *healthRedis) Expire(context.Context, string, time.Duration) error { return nil }
func (r *healthRedis) TTL(context.Context, string) (time.Duration, error)  { return 0, nil }
func (r *healthRedis) Eval(context.Context, string, []string, ...interface{}) (interface{}, error) {
	return nil, nil
}
func (r *healthRedis) Close() error { return nil }

func TestHealthCheckRequiresPostgreSQLAndRedis(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:database-health?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	tests := []struct {
		name     string
		database *Database
		wantErr  string
	}{
		{
			name:     "missing PostgreSQL",
			database: &Database{Redis: &healthRedis{}},
			wantErr:  "PostgreSQL client is not initialized",
		},
		{
			name:     "missing Redis",
			database: &Database{DB: db},
			wantErr:  "Redis client is not initialized",
		},
		{
			name:     "Redis unavailable",
			database: &Database{DB: db, Redis: &healthRedis{pingErr: errors.New("unavailable")}},
			wantErr:  "Redis ping failed",
		},
		{
			name:     "all dependencies healthy",
			database: &Database{DB: db, Redis: &healthRedis{}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.database.HealthCheck()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("HealthCheck() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("HealthCheck() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}
