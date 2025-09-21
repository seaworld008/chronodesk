package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
}

// NewSimpleJWTManager 创建简单JWT管理器
func NewSimpleJWTManager(accessSecret, refreshSecret string, accessExpire, refreshExpire time.Duration) *SimpleJWTManager {
	if accessSecret == "" {
		accessSecret = "default-access-secret-change-in-production"
	}
	if refreshSecret == "" {
		refreshSecret = "default-refresh-secret-change-in-production"
	}
	if accessExpire == 0 {
		accessExpire = 15 * time.Minute
	}
	if refreshExpire == 0 {
		refreshExpire = 7 * 24 * time.Hour
	}

	return &SimpleJWTManager{
		accessSecret:  accessSecret,
		refreshSecret: refreshSecret,
		accessExpire:  accessExpire,
		refreshExpire: refreshExpire,
		issuer:        "ticket-system",
	}
}

// JWTHeader JWT头部
type JWTHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// JWTPayload JWT载荷
type JWTPayload struct {
	UserID uint     `json:"user_id"`
	Role   UserRole `json:"role"`
	Type   string   `json:"type"` // access, refresh
	Iss    string   `json:"iss"`  // issuer
	Sub    string   `json:"sub"`  // subject
	Aud    string   `json:"aud"`  // audience
	Exp    int64    `json:"exp"`  // expiration time
	Nbf    int64    `json:"nbf"`  // not before
	Iat    int64    `json:"iat"`  // issued at
	Jti    string   `json:"jti"`  // JWT ID
}

// GenerateTokenPair 生成令牌对
func (j *SimpleJWTManager) GenerateTokenPair(userID uint, role UserRole) (accessToken, refreshToken string, err error) {
	now := time.Now()
	userIDStr := strconv.FormatUint(uint64(userID), 10)

	// 生成访问令牌
	accessPayload := &JWTPayload{
		UserID: userID,
		Role:   role,
		Type:   "access",
		Iss:    j.issuer,
		Sub:    userIDStr,
		Aud:    "ticket-system-api",
		Exp:    now.Add(j.accessExpire).Unix(),
		Nbf:    now.Unix(),
		Iat:    now.Unix(),
		Jti:    generateJTI(),
	}

	accessToken, err = j.generateToken(accessPayload, j.accessSecret)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate access token: %w", err)
	}

	// 生成刷新令牌
	refreshPayload := &JWTPayload{
		UserID: userID,
		Role:   role,
		Type:   "refresh",
		Iss:    j.issuer,
		Sub:    userIDStr,
		Aud:    "ticket-system-api",
		Exp:    now.Add(j.refreshExpire).Unix(),
		Nbf:    now.Unix(),
		Iat:    now.Unix(),
		Jti:    generateJTI(),
	}

	refreshToken, err = j.generateToken(refreshPayload, j.refreshSecret)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate refresh token: %w", err)
	}

	return accessToken, refreshToken, nil
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
		UserID: payload.UserID,
		Role:   payload.Role,
		Type:   payload.Type,
		Exp:    payload.Exp,
		Iat:    payload.Iat,
		Jti:    payload.Jti,
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
		UserID: payload.UserID,
		Role:   payload.Role,
		Type:   payload.Type,
		Exp:    payload.Exp,
		Iat:    payload.Iat,
		Jti:    payload.Jti,
	}, nil
}

// RevokeToken 撤销令牌（简单实现，实际应该使用黑名单）
func (j *SimpleJWTManager) RevokeToken(token string) error {
	// 在实际实现中，应该将令牌添加到黑名单
	// 这里只是一个占位符实现
	return nil
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
		UserID: payload.UserID,
		Role:   payload.Role,
		Type:   payload.Type,
		Exp:    payload.Exp,
		Iat:    payload.Iat,
		Jti:    payload.Jti,
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
	newAccessToken, _, err := j.GenerateTokenPair(claims.UserID, claims.Role)
	if err != nil {
		return "", false, err
	}

	return newAccessToken, true, nil
}
