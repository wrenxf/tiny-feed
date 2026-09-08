// Package video 实现视频领域的业务逻辑层（Service / Use Case）。
//
// 本包是三层架构的中间层，承载所有与视频生命周期相关的业务规则：发布、查询详情、
// 按作者列表、删除（含 owner 鉴权）、点赞数绝对修正。它向上为 VideoHandler 提供
// 与 HTTP 无关的 Go 方法接口，向下把数据持久化委托给 VideoRepository，并在需要时
// 跨域调用 account.AccountRepository 补全作者头像信息。
//
// # 分层定位
//
//   - 上层：video_handler.go 调用本包方法完成 HTTP 请求到业务动作的映射
//   - 本层：参数业务校验 → 存在性判断 → owner 鉴权 → 编排 Repo 调用 → 返回领域结果/错误
//   - 下层：video_repo.go 封装 videos 表 CRUD；account 包的 AccountRepository 提供账号查询
//
// 依赖方向：handler → service → repo → gorm。本包不 import gin，保证业务逻辑可被任意入口复用。
// 跨域依赖仅允许 Service→Repo 方向（video.Service 可调用 account.Repo），禁止反向依赖。
//
// # 核心类型
//
//   - VideoService: 视频业务的服务端聚合根，持有 *VideoRepository 和 *account.AccountRepository
//   - NewVideoService(repo, accountRepo): 构造函数，accountRepo 可为 nil（降级为不带头像）
//
// # Sentinel Errors（哨兵错误）
//
//   - ErrVideoNotFound:  视频不存在，Handler 映射为 404
//   - ErrVideoForbidden: 非作者本人操作，Handler 映射为 403
//
// 这两个是包级变量，用 errors.Is() 精确匹配，避免字符串比较的脆弱性。
//
// # 方法清单
//
// 发布:
//   - Publish            trim 校验 title/play_url/cover_url 非空 → 写入 DB → 返回完整实体
//
// 查询:
//   - GetDetail          按 ID 查详情，NotFound 转 ErrVideoNotFound，补 author.avatar_url
//   - ListByAuthorID     按作者查列表（倒序，上限 200），批量补 avatar_url
//
// 删除:
//   - Delete             取视频 → 校验存在 → 校验 owner → 删除（非 owner 返回 ErrVideoForbidden）
//
// 维护:
//   - UpdateLikesCount   绝对值覆盖点赞数（后台修正用），先 IsExist 再写入
//
// # 关键设计约定
//
//  1. 跨域数据补全: Video 结构体的 AvatarURL 字段标记 gorm:"-"（不存表），
//     在 GetDetail / ListByAuthorID 中临时从 account 表查出填入响应。
//     accountRepo 查失败不致命——没头像时留空，前端走 fallback 默认头像。
//
//  2. 错误转换: Repo 返回的 gorm.ErrRecordNotFound 在本层统一转换为 ErrVideoNotFound，
//     Handler 通过 errors.Is(err, video.ErrVideoNotFound) 映射 HTTP 404，
//     避免 Handler 直接依赖 gorm 包。
//
//  3. Owner 鉴权: Delete 方法校验 v.AuthorID == accountID，不匹配返回 ErrVideoForbidden。
//     accountID 来自 JWT 中间件注入的 context，不可被客户端伪造。
//
//  4. 防御性编程: accountRepo 使用前做 nil 检查（s.accountRepo != nil），
//     允许在单元测试或轻量部署中传入 nil 跳过跨域查询。
//
//  5. 绝对更新 vs 相对增减: UpdateLikesCount 是绝对值覆盖（后台修正用），
//     高并发点赞/取消点赞应使用 LikeService 中的 ChangeLikesCount（原子增减）。
//     两者并发安全性级别完全不同，通过方法命名区分。
//
//  6. Context 透传: 所有方法第一个参数都是 context.Context，一路传递到 Repo/GORM，
//     支持客户端断连取消、超时控制、链路追踪。
//
//  7. 最小暴露原则: 返回的 Video 实体中 Password/Token 等敏感字段永远不会出现
//     （Video 表本身不含这些字段，天然安全）；AvatarURL 是运行时临时填充的展示字段。
package video

// 视频服务层：负责视频发布、查询、点赞数维护等业务逻辑。
// 不依赖 Redis、MQ 等中间件，所有操作都走 MySQL。

import (
	"context"
	"errors"
	"strings"

	"tiny-feed/internal/account"
	"tiny-feed/internal/apierror"

	"gorm.io/gorm"
)

// 业务级 sentinel error，handler 用来映射到具体的 HTTP 状态码。
var (
	ErrVideoNotFound  = errors.New("video not found")
	ErrVideoForbidden = errors.New("permission denied: not the owner")
)

// VideoService 视频业务的服务端。
type VideoService struct {
	repo        *VideoRepository
	accountRepo *account.AccountRepository
}

// NewVideoService 构造视频服务。
// accountRepo 用来在 GetDetail / ListByAuthorID 时给 video 补 author.avatar_url。
func NewVideoService(repo *VideoRepository, accountRepo *account.AccountRepository) *VideoService {
	return &VideoService{repo: repo, accountRepo: accountRepo}
}

// Publish 发布一条新视频。
// accountID/username 来自 JWT 中间件，代表发布者。
// 校验：title、play_url、cover_url 都必须非空。
// 写入数据库后返回完整实体（包含自动生成的 ID 和创建时间）。
func (s *VideoService) Publish(ctx context.Context, accountID uint, username string, req *PublishVideoRequest) (*Video, error) {
	// 三个核心字段做 trim 后的非空校验。
	title := strings.TrimSpace(req.Title)
	playURL := strings.TrimSpace(req.PlayURL)
	coverURL := strings.TrimSpace(req.CoverURL)
	if title == "" || playURL == "" || coverURL == "" {
		return nil, errors.New("title, play_url, cover_url are required")
	}
	v := &Video{
		AuthorID:    accountID,
		Username:    username,
		Title:       title,
		Description: req.Description,
		PlayURL:     playURL,
		CoverURL:    coverURL,
	}
	if err := s.repo.CreateVideo(ctx, v); err != nil {
		return nil, err
	}
	return v, nil
}

// ListByAuthorID 列出某作者的全部视频，按创建时间倒序，最多 200 条。
func (s *VideoService) ListByAuthorID(ctx context.Context, authorID int64) ([]Video, error) {
	if authorID <= 0 {
		return nil, apierror.RequireID(uint(authorID), "author_id")
	}
	videos, err := s.repo.ListByAuthorID(ctx, authorID)
	if err != nil {
		return nil, err
	}
	// 给每条 video 补 author.avatar_url，方便前端在用户主页直接显示真实头像。
	// accountRepo 查失败不致命，没头像时 AvatarURL 留空，前端走 fallback。
	if s.accountRepo != nil {
		if acc, err := s.accountRepo.FindByID(ctx, uint(authorID)); err == nil && acc != nil {
			for i := range videos {
				videos[i].AvatarURL = acc.AvatarURL
			}
		}
	}
	return videos, nil
}

// GetDetail 按 ID 取视频详情。
// 不存在时返回 ErrVideoNotFound。
func (s *VideoService) GetDetail(ctx context.Context, id uint) (*Video, error) {
	v, err := s.repo.GetByID(ctx, id)
	if err != nil {
		// 把 gorm 的 not-found 转成业务可读错误。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoNotFound
		}
		return nil, err
	}
	// 补 author.avatar_url——Video 结构体上的 AvatarURL 不存表，gorm:"-",
	// 这里是临时从 account 表拿出来填到响应里的。
	if s.accountRepo != nil {
		if acc, err := s.accountRepo.FindByID(ctx, v.AuthorID); err == nil && acc != nil {
			v.AvatarURL = acc.AvatarURL
		}
	}
	return v, nil
}

// Delete 删除视频。
// 鉴权规则：只有作者本人能删自己发布的视频，其他人调用一律拒绝（ErrVideoForbidden）。
// 流程：取视频 → 校验存在 → 校验 owner → 删除。
// 鉴权失败时不暴露视频是否存在与否（无论视频存不存在都先做 owner 比较，
// 避免通过 403/404 差异判断别人的视频 ID 是否有效）。
func (s *VideoService) Delete(ctx context.Context, accountID, videoID uint) error {
	if err := apierror.RequireID(videoID, "video_id"); err != nil {
		return err
	}
	v, err := s.repo.GetByID(ctx, videoID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrVideoNotFound
		}
		return err
	}
	// 鉴权：必须是作者本人。
	if v.AuthorID != accountID {
		return ErrVideoForbidden
	}
	return s.repo.DeleteVideo(ctx, videoID)
}

// UpdateLikesCount 强制设置某视频的点赞数（绝对值覆盖）。
// 注意：点赞/取消点赞的并发场景应使用 LikeService.Like / Unlike
// 中的相对增减接口（ChangeLikesCount），这个绝对值接口一般用于后台修正。
func (s *VideoService) UpdateLikesCount(ctx context.Context, id uint, likesCount int64) error {
	exists, err := s.repo.IsExist(ctx, id)
	if err != nil {
		return err
	}
	if !exists {
		return ErrVideoNotFound
	}
	return s.repo.UpdateLikesCount(ctx, id, likesCount)
}
