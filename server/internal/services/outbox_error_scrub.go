package services

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

const maxOutboxFailureRunes = 4000

var (
	outboxAbsoluteURLPattern   = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)
	outboxQueryPattern         = regexp.MustCompile(`([[:alnum:]_./:@-]+)\?[^\s"'<>]*`)
	outboxAuthSchemePattern    = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[^\s,;}\]]+`)
	outboxAuthorizationPattern = regexp.MustCompile(
		`(?i)\bauthorization"?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\r\n,;]+)`,
	)
	outboxSensitiveKVPattern = regexp.MustCompile(
		`(?i)\b(access[_-]?token|refresh[_-]?token|token|secret|credential|password|api[_-]?key)"?(\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s&,;}\]]+)`,
	)
)

// scrubOutboxFailure is the single persistence boundary for delivery errors.
// Provider errors are untrusted and frequently embed callback URLs, query
// tokens or Authorization values. Admin APIs may expose LastError, so only a
// bounded, scrubbed diagnostic is stored.
func scrubOutboxFailure(deliveryErr error) string {
	if deliveryErr == nil {
		return ""
	}
	var urlError *url.Error
	if errors.As(deliveryErr, &urlError) {
		return "出站投递失败"
	}
	message := strings.TrimSpace(deliveryErr.Error())
	message = outboxAbsoluteURLPattern.ReplaceAllString(message, "[URL 已隐藏]")
	message = outboxQueryPattern.ReplaceAllString(message, "${1}?[查询参数已隐藏]")
	message = outboxAuthorizationPattern.ReplaceAllString(
		message,
		"Authorization: [凭据已隐藏]",
	)
	message = outboxAuthSchemePattern.ReplaceAllString(message, "$1 [凭据已隐藏]")
	message = outboxSensitiveKVPattern.ReplaceAllString(message, "$1$2[凭据已隐藏]")
	runes := []rune(message)
	if len(runes) > maxOutboxFailureRunes {
		message = string(runes[:maxOutboxFailureRunes])
	}
	return message
}

// ScrubOutboxFailureText provides defense in depth at read boundaries for
// records created before the persistence scrub was introduced.
func ScrubOutboxFailureText(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	return scrubOutboxFailure(errors.New(message))
}
