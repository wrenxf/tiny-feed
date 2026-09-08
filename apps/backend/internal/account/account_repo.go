// Package account 实现账号领域的数据访问层（Repository / DAO）。
//
// 本包是三层架构中最底层的一层，唯一职责是封装对 accounts 表的所有 SQL 操作，
// 向上层（Service）提供与数据库无关的 Go 方法接口。
//
// # 分层定位
//
//   - 上层：account_service.go 调用本包的方法完成业务编排
//   - 本层：只负责 CRUD + 事务，不包含任何业务规则（如密码哈希、用户名校验、token 签发）
//   - 下层：gorm.io/gorm → database/sql → MySQL/PostgreSQL 驱动
//
// 依赖方向严格单向：service → repo → gorm。本包不 import gin、不 import service，
// 保证数据层可以被任意上层复用（HTTP Handler、gRPC Server、CLI 工具、单元测试 mock）。
//
// # 核心类型
//
//   - AccountRepository: 仓储结构体，持有 *gorm.DB 连接池实例
//   - NewAccountRepository(db): 构造函数，用于依赖注入
//   - Account: 实体结构体（定义在 account_entity.go），映射 accounts 表
//
// # 方法清单
//
// 创建 (Create):
//   - CreateAccount      插入新用户，GORM 自动回填自增主键 ID
//
// 更新 (Update):
//   - Rename             单字段更新用户名，RowsAffected==0 时返回 ErrRecordNotFound
//   - RenameWithToken    原子性同时更新用户名和 token（事务保证，改名后强制重登录）
//   - ChangePassword     更新密码字段（传入值应已哈希，Repo 不负责加密）
//   - UpdateAvatar       更新头像 URL
//   - UpdateToken        单独更新 access token（刷新令牌场景）
//   - UpdateFields       批量更新多个字段，接收 map[string]interface{}，用 Updates(复数)
//   - Login              登录成功后写入 access token + refresh token
//   - Logout             清空 token 和 refresh_token，使旧 JWT 在服务端立即失效
//
// 查询 (Read):
//   - FindByID           按主键查单个用户，找不到返回 gorm.ErrRecordNotFound
//   - FindByUsername     按用户名查（注册查重 / 登录验证）
//   - FindAll            查所有用户（仅管理后台用，生产环境慎用，大数据量会 OOM）
//
// # 关键设计约定
//
//  1. Context 透传: 每个方法第一个参数都是 context.Context，通过 db.WithContext(ctx)
//     传递给 GORM。当 HTTP 客户端断开或超时，GORM 会提前终止 SQL，节省 DB 资源。
//
//  2. 参数化查询: 所有 Where 条件都用 "field = ?", value 形式，杜绝 SQL 注入。
//
//  3. 错误语义: 数据库错误直接返回原始 error；"记录不存在"统一返回 gorm.ErrRecordNotFound，
//     由 Service 层通过 apierror.ClassifyHTTPStatus 映射成 404。
//
//  4. 事务边界: 涉及多步写操作且要求原子性的场景（如 RenameWithToken），
//     使用 db.Transaction(func(tx *gorm.DB) error{...})，闭包内必须用 tx 而非 ar.db，
//     返回 non-nil error 自动 ROLLBACK，返回 nil 自动 COMMIT。
//
//  5. 单字段 vs 多字段更新: 单字段用 Update("col", val)；多字段用 Updates(map) 或 Updates(struct)。
//     注意 Updates 传 struct 时会忽略零值字段，需要更新为零值时必须用 map 或 Select。
//
//  6. Repo 不做业务判断: 密码是否够强、用户名是否合法、token 是否过期——这些都属于 Service 层。
//     Repo 只回答"数据库层面这次操作成功了吗"。
package account

import (
	"context" // 用于控制请求超时、取消等生命周期

	"gorm.io/gorm" // GORM 核心库
)

// ========== 仓储结构体定义 ==========

// AccountRepository 封装了所有与 accounts 表相关的数据库操作。
// 它持有一个 *gorm.DB 实例，代表数据库连接池。
type AccountRepository struct {
	db *gorm.DB
}

// NewAccountRepository 是构造函数（依赖注入的标准写法）。
// 在 main.go 或 wire 中，会把全局的 db 实例传进来，创建一个 repo 实例。
func NewAccountRepository(db *gorm.DB) *AccountRepository {
	return &AccountRepository{db: db}
}

// ========== 创建操作 (Create) ==========

// CreateAccount 向数据库中插入一条新的用户记录。
// account 指针在插入成功后，GORM 会自动回填自增主键 ID 到 account.ID 中。
func (ar *AccountRepository) CreateAccount(ctx context.Context, account *Account) error {
	// WithContext(ctx) 将 HTTP 请求的上下文传递给 GORM，
	// 如果客户端断开连接或超时，GORM 会提前终止 SQL 执行，节省数据库资源。
	if err := ar.db.WithContext(ctx).Create(account).Error; err != nil {
		return err
	}
	return nil
}

// ========== 更新操作 (Update) ==========

// Rename 仅更新用户的用户名。
// 这是一个"单字段更新"的典型写法。
func (ar *AccountRepository) Rename(ctx context.Context, id uint, newUsername string) error {
	// Model(&Account{}) 告诉 GORM 要操作哪张表（即使不传具体对象，也要传类型让 GORM 推断表名）
	// Where("id = ?", id) 使用参数化查询，防止 SQL 注入
	// Update("username", newUsername) 只更新 username 这一个字段
	result := ar.db.WithContext(ctx).Model(&Account{}).Where("id = ?", id).Update("username", newUsername)

	// 检查是否有数据库层面的错误（如连接断开、语法错误）
	if result.Error != nil {
		return result.Error
	}
	// 【关键细节】RowsAffected == 0 表示 SQL 执行成功了，但没有匹配到任何行。
	// 说明传入的 id 在数据库中不存在。手动返回 ErrRecordNotFound 让上层知道。
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// RenameWithToken 同时更新用户名和 Token，并且保证原子性。
// 为什么需要这个方法？因为改名后通常需要强制用户重新登录（使旧 token 失效），
// 这两个操作必须要么全成功，要么全失败，不能出现"名字改了但 token 没更新"的中间状态。
func (ar *AccountRepository) RenameWithToken(ctx context.Context, id uint, newUsername string, token string) error {
	// Transaction 开启一个数据库事务
	return ar.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 注意：事务内部必须使用 tx 而不是 ar.db 来执行操作！

		// 第一步：更新用户名
		result := tx.Model(&Account{}).Where("id = ?", id).Update("username", newUsername)
		if result.Error != nil {
			return result.Error // 返回 error 会自动触发事务回滚 (ROLLBACK)
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound // 同样触发回滚
		}

		// 第二步：更新 token
		if err := tx.Model(&Account{}).Where("id = ?", id).Update("token", token).Error; err != nil {
			return err // 触发回滚
		}

		// 返回 nil 表示一切顺利，GORM 会自动提交事务 (COMMIT)
		return nil
	})
}

// ChangePassword 更新密码字段。
// 注意：传入的 newPassword 应该已经是哈希过的值，Repo 层不负责加密。
func (ar *AccountRepository) ChangePassword(ctx context.Context, id uint, newPassword string) error {
	if err := ar.db.WithContext(ctx).Model(&Account{}).Where("id = ?", id).Update("password", newPassword).Error; err != nil {
		return err
	}
	return nil
}

// UpdateAvatar 更新头像 URL。
func (ar *AccountRepository) UpdateAvatar(ctx context.Context, accountID uint, avatarURL string) error {
	return ar.db.WithContext(ctx).Model(&Account{}).Where("id = ?", accountID).Update("avatar_url", avatarURL).Error
}

// UpdateToken 单独更新 token 字段（常用于刷新 token 场景）。
func (ar *AccountRepository) UpdateToken(ctx context.Context, id uint, token string) error {
	return ar.db.WithContext(ctx).Model(&Account{}).Where("id = ?", id).Update("token", token).Error
}

// UpdateFields 批量更新多个字段。
// updates 是一个 map，例如 map[string]interface{}{"email": "a@b.com", "bio": "hello"}
// 使用 Updates (复数) 而不是 Update (单数)。
func (ar *AccountRepository) UpdateFields(ctx context.Context, id uint, updates map[string]interface{}) error {
	return ar.db.WithContext(ctx).Model(&Account{}).Where("id = ?", id).Updates(updates).Error
}

// Login 登录成功后，将生成的 access token 和 refresh token 存入数据库。
// 使用 map 一次性更新两个字段。
func (ar *AccountRepository) Login(ctx context.Context, id uint, token, refreshToken string) error {
	if err := ar.db.WithContext(ctx).Model(&Account{}).Where("id = ?", id).Updates(map[string]interface{}{
		"token":         token,
		"refresh_token": refreshToken,
	}).Error; err != nil {
		return err
	}
	return nil
}

// Logout 登出时，清空数据库中的 token 和 refresh_token。
// 这样即使客户端还持有旧的 JWT，中间件的 TokenChecker 也会因为 stored == "" 而拒绝请求。
func (ar *AccountRepository) Logout(ctx context.Context, id uint) error {
	if err := ar.db.WithContext(ctx).Model(&Account{}).Where("id = ?", id).Updates(map[string]interface{}{
		"token":         "",
		"refresh_token": "",
	}).Error; err != nil {
		return err
	}
	return nil
}

// ========== 查询操作 (Read) ==========

// FindByID 根据主键 ID 查找单个用户。
// First 方法默认按主键排序并取第一条。如果找不到，GORM 会返回 gorm.ErrRecordNotFound。
func (ar *AccountRepository) FindByID(ctx context.Context, id uint) (*Account, error) {
	var account Account
	if err := ar.db.WithContext(ctx).First(&account, id).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

// FindByUsername 根据用户名查找用户（常用于注册时检查用户名是否重复，或登录时验证）。
func (ar *AccountRepository) FindByUsername(ctx context.Context, username string) (*Account, error) {
	var account Account
	if err := ar.db.WithContext(ctx).Where("username = ?", username).First(&account).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

// FindAll 查询所有用户（通常用于管理员后台，生产环境慎用，数据量大时会 OOM）。
func (ar *AccountRepository) FindAll(ctx context.Context) ([]*Account, error) {
	var accounts []*Account
	if err := ar.db.WithContext(ctx).Find(&accounts).Error; err != nil {
		return nil, err
	}
	return accounts, nil
}
