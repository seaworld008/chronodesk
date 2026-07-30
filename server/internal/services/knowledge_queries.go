package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

// GetProjectModelPolicy returns one project-scoped policy for trusted
// administrative adapters. Callers must expose a redacted response DTO rather
// than serializing the persistence model directly.
func (service *KnowledgeService) GetProjectModelPolicy(
	ctx context.Context,
	policyKey string,
) (*models.ProjectModelPolicy, error) {
	if service == nil || service.db == nil {
		return nil, errors.New("knowledge service is unavailable")
	}
	operation, err := knowledgeOperation(ctx)
	if err != nil {
		return nil, err
	}
	policyKey = strings.TrimSpace(policyKey)
	if policyKey == "" {
		policyKey = "knowledge"
	}
	var policy models.ProjectModelPolicy
	if err := knowledgeScopedQuery(
		service.db.WithContext(ctx),
		operation.Scope,
	).Where(
		"policy_key = ? AND is_active = ?",
		policyKey,
		true,
	).First(&policy).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrKnowledgeNotFound
		}
		return nil, fmt.Errorf("load project model policy: %w", err)
	}
	return &policy, nil
}
