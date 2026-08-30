# tiny-feed

视频 Feed 应用的极简实现：注册登录、发布视频、点赞评论、关注好友、5 种 Feed 流。
从 [feedsystem_video_go](https://github.com/LeoninCS/feedsystem_video_go) 精简而来，
**去掉了 Redis / RabbitMQ / pprof**，保留核心业务并保证正确性，是学习
Go + Gin + GORM 分层架构的绝佳实战样本。

## 功能特性

- **账号体系**：注册 / 登录 / 改密 / 改名 / 更新资料 / 登出即失效 / refresh token 续期
- **视频**：发布 / 上传（视频 ≤300MB，封面 ≤10MB）/ 删除（仅作者）/ 详情
- **互动**：点赞 / 取消点赞 / 是否已赞 / 我赞过的视频；评论发布 / 删除 / 列表
- **社交**：关注 / 取关 / 粉丝列表 / 关注列表 / 粉丝关注计数
- **Feed 流**：最新 / 点赞数 / 标签 / 我关注的 / 热度 5 种，全部游标分页
- **个人主页**：账号信息 + 视频数 + 累计获赞 + 粉丝数 + 关注数聚合

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.24 + Gin v1.11 + GORM v1.31 + MySQL 8.0 |
| 鉴权 | JWT（golang-jwt/v5）+ token 存库实现登出失效 |
| 配置 | yaml（出厂默认）+ 环境变量（部署覆盖） |
| 前端 | Vue 3.5 + TypeScript + Pinia + Vue Router + Vite |
| 部署 | Docker Compose（MySQL + 后端 + 前端 nginx） |

## 项目结构

```
.
├── docker-compose.yml          # 一键起 MySQL + 后端 + 前端
├── apps/
│   ├── backend/                # Go 后端
│   │   ├── cmd/main.go         # 入口：配置 → 连库 → AutoMigrate → 路由
│   │   ├── configs/config.yaml # 默认配置（env 可覆盖）
│   │   └── internal/
│   │       ├── config/         # 配置加载
│   │       ├── db/             # MySQL 连接 + 自动建表
│   │       ├── http/           # SetRouter 路由总入口 ★
│   │       ├── middleware/jwt/ # 鉴权中间件
│   │       ├── auth/           # JWT 签发
│   │       ├── apierror/       # 统一错误 → HTTP 状态码
│   │       ├── account/        # 账号（entity→repo→service→handler 范本）
│   │       ├── video/          # 视频 + 点赞 + 评论 + 标签
│   │       ├── social/         # 关注/粉丝
│   │       ├── profile/        # 主页聚合（跨表）
│   │       └── feed/           # 5 种 Feed
│   └── frontend/               # Vue 3 前端
│       └── src/
│           ├── api/            # 按模块封装的接口层（account/video/like/…）
│           ├── views/          # 页面
│           ├── stores/         # Pinia 状态
│           └── router/         # 路由守卫
```

## 架构设计

**四层结构**（每个业务模块统一遵循）：

```
handler（HTTP 参数绑定/响应） → service（业务逻辑） → repo（GORM 查询） → entity（模型）
```

三个值得品的设计点：

1. **JWT checker 回调解耦**：中间件不直接依赖 account 包，而是接收"查库比对 token"的回调函数——既打破 import cycle，又让"登出即失效"策略收敛在一处
2. **无 Redis 的点赞正确性**：`like` 表 `(video_id, account_id)` 联合唯一索引防重复点赞 + 一行原子 SQL 更新 `likes_count`，并发下也不出错
3. **查询驱动索引**：`video` 表上的 `(popularity, create_time)`、`(likes_count, create_time)` 复合索引分别支撑热度 / 点赞数 Feed

## 数据模型

7 张表，全部由 GORM AutoMigrate 自动创建：

| 表 | 关键设计 |
|----|---------|
| account | username 唯一；token/refresh_token 存库（登出失效的基础） |
| video | 冗余 username；复合索引支撑 Feed 排序 |
| like | (video_id, account_id) 联合唯一 → 防重复点赞 |
| comment | 冗余 username |
| social | 自引用（follower_id, vlogger_id）联合唯一，一张表管关注+粉丝 |
| tag / video_tag | 多对多中间表 |

## 快速开始

### 方式一：Docker Compose（推荐）

```bash
docker compose up -d --build
```

打开 http://localhost:8081。彻底重置数据：`docker compose down -v`。

### 方式二：本地启动

```sql
CREATE DATABASE feedsystem DEFAULT CHARSET utf8mb4;
```

```bash
# 终端 A：后端（默认连 localhost:3306 root/123456/feedsystem）
cd apps/backend
go mod download
go run ./cmd

# 终端 B：前端（Vite 开发模式，5173 端口，自动代理 /api 到 8080）
cd apps/frontend
npm ci
npm run dev
```

## 配置

`configs/config.yaml` 提供默认值，同名环境变量优先级更高：

| 环境变量 | 默认值 | 说明 |
|---------|--------|------|
| `SERVER_PORT` | 8080 | 后端监听端口 |
| `MYSQL_HOST` / `MYSQL_PORT` | localhost / 3306 | 数据库地址 |
| `MYSQL_USER` / `MYSQL_PASSWORD` | root / 123456 | 数据库账号 |
| `MYSQL_DATABASE` | feedsystem | 库名 |
| `JWT_SECRET` | please-change-me | ⚠️ 生产前务必改成自己的固定值 |

## API 一览（共 29 个业务接口）

鉴权：受保护接口带 `Authorization: Bearer <token>`；refresh 走 `X-Refresh-Token` 头。
错误统一返回 `{"error": "..."}`，状态码映射见 `internal/apierror`。

| 模块 | 接口 |
|------|------|
| 账号 | register / login / changePassword / findByID / findByUsername / getProfile / refresh（公开）；logout / rename / updateProfile（🔒） |
| 视频 | listByAuthorID / getDetail（公开）；publish / delete / uploadVideo / uploadCover（🔒） |
| 点赞 | like / unlike / isLiked / listMyLikedVideos（全部 🔒） |
| 评论 | listAll（公开）；publish / delete（🔒） |
| 关注 | getAllFollowers / getAllVloggers（公开）；follow / unfollow / getCounts（🔒） |
| Feed | listLatest / listLikesCount / listByTag（公开，可选登录）；listByFollowing / listByPopularity（🔒） |
| 其他 | GET /healthz（不依赖 MySQL 的探活）；GET /static/*（上传文件） |

