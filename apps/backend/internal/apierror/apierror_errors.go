package apierror

// apierror 把散落在各个 handler 里的"错误→HTTP 状态码"和"参数绑定"
// 两类重复模式收拢到一处。目标：让 service 用 sentinel error，
// handler 写 c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": ...}) 一行
// 就能拿到正确状态码，不再到处 if errors.Is(...) { c.JSON(404) } ... else { c.JSON(500) }。

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ========== 通用哨兵错误（Sentinel Errors）==========
//
// 这些是"全局唯一"的错误变量，service 层返回它们，
// handler 层用 errors.Is() 判断类型，统一映射到 HTTP 状态码。
// 使用 errors.New 创建 sentinel error 的关键好处是：
// 即使 service 在 error 上包装了多层（如 fmt.Errorf("xxx: %w", ErrNotFound)），
// errors.Is 依然能穿透包装层匹配到原始错误。
var (
	ErrUnauthorized = errors.New("unauthorized")     // 未认证（401）
	ErrForbidden    = errors.New("forbidden")        // 无权限（403）
	ErrNotFound     = errors.New("not found")        // 资源不存在（404）
	ErrValidation   = errors.New("validation error") // 参数校验失败（400）
)

// ========== ClassifyHTTPStatus：错误 → HTTP 状态码 映射器 ==========
//
//	分类HTTP状态
//
// 把任意 error 映射到 HTTP 状态码。
// 命中条件：
//   - nil                          → 200
//   - gorm.ErrRecordNotFound        → 404
//   - gorm.ErrDuplicatedKey         → 409（资源冲突，比如 username 撞名）
//   - ErrUnauthorized              → 401
//   - ErrForbidden                 → 403
//   - ErrNotFound / ErrValidation  → 404 / 400
//   - 其它（含业务自定义 sentinel）→ 500
//
// 注意：各业务包自己定义的 sentinel error（video.ErrVideoNotFound 等）
// 不会被这里识别——它们要么继续在 handler 里用 errors.Is 显式处理，
// 要么业务方包装成上面这几个通用 sentinel 之一。
func ClassifyHTTPStatus(err error) int {
	switch {
	case err == nil:
		// nil 表示没有错误，返回 200 OK
		return http.StatusOK
	case errors.Is(err, gorm.ErrRecordNotFound):
		// GORM 的"记录未找到"错误 → 404 Not Found
		return http.StatusNotFound
	case errors.Is(err, gorm.ErrDuplicatedKey):
		// GORM 的"重复键"错误（如唯一索引冲突）→ 409 Conflict
		return http.StatusConflict
	case errors.Is(err, ErrUnauthorized):
		// 未认证 → 401 Unauthorized
		return http.StatusUnauthorized
	case errors.Is(err, ErrForbidden):
		// 无权限 → 403 Forbidden
		return http.StatusForbidden
	case errors.Is(err, ErrNotFound):
		// 通用"未找到"业务错误 → 404 Not Found
		return http.StatusNotFound
	case errors.Is(err, ErrValidation):
		// 参数校验失败 → 400 Bad Request
		return http.StatusBadRequest
	default:
		// 兜底：所有未显式处理的错误 → 500 Internal Server Error
		return http.StatusInternalServerError
	}
}

// ========== BindJSON：替代重复的 ShouldBindJSON + 400 返回 ==========
//
// BindJSON 把"JSON 绑定 + 校验失败自动返回 400"两步合并为一行。
// 绑定失败时已经写好响应并返回 false，调用方只需：
//
//	if !apierror.BindJSON(c, &req) { return }
func BindJSON(c *gin.Context, req any) bool {
	// c.ShouldBindJSON 尝试将请求体 JSON 绑定到 req 结构体
	// 如果 JSON 格式非法或字段类型不匹配，会返回 error
	if err := c.ShouldBindJSON(req); err != nil {
		// 绑定失败：直接返回 400 + 错误信息，调用方无需再处理
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return false
	}
	// 绑定成功，调用方继续执行业务逻辑
	return true
}

// ========== RequireID：校验 ID 类参数非 0 ==========
//
// RequireID 校验 ID 类参数非 0，常用于"X is required" 校验。
// id 为 0 时返回带字段名的错误，调用方直接返回即可：
//
//	if err := apierror.RequireID(req.VideoID, "video_id"); err != nil { return err }
func RequireID(id uint, name string) error {
	// uint 类型的 0 通常表示"未填写"，因为有效的数据库 ID 从 1 开始
	if id == 0 {
		// 返回一个 fmt.Errorf 包装的错误，包含字段名方便前端定位
		return fmt.Errorf("%s is required", name)
	}
	// ID 有效，返回 nil
	return nil
}
