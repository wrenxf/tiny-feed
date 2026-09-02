package video

import "time"

// ==========================================
// 1. 核心数据库模型 (Entity/Model)
// ==========================================

// Video 是数据库中 `videos` 表的实体结构体
// 它定义了一个视频的完整数据格式
type Video struct {
	ID          uint      `gorm:"primaryKey" json:"id"`                                                                                                              // 视频的唯一主键 ID
	AuthorID    uint      `gorm:"index;not null" json:"author_id"`                                                                                                   // 发布该视频的作者 ID (设置索引加速按作者查询)
	Username    string    `gorm:"type:varchar(255);not null" json:"username"`                                                                                        // 作者的用户名 (冗余字段，避免联表查询，加速列表展示)
	Title       string    `gorm:"type:varchar(255);not null" json:"title"`                                                                                           // 视频标题
	Description string    `gorm:"type:varchar(255);" json:"description,omitempty"`                                                                                   // 视频描述，omitempty 表示如果为空则不返回给前端
	PlayURL     string    `gorm:"type:varchar(255);not null" json:"play_url"`                                                                                        // 视频的实际播放地址
	CoverURL    string    `gorm:"type:varchar(255);not null" json:"cover_url"`                                                                                       // 视频封面图的地址
	CreateTime  time.Time `gorm:"autoCreateTime;index:idx_videos_create_time,sort:desc;index:idx_videos_popularity_time_id,priority:2,sort:desc" json:"create_time"` // 创建时间，自动创建，并建立了用于排序的索引
	LikesCount  int64     `gorm:"column:likes_count;not null;default:0;index:idx_videos_likes_count_id,priority:1,sort:desc" json:"likes_count"`                     // 点赞数，默认为 0，带有索引方便进行热度排序
	Popularity  int64     `gorm:"column:popularity;not null;default:0;index:idx_videos_popularity_time_id,priority:1,sort:desc" json:"popularity"`                   // 综合热度值，默认为 0，用于生成热门视频列表的排序依据

	// AvatarURL 是作者头像。
	// 注意：gorm:"-" 表示这个字段不需要存入 videos 表。
	// 它是由 Service 层在处理请求时，根据 AuthorID 去 account 服务查出来并填充进去的，
	// 这样前端展示视频卡片时就不用再单独发请求去查用户信息了。
	AvatarURL string `gorm:"-" json:"avatar_url,omitempty"`
}

// ==========================================
// 2. API 请求结构体 (Requests)
// 对应不同的前端接口请求，规定前端应该发什么格式的数据过来
// ==========================================

// PublishVideoRequest 对应“发布视频”接口的请求参数
type PublishVideoRequest struct {
	Title       string `json:"title"`       // 标题
	Description string `json:"description"` // 描述
	PlayURL     string `json:"play_url"`    // 上传后得到的视频播放地址
	CoverURL    string `json:"cover_url"`   // 上传后得到的视频封面地址
}

// DeleteVideoRequest 对应“删除视频”接口的请求参数
type DeleteVideoRequest struct {
	ID uint `json:"id"` // 需要删除的视频 ID
}

// ListByAuthorIDRequest 对应“获取某人发布的所有视频”接口的请求参数
type ListByAuthorIDRequest struct {
	AuthorID uint `json:"author_id"` // 目标作者的 ID
}

// GetDetailRequest 对应“获取视频详情”接口的请求参数
type GetDetailRequest struct {
	ID uint `json:"id"` // 需要查看详情的视频 ID
}

// UpdateLikesCountRequest 对应“更新视频点赞数”接口的请求参数
// (通常由点赞/取消点赞的业务逻辑层内部调用)
type UpdateLikesCountRequest struct {
	ID         uint  `json:"id"`          // 视频 ID
	LikesCount int64 `json:"likes_count"` // 最新的点赞总数
}
