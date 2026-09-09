// Package video 实现视频领域的 HTTP 处理器层（Handler / Controller）。
//
// 本包是三层架构的最上层，唯一职责是把 HTTP 请求"翻译"成对 VideoService 的方法调用，
// 并把 Service 返回的结果/错误"翻译"回 HTTP 响应。它不包含任何业务规则，所有决策都委托给 Service。
//
// # 分层定位
//
//   - 上层：Gin Router 根据 URL + Method 分发到本包的某个方法
//   - 本层：提取 JWT 身份 → 绑定 JSON/Form → 调 Service → 映射错误到 HTTP 状态码 → 写响应
//   - 下层：video_service.go 承载业务逻辑；apierror 包提供通用的错误分类工具
//
// 依赖方向：router → handler → service → repo → gorm。本包 import gin、jwtmw、apierror，
// 但绝不 import gorm 或 database/sql——Handler 不应该知道数据是怎么存的。
//
// # 核心类型
//
//   - VideoHandler: 持有 *VideoService 的薄壳结构体，每个方法对应一个 HTTP 端点
//   - NewVideoHandler(service): 构造函数，用于依赖注入
//
// # 路由清单
//
// 受 JWT 保护（必须登录）:
//   - POST /video/publish        Publish       发布新视频（JSON body）
//   - POST /video/delete         Delete        删除自己的视频（owner 鉴权在 Service 层）
//   - POST /video/uploadVideo    UploadVideo   上传视频文件（multipart/form-data）
//   - POST /video/uploadCover    UploadCover   上传封面图（multipart/form-data）
//
// 公开接口（无需登录）:
//   - POST /video/listByAuthorID ListByAuthorID 按作者查视频列表
//   - POST /video/getDetail      GetDetail      按 ID 查视频详情
//
// # 关键设计约定
//
//  1. 身份从 JWT 取，不从 body 取: Publish / Delete 中的 accountID / username 通过
//     jwtmw.GetAccountID(c) / jwtmw.GetUsername(c) 从中间件注入的 context 中提取，
//     前端无法伪造。这是整个鉴权体系的信任锚点。
//
//  2. 错误映射三级策略:
//     - 业务 sentinel error (ErrVideoNotFound / ErrVideoForbidden) → 显式 switch/if 映射 404/403
//     - 其他已知错误 → apierror.ClassifyHTTPStatus(err) 通用映射
//     - 未知错误 → ClassifyHTTPStatus 兜底返回 500
//     Handler 不做错误包装，只负责"选状态码"。
//
//  3. 请求绑定统一入口: 所有 JSON body 解析都走 apierror.BindJSON(c, &req)，
//     它在内部处理了 ShouldBindJSON 的错误响应，返回 bool 表示是否成功。
//     Handler 只需 if !ok { return }，避免重复样板代码。
//
//  4. 文件上传双重校验: saveUploadedFile 同时检查扩展名白名单 + Content-Type 前缀白名单，
//     防止攻击者把 .exe 改名为 .mp4 上传。两者任一不通过即拒绝。
//
//  5. 目录穿越防护: filepath.Base(header.Filename) 剥离路径分量后，再用 HasPrefix 二次确认
//     最终落盘路径确实在 uploadsDir/<subdir>/ 内。防御 ../../etc/passwd 类攻击。
//
//  6. 文件大小硬限制: kindVideo 300MB / kindCover 10MB，在读取文件内容之前就拦截，
//     避免大文件耗尽磁盘和内存。生产环境还应配合 Nginx client_max_body_size。
//
//  7. URL 返回而非文件流: 上传成功后返回 /static/<subdir>/<filename> 路径，
//     前端通过独立的静态资源服务器（或 Gin 的 Static 路由）访问，Handler 不承担文件下载职责。
//
// # 文件上传安全 Checklist
//
//   - [x] 扩展名白名单 (hasAcceptedExt)
//   - [x] Content-Type 白名单 (hasAcceptedContentType)
//   - [x] 文件大小上限 (kind.maxSize)
//   - [x] 目录穿越防护 (filepath.Base + HasPrefix)
//   - [ ] 文件名随机化（当前用原始文件名，存在覆盖风险，生产建议用 UUID）
//   - [ ] 病毒扫描（当前未做，生产建议接入 ClamAV 等）
//   - [ ] 存储隔离（当前存本地磁盘，生产建议用 OSS/S3 + 签名 URL）
package video

// 视频 HTTP 处理器。

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"tiny-feed/internal/apierror"
	jwtmw "tiny-feed/internal/middleware/jwt"

	"github.com/gin-gonic/gin"
)

// VideoHandler 把 HTTP 请求转给 VideoService。
type VideoHandler struct {
	service *VideoService
}

// NewVideoHandler 构造视频处理器。
func NewVideoHandler(service *VideoService) *VideoHandler {
	return &VideoHandler{service: service}
}

// Publish 处理 POST /video/publish（受 JWT 保护）。
// 从 JWT 取出 accountID 和 username，避免前端伪造。
func (h *VideoHandler) Publish(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	username, err := jwtmw.GetUsername(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req PublishVideoRequest
	if !apierror.BindJSON(c, &req) {
		return
	}
	v, err := h.service.Publish(c.Request.Context(), accountID, username, &req)
	if err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, v)
}

// ListByAuthorID 处理 POST /video/listByAuthorID（公开）。
// 请求体：{author_id}。
func (h *VideoHandler) ListByAuthorID(c *gin.Context) {
	var req ListByAuthorIDRequest
	if !apierror.BindJSON(c, &req) {
		return
	}
	videos, err := h.service.ListByAuthorID(c.Request.Context(), int64(req.AuthorID))
	if err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"videos": videos})
}

// GetDetail 处理 POST /video/getDetail（公开）。
// 请求体：{id}。
func (h *VideoHandler) GetDetail(c *gin.Context) {
	var req GetDetailRequest
	if !apierror.BindJSON(c, &req) {
		return
	}
	v, err := h.service.GetDetail(c.Request.Context(), req.ID)
	if err != nil {
		// ErrVideoNotFound 不在 apierror 通用映射表里，必须显式判断为 404。
		if errors.Is(err, ErrVideoNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, v)
}

// Delete 处理 POST /video/delete（受 JWT 保护）。
// 鉴权失败返回 403，视频不存在返回 404，其他错误由 apierror 兜底映射。
func (h *VideoHandler) Delete(c *gin.Context) {
	accountID, err := jwtmw.GetAccountID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	var req DeleteVideoRequest
	if !apierror.BindJSON(c, &req) {
		return
	}
	if err := h.service.Delete(c.Request.Context(), accountID, req.ID); err != nil {
		// 业务级 sentinel 单独处理：404 / 403
		// 其它错误交给 apierror 通用映射。
		switch {
		case errors.Is(err, ErrVideoNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, ErrVideoForbidden):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// uploadsDir 是上传文件落盘的根目录，由 /static 反代对外提供。
const uploadsDir = "./.run/uploads"

// allowedFileKind 决定保存目录与返回 URL 的前缀。
type allowedFileKind int

const (
	kindVideo allowedFileKind = iota
	kindCover
)

func (k allowedFileKind) subdir() string {
	if k == kindCover {
		return "covers"
	}
	return "videos"
}

func (k allowedFileKind) maxSize() int64 {
	if k == kindCover {
		return 10 << 20 // 10 MB
	}
	return 300 << 20 // 300 MB
}

func (k allowedFileKind) acceptPrefix() []string {
	if k == kindCover {
		return []string{"image/jpeg", "image/png", "image/webp"}
	}
	return []string{"video/mp4", "video/"}
}

// saveUploadedFile 把 multipart 文件保存到 uploadsDir/<subdir>/<filename>。
// 返回值是前端可直接拼到 /static/... 的 URL 路径。
func saveUploadedFile(c *gin.Context, kind allowedFileKind) (string, error) {
	header, err := c.FormFile("file")
	if err != nil {
		return "", fmt.Errorf("missing form file: %w", err)
	}
	if header.Size > kind.maxSize() {
		return "", fmt.Errorf("file too large (max %d bytes)", kind.maxSize())
	}
	// 按扩展名 / content-type 双重过滤，避免上传任意类型。
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !hasAcceptedExt(ext, kind) {
		return "", fmt.Errorf("unsupported file extension %q", ext)
	}
	if len(header.Header.Get("Content-Type")) > 0 {
		ct := header.Header.Get("Content-Type")
		if !hasAcceptedContentType(ct, kind) {
			return "", fmt.Errorf("unsupported content type %q", ct)
		}
	}

	dir := filepath.Join(uploadsDir, kind.subdir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create upload dir: %w", err)
	}

	dst := filepath.Join(dir, filepath.Base(header.Filename))
	// 防目录穿越：filepath.Base 之后文件只能落在 dir 内。
	if !strings.HasPrefix(filepath.Clean(dst), filepath.Clean(dir)+string(os.PathSeparator)) &&
		filepath.Clean(dst) != filepath.Clean(dir) {
		return "", fmt.Errorf("invalid filename")
	}

	out, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	src, err := header.Open()
	if err != nil {
		return "", fmt.Errorf("open uploaded file: %w", err)
	}
	defer src.Close()

	if _, err := io.Copy(out, src); err != nil {
		return "", fmt.Errorf("save uploaded file: %w", err)
	}
	// 返回 URL 路径，前端用 `${API_BASE}/static/<subdir>/<filename>` 访问。
	return fmt.Sprintf("/static/%s/%s", kind.subdir(), filepath.Base(header.Filename)), nil
}

func hasAcceptedExt(ext string, kind allowedFileKind) bool {
	switch kind {
	case kindCover:
		return ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp"
	default:
		return ext == ".mp4" || ext == ".mov" || ext == ".webm" || ext == ".mkv"
	}
}

func hasAcceptedContentType(ct string, kind allowedFileKind) bool {
	for _, p := range kind.acceptPrefix() {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	return false
}

// UploadVideo 处理 POST /video/uploadVideo（受 JWT 保护）。
// 接收 multipart file 字段，存到 .run/uploads/videos/，返回 URL。
func (h *VideoHandler) UploadVideo(c *gin.Context) {
	url, err := saveUploadedFile(c, kindVideo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url})
}

// UploadCover 处理 POST /video/uploadCover（受 JWT 保护）。
// 接收 multipart file 字段，存到 .run/uploads/covers/，返回 URL。
func (h *VideoHandler) UploadCover(c *gin.Context) {
	url, err := saveUploadedFile(c, kindCover)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": url})
}
