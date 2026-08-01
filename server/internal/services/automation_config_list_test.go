package services

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
)

func TestAutomationConfigurationListsValidateDirectCalls(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.SLAConfig{},
		&models.TicketTemplate{},
		&models.QuickReply{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "automation-list-validation",
		Email:        "automation-list-validation@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	service := NewAutomationService(db)

	for _, test := range []struct {
		name string
		call func() error
	}{
		{
			name: "SLA zero page",
			call: func() error {
				_, _, err := service.GetSLAConfigs(ctx, nil, 0, 25)
				return err
			},
		},
		{
			name: "SLA oversize",
			call: func() error {
				_, _, err := service.GetSLAConfigs(ctx, nil, 1, 101)
				return err
			},
		},
		{
			name: "template offset overflow",
			call: func() error {
				_, _, err := service.GetTemplates(
					ctx,
					"",
					nil,
					math.MaxInt,
					100,
				)
				return err
			},
		},
		{
			name: "template control category",
			call: func() error {
				_, _, err := service.GetTemplates(
					ctx,
					"incident\nsecret",
					nil,
					1,
					25,
				)
				return err
			},
		},
		{
			name: "quick reply oversized keyword",
			call: func() error {
				_, _, err := service.GetQuickReplies(
					ctx,
					"",
					strings.Repeat(
						"x",
						MaxAutomationKeywordFilterLength+1,
					),
					nil,
					user.ID,
					1,
					25,
				)
				return err
			},
		},
		{
			name: "quick reply missing actor",
			call: func() error {
				_, _, err := service.GetQuickReplies(
					ctx,
					"",
					"",
					nil,
					0,
					1,
					25,
				)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(
				err,
				ErrInvalidAutomationListQuery,
			) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestAutomationConfigurationListsAreStableAndBounded(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.SLAConfig{},
		&models.TicketTemplate{},
		&models.QuickReply{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "automation-list-stability",
		Email:        "automation-list-stability@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope := operation.Scope
	createdAt := time.Date(2026, 8, 1, 1, 2, 3, 0, time.UTC)

	slas := make([]models.SLAConfig, 150)
	templates := make([]models.TicketTemplate, 150)
	replies := make([]models.QuickReply, 150)
	for index := range slas {
		slas[index] = models.SLAConfig{
			OrganizationID: scope.OrganizationID,
			ProjectID:      scope.ProjectID,
			Name:           "SLA",
			IsActive:       true,
			IsDefault:      index == 149,
			ResponseTime:   30,
			ResolutionTime: 60,
			CreatedAt:      createdAt,
		}
		templates[index] = models.TicketTemplate{
			OrganizationID: scope.OrganizationID,
			ProjectID:      scope.ProjectID,
			Name:           "模板",
			Category:       "incident",
			IsActive:       true,
			CreatedBy:      user.ID,
			CreatedAt:      createdAt,
		}
		replies[index] = models.QuickReply{
			OrganizationID: scope.OrganizationID,
			ProjectID:      scope.ProjectID,
			Name:           "回复",
			Category:       "incident",
			Content:        "处理步骤",
			IsPublic:       true,
			CreatedBy:      user.ID,
			CreatedAt:      createdAt,
		}
	}
	if err := db.CreateInBatches(&slas, 50).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInBatches(&templates, 50).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInBatches(&replies, 50).Error; err != nil {
		t.Fatal(err)
	}

	service := NewAutomationService(db)
	slaPage, slaTotal, err := service.GetSLAConfigs(ctx, nil, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if slaTotal != 150 || len(slaPage) != 100 ||
		!slaPage[0].IsDefault ||
		slaPage[1].ID <= slaPage[2].ID {
		t.Fatalf(
			"unstable SLA page: total=%d first=%+v second=%+v third=%+v",
			slaTotal,
			slaPage[0],
			slaPage[1],
			slaPage[2],
		)
	}
	templatePage, templateTotal, err := service.GetTemplates(
		ctx,
		"incident",
		nil,
		2,
		50,
	)
	if err != nil {
		t.Fatal(err)
	}
	if templateTotal != 150 || len(templatePage) != 50 ||
		templatePage[0].ID <= templatePage[1].ID {
		t.Fatalf(
			"unstable template page: total=%d first=%+v second=%+v",
			templateTotal,
			templatePage[0],
			templatePage[1],
		)
	}
	replyPage, replyTotal, err := service.GetQuickReplies(
		ctx,
		"incident",
		"",
		nil,
		user.ID,
		3,
		50,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replyTotal != 150 || len(replyPage) != 50 ||
		replyPage[0].ID <= replyPage[1].ID {
		t.Fatalf(
			"unstable quick reply page: total=%d first=%+v second=%+v",
			replyTotal,
			replyPage[0],
			replyPage[1],
		)
	}
}

func TestQuickReplySearchEscapesLikeWildcards(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.QuickReply{},
	); err != nil {
		t.Fatal(err)
	}
	user := models.User{
		Username:     "quick-reply-like",
		Email:        "quick-reply-like@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(t, db, models.HumanActor(user.ID))
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope := operation.Scope
	fixtures := []models.QuickReply{
		{
			OrganizationID: scope.OrganizationID,
			ProjectID:      scope.ProjectID,
			Name:           "CPU 100%_literal",
			Content:        "literal",
			IsPublic:       true,
			CreatedBy:      user.ID,
		},
		{
			OrganizationID: scope.OrganizationID,
			ProjectID:      scope.ProjectID,
			Name:           "CPU 100 percent wildcard",
			Content:        "other",
			IsPublic:       true,
			CreatedBy:      user.ID,
		},
	}
	if err := db.Create(&fixtures).Error; err != nil {
		t.Fatal(err)
	}

	items, total, err := NewAutomationService(db).GetQuickReplies(
		ctx,
		"",
		"%_",
		nil,
		user.ID,
		1,
		25,
	)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != fixtures[0].ID {
		t.Fatalf("literal LIKE search = total %d items %+v", total, items)
	}
}

func TestQuickReplyTagsAreNormalizedAndBounded(t *testing.T) {
	normalized, err := normalizeQuickReplyTags(
		"  Urgent , urgent, 客户 , ",
	)
	if err != nil {
		t.Fatal(err)
	}
	if normalized != "Urgent,客户" {
		t.Fatalf("normalized tags = %q", normalized)
	}
	validUnicode, err := normalizeQuickReplyTags(strings.Repeat("中", 50))
	if err != nil || validUnicode != strings.Repeat("中", 50) {
		t.Fatalf("50-rune tag = %q err=%v", validUnicode, err)
	}
	tooMany := make([]string, 21)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("tag-%d", index)
	}
	for _, value := range []string{
		strings.Repeat("中", 51),
		"line\nbreak",
		strings.Join(tooMany, ","),
		strings.Join(
			[]string{
				strings.Repeat("a", 50),
				strings.Repeat("b", 50),
				strings.Repeat("c", 50),
				strings.Repeat("d", 50),
			},
			",",
		),
	} {
		if _, err := normalizeQuickReplyTags(value); !errors.Is(
			err,
			ErrInvalidQuickReplyTags,
		) {
			t.Fatalf("invalid tags accepted: %q err=%v", value, err)
		}
	}
}

func TestQuickReplyUseRequiresOwnerOrPublicVisibility(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&models.User{},
		&models.QuickReply{},
	); err != nil {
		t.Fatal(err)
	}
	owner := models.User{
		Username:     "quick-reply-owner",
		Email:        "quick-reply-owner@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	consumer := models.User{
		Username:     "quick-reply-consumer",
		Email:        "quick-reply-consumer@example.com",
		PasswordHash: "hashed",
		PlatformRole: models.PlatformRoleMember,
		Status:       models.UserStatusActive,
	}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&consumer).Error; err != nil {
		t.Fatal(err)
	}
	ctx := testProjectOperationContext(t, db, models.HumanActor(consumer.ID))
	operation, err := OperationContextFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope := operation.Scope
	private := models.QuickReply{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "私有回复",
		Content:        "private",
		CreatedBy:      owner.ID,
	}
	public := models.QuickReply{
		OrganizationID: scope.OrganizationID,
		ProjectID:      scope.ProjectID,
		Name:           "公共回复",
		Content:        "public",
		IsPublic:       true,
		CreatedBy:      owner.ID,
	}
	if err := db.Create(&private).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&public).Error; err != nil {
		t.Fatal(err)
	}
	service := NewAutomationService(db)
	if err := service.UseQuickReply(
		ctx,
		private.ID,
		consumer.ID,
	); !errors.Is(err, ErrQuickReplyNotFound) {
		t.Fatalf("private cross-user reply use error = %v", err)
	}
	if err := service.UseQuickReply(ctx, public.ID, consumer.ID); err != nil {
		t.Fatalf("public reply use error = %v", err)
	}
	ownerContext, err := WithOperationContext(
		ctx,
		OperationContext{
			Scope:  scope,
			Actor:  models.HumanActor(owner.ID),
			Source: SourceProtocolHumanREST,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UseQuickReply(ownerContext, private.ID, owner.ID); err != nil {
		t.Fatalf("owner reply use error = %v", err)
	}
}
