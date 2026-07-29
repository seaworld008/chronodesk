package database

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateActorProjectionsBackfillsOnlyProvableActors(t *testing.T) {
	db := openActorProjectionMigrationDB(t, "provable")
	human := seedActorProjectionUser(t, db, "human")
	otherHuman := seedActorProjectionUser(t, db, "other")
	principal := seedActorProjectionPrincipal(t, db, "principal")

	if err := db.Exec(
		"ALTER TABLE service_principals ADD COLUMN compatibility_user_id INTEGER",
	).Error; err != nil {
		t.Fatalf("add legacy principal projection: %v", err)
	}
	if err := db.Exec(
		"UPDATE service_principals SET compatibility_user_id = ? WHERE id = ?",
		human.ID,
		principal.ID,
	).Error; err != nil {
		t.Fatalf("seed legacy principal projection: %v", err)
	}

	humanTicket := actorProjectionTicket("ACTOR-HUMAN", &human.ID)
	if err := db.Create(&humanTicket).Error; err != nil {
		t.Fatal(err)
	}
	principalTicket := actorProjectionTicket("ACTOR-PRINCIPAL", &human.ID)
	principalTicket.CreatedByActorType = models.ActorTypeServicePrincipal
	principalTicket.CreatedByActorID = principal.ID
	principalTicket.CreatedByServicePrincipalID = &principal.ID
	principalTicket.AssignedToID = &otherHuman.ID
	principalTicket.AssignedToActorType = models.ActorTypeServicePrincipal
	principalTicket.AssignedToActorID = principal.ID
	principalTicket.AssignedToServicePrincipalID = &principal.ID
	if err := db.Create(&principalTicket).Error; err != nil {
		t.Fatal(err)
	}

	comments := []models.TicketComment{
		{
			TicketID: humanTicket.ID, UserID: &human.ID,
			Content: "legacy human", Type: models.CommentTypePublic,
		},
		{
			TicketID: humanTicket.ID, UserID: &human.ID,
			Content: "legacy system", Type: models.CommentTypeSystem,
		},
		{
			TicketID: humanTicket.ID, UserID: &human.ID,
			ActorType: models.ActorTypeServicePrincipal, ActorID: principal.ID,
			ServicePrincipalID: &principal.ID,
			Content:            "native principal", Type: models.CommentTypeInternal,
		},
	}
	if err := db.Create(&comments).Error; err != nil {
		t.Fatal(err)
	}

	attachments := []models.TicketAttachment{
		{
			TicketID: humanTicket.ID, UploadedBy: &human.ID,
			FileName: "human.txt", OriginalName: "human.txt", FileSize: 1,
			StoragePath: "human.txt",
		},
		{
			TicketID: humanTicket.ID, UploadedBy: &human.ID,
			ActorType: models.ActorTypeServicePrincipal, ActorID: principal.ID,
			ServicePrincipalID: &principal.ID,
			FileName:           "principal.txt", OriginalName: "principal.txt", FileSize: 1,
			StoragePath: "principal.txt",
		},
	}
	if err := db.Create(&attachments).Error; err != nil {
		t.Fatal(err)
	}

	histories := []models.TicketHistory{
		{
			TicketID: humanTicket.ID, UserID: &human.ID,
			Action: models.HistoryActionCreate, Description: "legacy human",
		},
		{
			TicketID: humanTicket.ID, UserID: &human.ID,
			Action: models.HistoryActionSystem, Description: "legacy system",
			IsSystem: true,
		},
		{
			TicketID: humanTicket.ID, UserID: &human.ID,
			ActorType: models.ActorTypeServicePrincipal, ActorID: principal.ID,
			ServicePrincipalID: &principal.ID,
			Action:             models.HistoryActionUpdate, Description: "native principal",
		},
	}
	if err := db.Create(&histories).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateActorProjections(db); err != nil {
		t.Fatalf("migration attempt 1: %v", err)
	}
	const repeatUpdateCallback = "test:actor-projection-repeat-updates"
	var repeatedUpdates int
	if err := db.Callback().Update().Before("gorm:update").Register(
		repeatUpdateCallback,
		func(*gorm.DB) {
			repeatedUpdates++
		},
	); err != nil {
		t.Fatalf("register repeat update callback: %v", err)
	}
	defer func() {
		if err := db.Callback().Update().Remove(repeatUpdateCallback); err != nil {
			t.Errorf("remove repeat update callback: %v", err)
		}
	}()
	if err := MigrateActorProjections(db); err != nil {
		t.Fatalf("migration attempt 2: %v", err)
	}
	if repeatedUpdates != 0 {
		t.Fatalf(
			"idempotent actor migration emitted %d row updates",
			repeatedUpdates,
		)
	}

	var migratedHuman models.Ticket
	if err := db.First(&migratedHuman, humanTicket.ID).Error; err != nil {
		t.Fatal(err)
	}
	assertActorProjection(
		t,
		migratedHuman.CreatedByActorType,
		migratedHuman.CreatedByActorID,
		migratedHuman.CreatedByID,
		migratedHuman.CreatedByServicePrincipalID,
		models.ActorTypeHuman,
		strconv.FormatUint(uint64(human.ID), 10),
		&human.ID,
		nil,
	)

	var migratedPrincipal models.Ticket
	if err := db.First(&migratedPrincipal, principalTicket.ID).Error; err != nil {
		t.Fatal(err)
	}
	assertActorProjection(
		t,
		migratedPrincipal.CreatedByActorType,
		migratedPrincipal.CreatedByActorID,
		migratedPrincipal.CreatedByID,
		migratedPrincipal.CreatedByServicePrincipalID,
		models.ActorTypeServicePrincipal,
		principal.ID,
		nil,
		&principal.ID,
	)
	assertActorProjection(
		t,
		migratedPrincipal.AssignedToActorType,
		migratedPrincipal.AssignedToActorID,
		migratedPrincipal.AssignedToID,
		migratedPrincipal.AssignedToServicePrincipalID,
		models.ActorTypeServicePrincipal,
		principal.ID,
		nil,
		&principal.ID,
	)

	var migratedComments []models.TicketComment
	if err := db.Order("id ASC").Find(&migratedComments).Error; err != nil {
		t.Fatal(err)
	}
	assertActorProjection(
		t,
		migratedComments[0].ActorType,
		migratedComments[0].ActorID,
		migratedComments[0].UserID,
		migratedComments[0].ServicePrincipalID,
		models.ActorTypeHuman,
		strconv.FormatUint(uint64(human.ID), 10),
		&human.ID,
		nil,
	)
	assertActorProjection(
		t,
		migratedComments[1].ActorType,
		migratedComments[1].ActorID,
		migratedComments[1].UserID,
		migratedComments[1].ServicePrincipalID,
		models.ActorTypeSystem,
		actorProjectionSystemID,
		nil,
		nil,
	)
	assertActorProjection(
		t,
		migratedComments[2].ActorType,
		migratedComments[2].ActorID,
		migratedComments[2].UserID,
		migratedComments[2].ServicePrincipalID,
		models.ActorTypeServicePrincipal,
		principal.ID,
		nil,
		&principal.ID,
	)

	var migratedAttachments []models.TicketAttachment
	if err := db.Order("id ASC").Find(&migratedAttachments).Error; err != nil {
		t.Fatal(err)
	}
	assertActorProjection(
		t,
		migratedAttachments[0].ActorType,
		migratedAttachments[0].ActorID,
		migratedAttachments[0].UploadedBy,
		migratedAttachments[0].ServicePrincipalID,
		models.ActorTypeHuman,
		strconv.FormatUint(uint64(human.ID), 10),
		&human.ID,
		nil,
	)
	assertActorProjection(
		t,
		migratedAttachments[1].ActorType,
		migratedAttachments[1].ActorID,
		migratedAttachments[1].UploadedBy,
		migratedAttachments[1].ServicePrincipalID,
		models.ActorTypeServicePrincipal,
		principal.ID,
		nil,
		&principal.ID,
	)

	var migratedHistories []models.TicketHistory
	if err := db.Order("id ASC").Find(&migratedHistories).Error; err != nil {
		t.Fatal(err)
	}
	assertActorProjection(
		t,
		migratedHistories[0].ActorType,
		migratedHistories[0].ActorID,
		migratedHistories[0].UserID,
		migratedHistories[0].ServicePrincipalID,
		models.ActorTypeHuman,
		strconv.FormatUint(uint64(human.ID), 10),
		&human.ID,
		nil,
	)
	assertActorProjection(
		t,
		migratedHistories[1].ActorType,
		migratedHistories[1].ActorID,
		migratedHistories[1].UserID,
		migratedHistories[1].ServicePrincipalID,
		models.ActorTypeSystem,
		actorProjectionSystemID,
		nil,
		nil,
	)
	assertActorProjection(
		t,
		migratedHistories[2].ActorType,
		migratedHistories[2].ActorID,
		migratedHistories[2].UserID,
		migratedHistories[2].ServicePrincipalID,
		models.ActorTypeServicePrincipal,
		principal.ID,
		nil,
		&principal.ID,
	)

	if db.Migrator().HasColumn("service_principals", "compatibility_user_id") {
		t.Fatal("legacy service-principal human projection was not removed")
	}
	if err := db.Exec(
		`INSERT INTO ticket_comments
			(ticket_id, user_id, actor_type, actor_id, content, type)
		 VALUES (?, ?, 'human', ?, 'invalid', 'public')`,
		humanTicket.ID,
		human.ID,
		strconv.FormatUint(uint64(otherHuman.ID), 10),
	).Error; err == nil || !strings.Contains(err.Error(), "invalid actor projection") {
		t.Fatalf("inconsistent human projection was accepted: %v", err)
	}
}

func TestMigrateActorProjectionsFailsClosedForUnknownOrContradictoryRows(t *testing.T) {
	tests := []struct {
		name string
		seed func(*testing.T, *gorm.DB, models.User, models.Ticket)
		want string
	}{
		{
			name: "unknown history",
			seed: func(t *testing.T, db *gorm.DB, _ models.User, ticket models.Ticket) {
				t.Helper()
				if err := db.Create(&models.TicketHistory{
					TicketID: ticket.ID, Action: models.HistoryActionUpdate,
					Description: "unknown", ActorType: models.ActorTypeHuman,
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
			want: "has no user projection",
		},
		{
			name: "contradictory human",
			seed: func(t *testing.T, db *gorm.DB, user models.User, ticket models.Ticket) {
				t.Helper()
				if err := db.Create(&models.TicketComment{
					TicketID: ticket.ID, UserID: &user.ID,
					ActorType: models.ActorTypeHuman, ActorID: "999999",
					Content: "conflict", Type: models.CommentTypePublic,
				}).Error; err != nil {
					t.Fatal(err)
				}
			},
			want: "conflicts with user projection",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openActorProjectionMigrationDB(t, test.name)
			user := seedActorProjectionUser(t, db, test.name)
			ticket := actorProjectionTicket("FAIL-"+test.name, &user.ID)
			if err := db.Create(&ticket).Error; err != nil {
				t.Fatal(err)
			}
			test.seed(t, db, user, ticket)
			err := MigrateActorProjections(db)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("migration error = %v, want %q", err, test.want)
			}
			var persisted models.Ticket
			if err := db.First(&persisted, ticket.ID).Error; err != nil {
				t.Fatal(err)
			}
			if persisted.CreatedByActorID != "" {
				t.Fatalf("failed migration partially committed ticket projection: %+v", persisted)
			}
		})
	}
}

func TestMigrateActorProjectionsHandlesCloudBaselineShape(t *testing.T) {
	db := openActorProjectionMigrationDB(t, "cloud-shape")
	user := seedActorProjectionUser(t, db, "cloud-shape")

	tickets := make([]models.Ticket, 33)
	for i := range tickets {
		tickets[i] = actorProjectionTicket(fmt.Sprintf("CLOUD-%02d", i+1), &user.ID)
	}
	if err := db.CreateInBatches(&tickets, 33).Error; err != nil {
		t.Fatal(err)
	}
	comments := make([]models.TicketComment, 2591)
	for i := range comments {
		comments[i] = models.TicketComment{
			TicketID: tickets[i%len(tickets)].ID,
			UserID:   &user.ID,
			Content:  "legacy",
			Type:     models.CommentTypePublic,
		}
		if i >= 2571 {
			comments[i].ActorType = models.ActorTypeHuman
			comments[i].ActorID = strconv.FormatUint(uint64(user.ID), 10)
		}
	}
	if err := db.CreateInBatches(&comments, 250).Error; err != nil {
		t.Fatal(err)
	}
	histories := make([]models.TicketHistory, 122)
	for i := range histories {
		histories[i] = models.TicketHistory{
			TicketID: tickets[i%len(tickets)].ID,
			UserID:   &user.ID, Action: models.HistoryActionUpdate,
			Description: "legacy",
			ActorType:   models.ActorTypeHuman,
		}
		if i >= 48 {
			histories[i].ActorID = strconv.FormatUint(uint64(user.ID), 10)
		}
	}
	if err := db.CreateInBatches(&histories, 122).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateActorProjections(db); err != nil {
		t.Fatalf("migrate cloud baseline shape: %v", err)
	}
	for table, actorIDColumn := range map[string]string{
		"tickets":          "created_by_actor_id",
		"ticket_comments":  "actor_id",
		"ticket_histories": "actor_id",
	} {
		var missing int64
		if err := db.Table(table).
			Where(actorIDColumn + " IS NULL OR TRIM(" + actorIDColumn + ") = ''").
			Count(&missing).Error; err != nil {
			t.Fatal(err)
		}
		if missing != 0 {
			t.Fatalf("%s still has %d missing ActorRef rows", table, missing)
		}
	}
}

func openActorProjectionMigrationDB(t *testing.T, suffix string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:actor-projection-%s?mode=memory&cache=shared", suffix)),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.ServicePrincipal{},
		&models.Ticket{},
		&models.TicketComment{},
		&models.TicketAttachment{},
		&models.DomainEvent{},
		&models.TicketHistory{},
	); err != nil {
		t.Fatalf("migrate actor projection schema: %v", err)
	}
	return db
}

func seedActorProjectionUser(t *testing.T, db *gorm.DB, suffix string) models.User {
	t.Helper()
	user := models.User{
		Username:     "actor-" + suffix,
		Email:        "actor-" + suffix + "@example.com",
		PasswordHash: "hash",
		Role:         models.RoleAgent,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func seedActorProjectionPrincipal(
	t *testing.T,
	db *gorm.DB,
	suffix string,
) models.ServicePrincipal {
	t.Helper()
	principal := models.ServicePrincipal{
		ID:                 "principal-" + suffix,
		Name:               "principal-" + suffix,
		Status:             models.ServicePrincipalStatusActive,
		Scopes:             datatypes.JSON(`["tickets:read"]`),
		RateLimitPerMinute: 60,
		ConcurrentLimit:    1,
	}
	if err := db.Create(&principal).Error; err != nil {
		t.Fatal(err)
	}
	return principal
}

func actorProjectionTicket(number string, creatorID *uint) models.Ticket {
	return models.Ticket{
		TicketNumber: number,
		Title:        number,
		Description:  "legacy",
		Type:         models.TicketTypeRequest,
		Priority:     models.TicketPriorityNormal,
		Status:       models.TicketStatusOpen,
		Source:       models.TicketSourceWeb,
		CreatedByID:  creatorID,
		Version:      1,
	}
}

func assertActorProjection(
	t *testing.T,
	gotType models.ActorType,
	gotID string,
	gotHumanID *uint,
	gotPrincipalID *string,
	wantType models.ActorType,
	wantID string,
	wantHumanID *uint,
	wantPrincipalID *string,
) {
	t.Helper()
	if gotType != wantType ||
		gotID != wantID ||
		!uintPointersEqual(gotHumanID, wantHumanID) ||
		!stringPointersEqual(gotPrincipalID, wantPrincipalID) {
		t.Fatalf(
			"actor projection = %s/%q human=%v principal=%v, want %s/%q human=%v principal=%v",
			gotType,
			gotID,
			gotHumanID,
			gotPrincipalID,
			wantType,
			wantID,
			wantHumanID,
			wantPrincipalID,
		)
	}
}
