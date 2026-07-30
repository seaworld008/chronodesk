package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"
	"time"
	"unicode"
)

type inboundWebhookSignatureInput struct {
	Timestamp            string
	ProjectKey           string
	ConnectionPublicID   string
	MappingPublicID      string
	MessageID            string
	ExternalResourceType string
	ExternalResourceID   string
	ContentType          string
	Body                 []byte
}

func inboundWebhookSignature(
	input inboundWebhookSignatureInput,
	secret []byte,
) string {
	signed := inboundWebhookSigningPayload(input)
	defer clear(signed)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(signed)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// inboundWebhookSigningPayload mirrors the server's authenticated ingress
// framing. Each text field has already been validated to exclude CR/LF.
func inboundWebhookSigningPayload(input inboundWebhookSignatureInput) []byte {
	fields := []string{
		"v1",
		input.Timestamp,
		input.ProjectKey,
		input.ConnectionPublicID,
		input.MappingPublicID,
		input.MessageID,
		input.ExternalResourceType,
		input.ExternalResourceID,
		input.ContentType,
	}
	size := len(input.Body)
	for _, field := range fields {
		size += len(field) + 1
	}
	signed := make([]byte, 0, size)
	for _, field := range fields {
		signed = append(signed, field...)
		signed = append(signed, '\n')
	}
	return append(signed, input.Body...)
}

// outboundWebhookSignature verifies ChronoDesk Domain Event callbacks. The
// outbound contract intentionally remains timestamp + "." + exact_raw_body;
// it must not be reused for the richer inbound integration framing above.
func outboundWebhookSignature(timestamp string, body, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func normalizeInboundWebhookContentType(raw string) (string, error) {
	if raw == "" || len(raw) > 128 {
		return "", commandError{
			message: "content-type 只支持 application/json 或 application/cloudevents+json，可选 charset=utf-8",
		}
	}
	mediaType, parameters, err := mime.ParseMediaType(raw)
	if err != nil ||
		(mediaType != "application/json" &&
			mediaType != "application/cloudevents+json") {
		return "", commandError{
			message: "content-type 只支持 application/json 或 application/cloudevents+json，可选 charset=utf-8",
		}
	}
	for key, value := range parameters {
		if !strings.EqualFold(key, "charset") ||
			!strings.EqualFold(strings.TrimSpace(value), "utf-8") {
			return "", commandError{
				message: "content-type 只支持 application/json 或 application/cloudevents+json，可选 charset=utf-8",
			}
		}
	}
	return mediaType, nil
}

func readWebhookSecret(environmentName string) ([]byte, error) {
	secret, err := readEnvironmentSecret(environmentName, "Webhook 密钥")
	if err != nil {
		return nil, err
	}
	if len(secret) < 32 || len(secret) > 4096 {
		clear(secret)
		return nil, commandError{message: "Webhook 密钥长度必须为 32 到 4096 字节"}
	}
	return secret, nil
}

func runWebhookDryRun(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := newFlagSet("webhook dry-run", stderr)
	baseURL := flags.String("base-url", "http://localhost:8081", "ChronoDesk 根地址")
	projectKey := flags.String("project-key", "", "目标项目键")
	connectionID := flags.String("connection-id", "", "项目内 Connection UUID")
	mappingID := flags.String("mapping-id", "", "不可变 MappingVersion UUID")
	externalType := flags.String("external-resource-type", "", "外部对象类型")
	externalID := flags.String("external-resource-id", "", "外部对象 ID")
	idempotencyKey := flags.String("idempotency-key", "", "幂等键")
	bodyPath := flags.String("body", "", "原始请求体文件，或 -")
	contentType := flags.String(
		"content-type",
		"application/json",
		"application/json 或 application/cloudevents+json；可选 charset=utf-8",
	)
	timestamp := flags.String("timestamp", "", "Unix 秒；默认当前时间")
	secretEnvironment := flags.String(
		"secret-env",
		"CHRONODESK_WEBHOOK_SECRET",
		"保存 Webhook 密钥的环境变量名",
	)
	if err := flags.Parse(args); err != nil {
		return commandError{message: err.Error()}
	}
	if err := requireNoArguments(flags); err != nil {
		return err
	}
	if err := validateProjectKey(*projectKey); err != nil {
		return err
	}
	if !uuidPattern.MatchString(*connectionID) || !uuidPattern.MatchString(*mappingID) {
		return commandError{message: "connection-id 和 mapping-id 必须是规范 UUID"}
	}
	if !externalTypePattern.MatchString(*externalType) {
		return commandError{message: "external-resource-type 格式无效"}
	}
	if value := strings.TrimSpace(*externalID); value == "" ||
		value != *externalID || len(value) > 191 ||
		strings.ContainsFunc(value, unicode.IsControl) {
		return commandError{message: "external-resource-id 必须为 1 到 191 个无控制字符"}
	}
	if value := strings.TrimSpace(*idempotencyKey); value == "" ||
		value != *idempotencyKey || len(value) > 191 ||
		strings.ContainsFunc(value, unicode.IsControl) {
		return commandError{message: "idempotency-key 必须为 1 到 191 个无控制字符"}
	}
	normalizedContentType, err := normalizeInboundWebhookContentType(*contentType)
	if err != nil {
		return err
	}
	base, err := parseBaseURL(*baseURL)
	if err != nil {
		return err
	}
	body, err := readBody(*bodyPath, stdin)
	if err != nil {
		return err
	}
	defer clear(body)
	if *timestamp == "" {
		*timestamp = fmt.Sprintf("%d", time.Now().UTC().Unix())
	}
	if _, err := parsePositiveTimestamp(*timestamp); err != nil {
		return err
	}
	secret, err := readWebhookSecret(*secretEnvironment)
	if err != nil {
		return err
	}
	signature := inboundWebhookSignature(
		inboundWebhookSignatureInput{
			Timestamp:            *timestamp,
			ProjectKey:           *projectKey,
			ConnectionPublicID:   *connectionID,
			MappingPublicID:      *mappingID,
			MessageID:            *idempotencyKey,
			ExternalResourceType: *externalType,
			ExternalResourceID:   *externalID,
			ContentType:          normalizedContentType,
			Body:                 body,
		},
		secret,
	)
	clear(secret)
	headers := map[string]string{
		"Content-Type":                        normalizedContentType,
		"Idempotency-Key":                     *idempotencyKey,
		"X-ChronoDesk-Timestamp":              *timestamp,
		"X-ChronoDesk-Signature":              signature,
		"X-ChronoDesk-External-Resource-Type": *externalType,
		"X-ChronoDesk-External-Resource-ID":   *externalID,
	}
	bodyDigest := sha256.Sum256(body)
	requestPath := "/api/v2/projects/" + url.PathEscape(*projectKey) +
		"/integrations/inbound/" + url.PathEscape(*connectionID) +
		"/mappings/" + url.PathEscape(*mappingID) + "/messages"
	result := map[string]any{
		"dry_run":     true,
		"method":      "POST",
		"url":         endpoint(base, requestPath),
		"project_key": *projectKey,
		"headers":     headers,
		"body_bytes":  len(body),
		"body_sha256": hex.EncodeToString(bodyDigest[:]),
		"sent":        false,
	}
	return writeJSON(stdout, result)
}

func runWebhookVerify(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := newFlagSet("webhook verify", stderr)
	bodyPath := flags.String("body", "", "收到的原始请求体文件，或 -")
	timestamp := flags.String("timestamp", "", "收到的 X-ChronoDesk-Timestamp")
	signature := flags.String("signature", "", "收到的 X-ChronoDesk-Signature")
	maxAge := flags.Duration("max-age", 5*time.Minute, "允许的最大时间偏差")
	secretEnvironment := flags.String(
		"secret-env",
		"CHRONODESK_WEBHOOK_SECRET",
		"保存 Webhook 密钥的环境变量名",
	)
	previousSecretEnvironment := flags.String(
		"previous-secret-env",
		"",
		"可选的上一版本 Webhook 密钥环境变量名",
	)
	if err := flags.Parse(args); err != nil {
		return commandError{message: err.Error()}
	}
	if err := requireNoArguments(flags); err != nil {
		return err
	}
	signedAt, err := parsePositiveTimestamp(*timestamp)
	if err != nil {
		return err
	}
	if *maxAge <= 0 || *maxAge > 24*time.Hour {
		return commandError{message: "max-age 必须大于 0 且不超过 24h"}
	}
	now := time.Now().UTC()
	delta := now.Sub(time.Unix(signedAt, 0).UTC())
	if delta < 0 {
		delta = -delta
	}
	if delta > *maxAge {
		return &diagnosticError{
			Message:   "Webhook 时间戳超出重放窗口",
			Unhealthy: true,
		}
	}
	if len(*signature) != len("v1=")+sha256.Size*2 ||
		!strings.HasPrefix(*signature, "v1=") ||
		!lowerHexSignature(*signature) {
		return &diagnosticError{Message: "Webhook 签名格式无效", Unhealthy: true}
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(*signature, "v1="))
	if err != nil || len(provided) != sha256.Size {
		return &diagnosticError{Message: "Webhook 签名格式无效", Unhealthy: true}
	}
	defer clear(provided)
	body, err := readBody(*bodyPath, stdin)
	if err != nil {
		return err
	}
	defer clear(body)
	secret, err := readWebhookSecret(*secretEnvironment)
	if err != nil {
		return err
	}
	expectedValue := outboundWebhookSignature(*timestamp, body, secret)
	clear(secret)
	expected, _ := hex.DecodeString(strings.TrimPrefix(expectedValue, "v1="))
	defer clear(expected)
	currentMatches := hmac.Equal(provided, expected)
	previousMatches := false
	if *previousSecretEnvironment != "" {
		previousSecret, err := readWebhookSecret(*previousSecretEnvironment)
		if err != nil {
			return err
		}
		previousValue := outboundWebhookSignature(
			*timestamp,
			body,
			previousSecret,
		)
		clear(previousSecret)
		previousExpected, _ := hex.DecodeString(
			strings.TrimPrefix(previousValue, "v1="),
		)
		previousMatches = hmac.Equal(provided, previousExpected)
		clear(previousExpected)
	}
	valid := currentMatches || previousMatches
	bodyDigest := sha256.Sum256(body)
	matchedKey := ""
	if currentMatches {
		matchedKey = "current"
	} else if previousMatches {
		matchedKey = "previous"
	}
	if err := writeJSON(stdout, map[string]any{
		"valid":       valid,
		"matched_key": matchedKey,
		"timestamp":   *timestamp,
		"max_age":     maxAge.String(),
		"body_sha256": hex.EncodeToString(bodyDigest[:]),
	}); err != nil {
		return err
	}
	if !valid {
		return &diagnosticError{Message: "Webhook 签名不匹配", Unhealthy: true}
	}
	return nil
}

func lowerHexSignature(signature string) bool {
	for _, character := range strings.TrimPrefix(signature, "v1=") {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
