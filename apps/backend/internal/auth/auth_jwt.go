package auth

import (
	"crypto/rand"  // 加密安全的随机数生成器
	"encoding/hex" // 十六进制编码/解码
	"errors"       // 标准错误处理
	"log"          // 日志输出
	"os"           // 操作系统接口（读取环境变量）
	"sync"         // 同步原语（sync.Once）
	"time"         // 时间处理

	"github.com/golang-jwt/jwt/v5" // JWT 库（v5 版本）
)

// ========== JWT 密钥管理：懒加载 + 线程安全 ==========
//
// jwtSecret 用 sync.Once 保护懒加载，避免多个 goroutine 第一次访问时
// 都各自生成一次随机密钥互相覆盖的问题。
var (
	once   sync.Once // 确保初始化逻辑只执行一次
	secret []byte    // 存储最终的 JWT 签名密钥
)

// jwtSecret 返回进程内统一的 JWT 签名密钥。
// 优先用环境变量 JWT_SECRET；如果没设，则懒生成一个随机密钥并 log 警告。
// 随机密钥情况下，服务重启会让所有已签发的 token 失效——这是有意为之。
func jwtSecret() []byte {
	// sync.Once.Do 保证内部的 func() 在整个程序生命周期内只执行一次
	// 即使有 100 个 goroutine 同时调用 jwtSecret()，也只有一个会执行初始化
	once.Do(func() {
		// 1. 优先从环境变量读取密钥（生产环境推荐做法）
		s := os.Getenv("JWT_SECRET")
		if s == "" {
			// 2. 环境变量未设置 → 生成 32 字节随机密钥
			b := make([]byte, 32)
			// crypto/rand.Read 使用操作系统提供的加密安全随机源
			if _, err := rand.Read(b); err != nil {
				// 极端情况：连随机数都生成不了，使用硬编码兜底（仅开发环境）
				log.Printf("严重错误：无法生成 JWT 密钥：%v", err)
				secret = []byte("fallback-unsafe-key-change-me")
				return
			}
			// 将随机字节转为十六进制字符串（64 字符），方便日志查看
			s = hex.EncodeToString(b)
			log.Printf("警告：未设置 JWT_SECRET，已生成随机密钥，服务重启后所有令牌将失效。")
		}
		// 3. 将字符串密钥转为 []byte 存储（JWT 库需要字节切片）
		secret = []byte(s)
	})
	return secret
}

// ========== Claims：JWT 载荷结构体 ==========
//
// Claims 定义了 JWT token 中携带的业务数据。
// 嵌入 jwt.RegisteredClaims 可以获得标准的 JWT 字段（exp, iat, nbf 等）。
type Claims struct {
	AccountID            uint   `json:"account_id"` // 用户 ID
	Username             string `json:"username"`   // 用户名
	jwt.RegisteredClaims        // 嵌入标准声明（过期时间、签发时间等）
}

// ========== GenerateToken：生成访问令牌（Access Token） ==========
//
// GenerateToken 为指定用户生成一个短期有效的 JWT access token。
// 有效期 15 分钟，用于 API 请求的身份验证。
func GenerateToken(accountID uint, username string) (string, error) {
	now := time.Now()

	// 构造 Claims，填充业务数据和标准时间字段
	claims := Claims{
		AccountID: accountID,
		Username:  username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)), // 15 分钟后过期
			IssuedAt:  jwt.NewNumericDate(now),                       // 签发时间
			NotBefore: jwt.NewNumericDate(now),                       // 生效时间（立即生效）
		},
	}

	// 使用 HS256 算法创建 token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// 用密钥签名，返回最终的 token 字符串
	return token.SignedString(jwtSecret())
}

// ========== GenerateRefreshToken：生成刷新令牌（Refresh Token） ==========
//
// GenerateRefreshToken 生成一个长期有效的随机字符串，用于在 access token 过期后
// 换取新的 access token。这里简化实现为 32 字节随机十六进制字符串。
// 注意：生产环境中 refresh token 通常应存储在数据库中，以便支持撤销。
func GenerateRefreshToken(accountID uint) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// 返回 64 字符的十六进制字符串
	return hex.EncodeToString(b), nil
}

// ========== ParseToken：解析并验证 JWT ==========
//
// ParseToken 接收前端传来的 token 字符串，验证签名和有效期，
// 成功则返回 Claims，失败则返回 error。
func ParseToken(tokenString string) (*Claims, error) {
	// jwt.ParseWithClaims 是 v5 版本的核心解析函数
	// 第二个参数传入空的 Claims 结构体指针，用于接收解析结果
	// 第三个参数是 keyFunc，用于提供验证签名所需的密钥
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(token *jwt.Token) (interface{}, error) {
			// 安全检查：确保 token 使用的签名算法是我们预期的 HS256
			// 防止"none"算法攻击或算法混淆攻击
			if token.Method == nil || token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, errors.New("unexpected signing method")
			}
			// 返回密钥用于验证签名
			return jwtSecret(), nil
		},
	)
	if err != nil {
		// 解析失败（签名错误、格式错误、已过期等）
		return nil, err
	}

	// 类型断言：将通用的 Claims 接口转为具体的 *Claims 指针
	claims, ok := token.Claims.(*Claims)
	// 双重检查：类型断言成功 且 token 被标记为有效（未过期、签名正确）
	if !ok || !token.Valid {
		return nil, jwt.ErrTokenInvalidClaims
	}

	return claims, nil
}
