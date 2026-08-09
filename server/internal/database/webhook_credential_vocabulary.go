package database

import (
	"fmt"
	"strings"
)

func closedVocabularyConstraintExpression[T ~string](
	column string,
	values []T,
) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(
			parts,
			column+" = "+quoteClosedVocabularyValue(string(value)),
		)
	}
	return strings.Join(parts, " OR ")
}

func nullableClosedVocabularyConstraintExpression[T ~string](
	column string,
	values []T,
) string {
	expression := closedVocabularyConstraintExpression(column, values)
	if expression == "" {
		return column + " IS NULL"
	}
	return column + " IS NULL OR " + expression
}

func closedVocabularyINConstraintExpression[T ~string](
	column string,
	values []T,
) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(
			quoted,
			quoteClosedVocabularyValue(string(value)),
		)
	}
	return column + " IN (" + strings.Join(quoted, ", ") + ")"
}

func quoteClosedVocabularyValue(value string) string {
	return fmt.Sprintf("'%s'", strings.ReplaceAll(value, "'", "''"))
}
