package video

import "regexp"

// ==========================================
// 1. 数据模型 (Models)
// ==========================================

// Tag 代表一个独立的标签（例如 "科技"、"音乐"）
type Tag struct {
	ID uint `gorm:"primaryKey" json:"id"`
	// Name 是标签的名称
	// uniqueIndex 保证数据库中标签名不重复
	// type:varchar(100) 限制标签最大长度为 100
	// not null 表示该字段不能为空
	Name string `gorm:"uniqueIndex;type:varchar(100);not null" json:"name"`
}

// VideoTag 用于记录视频和标签的多对多关联关系
type VideoTag struct {
	ID uint `gorm:"primaryKey"`
	// VideoID 是视频的 ID，加索引是为了加速查找“某个视频关联了哪些标签”
	VideoID uint `gorm:"index;not null"`
	// TagID 是标签的 ID，加索引是为了加速查找“某个标签被哪些视频使用”
	TagID uint `gorm:"index;not null"`
}

// ==========================================
// 2. 核心工具函数 (Utility)
// ==========================================

// tagRegex 是一个预编译的正则表达式，用于匹配以 # 开头的标签
// [\p{L}\p{N}_]+ 表示匹配一个或多个的 Unicode字母、数字或下划线
var tagRegex = regexp.MustCompile(`#([\p{L}\p{N}_]+)`)

// ExtractTags 从一段文本（如视频标题或描述）中提取出所有的标签
func ExtractTags(text string) []string {
	// 使用正则表达式在文本中查找所有匹配项
	matches := tagRegex.FindAllStringSubmatch(text, -1)

	// seen 是一个 map，用于记录已经提取过的标签，起到“去重”的作用
	seen := make(map[string]bool)
	// tags 用于存储最终去重后的标签列表
	var tags []string

	// 遍历所有匹配到的结果
	for _, m := range matches {
		// m[0] 是完整匹配（带 #），m[1] 是子匹配（不带 # 的纯标签名）
		tag := m[1]
		// 如果这个标签还没被提取过
		if !seen[tag] {
			seen[tag] = true         // 标记为已存在
			tags = append(tags, tag) // 加入到最终的标签列表中
		}
	}

	return tags
}
