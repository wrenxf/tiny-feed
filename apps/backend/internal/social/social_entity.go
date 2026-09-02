package social

// 导入 account 包，用于在返回粉丝/关注列表时，直接使用定义好的 Account 用户结构体
import "tiny-feed/internal/account"

// ==========================================
// 1. 核心数据库模型 (Entity/Model)
// ==========================================

// Social 代表数据库中的一条关注关系记录
// 它定义了 "social" 表的结构，用于记录 A 用户关注了 B 用户的关系
type Social struct {
	ID uint `gorm:"primaryKey"` // 主键 ID
	// 粉丝ID (关注者)。设置非空、普通索引（加速查询某人的所有关注），以及联合唯一索引（防止重复关注）
	FollowerID uint `gorm:"not null;index:idx_social_follower;uniqueIndex:idx_social_follower_vlogger"`
	// 博主ID (被关注者)。设置非空、普通索引（加速查询某人的所有粉丝），以及与上面相同的联合唯一索引
	VloggerID uint `gorm:"not null;index:idx_social_vlogger;uniqueIndex:idx_social_follower_vlogger"`
}

// ==========================================
// 2. API 请求结构体 (Requests)
// 用于接收前端发送的 JSON 格式数据
// ==========================================

// FollowRequest 对应“关注用户”接口的请求参数
type FollowRequest struct {
	VloggerID uint `json:"vlogger_id"` // 前端发送的要关注的博主 ID
}

// UnfollowRequest 对应“取消关注”接口的请求参数
type UnfollowRequest struct {
	VloggerID uint `json:"vlogger_id"` // 前端发送的要取消关注的博主 ID
}

// GetAllFollowersRequest 对应“获取某人的粉丝列表”接口的请求参数
type GetAllFollowersRequest struct {
	VloggerID uint `json:"vlogger_id"` // 前端发送的、要查询粉丝的目标博主 ID
}

// GetAllVloggersRequest 对应“获取某人关注的列表（我关注了谁）”接口的请求参数
type GetAllVloggersRequest struct {
	FollowerID uint `json:"follower_id"` // 前端发送的、要查询关注列表的当前用户 ID
}

// ==========================================
// 3. API 响应结构体 (Responses)
// 用于向后端返回给前端的 JSON 格式数据
// ==========================================

// GetAllFollowersResponse 对应“获取粉丝列表”接口的返回数据
type GetAllFollowersResponse struct {
	Followers     []*account.Account `json:"followers"`      // 粉丝的用户信息列表（指针切片，复用了 account 包的定义）
	FollowerCount int64              `json:"follower_count"` // 粉丝总数
}

// GetAllVloggersResponse 对应“获取关注列表（关注了谁）”接口的返回数据
type GetAllVloggersResponse struct {
	Vloggers     []*account.Account `json:"vloggers"`      // 关注的用户列表
	VloggerCount int64              `json:"vlogger_count"` // 关注总数
}

// SocialCounts 用于展示个人主页上的社交数据统计（通常作为用户 Profile 数据的一部分返回）
type SocialCounts struct {
	FollowerCount int64 `json:"follower_count"` // 粉丝数
	VloggerCount  int64 `json:"vlogger_count"`  // 关注数
}
