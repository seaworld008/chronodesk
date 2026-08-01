package services

import (
	"errors"
	"testing"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
)

func TestKnowledgeContributorGrantRevocationImmediatelyClosesEveryDraftPath(
	t *testing.T,
) {
	fixture := newAuthoredKnowledgeFixture(t)
	contributorContext := fixture.contributorContext(
		t,
		910,
		models.ProjectRoleRequester,
	)
	created, err := fixture.service.CreateAuthoredArticle(
		contributorContext,
		CreateAuthoredArticleInput{
			Key:      "revoked-contributor",
			Title:    "撤销后不可继续维护",
			Markdown: "## 现象\n\n授权撤销测试。\n",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.ProjectMembership{}).
		Where(
			"project_id = ? AND user_id = ?",
			fixture.scope.ProjectID,
			910,
		).
		Updates(map[string]any{
			"knowledge_contributor": false,
			"version":               gorm.Expr("version + 1"),
		}).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.ListArticles(
		contributorContext,
		KnowledgeArticleListFilter{ManagedByActor: true},
		DirectoryPageRequest{
			Page: 1, PageSize: 25, SortBy: "updated_at", SortOrder: "desc",
		},
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("revoked contributor mine view error = %v", err)
	}
	if _, err := fixture.service.CreateAuthoredArticle(
		contributorContext,
		CreateAuthoredArticleInput{
			Key:      "revoked-contributor-second",
			Title:    "不应创建",
			Markdown: "## 现象\n\n不应写入。\n",
		},
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("revoked contributor article creation error = %v", err)
	}
	if _, err := fixture.service.CreateAuthoredVersion(
		contributorContext,
		created.Article.ID,
		CreateAuthoredVersionInput{
			Title:    "不应修订",
			Markdown: "## 现象\n\n不应写入新版本。\n",
		},
	); !errors.Is(err, ErrProjectKnowledgeAccessDenied) {
		t.Fatalf("revoked contributor version creation error = %v", err)
	}
}
