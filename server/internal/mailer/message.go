// Package mailer builds injection-safe RFC 5322 messages for ChronoDesk.
package mailer

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxMailboxBytes = 254
	maxHeaderRunes  = 255
	maxBodyBytes    = 2 * 1024 * 1024
	mimeLineLength  = 76
)

// CanonicalMailbox validates a single SMTP mailbox and returns its canonical
// addr-spec. Display names and SMTPUTF8 addresses are intentionally rejected:
// callers supply display names separately and net/smtp does not negotiate
// SMTPUTF8.
func CanonicalMailbox(raw string) (string, error) {
	if err := validateHeaderText("邮箱地址", raw, false); err != nil {
		return "", err
	}
	value := strings.TrimSpace(raw)
	if len(value) > maxMailboxBytes {
		return "", errors.New("邮箱地址过长")
	}
	for _, char := range value {
		if char > unicode.MaxASCII {
			return "", errors.New("SMTP邮箱地址必须使用ASCII字符")
		}
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Name != "" || address.Address != value {
		return "", errors.New("邮箱地址格式无效")
	}
	return address.Address, nil
}

// BuildHTMLMessage builds a UTF-8 HTML message whose body is transported with
// MIME base64. The caller remains responsible for context-aware HTML template
// escaping before invoking this function.
func BuildHTMLMessage(fromEmail, fromName, subject, body string) ([]byte, error) {
	return buildMessage(fromEmail, fromName, subject, "text/html; charset=UTF-8", body)
}

// BuildTextMessage builds a UTF-8 plain-text message. Control characters that
// have no valid text-body meaning are rejected rather than copied into SMTP
// data.
func BuildTextMessage(fromEmail, fromName, subject, body string) ([]byte, error) {
	for _, char := range body {
		if unicode.IsControl(char) && char != '\r' && char != '\n' && char != '\t' {
			return nil, errors.New("邮件正文包含无效控制字符")
		}
	}
	return buildMessage(fromEmail, fromName, subject, "text/plain; charset=UTF-8", body)
}

// ValidateHeaderValue rejects CR/LF and every other control character while
// preserving legal UTF-8 text such as Chinese display names and subjects.
func ValidateHeaderValue(field, value string, allowEmpty bool) error {
	return validateHeaderText(field, value, allowEmpty)
}

func buildMessage(fromEmail, fromName, subject, contentType, body string) ([]byte, error) {
	from, err := CanonicalMailbox(fromEmail)
	if err != nil {
		return nil, fmt.Errorf("发件邮箱无效: %w", err)
	}
	if err := ValidateHeaderValue("发件人名称", fromName, true); err != nil {
		return nil, err
	}
	if err := ValidateHeaderValue("邮件主题", subject, false); err != nil {
		return nil, err
	}
	if len(body) > maxBodyBytes {
		return nil, errors.New("邮件正文超过大小限制")
	}

	fromHeader := from
	if fromName != "" {
		fromHeader = encodeHeaderWord(fromName) + " <" + from + ">"
	}

	var message strings.Builder
	message.Grow(len(body) + 512)
	message.WriteString("Date: ")
	message.WriteString(time.Now().UTC().Format(time.RFC1123Z))
	message.WriteString("\r\nFrom: ")
	message.WriteString(fromHeader)
	// Delivery uses the SMTP envelope. A fixed group recipient prevents a
	// user-controlled mailbox from becoming raw message-header content and is
	// the standard representation for undisclosed/Bcc recipients.
	message.WriteString("\r\nTo: undisclosed-recipients:;\r\nSubject: ")
	message.WriteString(encodeHeaderWord(subject))
	message.WriteString("\r\nMIME-Version: 1.0\r\nContent-Type: ")
	message.WriteString(contentType)
	message.WriteString("\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	message.WriteString(encodeMIMEBase64(body))
	return []byte(message.String()), nil
}

func validateHeaderText(field, value string, allowEmpty bool) error {
	if !allowEmpty && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s不能为空", field)
	}
	if utf8.RuneCountInString(value) > maxHeaderRunes {
		return fmt.Errorf("%s过长", field)
	}
	for _, char := range value {
		if char == '\r' || char == '\n' || unicode.IsControl(char) {
			return fmt.Errorf("%s包含无效控制字符", field)
		}
	}
	return nil
}

// encodeHeaderWord emits one RFC 2047 encoded-word. Input was already checked
// for CR/LF and controls; base64 also keeps legal Chinese header text intact
// without ever copying it as raw header syntax.
func encodeHeaderWord(value string) string {
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(value)) + "?="
}

func encodeMIMEBase64(body string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	if encoded == "" {
		return ""
	}

	var wrapped strings.Builder
	wrapped.Grow(len(encoded) + len(encoded)/mimeLineLength*2)
	for len(encoded) > mimeLineLength {
		wrapped.WriteString(encoded[:mimeLineLength])
		wrapped.WriteString("\r\n")
		encoded = encoded[mimeLineLength:]
	}
	wrapped.WriteString(encoded)
	wrapped.WriteString("\r\n")
	return wrapped.String()
}
