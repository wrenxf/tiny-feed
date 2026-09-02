package video

import "time"

// ==========================================
// 1. 核心数据库模型 (Entity/Model)
// ==========================================

// Comment 代表数据库中的一条评论记录
type Comment struct {
	// ID 是这条评论的自增主键
	ID uint `gorm:"primaryKey" json:"id"`

	// Username 是发表评论的用户的昵称
	// 加上 index 索引是为了加速后续根据用户名查询评论的速度
	Username string `gorm:"index" json:"username"`

	// VideoID 是这条评论所属的视频 ID
	// 加上 index 索引是为了加速后续查询“某个视频下的所有评论”
	VideoID uint `gorm:"index" json:"video_id"`

	// AuthorID 是发表评论的用户的唯一 ID
	// 加上 index 索引是为了加速后续查询“某个用户发布的所有评论”
	AuthorID uint `gorm:"index" json:"author_id"`

	// Content 是评论的具体文本内容
	// type:text 告诉数据库将其存储为 TEXT 类型，以支持较长的评论内容
	Content string `gorm:"type:text" json:"content"`

	// CreatedAt 记录评论创建的时间
	// autoCreateTime 表示 GORM 会在执行 Create 操作时自动填充当前时间
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// ==========================================
// 2. API 请求结构体 (Requests)
// 用于接收前端发送的 JSON 格式请求数据
// ==========================================

// PublishCommentRequest 对应前端发起的“发布评论”接口的请求参数
type PublishCommentRequest struct {
	// VideoID 是前端传递的、要发表评论的目标视频 ID
	VideoID uint `json:"video_id"`
	// Content 是前端传递的评论文本内容
	Content string `json:"content"`
}

// DeleteCommentRequest 对应前端发起的“删除评论”接口的请求参数
type DeleteCommentRequest struct {
	// CommentID 是前端传递的、需要删除的目标评论 ID
	CommentID uint `json:"comment_id"`
}

// GetAllCommentsRequest 对应前端发起的“获取全部评论”接口的请求参数
type GetAllCommentsRequest struct {
	// VideoID 是前端传递的、要查询其所有评论的目标视频 ID
	VideoID uint `json:"video_id"`
}
