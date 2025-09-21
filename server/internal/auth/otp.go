package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"
)

// SimpleOTPService 简单OTP服务实现
type SimpleOTPService struct {
	issuer string
	period int // TOTP周期，默认30秒
	digits int // OTP位数，默认6位
}

// NewSimpleOTPService 创建简单OTP服务
func NewSimpleOTPService(issuer string) *SimpleOTPService {
	if issuer == "" {
		issuer = "Ticket System"
	}
	return &SimpleOTPService{
		issuer: issuer,
		period: 30,
		digits: 6,
	}
}

// GenerateSecret 生成OTP密钥
func (s *SimpleOTPService) GenerateSecret() (string, error) {
	// 生成20字节的随机密钥
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("failed to generate random secret: %w", err)
	}

	// 使用Base32编码
	return base32.StdEncoding.EncodeToString(secret), nil
}

// GenerateQRCode 生成QR码URL
func (s *SimpleOTPService) GenerateQRCode(secret, email string) (string, error) {
	if secret == "" || email == "" {
		return "", fmt.Errorf("secret and email cannot be empty")
	}

	// 构建TOTP URL
	// otpauth://totp/Issuer:user@example.com?secret=SECRET&issuer=Issuer
	url := fmt.Sprintf(
		"otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		s.issuer,
		email,
		secret,
		s.issuer,
		s.digits,
		s.period,
	)

	return url, nil
}

// GenerateCode 生成当前时间的OTP代码
func (s *SimpleOTPService) GenerateCode(secret string) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("secret cannot be empty")
	}

	// 获取当前时间戳
	timestamp := time.Now().Unix() / int64(s.period)

	return s.generateCodeAtTime(secret, timestamp)
}

// VerifyCode 验证OTP代码
func (s *SimpleOTPService) VerifyCode(secret, code string) bool {
	if secret == "" || code == "" {
		return false
	}

	// 获取当前时间戳
	currentTime := time.Now().Unix() / int64(s.period)

	// 允许前后1个时间窗口的误差（总共3个窗口）
	for i := -1; i <= 1; i++ {
		timestamp := currentTime + int64(i)
		expectedCode, err := s.generateCodeAtTime(secret, timestamp)
		if err != nil {
			continue
		}
		if expectedCode == code {
			return true
		}
	}

	return false
}

// GenerateBackupCodes 生成备用代码
func (s *SimpleOTPService) GenerateBackupCodes() ([]string, error) {
	codes := make([]string, 10) // 生成10个备用代码

	for i := 0; i < 10; i++ {
		code, err := s.generateBackupCode()
		if err != nil {
			return nil, fmt.Errorf("failed to generate backup code %d: %w", i+1, err)
		}
		codes[i] = code
	}

	return codes, nil
}

// 内部方法

// generateCodeAtTime 在指定时间生成OTP代码
func (s *SimpleOTPService) generateCodeAtTime(secret string, timestamp int64) (string, error) {
	// 解码Base32密钥
	key, err := base32.StdEncoding.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", fmt.Errorf("failed to decode secret: %w", err)
	}

	// 将时间戳转换为8字节的大端序
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(timestamp))

	// 使用HMAC-SHA1计算哈希
	h := hmac.New(sha1.New, key)
	h.Write(buf)
	hash := h.Sum(nil)

	// 动态截取
	offset := hash[len(hash)-1] & 0x0F
	code := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7FFFFFFF

	// 取模得到指定位数的代码
	mod := int(math.Pow10(s.digits))
	otp := int(code) % mod

	// 格式化为指定位数的字符串
	return fmt.Sprintf("%0*d", s.digits, otp), nil
}

// generateBackupCode 生成备用代码
func (s *SimpleOTPService) generateBackupCode() (string, error) {
	// 生成8位数字的备用代码
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	// 转换为8位数字
	code := binary.BigEndian.Uint32(bytes) % 100000000
	return fmt.Sprintf("%08d", code), nil
}

// ValidateSecret 验证密钥格式
func (s *SimpleOTPService) ValidateSecret(secret string) error {
	if secret == "" {
		return fmt.Errorf("secret cannot be empty")
	}

	// 检查Base32格式
	_, err := base32.StdEncoding.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return fmt.Errorf("invalid secret format: %w", err)
	}

	return nil
}

// GetCurrentTimeWindow 获取当前时间窗口
func (s *SimpleOTPService) GetCurrentTimeWindow() int64 {
	return time.Now().Unix() / int64(s.period)
}

// GetTimeRemaining 获取当前时间窗口剩余时间
func (s *SimpleOTPService) GetTimeRemaining() int {
	now := time.Now().Unix()
	remaining := int64(s.period) - (now % int64(s.period))
	return int(remaining)
}

// GenerateCodeForTime 为指定时间生成代码（用于测试）
func (s *SimpleOTPService) GenerateCodeForTime(secret string, t time.Time) (string, error) {
	timestamp := t.Unix() / int64(s.period)
	return s.generateCodeAtTime(secret, timestamp)
}

// VerifyCodeAtTime 在指定时间验证代码（用于测试）
func (s *SimpleOTPService) VerifyCodeAtTime(secret, code string, t time.Time) bool {
	timestamp := t.Unix() / int64(s.period)
	expectedCode, err := s.generateCodeAtTime(secret, timestamp)
	if err != nil {
		return false
	}
	return expectedCode == code
}

// SetPeriod 设置TOTP周期
func (s *SimpleOTPService) SetPeriod(period int) {
	if period > 0 {
		s.period = period
	}
}

// SetDigits 设置OTP位数
func (s *SimpleOTPService) SetDigits(digits int) {
	if digits >= 4 && digits <= 8 {
		s.digits = digits
	}
}

// GetPeriod 获取TOTP周期
func (s *SimpleOTPService) GetPeriod() int {
	return s.period
}

// GetDigits 获取OTP位数
func (s *SimpleOTPService) GetDigits() int {
	return s.digits
}

// GetIssuer 获取发行者
func (s *SimpleOTPService) GetIssuer() string {
	return s.issuer
}

// FormatSecret 格式化密钥显示
func (s *SimpleOTPService) FormatSecret(secret string) string {
	if len(secret) <= 4 {
		return secret
	}

	// 每4个字符添加一个空格
	var formatted strings.Builder
	for i, char := range secret {
		if i > 0 && i%4 == 0 {
			formatted.WriteRune(' ')
		}
		formatted.WriteRune(char)
	}

	return formatted.String()
}

// ParseOTPURL 解析OTP URL
func ParseOTPURL(url string) (issuer, account, secret string, err error) {
	if !strings.HasPrefix(url, "otpauth://totp/") {
		return "", "", "", fmt.Errorf("invalid OTP URL format")
	}

	// 简化的URL解析
	// 实际实现中应该使用更完整的URL解析
	parts := strings.Split(url, "?")
	if len(parts) != 2 {
		return "", "", "", fmt.Errorf("invalid OTP URL format")
	}

	// 解析路径部分
	path := strings.TrimPrefix(parts[0], "otpauth://totp/")
	if strings.Contains(path, ":") {
		pathParts := strings.SplitN(path, ":", 2)
		issuer = pathParts[0]
		account = pathParts[1]
	} else {
		account = path
	}

	// 解析查询参数
	params := strings.Split(parts[1], "&")
	for _, param := range params {
		if strings.HasPrefix(param, "secret=") {
			secret = strings.TrimPrefix(param, "secret=")
		} else if strings.HasPrefix(param, "issuer=") && issuer == "" {
			issuer = strings.TrimPrefix(param, "issuer=")
		}
	}

	if secret == "" {
		return "", "", "", fmt.Errorf("secret not found in URL")
	}

	return issuer, account, secret, nil
}
