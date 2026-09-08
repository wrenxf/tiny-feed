// Package video 实现视频领域的数据访问层（Repository / DAO）。
//
// 本包是三层架构中最底层的一层，唯一职责是封装对 videos 表的所有 SQL 操作，
// 向上层（VideoService）提供与数据库无关的 Go 方法接口。
//
// # 分层定位
//
//   - 上层：video_service.go 调用本包的方法完成业务编排
//   - 本层：只负责 CRUD + 聚合统计 + 原子计数器更新，不包含任何业务规则
//     （如发布审核、权限校验、推荐算法），那些属于 Service 层
//   - 下层：gorm.io/gorm → database/sql → MySQL/PostgreSQL 驱动
//
// 依赖方向严格单向：service → repo → gorm。本包不 import gin、不 import service，
// 保证数据层可以被任意上层复用（HTTP Handler、gRPC Server、CLI 工具、单元测试 mock）。
//
// # 核心类型
//
//   - VideoRepository: 仓储结构体，持有 *gorm.DB 连接池实例
//   - NewVideoRepository(db): 构造函数，用于依赖注入
//   - Video: 实体结构体（定义在 video_entity.go），映射 videos 表
//
// # 方法清单
//
// 创建 (Create):
//   - CreateVideo          插入新视频，GORM 自动回填自增主键 ID
//
// 删除 (Delete):
//   - DeleteVideo          按主键删除（配合软删除字段时为逻辑删除）
//
// 查询 (Read):
//   - GetByID              按主键查单个视频，找不到返回 gorm.ErrRecordNotFound
//   - ListByAuthorID       按作者查视频列表，按 create_time 倒序，上限 200 条
//   - IsExist              存在性检查，把 NotFound 转为 (false, nil)，仅真异常返回 error
//   - CountByAuthor        统计某作者的视频总数 (COUNT)
//   - TotalLikesByAuthor   统计某作者所有视频的点赞总和 (SUM + COALESCE)
//
// 更新 (Update):
//   - UpdateLikesCount     绝对设置点赞数（后台修正/定时同步用，非并发安全）
//   - ChangeLikesCount     原子增减点赞数，GREATEST 保证不低于 0（并发安全）
//   - UpdatePopularity     绝对设置热度值
//   - ChangePopularity     原子增减热度值，GREATEST 保证不低于 0（并发安全）
//
// # 关键设计约定
//
//  1. Context 透传: 每个方法第一个参数都是 context.Context，通过 db.WithContext(ctx)
//     传递给 GORM。当 HTTP 客户端断开或超时，GORM 会提前终止 SQL，节省 DB 资源。
//
//  2. 参数化查询: 所有 Where 条件都用 "field = ?", value 形式，杜绝 SQL 注入。
//
//  3. 两种更新模式:
//     - Update(col, val): 触发 Hook + 自动更新时间戳，用于常规业务字段修改
//     - UpdateColumn(col, val): 跳过 Hook 和时间戳，纯 SQL 执行，用于高频计数器操作
//     记忆口诀：改业务数据用 Update，改计数器用 UpdateColumn
//
//  4. 原子计数器: ChangeLikesCount / ChangePopularity 使用 gorm.Expr("GREATEST(col + ?, 0)", change)
//     在数据库层面做下限保护，避免并发减赞/减热度导致负数。
//     绝不在 Go 代码里先读后写——那有 TOCTOU 竞态条件。
//
//  5. 聚合查询: COUNT 用 .Count(&count)；SUM/AVG 等用 .Select("COALESCE(..., 0)").Scan(&var)。
//     永远用 COALESCE 包裹聚合结果，防止无匹配行时 SQL NULL 导致 Go scan 报错。
//
//  6. 存在性检查语义: IsExist 把 gorm.ErrRecordNotFound 转换为 (false, nil)，
//     只有真正的数据库错误才返回 non-nil error。调用方无需二次 errors.Is 判断。
//
//  7. 命名语义: UpdateXxx = 绝对赋值（覆盖式写入），ChangeXxx = 相对增减（原子操作）。
//     通过命名区分两种完全不同的并发安全性级别。
//
//  8. 错误原样返回: Repo 不做 HTTP 状态码映射，不做业务错误包装，
//     所有错误分类和转换由 Service 层的 apierror.ClassifyHTTPStatus 统一处理。
package video

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// VideoRepository 封装了所有与 videos 表相关的数据库操作。
// 持有 *gorm.DB 实例，代表数据库连接池。
type VideoRepository struct {
	db *gorm.DB
}

// NewVideoRepository 是构造函数（依赖注入的标准写法）。
// 在 main.go 或 wire 中传入全局 db 实例，创建 repo 实例。
func NewVideoRepository(db *gorm.DB) *VideoRepository {
	return &VideoRepository{db: db}
}

// ========== 创建 (Create) ==========

// CreateVideo 向数据库中插入一条新的视频记录。
// video 指针在插入成功后，GORM 会自动回填自增主键 ID 到 video.ID 中。
func (vr *VideoRepository) CreateVideo(ctx context.Context, video *Video) error {
	if err := vr.db.WithContext(ctx).Create(video).Error; err != nil {
		return err
	}
	return nil
}

// ========== 删除 (Delete) ==========

// DeleteVideo 根据主键 ID 删除视频记录。
// GORM 的 Delete 配合软删除字段（deleted_at）时执行 UPDATE SET deleted_at=NOW()，
// 没有软删除字段时才执行真正的 DELETE FROM。
func (vr *VideoRepository) DeleteVideo(ctx context.Context, id uint) error {
	if err := vr.db.WithContext(ctx).Delete(&Video{}, id).Error; err != nil {
		return err
	}
	return nil
}

// ========== 查询 (Read) ==========

// ListByAuthorID 查询某作者的视频列表，按创建时间倒序排列，最多返回 200 条。
// Limit(200) 是防止一次拉取过多数据导致 OOM 的安全上限；生产环境通常还需要分页参数。
func (vr *VideoRepository) ListByAuthorID(ctx context.Context, authorID int64) ([]Video, error) {
	var videos []Video
	if err := vr.db.WithContext(ctx).
		Where("author_id = ?", authorID).
		Order("create_time desc").
		Limit(200).
		Find(&videos).Error; err != nil {
		return nil, err
	}
	return videos, nil
}

// GetByID 根据主键 ID 查找单个视频。
// First 默认按主键排序取第一条；找不到时 GORM 返回 gorm.ErrRecordNotFound。
func (vr *VideoRepository) GetByID(ctx context.Context, id uint) (*Video, error) {
	var video Video
	if err := vr.db.WithContext(ctx).First(&video, id).Error; err != nil {
		return nil, err
	}
	return &video, nil
}

// IsExist 检查指定 ID 的视频是否存在。
// 这是一个"存在性检查"的惯用写法：把 gorm.ErrRecordNotFound 转换为 bool false + nil error，
// 只有真正的数据库错误才返回 non-nil error。调用方不需要处理 NotFound 异常。
func (vr *VideoRepository) IsExist(ctx context.Context, id uint) (bool, error) {
	var video Video
	if err := vr.db.WithContext(ctx).First(&video, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil // 不存在不是错误，是正常的业务状态
		}
		return false, err // 连接断开、SQL 语法错误等才是真正的异常
	}
	return true, nil
}

// CountByAuthor 统计某作者的视频总数。
// SELECT COUNT(*) FROM videos WHERE author_id = ?
func (vr *VideoRepository) CountByAuthor(ctx context.Context, authorID uint) (int64, error) {
	var count int64
	if err := vr.db.WithContext(ctx).Model(&Video{}).Where("author_id = ?", authorID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// TotalLikesByAuthor 统计某作者所有视频的点赞总和。
// SELECT COALESCE(SUM(likes_count), 0) FROM videos WHERE author_id = ?
// COALESCE(..., 0) 确保该作者没有任何视频时返回 0 而非 NULL，避免 Go 侧 scan 报错。
func (vr *VideoRepository) TotalLikesByAuthor(ctx context.Context, authorID uint) (int64, error) {
	var total int64
	if err := vr.db.WithContext(ctx).
		Model(&Video{}).
		Where("author_id = ?", authorID).
		Select("COALESCE(SUM(likes_count), 0)").
		Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// ========== 更新 (Update) ==========

// UpdateLikesCount 直接设置视频的点赞数为指定值（绝对更新）。
// 适用于后台管理修正数据、定时任务同步计数器等场景。
// 注意：这是覆盖式写入，并发调用会互相覆盖，不适合高并发点赞/取消点赞。
func (vr *VideoRepository) UpdateLikesCount(ctx context.Context, id uint, likesCount int64) error {
	if err := vr.db.WithContext(ctx).Model(&Video{}).
		Where("id = ?", id).
		Update("likes_count", likesCount).Error; err != nil {
		return err
	}
	return nil
}

// ChangeLikesCount 原子性地增减视频点赞数（相对更新），且保证不低于 0。
// GREATEST(likes_count + ?, 0) 在数据库层面做下限保护，避免并发减赞导致负数。
// 使用 UpdateColumn 而非 Update：UpdateColumn 跳过 GORM 的 hook 和自动更新时间戳，
// 对于高频计数器操作性能更好。
func (vr *VideoRepository) ChangeLikesCount(ctx context.Context, id uint, change int64) error {
	if err := vr.db.WithContext(ctx).Model(&Video{}).
		Where("id = ?", id).
		UpdateColumn("likes_count", gorm.Expr("GREATEST(likes_count + ?, 0)", change)).Error; err != nil {
		return err
	}
	return nil
}

// UpdatePopularity 直接设置视频热度为指定值（绝对更新）。
func (vr *VideoRepository) UpdatePopularity(ctx context.Context, id uint, popularity int64) error {
	if err := vr.db.WithContext(ctx).Model(&Video{}).
		Where("id = ?", id).
		Update("popularity", popularity).Error; err != nil {
		return err
	}
	return nil
}

// ChangePopularity 原子性地增减视频热度，且保证不低于 0。
// 与 ChangeLikesCount 同理，使用 GREATEST + UpdateColumn 实现安全的并发计数器更新。
func (vr *VideoRepository) ChangePopularity(ctx context.Context, id uint, change int64) error {
	if err := vr.db.WithContext(ctx).Model(&Video{}).
		Where("id = ?", id).
		UpdateColumn("popularity", gorm.Expr("GREATEST(popularity + ?, 0)", change)).Error; err != nil {
		return err
	}
	return nil
}
