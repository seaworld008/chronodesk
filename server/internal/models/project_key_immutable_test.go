package models

import "testing"

func TestProjectKeyCannotChangeAfterCreation(t *testing.T) {
	db := openProjectModelDB(t, "immutable-project-key")
	organization := createProjectTestOrganization(
		t,
		db,
		"immutable-project-key",
	)
	unit := createProjectTestBusinessUnit(
		t,
		db,
		organization.ID,
		"OPS",
	)
	project := createProjectTestProject(
		t,
		db,
		organization.ID,
		unit.ID,
		"OPS",
	)

	assertPersistedKey := func(want ProjectKey) Project {
		t.Helper()
		var persisted Project
		if err := db.First(&persisted, project.ID).Error; err != nil {
			t.Fatal(err)
		}
		if persisted.Key != want {
			t.Fatalf(
				"persisted project key = %q, want immutable %s",
				persisted.Key,
				want,
			)
		}
		return persisted
	}

	if err := db.Model(&project).Update("key", "SUPPORT").Error; err != nil {
		t.Fatalf("ignore single-column project key update: %v", err)
	}
	project = assertPersistedKey(ProjectKey("OPS"))

	if err := db.Model(&project).Updates(map[string]any{
		"key":  "SUPPORT",
		"name": "Renamed without key change",
	}).Error; err != nil {
		t.Fatalf("update mutable project fields with key present: %v", err)
	}
	project = assertPersistedKey(ProjectKey("OPS"))
	if project.Name != "Renamed without key change" {
		t.Fatalf("mutable project name = %q", project.Name)
	}

	if err := db.Model(&project).Updates(Project{
		Name: "Struct update without key",
	}).Error; err != nil {
		t.Fatalf("update mutable project fields with zero-value key: %v", err)
	}
	project = assertPersistedKey(ProjectKey("OPS"))
	if project.Name != "Struct update without key" {
		t.Fatalf("struct-updated mutable project name = %q", project.Name)
	}

	project.Key = ProjectKey("SUPPORT")
	project.Name = "Saved without key change"
	if err := db.Save(&project).Error; err != nil {
		t.Fatalf("save project with changed in-memory key: %v", err)
	}
	project = assertPersistedKey(ProjectKey("OPS"))
	if project.Name != "Saved without key change" {
		t.Fatalf("saved mutable project name = %q", project.Name)
	}
}
