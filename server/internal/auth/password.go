package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// SimplePasswordService 简单密码服务实现
type SimplePasswordService struct {
	minLength int
	salt      string
}

// NewSimplePasswordService 创建简单密码服务
func NewSimplePasswordService(minLength int, salt string) *SimplePasswordService {
	if minLength < 8 {
		minLength = 8
	}
	if salt == "" {
		salt = "default-salt-change-in-production"
	}
	return &SimplePasswordService{
		minLength: minLength,
		salt:      salt,
	}
}

// HashPassword 哈希密码
func (s *SimplePasswordService) HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password cannot be empty")
	}

	// 使用 SHA256 + 盐值进行哈希
	hasher := sha256.New()
	hasher.Write([]byte(s.salt))
	hasher.Write([]byte(password))
	hasher.Write([]byte(s.salt)) // 双重盐值
	hash := hasher.Sum(nil)

	return hex.EncodeToString(hash), nil
}

// VerifyPassword 验证密码
func (s *SimplePasswordService) VerifyPassword(hashedPassword, password string) error {
	if hashedPassword == "" || password == "" {
		return errors.New("password and hash cannot be empty")
	}

	// 计算输入密码的哈希
	computedHash, err := s.HashPassword(password)
	if err != nil {
		return err
	}

	// 比较哈希值
	if computedHash != hashedPassword {
		return errors.New("password verification failed")
	}

	return nil
}

// ValidatePassword 验证密码强度
func (s *SimplePasswordService) ValidatePassword(password string) error {
	if len(password) < s.minLength {
		return fmt.Errorf("password must be at least %d characters long", s.minLength)
	}

	if len(password) > 128 {
		return errors.New("password must be less than 128 characters")
	}

	// 检查是否包含至少一个数字
	hasDigit := false
	hasLower := false
	hasUpper := false
	hasSpecial := false

	for _, char := range password {
		switch {
		case unicode.IsDigit(char):
			hasDigit = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}

	if !hasDigit {
		return errors.New("password must contain at least one digit")
	}

	if !hasLower {
		return errors.New("password must contain at least one lowercase letter")
	}

	if !hasUpper {
		return errors.New("password must contain at least one uppercase letter")
	}

	if !hasSpecial {
		return errors.New("password must contain at least one special character")
	}

	// 检查常见弱密码
	weakPasswords := []string{
		"password", "123456", "123456789", "qwerty", "abc123",
		"password123", "admin", "letmein", "welcome", "monkey",
		"1234567890", "qwertyuiop", "asdfghjkl", "zxcvbnm",
	}

	lowerPassword := strings.ToLower(password)
	for _, weak := range weakPasswords {
		if strings.Contains(lowerPassword, weak) {
			return fmt.Errorf("password contains common weak pattern: %s", weak)
		}
	}

	// 检查重复字符
	if hasRepeatingChars(password, 3) {
		return errors.New("password cannot contain 3 or more repeating characters")
	}

	// 检查连续字符
	if hasSequentialChars(password, 4) {
		return errors.New("password cannot contain 4 or more sequential characters")
	}

	return nil
}

// GenerateRandomPassword 生成随机密码
func (s *SimplePasswordService) GenerateRandomPassword(length int) (string, error) {
	if length < s.minLength {
		length = s.minLength
	}
	if length > 128 {
		length = 128
	}

	// 字符集
	lowercase := "abcdefghijklmnopqrstuvwxyz"
	uppercase := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digits := "0123456789"
	special := "!@#$%^&*()_+-=[]{}|;:,.<>?"

	allChars := lowercase + uppercase + digits + special

	// 确保至少包含每种类型的字符
	password := make([]byte, 0, length)

	// 添加必需的字符类型
	password = append(password, lowercase[randInt(len(lowercase))])
	password = append(password, uppercase[randInt(len(uppercase))])
	password = append(password, digits[randInt(len(digits))])
	password = append(password, special[randInt(len(special))])

	// 填充剩余长度
	for len(password) < length {
		password = append(password, allChars[randInt(len(allChars))])
	}

	// 打乱顺序
	for i := len(password) - 1; i > 0; i-- {
		j := randInt(i + 1)
		password[i], password[j] = password[j], password[i]
	}

	return string(password), nil
}

// 辅助函数

// hasRepeatingChars 检查是否有重复字符
func hasRepeatingChars(password string, maxRepeat int) bool {
	if len(password) < maxRepeat {
		return false
	}

	for i := 0; i <= len(password)-maxRepeat; i++ {
		char := password[i]
		count := 1
		for j := i + 1; j < len(password) && j < i+maxRepeat; j++ {
			if password[j] == char {
				count++
			} else {
				break
			}
		}
		if count >= maxRepeat {
			return true
		}
	}
	return false
}

// hasSequentialChars 检查是否有连续字符
func hasSequentialChars(password string, maxSequential int) bool {
	if len(password) < maxSequential {
		return false
	}

	for i := 0; i <= len(password)-maxSequential; i++ {
		isSequential := true
		for j := 1; j < maxSequential; j++ {
			if int(password[i+j]) != int(password[i])+j {
				isSequential = false
				break
			}
		}
		if isSequential {
			return true
		}

		// 检查递减序列
		isSequential = true
		for j := 1; j < maxSequential; j++ {
			if int(password[i+j]) != int(password[i])-j {
				isSequential = false
				break
			}
		}
		if isSequential {
			return true
		}
	}
	return false
}

// randInt 生成随机整数
func randInt(max int) int {
	if max <= 0 {
		return 0
	}

	bytes := make([]byte, 4)
	rand.Read(bytes)

	// 转换为整数
	num := int(bytes[0])<<24 | int(bytes[1])<<16 | int(bytes[2])<<8 | int(bytes[3])
	if num < 0 {
		num = -num
	}

	return num % max
}

// IsValidEmail 验证邮箱格式
func IsValidEmail(email string) bool {
	if len(email) < 3 || len(email) > 254 {
		return false
	}

	// 简单的邮箱正则表达式
	emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	return emailRegex.MatchString(email)
}

// IsValidUsername 验证用户名格式
func IsValidUsername(username string) bool {
	if len(username) < 3 || len(username) > 50 {
		return false
	}

	// 用户名只能包含字母、数字、下划线和连字符
	usernameRegex := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	return usernameRegex.MatchString(username)
}

// SanitizeInput 清理输入
func SanitizeInput(input string) string {
	// 移除前后空格
	input = strings.TrimSpace(input)

	// 移除控制字符
	result := make([]rune, 0, len(input))
	for _, r := range input {
		if !unicode.IsControl(r) || r == '\t' || r == '\n' || r == '\r' {
			result = append(result, r)
		}
	}

	return string(result)
}

// GenerateSecureToken 生成安全令牌
func GenerateSecureToken(length int) (string, error) {
	if length <= 0 {
		length = 32
	}

	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	return hex.EncodeToString(bytes), nil
}

// GenerateNumericCode 生成数字验证码
func GenerateNumericCode(length int) (string, error) {
	if length <= 0 || length > 10 {
		length = 6
	}

	bytes := make([]byte, length)
	for i := 0; i < length; i++ {
		bytes[i] = byte('0' + randInt(10))
	}

	return string(bytes), nil
}
