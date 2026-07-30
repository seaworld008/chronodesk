package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SimpleJWTManager 简单JWT管理器实现
type SimpleJWTManager struct {
	accessSecret  string
	refreshSecret string
	accessExpire  time.Duration
	refreshExpire time.Duration
	issuer        string
	audience      string
}

const minimumHumanJWTSecretLength = 32

// JWTManagerConfig is the explicit trust contract for human browser/session
// tokens. Issuer is the canonical APP_URL origin and Audience is its /api
// resource identifier.
type JWTManagerConfig struct {
	AccessSecret  string
	RefreshSecret string
	AccessExpire  time.Duration
	RefreshExpire time.Duration
	Issuer        string
	Audience      string
}

// NewSimpleJWTManager 创建 JWT 管理器。无效或缺失的信任配置会在启动时
// 直接失败；不存在公开固定密钥、issuer 或 audience 回退。
func NewSimpleJWTManager(config JWTManagerConfig) (*SimpleJWTManager, error) {
	if err := validateHumanJWTSecret("access", config.AccessSecret); err != nil {
		return nil, err
	}
	if err := validateHumanJWTSecret("refresh", config.RefreshSecret); err != nil {
		return nil, err
	}
	if config.AccessSecret == config.RefreshSecret {
		return nil, errors.New("human JWT access and refresh secrets must be different")
	}
	if config.AccessExpire <= 0 || config.RefreshExpire <= 0 {
		return nil, errors.New("human JWT access and refresh expiration must be positive")
	}
	if err := validateHumanJWTEndpointContract(config.Issuer, config.Audience); err != nil {
		return nil, err
	}

	return &SimpleJWTManager{
		accessSecret:  config.AccessSecret,
		refreshSecret: config.RefreshSecret,
		accessExpire:  config.AccessExpire,
		refreshExpire: config.RefreshExpire,
		issuer:        config.Issuer,
		audience:      config.Audience,
	}, nil
}

func validateHumanJWTSecret(name, secret string) error {
	if secret != strings.TrimSpace(secret) || len(secret) < minimumHumanJWTSecretLength {
		return fmt.Errorf(
			"human JWT %s secret must be at least %d characters without surrounding whitespace",
			name,
			minimumHumanJWTSecretLength,
		)
	}
	return nil
}

func validateHumanJWTEndpointContract(issuer, audience string) error {
	if issuer != strings.TrimSpace(issuer) {
		return errors.New("human JWT issuer must not contain surrounding whitespace")
	}
	parsed, err := url.Parse(issuer)
	if err != nil ||
		!parsed.IsAbs() ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		parsed.Path != "" ||
		parsed.RawPath != "" {
		return errors.New("human JWT issuer must be an absolute canonical origin")
	}
	if parsed.Scheme != "https" &&
		!(parsed.Scheme == "http" && isLoopbackJWTIssuer(parsed.Hostname())) {
		return errors.New("human JWT issuer must use HTTPS except for loopback development")
	}
	expectedAudience := issuer + "/api"
	if audience != expectedAudience {
		return fmt.Errorf("human JWT audience must exactly match %q", expectedAudience)
	}
	return nil
}

func isLoopbackJWTIssuer(hostname string) bool {
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	address := net.ParseIP(hostname)
	return address != nil && address.IsLoopback()
}

// JWTHeader JWT头部
type JWTHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// JWTPayload JWT载荷
type JWTPayload struct {
	UserID       uint         `json:"user_id"`
	PlatformRole PlatformRole `json:"platform_role"`
	Type         string       `json:"type"` // access, refresh
	SessionID    string       `json:"sid"`  // server-authoritative login session
	Iss          string       `json:"iss"`  // issuer
	Sub          string       `json:"sub"`  // subject
	Aud          string       `json:"aud"`  // audience
	Exp          int64        `json:"exp"`  // expiration time
	Nbf          int64        `json:"nbf"`  // not before
	Iat          int64        `json:"iat"`  // issued at
	Jti          string       `json:"jti"`  // JWT ID
}

// GenerateTokenPair 生成令牌对
func (j *SimpleJWTManager) GenerateTokenPair(
	userID uint,
	platformRole PlatformRole,
	sessionID string,
) (accessToken, refreshToken string, err error) {
	return j.generateTokenPairAt(
		userID,
		platformRole,
		sessionID,
		time.Now(),
		generateJTI(),
		generateJTI(),
	)
}

// GenerateRefreshTokenPair deterministically derives one replacement pair from
// the old refresh token and the persisted rotation time. This lets a client
// replay an interrupted refresh without storing either replacement bearer
// token in plaintext. The rotation seed is itself a confidential bearer token
// and is only used as HMAC input.
func (j *SimpleJWTManager) GenerateRefreshTokenPair(
	userID uint,
	platformRole PlatformRole,
	sessionID, rotationSeed string,
	issuedAt time.Time,
) (accessToken, refreshToken string, err error) {
	if strings.TrimSpace(rotationSeed) == "" || issuedAt.IsZero() {
		return "", "", errors.New("refresh rotation seed and issue time are required")
	}
	issuedAt = issuedAt.UTC().Truncate(time.Second)
	return j.generateTokenPairAt(
		userID,
		platformRole,
		sessionID,
		issuedAt,
		j.rotationJTI(j.accessSecret, "access", rotationSeed, issuedAt),
		j.rotationJTI(j.refreshSecret, "refresh", rotationSeed, issuedAt),
	)
}

func (j *SimpleJWTManager) generateTokenPairAt(
	userID uint,
	platformRole PlatformRole,
	sessionID string,
	now time.Time,
	accessJTI, refreshJTI string,
) (accessToken, refreshToken string, err error) {
	sessionID = strings.TrimSpace(sessionID)
	if userID == 0 ||
		!platformRole.IsValid() ||
		sessionID == "" ||
		len(sessionID) > 128 ||
		now.IsZero() ||
		accessJTI == "" ||
		refreshJTI == "" {
		return "", "", errors.New("valid user and session identifiers are required")
	}
	userIDStr := strconv.FormatUint(uint64(userID), 10)

	// 生成访问令牌
	accessPayload := &JWTPayload{
		UserID:       userID,
		PlatformRole: platformRole,
		Type:         "access",
		SessionID:    sessionID,
		Iss:          j.issuer,
		Sub:          userIDStr,
		Aud:          j.audience,
		Exp:          now.Add(j.accessExpire).Unix(),
		Nbf:          now.Unix(),
		Iat:          now.Unix(),
		Jti:          accessJTI,
	}

	accessToken, err = j.generateToken(accessPayload, j.accessSecret)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate access token: %w", err)
	}

	// 生成刷新令牌
	refreshPayload := &JWTPayload{
		UserID:       userID,
		PlatformRole: platformRole,
		Type:         "refresh",
		SessionID:    sessionID,
		Iss:          j.issuer,
		Sub:          userIDStr,
		Aud:          j.audience,
		Exp:          now.Add(j.refreshExpire).Unix(),
		Nbf:          now.Unix(),
		Iat:          now.Unix(),
		Jti:          refreshJTI,
	}

	refreshToken, err = j.generateToken(refreshPayload, j.refreshSecret)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate refresh token: %w", err)
	}

	return accessToken, refreshToken, nil
}

func (j *SimpleJWTManager) rotationJTI(
	secret, purpose, seed string,
	issuedAt time.Time,
) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("chronodesk:refresh-rotation:v1\x00"))
	_, _ = mac.Write([]byte(purpose))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(seed))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(strconv.FormatInt(issuedAt.Unix(), 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyAccessToken 验证访问令牌
func (j *SimpleJWTManager) VerifyAccessToken(token string) (*Claims, error) {
	payload, err := j.verifyToken(token, j.accessSecret)
	if err != nil {
		return nil, err
	}

	if payload.Type != "access" {
		return nil, errors.New("invalid token type")
	}

	return &Claims{
		UserID:       payload.UserID,
		PlatformRole: payload.PlatformRole,
		Type:         payload.Type,
		SessionID:    payload.SessionID,
		Exp:          payload.Exp,
		Iat:          payload.Iat,
		Jti:          payload.Jti,
	}, nil
}

// VerifyRefreshToken 验证刷新令牌
func (j *SimpleJWTManager) VerifyRefreshToken(token string) (*Claims, error) {
	payload, err := j.verifyToken(token, j.refreshSecret)
	if err != nil {
		return nil, err
	}

	if payload.Type != "refresh" {
		return nil, errors.New("invalid token type")
	}

	return &Claims{
		UserID:       payload.UserID,
		PlatformRole: payload.PlatformRole,
		Type:         payload.Type,
		SessionID:    payload.SessionID,
		Exp:          payload.Exp,
		Iat:          payload.Iat,
		Jti:          payload.Jti,
	}, nil
}

// 内部方法

// generateToken 生成JWT令牌
func (j *SimpleJWTManager) generateToken(payload *JWTPayload, secret string) (string, error) {
	// 创建头部
	header := &JWTHeader{
		Alg: "HS256",
		Typ: "JWT",
	}

	// 编码头部
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("failed to marshal header: %w", err)
	}
	headerEncoded := base64.RawURLEncoding.EncodeToString(headerBytes)

	// 编码载荷
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payloadBytes)

	// 创建签名
	message := headerEncoded + "." + payloadEncoded
	signature := j.sign(message, secret)

	// 组合令牌
	token := message + "." + signature
	return token, nil
}

// verifyToken 验证JWT令牌
func (j *SimpleJWTManager) verifyToken(token, secret string) (*JWTPayload, error) {
	// 分割令牌
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}

	headerEncoded := parts[0]
	payloadEncoded := parts[1]
	signatureEncoded := parts[2]

	// 验证签名
	message := headerEncoded + "." + payloadEncoded
	expectedSignature := j.sign(message, secret)
	if !j.verifySignature(signatureEncoded, expectedSignature) {
		return nil, errors.New("invalid signature")
	}

	// 解码载荷
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadEncoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode payload: %w", err)
	}

	var payload JWTPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	// 验证时间
	now := time.Now().Unix()
	if payload.Exp < now {
		return nil, ErrTokenExpired
	}
	if payload.Nbf > now {
		return nil, errors.New("token not yet valid")
	}

	// 验证发行者
	if payload.Iss != j.issuer {
		return nil, errors.New("invalid issuer")
	}
	if payload.Aud != j.audience {
		return nil, errors.New("invalid audience")
	}
	if payload.Sub != strconv.FormatUint(uint64(payload.UserID), 10) {
		return nil, errors.New("invalid subject")
	}
	if payload.UserID == 0 ||
		!payload.PlatformRole.IsValid() ||
		strings.TrimSpace(payload.SessionID) == "" ||
		len(payload.SessionID) > 128 ||
		strings.TrimSpace(payload.Jti) == "" {
		return nil, errors.New("invalid session claims")
	}

	return &payload, nil
}

// sign 签名
func (j *SimpleJWTManager) sign(message, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	signature := h.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(signature)
}

// verifySignature 验证签名
func (j *SimpleJWTManager) verifySignature(provided, expected string) bool {
	return hmac.Equal([]byte(provided), []byte(expected))
}

// generateJTI 生成JWT ID
func generateJTI() string {
	token, _ := GenerateSecureToken(16)
	return token
}

// ParseTokenClaims 解析令牌声明（不验证签名，用于获取过期令牌信息）
func (j *SimpleJWTManager) ParseTokenClaims(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}

	payloadEncoded := parts[1]
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadEncoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode payload: %w", err)
	}

	var payload JWTPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	return &Claims{
		UserID:       payload.UserID,
		PlatformRole: payload.PlatformRole,
		Type:         payload.Type,
		SessionID:    payload.SessionID,
		Exp:          payload.Exp,
		Iat:          payload.Iat,
		Jti:          payload.Jti,
	}, nil
}

// GetTokenExpiration 获取令牌过期时间
func (j *SimpleJWTManager) GetTokenExpiration(tokenType string) time.Duration {
	switch tokenType {
	case "access":
		return j.accessExpire
	case "refresh":
		return j.refreshExpire
	default:
		return j.accessExpire
	}
}

// IsTokenExpired 检查令牌是否过期
func (j *SimpleJWTManager) IsTokenExpired(token string) bool {
	claims, err := j.ParseTokenClaims(token)
	if err != nil {
		return true
	}
	return time.Now().Unix() > claims.Exp
}

// GetTokenRemainingTime 获取令牌剩余时间
func (j *SimpleJWTManager) GetTokenRemainingTime(token string) time.Duration {
	claims, err := j.ParseTokenClaims(token)
	if err != nil {
		return 0
	}

	expTime := time.Unix(claims.Exp, 0)
	remaining := time.Until(expTime)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// RefreshTokenIfNeeded 如果需要则刷新令牌
func (j *SimpleJWTManager) RefreshTokenIfNeeded(accessToken string, threshold time.Duration) (newToken string, needRefresh bool, err error) {
	remaining := j.GetTokenRemainingTime(accessToken)
	if remaining > threshold {
		return accessToken, false, nil
	}

	// 解析令牌获取用户信息
	claims, err := j.ParseTokenClaims(accessToken)
	if err != nil {
		return "", false, err
	}

	// 生成新的访问令牌
	newAccessToken, _, err := j.GenerateTokenPair(
		claims.UserID,
		claims.PlatformRole,
		claims.SessionID,
	)
	if err != nil {
		return "", false, err
	}

	return newAccessToken, true, nil
}
