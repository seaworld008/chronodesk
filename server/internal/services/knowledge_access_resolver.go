package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

var ErrProjectKnowledgeAccessDenied = errors.New(
	"project knowledge access denied",
)

// ProjectKnowledgeAccessResolver derives additional knowledge ACL subjects
// from trusted project authorization records. The KnowledgeService itself adds
// all_project and the concrete human/service-principal subject.
type ProjectKnowledgeAccessResolver struct {
	db  *gorm.DB
	now func() time.Time
}

func NewProjectKnowledgeAccessResolver(
	db *gorm.DB,
) (*ProjectKnowledgeAccessResolver, error) {
	if db == nil {
		return nil, errors.New("knowledge access database is required")
	}
	return &ProjectKnowledgeAccessResolver{db: db, now: time.Now}, nil
}

func (resolver *ProjectKnowledgeAccessResolver) ResolveKnowledgeSubjects(
	ctx context.Context,
	scope models.ProjectScope,
	actor models.ActorRef,
) ([]models.KnowledgeACLSubject, error) {
	if resolver == nil || resolver.db == nil || resolver.now == nil {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	if err := scope.Validate(); err != nil {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	if err := actor.Validate(); err != nil {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	operation, err := OperationContextFromContext(ctx)
	if err != nil ||
		operation.Scope != scope ||
		operation.Actor != actor {
		return nil, ErrProjectKnowledgeAccessDenied
	}

	switch actor.Type {
	case models.ActorTypeHuman:
		return resolver.resolveHumanSubjects(ctx, scope, actor)
	case models.ActorTypeServicePrincipal:
		return resolver.resolvePrincipalSubjects(ctx, scope, actor)
	default:
		return nil, ErrProjectKnowledgeAccessDenied
	}
}

func (resolver *ProjectKnowledgeAccessResolver) resolveHumanSubjects(
	ctx context.Context,
	scope models.ProjectScope,
	actor models.ActorRef,
) ([]models.KnowledgeACLSubject, error) {
	userID, err := parseKnowledgeHumanID(actor.ID)
	if err != nil {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	var membership struct {
		Role models.ProjectRole
	}
	err = resolver.db.WithContext(ctx).
		Table("project_memberships AS memberships").
		Select("memberships.role").
		Joins(
			"JOIN projects AS projects ON projects.id = memberships.project_id",
		).
		Where(
			"memberships.project_id = ? AND projects.id = ? AND projects.organization_id = ?",
			scope.ProjectID,
			scope.ProjectID,
			scope.OrganizationID,
		).
		Where(
			"memberships.user_id = ? AND memberships.is_active = ? AND projects.status = ?",
			userID,
			true,
			models.ProjectStatusActive,
		).
		Take(&membership).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	if err != nil {
		return nil, fmt.Errorf("resolve scoped project membership: %w", err)
	}
	if !membership.Role.IsValid() {
		return nil, ErrProjectKnowledgeAccessDenied
	}

	var teams []struct {
		PublicID string
	}
	if err := resolver.db.WithContext(ctx).
		Table("team_memberships AS memberships").
		Select("teams.public_id").
		Joins("JOIN teams AS teams ON teams.id = memberships.team_id").
		Joins("JOIN projects AS projects ON projects.id = teams.project_id").
		Where(
			"teams.project_id = ? AND projects.id = ? AND projects.organization_id = ?",
			scope.ProjectID,
			scope.ProjectID,
			scope.OrganizationID,
		).
		Where(
			"memberships.user_id = ? AND memberships.is_active = ? AND teams.status = ? AND projects.status = ?",
			userID,
			true,
			models.TeamStatusActive,
			models.ProjectStatusActive,
		).
		Order("teams.public_id ASC").
		Scan(&teams).Error; err != nil {
		return nil, fmt.Errorf("resolve scoped Team memberships: %w", err)
	}

	subjects := make([]models.KnowledgeACLSubject, 0, len(teams)+1)
	subjects = append(subjects, models.KnowledgeACLSubject{
		Type: models.KnowledgeACLProjectRole,
		ID:   string(membership.Role),
	})
	seenTeams := make(map[string]struct{}, len(teams))
	for _, team := range teams {
		teamID := strings.TrimSpace(team.PublicID)
		if teamID == "" {
			return nil, ErrProjectKnowledgeAccessDenied
		}
		if _, exists := seenTeams[teamID]; exists {
			continue
		}
		seenTeams[teamID] = struct{}{}
		subjects = append(subjects, models.KnowledgeACLSubject{
			Type: models.KnowledgeACLTeam,
			ID:   teamID,
		})
	}
	return subjects, nil
}

func (resolver *ProjectKnowledgeAccessResolver) resolvePrincipalSubjects(
	ctx context.Context,
	scope models.ProjectScope,
	actor models.ActorRef,
) ([]models.KnowledgeACLSubject, error) {
	var grant struct {
		Role models.ProjectRole
	}
	err := resolver.db.WithContext(ctx).
		Table("project_principal_grants AS grants").
		Select("grants.role").
		Joins("JOIN projects AS projects ON projects.id = grants.project_id").
		Where(
			"grants.project_id = ? AND projects.id = ? AND projects.organization_id = ?",
			scope.ProjectID,
			scope.ProjectID,
			scope.OrganizationID,
		).
		Where(
			"grants.service_principal_id = ? AND grants.is_active = ? AND projects.status = ?",
			actor.ID,
			true,
			models.ProjectStatusActive,
		).
		Where(
			"(grants.expires_at IS NULL OR grants.expires_at > ?)",
			resolver.now().UTC(),
		).
		Take(&grant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	if err != nil {
		return nil, fmt.Errorf("resolve scoped project principal grant: %w", err)
	}
	if !grant.Role.IsValid() {
		return nil, ErrProjectKnowledgeAccessDenied
	}
	return []models.KnowledgeACLSubject{{
		Type: models.KnowledgeACLProjectRole,
		ID:   string(grant.Role),
	}}, nil
}

func parseKnowledgeHumanID(value string) (uint, error) {
	trimmed := strings.TrimSpace(value)
	parsed, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil || parsed == 0 {
		return 0, errors.New("human actor id must be a positive integer")
	}
	if trimmed != value || strconv.FormatUint(parsed, 10) != value {
		return 0, errors.New("human actor id must use canonical decimal form")
	}
	userID := uint(parsed)
	if uint64(userID) != parsed {
		return 0, errors.New("human actor id exceeds platform range")
	}
	return userID, nil
}
