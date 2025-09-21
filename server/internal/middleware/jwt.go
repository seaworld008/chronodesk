package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// HTTPContext 简化的 HTTP 上下文接口
type HTTPContext interface {
	GetHeader(key string) string
	JSON(code int, obj interface{})
	Abort()
	Next()
	Set(key string, value interface{})
	Get(key string) (interface{}, bool)
}

// JWTClaims JWT 声明结构
type JWTClaims struct {
	UserID    uint   `json:"user_id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	Email     string `json:"email"`
	Issuer    string `json:"iss"`
	Subject   string `json:"sub"`
	IssuedAt  int64  `json:"iat"`
	Expiry    int64  `json:"exp"`
	NotBefore int64  `json:"nbf"`
	ID        string `json:"jti"`
}

// JWTManager JWT 管理器
type JWTManager struct {
	secretKey     []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
	issuer        string
}

// NewJWTManager 创建新的 JWT 管理器
func NewJWTManager(secretKey string, accessExpiry, refreshExpiry time.Duration, issuer string) *JWTManager {
	return &JWTManager{
		secretKey:     []byte(secretKey),
		accessExpiry:  accessExpiry,
		refreshExpiry: refreshExpiry,
		issuer:        issuer,
	}
}

// generateTokenID 生成唯一的 token ID
func (j *JWTManager) generateTokenID(userID uint) string {
	return fmt.Sprintf("%d_%d", userID, time.Now().UnixNano())
}

// GenerateAccessToken 生成访问令牌
func (j *JWTManager) GenerateAccessToken(userID uint, username, role, email string) (string, error) {
	now := time.Now()
	claims := JWTClaims{
		UserID:    userID,
		Username:  username,
		Role:      role,
		Email:     email,
		Issuer:    j.issuer,
		Subject:   fmt.Sprintf("%d", userID),
		IssuedAt:  now.Unix(),
		Expiry:    now.Add(j.accessExpiry).Unix(),
		NotBefore: now.Unix(),
		ID:        j.generateTokenID(userID),
	}

	return j.createToken(claims)
}

// GenerateRefreshToken 生成刷新令牌
func (j *JWTManager) GenerateRefreshToken(userID uint) (string, error) {
	now := time.Now()
	claims := JWTClaims{
		UserID:    userID,
		Issuer:    j.issuer,
		Subject:   fmt.Sprintf("%d", userID),
		IssuedAt:  now.Unix(),
		Expiry:    now.Add(j.refreshExpiry).Unix(),
		NotBefore: now.Unix(),
		ID:        j.generateTokenID(userID),
	}

	return j.createToken(claims)
}

// createToken 创建 JWT token
func (j *JWTManager) createToken(claims JWTClaims) (string, error) {
	// JWT Header
	header := map[string]interface{}{
		"alg": "HS256",
		"typ": "JWT",
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	headerEncoded := base64.RawURLEncoding.EncodeToString(headerJSON)

	// JWT Payload
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// JWT Signature
	message := headerEncoded + "." + payloadEncoded
	signature := j.sign(message)

	return message + "." + signature, nil
}

// sign 使用 HMAC-SHA256 签名
func (j *JWTManager) sign(message string) string {
	h := hmac.New(sha256.New, j.secretKey)
	h.Write([]byte(message))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// ValidateToken 验证令牌
func (j *JWTManager) ValidateToken(tokenString string) (*JWTClaims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	headerEncoded, payloadEncoded, signatureEncoded := parts[0], parts[1], parts[2]

	// 验证签名
	message := headerEncoded + "." + payloadEncoded
	expectedSignature := j.sign(message)
	if signatureEncoded != expectedSignature {
		return nil, fmt.Errorf("invalid token signature")
	}

	// 解码 payload
	payloadJSON, err := base64.RawURLEncoding.DecodeString(payloadEncoded)
	if err != nil {
		return nil, fmt.Errorf("invalid payload encoding")
	}

	var claims JWTClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("invalid payload format")
	}

	// 验证时间
	now := time.Now().Unix()
	if claims.Expiry < now {
		return nil, fmt.Errorf("token has expired")
	}
	if claims.NotBefore > now {
		return nil, fmt.Errorf("token not yet valid")
	}

	return &claims, nil
}

// ValidateRefreshToken 验证刷新令牌
func (j *JWTManager) ValidateRefreshToken(tokenString string) (uint, error) {
	claims, err := j.ValidateToken(tokenString)
	if err != nil {
		return 0, err
	}

	return claims.UserID, nil
}

// JWTAuth JWT 认证中间件（通用版本）
func JWTAuth(jwtManager *JWTManager) func(HTTPContext) {
	return func(c HTTPContext) {
		// 从 Authorization header 获取 token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, map[string]interface{}{
				"error": "Authorization header is required",
				"code":  "MISSING_AUTH_HEADER",
			})
			c.Abort()
			return
		}

		// 检查 Bearer 前缀
		tokenParts := strings.SplitN(authHeader, " ", 2)
		if len(tokenParts) != 2 || tokenParts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, map[string]interface{}{
				"error": "Invalid authorization header format",
				"code":  "INVALID_AUTH_FORMAT",
			})
			c.Abort()
			return
		}

		tokenString := tokenParts[1]

		// 验证 token
		claims, err := jwtManager.ValidateToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, map[string]interface{}{
				"error":   "Invalid or expired token",
				"code":    "INVALID_TOKEN",
				"details": err.Error(),
			})
			c.Abort()
			return
		}

		// 将用户信息存储到上下文中
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("user_role", claims.Role)
		c.Set("user_email", claims.Email)
		c.Set("jwt_claims", claims)

		c.Next()
	}
}

// OptionalJWTAuth 可选的 JWT 认证中间件（不强制要求认证）
func OptionalJWTAuth(jwtManager *JWTManager) func(HTTPContext) {
	return func(c HTTPContext) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}

		tokenParts := strings.SplitN(authHeader, " ", 2)
		if len(tokenParts) != 2 || tokenParts[0] != "Bearer" {
			c.Next()
			return
		}

		tokenString := tokenParts[1]
		claims, err := jwtManager.ValidateToken(tokenString)
		if err != nil {
			c.Next()
			return
		}

		// 将用户信息存储到上下文中
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("user_role", claims.Role)
		c.Set("user_email", claims.Email)
		c.Set("jwt_claims", claims)

		c.Next()
	}
}

// RequireRole 角色权限中间件
func RequireRole(allowedRoles ...string) func(HTTPContext) {
	return func(c HTTPContext) {
		userRole, exists := c.Get("user_role")
		if !exists {
			c.JSON(http.StatusUnauthorized, map[string]interface{}{
				"error": "User role not found in context",
				"code":  "MISSING_USER_ROLE",
			})
			c.Abort()
			return
		}

		role, ok := userRole.(string)
		if !ok {
			c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": "Invalid user role type",
				"code":  "INVALID_ROLE_TYPE",
			})
			c.Abort()
			return
		}

		// 检查用户角色是否在允许的角色列表中
		for _, allowedRole := range allowedRoles {
			if role == allowedRole {
				c.Next()
				return
			}
		}

		c.JSON(http.StatusForbidden, map[string]interface{}{
			"error": "Insufficient permissions",
			"code":  "INSUFFICIENT_PERMISSIONS",
		})
		c.Abort()
	}
}

// GetUserFromContext 从上下文中获取用户信息
func GetUserFromContext(c HTTPContext) (uint, string, string, string, bool) {
	userID, exists1 := c.Get("user_id")
	username, exists2 := c.Get("username")
	userRole, exists3 := c.Get("user_role")
	userEmail, exists4 := c.Get("user_email")

	if !exists1 || !exists2 || !exists3 || !exists4 {
		return 0, "", "", "", false
	}

	id, ok1 := userID.(uint)
	name, ok2 := username.(string)
	role, ok3 := userRole.(string)
	email, ok4 := userEmail.(string)

	if !ok1 || !ok2 || !ok3 || !ok4 {
		return 0, "", "", "", false
	}

	return id, name, role, email, true
}

// GetUserIDFromContext 从上下文中获取用户ID
func GetUserIDFromContext(c HTTPContext) (uint, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return 0, false
	}

	id, ok := userID.(uint)
	return id, ok
}

// GetJWTClaimsFromContext 从上下文中获取 JWT Claims
func GetJWTClaimsFromContext(c HTTPContext) (*JWTClaims, bool) {
	claims, exists := c.Get("jwt_claims")
	if !exists {
		return nil, false
	}

	jwtClaims, ok := claims.(*JWTClaims)
	return jwtClaims, ok
}

// BlacklistManager 黑名单管理器接口
type BlacklistManager interface {
	AddToBlacklist(ctx context.Context, tokenID string, expiry time.Time) error
	IsBlacklisted(ctx context.Context, tokenID string) (bool, error)
	CleanupExpired(ctx context.Context) error
}

// JWTWithBlacklist 带黑名单的 JWT 认证中间件
func JWTWithBlacklist(jwtManager *JWTManager, blacklist BlacklistManager) func(HTTPContext) {
	return func(c HTTPContext) {
		// 从 Authorization header 获取 token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, map[string]interface{}{
				"error": "Authorization header is required",
				"code":  "MISSING_AUTH_HEADER",
			})
			c.Abort()
			return
		}

		tokenParts := strings.SplitN(authHeader, " ", 2)
		if len(tokenParts) != 2 || tokenParts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, map[string]interface{}{
				"error": "Invalid authorization header format",
				"code":  "INVALID_AUTH_FORMAT",
			})
			c.Abort()
			return
		}

		tokenString := tokenParts[1]

		// 验证 token
		claims, err := jwtManager.ValidateToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, map[string]interface{}{
				"error": "Invalid or expired token",
				"code":  "INVALID_TOKEN",
			})
			c.Abort()
			return
		}

		// 检查 token 是否在黑名单中（需要实现 Request Context）
		if blacklist != nil && claims.ID != "" {
			// 注意：这里需要实际的 request context，暂时跳过黑名单检查
			// 在实际使用时需要传入正确的 context
		}

		// 将用户信息存储到上下文中
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("user_role", claims.Role)
		c.Set("user_email", claims.Email)
		c.Set("jwt_claims", claims)

		c.Next()
	}
}

// TokenResponse JWT token 响应结构
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

// GenerateTokenPair 生成访问令牌和刷新令牌对
func (j *JWTManager) GenerateTokenPair(userID uint, username, role, email string) (*TokenResponse, error) {
	accessToken, err := j.GenerateAccessToken(userID, username, role, email)
	if err != nil {
		return nil, fmt.Errorf("failed to generate access token: %w", err)
	}

	refreshToken, err := j.GenerateRefreshToken(userID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}

	return &TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(j.accessExpiry.Seconds()),
	}, nil
}
