package database

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/seaworld008/chronodesk/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const knowledgeOnePublishedIndex = "idx_knowledge_one_published"

var knowledgeIndexCastPattern = regexp.MustCompile(
	`::[a-z_][a-z0-9_]*(?:\[\])?`,
)

// PrepareKnowledgePublishedVersionContract repairs only an unambiguous legacy
// state: the article's canonical current_version_id must identify exactly one
// of the duplicate published versions. Ambiguous data fails closed for
// operator review instead of silently choosing a winner.
func PrepareKnowledgePublishedVersionContract(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	if !db.Migrator().HasTable(&models.KnowledgeArticle{}) ||
		!db.Migrator().HasTable(&models.KnowledgeArticleVersion{}) {
		return nil
	}
	type duplicateGroup struct {
		OrganizationID uint
		ProjectID      uint
		ArticleID      string
		Count          int64
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var groups []duplicateGroup
		if err := tx.Model(&models.KnowledgeArticleVersion{}).
			Select(
				"organization_id, project_id, article_id, COUNT(*) AS count",
			).
			Where("status = ?", models.KnowledgeVersionPublished).
			Group("organization_id, project_id, article_id").
			Having("COUNT(*) > 1").
			Order("organization_id ASC, project_id ASC, article_id ASC").
			Scan(&groups).Error; err != nil {
			return fmt.Errorf(
				"list duplicate published knowledge versions: %w",
				err,
			)
		}
		now := time.Now().UTC()
		for _, group := range groups {
			var article models.KnowledgeArticle
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ?",
					group.ArticleID,
					group.OrganizationID,
					group.ProjectID,
				).
				Take(&article).Error; err != nil {
				return fmt.Errorf(
					"load canonical article for duplicate published versions: %w",
					err,
				)
			}
			if article.CurrentVersion == nil ||
				*article.CurrentVersion == "" {
				return fmt.Errorf(
					"knowledge article %s has %d published versions and no canonical current version",
					group.ArticleID,
					group.Count,
				)
			}
			var canonicalCount int64
			if err := tx.Model(&models.KnowledgeArticleVersion{}).
				Where(
					"id = ? AND organization_id = ? AND project_id = ? AND article_id = ? AND status = ?",
					*article.CurrentVersion,
					group.OrganizationID,
					group.ProjectID,
					group.ArticleID,
					models.KnowledgeVersionPublished,
				).
				Count(&canonicalCount).Error; err != nil {
				return fmt.Errorf(
					"verify canonical published knowledge version: %w",
					err,
				)
			}
			if canonicalCount != 1 {
				return fmt.Errorf(
					"knowledge article %s current version is not one of its published versions",
					group.ArticleID,
				)
			}
			result := tx.Model(&models.KnowledgeArticleVersion{}).
				Where(
					"organization_id = ? AND project_id = ? AND article_id = ? AND status = ? AND id <> ?",
					group.OrganizationID,
					group.ProjectID,
					group.ArticleID,
					models.KnowledgeVersionPublished,
					*article.CurrentVersion,
				).
				UpdateColumns(map[string]any{
					"status":     models.KnowledgeVersionSuperseded,
					"updated_at": now,
				})
			if result.Error != nil {
				return fmt.Errorf(
					"repair duplicate published knowledge versions: %w",
					result.Error,
				)
			}
			if result.RowsAffected != group.Count-1 {
				return fmt.Errorf(
					"knowledge article %s duplicate repair changed %d rows, want %d",
					group.ArticleID,
					result.RowsAffected,
					group.Count-1,
				)
			}
		}
		return nil
	})
}

func MigrateKnowledgePublishedVersionContract(db *gorm.DB) error {
	if err := PrepareKnowledgePublishedVersionContract(db); err != nil {
		return err
	}
	if !db.Migrator().HasIndex(
		&models.KnowledgeArticleVersion{},
		knowledgeOnePublishedIndex,
	) {
		if err := db.Migrator().CreateIndex(
			&models.KnowledgeArticleVersion{},
			knowledgeOnePublishedIndex,
		); err != nil {
			return fmt.Errorf(
				"create one-published-knowledge-version index: %w",
				err,
			)
		}
	}
	return ValidateKnowledgePublishedVersionContract(db)
}

func ValidateKnowledgePublishedVersionContract(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	if !db.Migrator().HasTable(&models.KnowledgeArticleVersion{}) {
		return fmt.Errorf("knowledge_article_versions table is missing")
	}
	if !db.Migrator().HasIndex(
		&models.KnowledgeArticleVersion{},
		knowledgeOnePublishedIndex,
	) {
		return fmt.Errorf(
			"knowledge published-version uniqueness index is missing",
		)
	}
	if err := validateKnowledgePublishedIndexDefinition(db); err != nil {
		return err
	}
	var duplicateGroups int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM (
			SELECT 1
			FROM knowledge_article_versions
			WHERE status = ?
			GROUP BY organization_id, project_id, article_id
			HAVING COUNT(*) > 1
		) AS duplicate_published_versions
	`, models.KnowledgeVersionPublished).Scan(&duplicateGroups).Error; err != nil {
		return fmt.Errorf(
			"validate published knowledge version uniqueness: %w",
			err,
		)
	}
	if duplicateGroups != 0 {
		return fmt.Errorf(
			"knowledge published-version uniqueness has %d duplicate groups",
			duplicateGroups,
		)
	}
	var publishedCurrentMismatches int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM knowledge_article_versions AS versions
		LEFT JOIN knowledge_articles AS articles
		  ON articles.id = versions.article_id
		 AND articles.organization_id = versions.organization_id
		 AND articles.project_id = versions.project_id
		WHERE versions.status = ?
		  AND (
		    articles.id IS NULL
		    OR articles.current_version_id IS NULL
		    OR articles.current_version_id <> versions.id
		  )
	`, models.KnowledgeVersionPublished).
		Scan(&publishedCurrentMismatches).Error; err != nil {
		return fmt.Errorf(
			"validate published knowledge canonical pointers: %w",
			err,
		)
	}
	if publishedCurrentMismatches != 0 {
		return fmt.Errorf(
			"knowledge published-version contract has %d published rows that are not canonical",
			publishedCurrentMismatches,
		)
	}
	var currentPublishedMismatches int64
	if err := db.Raw(`
		SELECT COUNT(*)
		FROM knowledge_articles AS articles
		LEFT JOIN knowledge_article_versions AS versions
		  ON versions.id = articles.current_version_id
		 AND versions.article_id = articles.id
		 AND versions.organization_id = articles.organization_id
		 AND versions.project_id = articles.project_id
		WHERE articles.current_version_id IS NOT NULL
		  AND (
		    versions.id IS NULL
		    OR versions.status <> ?
		  )
	`, models.KnowledgeVersionPublished).
		Scan(&currentPublishedMismatches).Error; err != nil {
		return fmt.Errorf(
			"validate canonical knowledge published versions: %w",
			err,
		)
	}
	if currentPublishedMismatches != 0 {
		return fmt.Errorf(
			"knowledge published-version contract has %d canonical pointers without a matching published version",
			currentPublishedMismatches,
		)
	}
	return nil
}

func validateKnowledgePublishedIndexDefinition(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "postgres":
		var definition struct {
			IsUnique  bool `gorm:"column:is_unique"`
			Columns   string
			Predicate string
		}
		result := db.Raw(`
			SELECT
			  indexes.indisunique AS is_unique,
			  COALESCE((
			    SELECT string_agg(
			      attributes.attname,
			      ',' ORDER BY keys.ordinality
			    )
			    FROM unnest(indexes.indkey)
			      WITH ORDINALITY AS keys(attnum, ordinality)
			    JOIN pg_attribute AS attributes
			      ON attributes.attrelid = indexes.indrelid
			     AND attributes.attnum = keys.attnum
			    WHERE keys.ordinality <= indexes.indnkeyatts
			  ), '') AS columns,
			  COALESCE(
			    pg_get_expr(indexes.indpred, indexes.indrelid),
			    ''
			  ) AS predicate
			FROM pg_index AS indexes
			JOIN pg_class AS index_class
			  ON index_class.oid = indexes.indexrelid
			JOIN pg_class AS table_class
			  ON table_class.oid = indexes.indrelid
			JOIN pg_namespace AS namespace
			  ON namespace.oid = table_class.relnamespace
			WHERE namespace.nspname = current_schema()
			  AND table_class.relname = 'knowledge_article_versions'
			  AND index_class.relname = ?
		`, knowledgeOnePublishedIndex).Take(&definition)
		if result.Error != nil {
			return fmt.Errorf(
				"inspect knowledge published-version index: %w",
				result.Error,
			)
		}
		if !definition.IsUnique ||
			definition.Columns !=
				"organization_id,project_id,article_id" ||
			normalizeKnowledgeIndexPredicate(definition.Predicate) !=
				"status='published'" {
			return fmt.Errorf(
				"knowledge published-version index definition is invalid",
			)
		}
	case "sqlite":
		var sql string
		if err := db.Raw(
			`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`,
			knowledgeOnePublishedIndex,
		).Scan(&sql).Error; err != nil {
			return fmt.Errorf(
				"inspect SQLite knowledge published-version index: %w",
				err,
			)
		}
		normalized := strings.NewReplacer(
			" ", "",
			"\n", "",
			"\t", "",
			`"`, "",
			"`", "",
		).Replace(strings.ToLower(sql))
		if !strings.HasPrefix(normalized, "createuniqueindex") ||
			!strings.Contains(
				normalized,
				"onknowledge_article_versions(organization_id,project_id,article_id)",
			) ||
			normalizeKnowledgeIndexPredicate(sql) !=
				"createuniqueindex"+knowledgeOnePublishedIndex+
					"onknowledge_article_versions"+
					"organization_id,project_id,article_id"+
					"wherestatus='published'" {
			return fmt.Errorf(
				"knowledge published-version index definition is invalid",
			)
		}
	}
	return nil
}

func normalizeKnowledgeIndexPredicate(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, `"`, "")
	value = strings.ReplaceAll(value, "`", "")
	value = strings.Map(func(character rune) rune {
		switch character {
		case ' ', '\n', '\r', '\t', '(', ')':
			return -1
		default:
			return character
		}
	}, value)
	return knowledgeIndexCastPattern.ReplaceAllString(value, "")
}
