package video

import "time"

// ==========================================
// 1. 核心数据库模型 (Entity/Model)
// ==========================================

// Like 代表数据库中的一条点赞记录
// 它记录了哪个用户 (AccountID) 点赞了哪个视频 (VideoID)
type Like struct {
	// ID 是这条点赞记录的自增主键
	ID uint `gorm:"primaryKey" json:"id"`

	// VideoID 是被点赞的视频 ID
	// uniqueIndex:idx_like_video_account 与 AccountID 共同组成联合唯一索引
	// 保证同一个用户 (AccountID) 对同一个视频 (VideoID) 只能点赞一次，防止重复点赞
	// not null 表示该字段在数据库中不能为空
	VideoID uint `gorm:"uniqueIndex:idx_like_video_account;not null" json:"video_id"`

	// AccountID 是点赞用户的 ID
	// uniqueIndex:idx_like_video_account 与 VideoID 共同组成联合唯一索引
	// not null 表示该字段在数据库中不能为空
	AccountID uint `gorm:"uniqueIndex:idx_like_video_account;not null" json:"account_id"`

	// CreatedAt 记录点赞发生的时间
	// 如果使用了 GORM 的默认模型(gorm.Model)，这个字段会自动管理(创建时自动填充)，这里手动定义了
	CreatedAt time.Time `json:"created_at"`
}

// ==========================================
// 2. API 请求结构体 (Requests)
// 用于接收前端发送的 JSON 格式请求数据
// ==========================================

// LikeRequest 对应前端发起的“点赞视频”接口的请求参数
type LikeRequest struct {
	// VideoID 是前端传递的、需要被点赞的目标视频 ID
	// (当前操作的用户ID通常由后端从 Token 或 Cookie 中提取，不需要前端传)
	VideoID uint `json:"video_id"`
}
