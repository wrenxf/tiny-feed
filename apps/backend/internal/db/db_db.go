package db

import (
	"fmt"                        // 字符串格式化
	"tiny-feed/internal/account" // 账户模块的数据模型
	"tiny-feed/internal/config"  // 配置文件读取
	"tiny-feed/internal/social"  // 社交模块的数据模型（点赞、关注等）
	"tiny-feed/internal/video"   // 视频模块的数据模型

	"gorm.io/driver/mysql" // GORM 的 MySQL 驱动
	"gorm.io/gorm"         // GORM 框架核心
)

// NewDB 创建并返回一个 GORM 数据库连接实例
// 参数 dbcfg：从配置文件中读取的数据库连接参数
// 返回值：*gorm.DB 连接对象，以及可能的错误
func NewDB(dbcfg config.DatabaseConfig) (*gorm.DB, error) {
	// 按照 MySQL DSN 格式拼接连接字符串
	// 格式: 用户名:密码@tcp(主机:端口)/数据库名?参数
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		dbcfg.User,     // 数据库用户名
		dbcfg.Password, // 数据库密码
		dbcfg.Host,     // 数据库主机地址（如 127.0.0.1）
		dbcfg.Port,     // 数据库端口（如 3306）
		dbcfg.DBName,   // 数据库名称
	)

	// 使用 GORM 打开 MySQL 连接
	// mysql.Open(dsn) 创建底层驱动连接
	// &gorm.Config{} 传入 GORM 配置（此处使用默认配置）
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		// 如果连接失败，返回 nil 和错误信息
		return nil, err
	}

	// 连接成功，返回数据库连接对象
	return db, nil
}

// AutoMigrate 自动迁移（创建/更新）所有数据表
// 参数 db：已建立的数据库连接
// 返回值：可能的错误
//
// AutoMigrate 是 GORM 提供的"一键建表"功能：
// - 如果表不存在，会自动创建
// - 如果表已存在但缺少字段，会自动添加
// - 如果字段类型不匹配，会自动修改（有风险，生产环境慎用）
func AutoMigrate(db *gorm.DB) error {
	// 传入所有需要自动迁移的模型结构体指针
	// 这些结构体分别来自项目的不同业务模块
	return db.AutoMigrate(
		&account.Account{}, // 用户账户表
		&video.Video{},     // 视频信息表
		&video.Like{},      // 点赞记录表
		&video.Comment{},   // 评论表
		&social.Social{},   // 社交关系表（关注/粉丝等）
		&video.Tag{},       // 标签表
		&video.VideoTag{},  // 视频-标签关联表（多对多）
	)
}

// CloseDB 关闭数据库连接，释放资源
// 参数 db：已建立的数据库连接
// 返回值：可能的错误
func CloseDB(db *gorm.DB) error {
	// GORM 的 *gorm.DB 底层封装了 *sql.DB
	// 需要通过 db.DB() 获取底层的 *sql.DB 才能关闭
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	// 关闭数据库连接池
	return sqlDB.Close()
}
