package models

import (
	"encoding/json"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPlatformRoleClosedSet(t *testing.T) {
	valid := []PlatformRole{
		PlatformRolePlatformAdmin,
		PlatformRoleSecurityAuditor,
		PlatformRoleEmergencyOperator,
		PlatformRoleMember,
	}
	for _, role := range valid {
		if !role.IsValid() {
			t.Errorf("PlatformRole(%q).IsValid() = false", role)
		}
	}
	for _, role := range []PlatformRole{
		"",
		"admin",
		"supervisor",
		"agent",
		"customer",
		"unknown",
	} {
		if role.IsValid() {
			t.Errorf("legacy or unknown PlatformRole(%q) was accepted", role)
		}
	}
}

func TestUserPlatformRolePersistenceAndJSONContract(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:user-platform-role-contract?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&User{}); err != nil {
		t.Fatal(err)
	}
	user := User{
		Username:     "platform-member",
		Email:        "platform-member@example.test",
		PasswordHash: "hash",
		Status:       UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create default platform member: %v", err)
	}
	if user.PlatformRole != PlatformRoleMember {
		t.Fatalf("default platform role = %q, want member", user.PlatformRole)
	}
	encoded, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["platform_role"]; !ok {
		t.Fatal("User JSON is missing platform_role")
	}
	if _, ok := fields["role"]; ok {
		t.Fatal("User JSON retained destructive legacy role field")
	}
	columns, err := db.Migrator().ColumnTypes(&User{})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		if column.Name() == "role" {
			t.Fatal("fresh User schema retained destructive legacy role column")
		}
	}
}
