// Package account 实现账号领域的 HTTP 处理器层（Handler / Controller）。
//
// 本包是三层架构中最上层的一层，唯一职责是在 HTTP 协议与业务逻辑之间做"翻译"：
// 把 gin.Context 里的原始请求转换成 Service 层的方法调用，再把 Service 的返回值
// 或错误转换回统一格式的 JSON 响应。
//
// # 分层定位
//
//   - 上层：http_router.go 把本包的 Handler 方法注册到 Gin 路由表
//   - 本层：参数绑定 → 身份提取 → 调用 Service → 序列化响应
//   - 下层：account_service.go 实现真正的业务规则（密码哈希、token 签发、唯一性校验）
//
// 依赖方向严格单向：router → handler → service → repo。本包不 import gorm、不 import repo，
// 保证 Handler 层可以被独立单元测试（注入 mock Service 即可，无需启动数据库）。
//
// # 核心类型
//
//   - AccountHandler: 处理器结构体，持有 *AccountService 指针（依赖注入）
//   - NewAccountHandler(service): 构造函数，在应用启动时由 main.go 或 wire 组装
//
// # 方法清单（每个方法对应 router 中注册的一条路由）
//
// 公开接口（无需 JWT）:
//   - CreateAccount    POST /account/register       注册新用户
//   - Login            POST /account/login          登录，返回 access + refresh token
//   - ChangePassword   POST /account/changePassword 通过旧密码改密（独立认证方式）
//   - FindByID         POST /account/findByID       按 ID 查用户（内部/管理用）
//   - FindByUsername   POST /account/findByUsername 按用户名查用户
//   - Refresh          POST /account/refresh        用 X-Refresh-Token 头刷新令牌对
//
// 受保护接口（需 JWT 中间件）:
//   - Logout           POST /account/logout         清空 DB 中的 token，使旧 JWT 失效
//   - Rename           POST /account/rename         改名并重签 token
//   - UpdateProfile    POST /account/updateProfile  部分更新头像/bio
//
// # 统一处理模板
//
// 几乎每个 Handler 方法都遵循同一个四步模板：
//
//  1. apierror.BindJSON(c, &req)        绑定并校验请求体，失败自动写 400 响应
//  2. jwtmw.GetAccountID(c)             （仅受保护接口）从 context 提取当前用户 ID
//  3. h.service.XXX(c.Request.Context(), ...)  调用 Service，透传 Context 支持取消
//  4. c.JSON(status, gin.H{...})        成功返回数据，失败用 ClassifyHTTPStatus 映射状态码
//
// # 关键设计约定
//
//  1. 职责边界: Handler 只做格式校验（字段非空、类型匹配），不做业务校验
//     （密码强度、用户名唯一性、token 是否过期）。后者属于 Service 层。
//
//  2. 身份来源: 受保护接口的 accountID 必须从 jwtmw.GetAccountID(c) 获取，
//     绝不能从请求体读取。这是防止水平越权（用户篡改 body 中的 ID 操作他人账号）的安全红线。
//
//  3. Context 透传: 所有 Service 调用都传 c.Request.Context()，而非 context.Background()。
//     当客户端断开或超时，Context 取消会一路传递到 GORM，提前终止 SQL 节省 DB 资源。
//
//  4. 错误分类: 通用错误用 apierror.ClassifyHTTPStatus(err) 自动映射 400/404/409/500；
//     登录/刷新等敏感接口固定返回 401 + 模糊提示，避免泄露"用户名是否存在"等细节（防枚举攻击）。
//
//  5. 最小暴露: 成功响应只返回前端需要的字段（如 id, username），
//     绝不暴露 password hash、created_at、内部 token 等敏感或冗余信息。
//
//  6. 双令牌机制: access token 短期有效（~15min），放 Authorization: Bearer 头；
//     refresh token 长期有效（~7d），放自定义 X-Refresh-Token 头。Refresh 接口会轮换两者，
//     旧 refresh token 随即失效，实现滑动过期 + 可撤销的安全策略。
package account

// =============================================================================
// 账号 HTTP 处理器（Handler / Controller 层）
// =============================================================================
// 职责边界：
//   1. 参数绑定与校验（BindJSON）—— 只做格式校验，不做业务校验
//   2. 从 gin.Context 提取身份信息（JWT 中间件注入的 accountID）
//   3. 调用 Service 层执行业务逻辑
//   4. 将 Service 的返回值或错误转换为统一格式的 HTTP JSON 响应
// 注意：Handler 层【不应该】包含任何业务规则（如密码强度校验、用户名唯一性检查），
//       那些都属于 Service 层的职责。
// =============================================================================

import (
	"net/http"

	"tiny-feed/internal/apierror"
	jwtmw "tiny-feed/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

// AccountHandler 把 HTTP 层的请求转给 AccountService。
// 采用组合而非继承：持有 service 指针，实现依赖注入。
type AccountHandler struct {
	service *AccountService // 业务逻辑层的依赖，由外部注入
}

// NewAccountHandler 构造账号处理器。
// 在应用启动时由 wire 或手动组装调用，传入已初始化好的 AccountService。
func NewAccountHandler(service *AccountService) *AccountHandler {
	return &AccountHandler{service: service}
}

// CreateAccount 处理 POST /account/register。
// 请求体：{username, password}。
// 成功返回 200 + {id, username}；用户名/密码为空或已存在返回 400。
func (h *AccountHandler) CreateAccount(c *gin.Context) {
	// Step 1: 绑定请求体到结构体，同时做基础校验（非空、类型匹配等）
	var req CreateAccountRequest
	if !apierror.BindJSON(c, &req) {
		// BindJSON 内部已经写了错误响应，这里直接返回即可
		return
	}

	// Step 2: 调用 Service 层。注意传入 c.Request.Context()，
	//         这样当客户端断开连接时，Context 会被取消，Service/Repo 层可以提前终止 DB 查询。
	acc, err := h.service.CreateAccount(c.Request.Context(), &req)
	if err != nil {
		// Step 3: 错误分类。ClassifyHTTPStatus 会根据错误类型返回 400/409/500 等
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}

	// Step 4: 成功响应。只暴露必要字段（id, username），不暴露 password hash 等敏感信息
	c.JSON(http.StatusOK, gin.H{"id": acc.ID, "username": acc.Username})
}

// Login 处理 POST /account/login。
// 请求体：{username, password}。
// 成功返回 200 + {token, refresh_token, account_id, username}。
// 安全要点：用户名/密码错误统一返回 401 + 模糊提示，避免枚举攻击（不告诉攻击者"用户名不存在"还是"密码错误"）。
func (h *AccountHandler) Login(c *gin.Context) {
	var req LoginRequest
	if !apierror.BindJSON(c, &req) {
		return
	}
	resp, err := h.service.Login(c.Request.Context(), &req)
	if err != nil {
		// 登录失败固定返回 401，不使用 ClassifyHTTPStatus，防止泄露错误细节
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ChangePassword 处理 POST /account/changePassword。
// 请求体：{username, old_password, new_password}。
// 设计决策：该接口在当前 router 中是公开的（不要求 JWT），
//
//	因为它本身通过"知道旧密码"来完成身份验证，等同于一种独立的认证方式。
func (h *AccountHandler) ChangePassword(c *gin.Context) {
	var req ChangePasswordRequest
	if !apierror.BindJSON(c, &req) {
		return
	}
	if err := h.service.ChangePassword(c.Request.Context(), &req); err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// FindByID 处理 POST /account/findByID。
// 请求体：{id}。通常用于内部服务间调用或管理后台。
func (h *AccountHandler) FindByID(c *gin.Context) {
	var req FindByIDRequest
	if !apierror.BindJSON(c, &req) {
		return
	}
	resp, err := h.service.FindByID(c.Request.Context(), req.ID)
	if err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// FindByUsername 处理 POST /account/findByUsername。
// 请求体：{username}。
func (h *AccountHandler) FindByUsername(c *gin.Context) {
	var req FindByUsernameRequest
	if !apierror.BindJSON(c, &req) {
		return
	}
	resp, err := h.service.FindByUsername(c.Request.Context(), req.Username)
	if err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// Logout 处理 POST /account/logout（受 JWT 中间件保护）。
// 从 gin.Context 取出当前登录用户 ID，调 service 把 DB 里的 token 清空/加入黑名单。
func (h *AccountHandler) Logout(c *gin.Context) {
	// 关键安全点：accountID 来自 JWT 中间件注入的 context，而非请求体。
	// 这防止了用户通过篡改请求体中的 ID 来注销别人的账号（水平越权）。
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		// 理论上不会触发：能走到这里说明已经过了 JWT 中间件。
		// 兜底返回 401，属于防御性编程。
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.Logout(c.Request.Context(), accountID); err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Rename 处理 POST /account/rename（受 JWT 保护）。
// 请求体：{new_username}。
// 注意：改名后通常会重新签发 token（因为 token payload 里可能包含 username），
//
//	前端需要用响应里的新 token 替换本地存储的旧值。
func (h *AccountHandler) Rename(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req RenameRequest
	if !apierror.BindJSON(c, &req) {
		return
	}
	if err := h.service.Rename(c.Request.Context(), accountID, req.NewUsername); err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// UpdateProfile 处理 POST /account/updateProfile（受 JWT 保护）。
// 请求体：{avatar_url, bio}，只更新非空字段（部分更新 / PATCH 语义）。
func (h *AccountHandler) UpdateProfile(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req UpdateProfileRequest
	if !apierror.BindJSON(c, &req) {
		return
	}
	if err := h.service.UpdateProfile(c.Request.Context(), accountID, &req); err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Refresh 处理 POST /account/refresh。
// 认证方式特殊：不走 Authorization: Bearer <access_token>，
//
//	而是通过自定义 Header X-Refresh-Token 传递长期有效的刷新令牌。
//
// 流程：取 header → 查 DB 验证 refresh token → 签发新的一对令牌 → 旧 refresh token 失效 → 返回新令牌。
// 安全意义：即使 access token 泄露，攻击者也无法无限续期；refresh token 泄露后可通过登出撤销。
func (h *AccountHandler) Refresh(c *gin.Context) {
	// refresh token 通过专用 header 传，避免和 Bearer access token 混在一起，也避免出现在 URL query 中被日志记录。
	refreshToken := c.GetHeader("X-Refresh-Token")
	if refreshToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refresh token required"})
		return
	}

	// 根据 refresh token 找出账号。Service 层会校验 token 是否在 DB 中存在且未过期。
	accountID, username, err := h.service.ResolveRefreshToken(c.Request.Context(), refreshToken)
	if err != nil {
		// refresh token 无效、已过期或已被撤销，统一返回 401，要求用户重新登录。
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	// 签发新的一对令牌。Service 层内部会：
	//   1. 生成新的 access token（短期，如 15min）
	//   2. 生成新的 refresh token（长期，如 7d）
	//   3. 把新 refresh token 写入 DB，覆盖/删除旧的
	newToken, newRefresh, err := h.service.IssueTokens(c.Request.Context(), accountID, username)
	if err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":         newToken,   // 新的 access token，放 Authorization header
		"refresh_token": newRefresh, // 新的 refresh token，放 X-Refresh-Token header 或 secure cookie
		"account_id":    accountID,  // 方便前端更新本地用户状态
		"username":      username,   // 同上
	})
}
