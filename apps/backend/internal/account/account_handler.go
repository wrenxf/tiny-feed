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
