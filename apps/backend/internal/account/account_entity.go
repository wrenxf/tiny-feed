package account

// ==========================================
// 1. 数据库模型 (Entity/Model)
// ==========================================

// Account 是用户账户的核心数据库实体结构体
// 它定义了数据库中 `accounts` 表的结构，对应 GORM 和 JSON 标签
type Account struct {
	ID           uint   `gorm:"primaryKey" json:"id"`                          // 主键 ID，自增
	Username     string `gorm:"unique" json:"username"`                        // 用户名，在数据库中设置为唯一约束
	Password     string `json:"-"`                                             // 密码，json:"-" 表示在序列化 JSON 时直接忽略该字段，确保安全
	Token        string `json:"-"`                                             // 访问令牌，不对外暴露
	RefreshToken string `json:"-"`                                             // 刷新令牌，不对外暴露
	AvatarURL    string `gorm:"type:varchar(512)" json:"avatar_url,omitempty"` // 头像图片的 URL 链接
	Bio          string `gorm:"type:varchar(255)" json:"bio,omitempty"`        // 用户个人简介，omitempty 表示如果为空字符串则不显示在 JSON 中
}

// ==========================================
// 2. API 请求结构体 (Requests)
// 用于接收前端发送的 JSON 格式的数据
// ==========================================

// CreateAccountRequest 用于接收前端发送的用户注册请求
type CreateAccountRequest struct {
	Username string `json:"username"` // 注册所需的用户名
	Password string `json:"password"` // 注册所需的密码
}

// RenameRequest 用于接收前端发送的修改用户名请求
type RenameRequest struct {
	NewUsername string `json:"new_username"` // 新的用户名
}

// FindByIDRequest 用于接收前端发送的根据 ID 查找用户的请求
type FindByIDRequest struct {
	ID uint `json:"id"` // 用户的唯一标识符 ID
}

// FindByUsernameRequest 用于接收前端发送的根据用户名查找用户的请求
type FindByUsernameRequest struct {
	Username string `json:"username"` // 目标用户名
}

// ChangePasswordRequest 用于接收前端发送的修改密码请求
type ChangePasswordRequest struct {
	Username    string `json:"username"`     // 当前用户名
	OldPassword string `json:"old_password"` // 旧密码
	NewPassword string `json:"new_password"` // 新密码
}

// LoginRequest 用于接收前端发送的登录请求
type LoginRequest struct {
	Username string `json:"username"` // 登录用户名
	Password string `json:"password"` // 登录密码
}

// UpdateProfileRequest 用于接收前端发送的更新个人资料（头像、简介）的请求
type UpdateProfileRequest struct {
	AvatarURL string `json:"avatar_url"` // 更新的头像 URL
	Bio       string `json:"bio"`        // 更新的简介内容
}

// RefreshRequest 用于接收前端发送的刷新 Token 的请求
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"` // 传入的刷新令牌
}

// GetProfileRequest 用于接收前端发送的获取个人主页信息的请求
type GetProfileRequest struct {
	AccountID uint `json:"account_id"` // 目标用户的 ID
}

// ==========================================
// 3. API 响应结构体 (Responses)
// 用于向后端返回给前端的 JSON 格式的数据
// ==========================================

// FindByIDResponse 返回根据 ID 查找到的用户基本信息
// 注意：这里绝对不包含密码等敏感字段，保证安全
type FindByIDResponse struct {
	ID        uint   `json:"id"`                   // 用户 ID
	Username  string `json:"username"`             // 用户名
	AvatarURL string `json:"avatar_url,omitempty"` // 头像 URL
	Bio       string `json:"bio,omitempty"`        // 个人简介
}

// FindByUsernameResponse 返回根据用户名查找到的用户基本信息
type FindByUsernameResponse struct {
	ID       uint   `json:"id"`       // 用户 ID
	Username string `json:"username"` // 用户名
}

// LoginResponse 返回登录成功后的凭证信息
type LoginResponse struct {
	Token        string `json:"token"`         // 访问令牌，用于后续接口鉴权
	RefreshToken string `json:"refresh_token"` // 刷新令牌，用于在 Token 过期时获取新 Token
	AccountID    uint   `json:"account_id"`    // 当前登录用户的 ID
	Username     string `json:"username"`      // 当前登录用户的用户名
}

// GetProfileResponse 返回用户个人主页的完整信息概览
type GetProfileResponse struct {
	Account       FindByIDResponse `json:"account"`        // 嵌套了用户的基础信息结构体
	VideoCount    int64            `json:"video_count"`    // 该用户发布的视频总数
	TotalLikes    int64            `json:"total_likes"`    // 该用户所有视频获得的总点赞数
	FollowerCount int64            `json:"follower_count"` // 该用户的粉丝数
	VloggerCount  int64            `json:"vlogger_count"`  // 该用户关注的人数
}
