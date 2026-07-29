package database

import "testing"

func TestValidatePostgresTransport(t *testing.T) {
	tests := []struct {
		name          string
		dsn           string
		allowInsecure bool
		wantErr       bool
	}{
		{
			name:    "remote URL requires TLS",
			dsn:     "postgresql://user:password@db.example.test/desk?sslmode=require",
			wantErr: false,
		},
		{
			name:    "remote keyword DSN verifies certificate",
			dsn:     "host=db.example.test user=user dbname=desk sslmode=verify-full",
			wantErr: false,
		},
		{
			name:    "remote plaintext is rejected",
			dsn:     "postgresql://user:password@db.example.test/desk?sslmode=disable",
			wantErr: true,
		},
		{
			name:    "remote prefer downgrade is rejected",
			dsn:     "postgresql://user:password@db.example.test/desk?sslmode=prefer",
			wantErr: true,
		},
		{
			name:    "loopback development may be plaintext",
			dsn:     "postgresql://user:password@127.0.0.1/desk?sslmode=disable",
			wantErr: false,
		},
		{
			name:          "explicit controlled override",
			dsn:           "postgresql://user:password@db.example.test/desk?sslmode=disable",
			allowInsecure: true,
			wantErr:       false,
		},
		{
			name:    "malformed DSN",
			dsn:     "postgresql://%",
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePostgresTransport(test.dsn, test.allowInsecure)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidatePostgresTransport() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
