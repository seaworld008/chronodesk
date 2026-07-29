package auth

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidBackupCodeStorage = errors.New("invalid backup-code storage")

func hashBackupCodes(codes []string) (string, error) {
	if len(codes) == 0 {
		return "", nil
	}
	hashes := make([]string, 0, len(codes))
	for _, rawCode := range codes {
		code := strings.TrimSpace(rawCode)
		if code == "" {
			return "", ErrInvalidBackupCodeStorage
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return "", fmt.Errorf("hash backup code: %w", err)
		}
		hashes = append(hashes, string(hash))
	}
	return strings.Join(hashes, ","), nil
}

func parseBackupCodeHashes(serialized string) ([]string, error) {
	serialized = strings.TrimSpace(serialized)
	if serialized == "" {
		return nil, nil
	}
	parts := strings.Split(serialized, ",")
	hashes := make([]string, 0, len(parts))
	for _, rawHash := range parts {
		hash := strings.TrimSpace(rawHash)
		if hash == "" {
			return nil, ErrInvalidBackupCodeStorage
		}
		if _, err := bcrypt.Cost([]byte(hash)); err != nil {
			return nil, ErrInvalidBackupCodeStorage
		}
		hashes = append(hashes, hash)
	}
	return hashes, nil
}

func matchBackupCode(hashes []string, candidate string) int {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return -1
	}
	for i, hash := range hashes {
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(candidate)) == nil {
			return i
		}
	}
	return -1
}
