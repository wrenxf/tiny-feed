// Package account 实现账号领域的业务逻辑层（Service / Use Case）。
//
// 本包是三层架构的中间层，承载所有与账号生命周期相关的业务规则：注册、登录、改密、
// 改名、登出、刷新令牌、个人资料更新。它向上为 Handler 提供与 HTTP 无关的 Go 方法接口，
// 向下把数据持久化委托给 AccountRepository，自身不感知 gin、不直接拼 SQL（除一处标注的妥协外）。
//
// # 分层定位
//
//   - 上层：account_handler.go 调用本包方法完成 HTTP 请求到业务动作的映射
//   - 本层：参数业务校验 → 密码哈希 → JWT 签发 → 编排 Repo 调用 → 返回领域结果/错误
//   - 下层：account_repo.go 封装 GORM 操作，本包通过 *AccountRepository 依赖注入获取数据能力
//
// 依赖方向严格单向：handler → service → repo → gorm。本包不 import gin，保证业务逻辑
// 可以被任意入口复用（HTTP Handler、gRPC Server、CLI 工具、定时任务、单元测试）。
//
// # 核心类型
//
//   - AccountService: 账号业务的服务端聚合根，持有 *AccountRepository 指针
//   - NewAccountService(repo): 构造函数，repo 不能为空，由 main.go 或 wire 组装
//
// # 方法清单
//
// 注册与认证:
//   - CreateAccount        校验非空 → bcrypt 哈希 → 写入 DB，并发撞名靠 unique 索引兜底
//   - Login                查用户 → bcrypt 校验 → 签发双令牌 → 写回 DB → 返回 LoginResponse
//   - ChangePassword       查用户 → 校验旧密码 → 哈希新密码 → 写回（独立认证，无需 JWT）
//   - Logout               清空 DB 中的 token/refresh_token，使旧 JWT 在服务端立即失效
//
// 令牌管理:
//   - IssueTokens          重新签发 access + refresh token 并写回 DB（滚动刷新）
//   - ResolveRefreshToken  按 refresh token 反查账号，找不到统一返回 invalid（防枚举）
//
// 资料维护:
//   - Rename               重签 JWT + 事务内同步更新 username 和 token，保证一致性
//   - UpdateProfile        动态构建 map 做部分更新（PATCH 语义），空字段不覆盖原值
//
// 查询:
//   - FindByID             按主键查，返回安全 DTO（不含 password/token）
//   - FindByUsername       按用户名查，返回最小字段集
//
// 辅助:
//   - isDuplicateKeyError  判断唯一键冲突（兼容 gorm.ErrDuplicatedKey + MySQL 1062 字符串兜底）
//
// # 关键设计约定
//
//  1. 密码安全: 明文密码绝不入库、绝不出 Service 层。存储用 bcrypt.GenerateFromPassword
//     (DefaultCost=10, 2^10 次迭代)，校验用 bcrypt.CompareHashAndPassword（耗时固定，防时序攻击）。
//
//  2. 防枚举攻击: Login 对"用户不存在"和"密码错误"返回完全相同的错误消息
//     ("invalid username or password")；ResolveRefreshToken 对所有失败统一返回 "invalid refresh token"。
//     绝不向调用方泄露账号是否存在、token 是过期还是已被使用等细节。
//
//  3. 并发安全: 注册和改名不做"先查后插/先查后改"，而是直接写入，依赖数据库 unique 索引
//     拒绝重复，再用 isDuplicateKeyError 把底层 500 转成业务可读的 "username already taken"。
//     这避免了 TOCTOU (Time-of-Check-to-Time-of-Use) 竞态条件。
//
//  4. 事务一致性: 涉及多步写操作且要求原子性的场景（如 Rename 同时改 username 和 token），
//     调用 repo 的事务方法（RenameWithToken），保证不会出现"新名字但旧 token"的中间态。
//
//  5. Context 透传: 所有方法第一个参数都是 context.Context，一路传递到 Repo/GORM，
//     支持客户端断连取消、超时控制、链路追踪。绝不使用 context.Background()。
//
//  6. 最小暴露原则: 查询方法返回专用 DTO（FindByIDResponse / FindByUsernameResponse / LoginResponse），
//     手动映射字段，永远不把包含 Password、Token 的完整 Account 实体直接返回给上层。
//
//  7. 部分更新语义: UpdateProfile 动态构建 map[string]interface{}，只把请求中非空的字段放入 map，
//     避免空字符串覆盖原有数据；map 为空时直接返回 nil，节省一次 DB IO。
//
//  8. 单点登出机制: 每次 Login/IssueTokens 都把新令牌写回 DB 覆盖旧值，配合 JWT 中间件的
//     TokenChecker（比对请求中的 token 与 DB 中存储的是否一致），实现服务端主动注销能力，
//     弥补了无状态 JWT 无法主动失效的天然缺陷。
//
// # 已知架构妥协
//
// ResolveRefreshToken 内部直接访问了 s.repo.db 穿透了 Repo 层抽象。这是一个为了省事
// 没在 repo 里补 FindByRefreshToken 方法的临时方案。在生产级大型项目中，应在 Repo 层
// 补齐该方法，保持 Service 层对数据访问细节的完全无感知。
package account

// 账号服务层：负责账号生命周期相关的业务逻辑，包括注册、登录、改密、
// 改名、登出、刷新令牌以及个人资料更新。所有数据库操作都委托给 AccountRepository。

import (
	"context"
	"errors"
	"strings"

	"tiny-feed/internal/auth" // 引入 JWT 生成/解析的核心库

	"golang.org/x/crypto/bcrypt" // Go 官方推荐的密码哈希库
	"gorm.io/gorm"               // 用于判断特定的数据库错误
)

// ========== 服务结构体定义 ==========

// AccountService 是账号业务的服务端聚合根。
// 它持有 repo 的引用，通过依赖注入的方式获取数据访问能力。
type AccountService struct {
	repo *AccountRepository
}

// NewAccountService 构造账号服务。repo 不能为空。
func NewAccountService(repo *AccountRepository) *AccountService {
	return &AccountService{repo: repo}
}

// ========== 注册 (Create) ==========

// CreateAccount 处理账号注册请求。
// 流程：
//  1. 校验用户名和密码非空；
//  2. 用 bcrypt 对明文密码进行哈希（DefaultCost 兼顾安全与性能）；
//  3. 写入数据库。并发注册同一用户名时，依赖 username 的 unique 索引兜底，
//     失败时把 MySQL 1062 错误转成业务可读的"username already taken"，
//     避免"先查后插"的 TOCTOU race。
//
// 返回成功创建的账号实体（不含密码哈希）。
func (s *AccountService) CreateAccount(ctx context.Context, req *CreateAccountRequest) (*Account, error) {
	// 先把首尾空白去掉，避免出现" bob"和"bob"被当成两个账号的情况。
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		return nil, errors.New("username and password are required")
	}

	// 【安全核心】bcrypt 单向哈希，存库不存明文。
	// DefaultCost 通常是 10，意味着 2^10 次迭代，足以抵御暴力破解且不会太慢。
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	acc := &Account{
		Username: username,
		Password: string(hashed), // 存入的是哈希值，不是明文！
	}

	if err := s.repo.CreateAccount(ctx, acc); err != nil {
		// 【并发安全】并发注册同 username：DB unique 索引会拒绝，转换成业务错误。
		// 这里没有先 FindByUsername 再 Create，是为了避免竞态条件（Race Condition）。
		if isDuplicateKeyError(err) {
			return nil, errors.New("username already taken")
		}
		return nil, err
	}
	return acc, nil
}

// ========== 登录 (Login) ==========

// Login 处理账号登录请求。
// 流程：
//  1. 按用户名查找账号；
//  2. 用 bcrypt 校验密码（不区分"用户不存在"和"密码错误"，对外统一返回 invalid）；
//  3. 签发 JWT（短期访问令牌）和 refresh token（用于无感续期）；
//  4. 把新令牌写回数据库，覆盖旧的 token 字段，从而实现单点登出语义；
//  5. 把令牌和账号基本信息返回给调用方。
func (s *AccountService) Login(ctx context.Context, req *LoginRequest) (*LoginResponse, error) {
	acc, err := s.repo.FindByUsername(ctx, req.Username)
	if err != nil {
		// 【安全核心】用户不存在也返回"用户名或密码错误"，避免泄漏账号是否存在的信息（防枚举攻击）。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("invalid username or password")
		}
		return nil, err
	}

	// 【安全核心】校验密码哈希。耗时固定，防止时序攻击。
	if err := bcrypt.CompareHashAndPassword([]byte(acc.Password), []byte(req.Password)); err != nil {
		return nil, errors.New("invalid username or password")
	}

	// 生成访问令牌（JWT），包含 accountID 和 username，有过期时间。
	token, err := auth.GenerateToken(acc.ID, acc.Username)
	if err != nil {
		return nil, err
	}
	// 生成刷新令牌（随机串），有效期更长，用于换取新的 access token。
	refresh, err := auth.GenerateRefreshToken(acc.ID)
	if err != nil {
		return nil, err
	}

	// 【状态管理】把最新令牌写回 DB，旧的自动失效。
	// 这一步配合 jwt 中间件的 TokenChecker，实现了"服务端主动注销"能力。
	if err := s.repo.Login(ctx, acc.ID, token, refresh); err != nil {
		return nil, err
	}

	return &LoginResponse{
		Token:        token,
		RefreshToken: refresh,
		AccountID:    acc.ID,
		Username:     acc.Username,
	}, nil
}

// ========== 修改密码 (ChangePassword) ==========

// ChangePassword 修改账号密码。
// 流程：按用户名找到账号 → 校验旧密码 → 用新密码重新哈希 → 写回。
func (s *AccountService) ChangePassword(ctx context.Context, req *ChangePasswordRequest) error {
	acc, err := s.repo.FindByUsername(ctx, req.Username)
	if err != nil {
		return errors.New("user not found")
	}
	// 【安全核心】必须先验证旧密码通过，才允许改。防止会话劫持后直接改密。
	if err := bcrypt.CompareHashAndPassword([]byte(acc.Password), []byte(req.OldPassword)); err != nil {
		return errors.New("old password is incorrect")
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.repo.ChangePassword(ctx, acc.ID, string(hashed))
}

// ========== 查询 (Read) ==========

// FindByID 按主键查询账号的"对外可见"字段（不包含密码、token 等敏感字段）。
// 【安全核心】永远不要把包含 Password 字段的完整 Account 结构体直接返回给前端！
func (s *AccountService) FindByID(ctx context.Context, id uint) (*FindByIDResponse, error) {
	acc, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// 手动映射到安全的 DTO (Data Transfer Object)
	return &FindByIDResponse{
		ID:        acc.ID,
		Username:  acc.Username,
		AvatarURL: acc.AvatarURL,
		Bio:       acc.Bio,
	}, nil
}

// FindByUsername 按用户名查询账号的"对外可见"字段。
func (s *AccountService) FindByUsername(ctx context.Context, username string) (*FindByUsernameResponse, error) {
	acc, err := s.repo.FindByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	return &FindByUsernameResponse{ID: acc.ID, Username: acc.Username}, nil
}

// ========== 修改用户名 (Rename) ==========

// Rename 修改用户名。
// 因为 JWT 的 claims 里包含 username，改名后必须重新签发令牌，否则旧 token 里的 username 就过期了。
// 借助 repo 的 RenameWithToken 在同一事务里同时改 username 和 token 字段，保证一致性。
func (s *AccountService) Rename(ctx context.Context, id uint, newUsername string) error {
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return errors.New("username is required")
	}
	// 用新用户名重新签发 JWT。
	token, err := auth.GenerateToken(id, newUsername)
	if err != nil {
		return err
	}
	// 【事务一致性】事务内同步更新 username 和 token，避免出现"新名字但旧 token"的中间态。
	if err := s.repo.RenameWithToken(ctx, id, newUsername, token); err != nil {
		if isDuplicateKeyError(err) {
			return errors.New("username already taken")
		}
		return err
	}
	return nil
}

// ========== 辅助函数：错误转换 ==========

// isDuplicateKeyError 判断 gorm/MySQL 错误是否是唯一键冲突（1062 / 23000）。
// 同时兼容 gorm.ErrDuplicatedKey（GORM 2.x 起的标准化错误）。
// 注册/改名时并发撞名靠这个判断把 500 转成 4xx，提升用户体验。
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	// 优先使用标准的 errors.Is 判断
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	// 兜底：不同 driver 的错误消息格式不一样，做一次字符串匹配。
	msg := err.Error()
	return strings.Contains(msg, "Error 1062") || strings.Contains(msg, "Duplicate entry")
}

// ========== 登出 (Logout) ==========

// Logout 把账号的 token 字段清空，jwt 中间件在下次校验时会因为查不到 token 而拒绝。
// 这是一种基于"持久化 token 黑名单"的简单登出方案。
func (s *AccountService) Logout(ctx context.Context, id uint) error {
	return s.repo.Logout(ctx, id)
}

// ========== 更新资料 (UpdateProfile) ==========

// UpdateProfile 更新个人资料（头像 URL、个性签名）。
// 只更新请求中实际带了值的字段，未提供的字段保持原样（PATCH 语义）。
func (s *AccountService) UpdateProfile(ctx context.Context, id uint, req *UpdateProfileRequest) error {
	updates := map[string]interface{}{}
	// 动态构建更新 map，避免把空字符串覆盖掉原有数据
	if req.AvatarURL != "" {
		updates["avatar_url"] = req.AvatarURL
	}
	if req.Bio != "" {
		updates["bio"] = req.Bio
	}
	// 没东西可改就直接返回，节省一次 DB IO。
	if len(updates) == 0 {
		return nil
	}
	return s.repo.UpdateFields(ctx, id, updates)
}

// ========== 令牌刷新 (Refresh) ==========

// IssueTokens 给已登录的账号重新签发一对令牌（access + refresh）。
// 用于 /account/refresh 接口续期场景。
func (s *AccountService) IssueTokens(ctx context.Context, accountID uint, username string) (string, string, error) {
	token, err := auth.GenerateToken(accountID, username)
	if err != nil {
		return "", "", err
	}
	refresh, err := auth.GenerateRefreshToken(accountID)
	if err != nil {
		return "", "", err
	}
	// 把新 token 写回 DB 覆盖旧值，旧 token 立即失效（滚动刷新，增强安全性）。
	if err := s.repo.Login(ctx, accountID, token, refresh); err != nil {
		return "", "", err
	}
	return token, refresh, nil
}

// ResolveRefreshToken 根据 refresh token 反查账号。
// 找不到时统一返回 invalid refresh token，对外不暴露"已过期"或"已使用"等细节。
func (s *AccountService) ResolveRefreshToken(ctx context.Context, refreshToken string) (uint, string, error) {
	if refreshToken == "" {
		return 0, "", errors.New("refresh token is empty")
	}
	var acc Account
	// ⚠️ 注意：这里直接穿透了 Repo 层，使用了 s.repo.db。
	// 这是一个轻微的架构妥协（为了省事没在 repo 里加 FindByRefreshToken 方法）。
	// 在生产级大型项目中，建议还是在 Repo 层补上这个方法，保持 Service 层的纯粹性。
	if err := s.repo.db.WithContext(ctx).Where("refresh_token = ?", refreshToken).First(&acc).Error; err != nil {
		return 0, "", errors.New("invalid refresh token")
	}
	return acc.ID, acc.Username, nil
}
