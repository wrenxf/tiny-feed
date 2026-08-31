package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// tag 是 yaml 和 Go 结构体之间的"翻译官"
type Config struct {
	Server   ServerConfig   `yaml:"server"` //告诉 yaml 解析器"文件里的 server 段对应这个字段
	Database DatabaseConfig `yaml:"database"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
}

func Load(filename string) (Config, error) {
	data, err := os.ReadFile(filename) // ① 把整个 yaml 文件读进内存，得到 []byte
	if err != nil {
		return Config{}, fmt.Errorf("failed to read configs file:%w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil { //把 yaml 格式字节切片 `data`，解析填充到结构体变量`cfg`
		return Config{}, fmt.Errorf("parse configs %s: %w", filename, err)
	}
	//改 yaml 文件 → 部署时得把文件塞进镜像，麻烦且易错
	//改代码 → 不同环境不同代码，灾难
	//有了 ApplyEnvOverrides，部署方只需要：启动时传环境变量，配置自动变。代码和文件都保持不动。
	ApplyEnvOverrides(&cfg)
	return cfg, nil
}

// ApplyEnvOverrides 用于将系统环境变量中的配置覆盖到传入的配置结构体中。
// 这通常用于实现配置优先级（例如：环境变量 > 配置文件 > 默认值），
// 在容器化部署（如 Docker/K8s）或不同环境切换时非常实用。
func ApplyEnvOverrides(cfg *Config) {
	// 1. 空指针保护：如果传入的配置为 nil，直接返回，防止后续赋值导致 panic
	if cfg == nil {
		return
	}

	// 2. 覆盖服务端口配置
	// 注意：环境变量获取到的都是字符串，需要转换为整数
	if v := os.Getenv("SERVER_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
		// 提示：如果环境变量设置了非数字字符串（如 "abc"），Atoi 会报错，
		// 此时 err != nil，代码会静默跳过，保留原配置文件中的端口值。
	}

	// 3. 覆盖数据库相关配置
	if v := os.Getenv("MYSQL_HOST"); v != "" {
		cfg.Database.Host = v
	}

	// 覆盖数据库端口（同样需要安全的字符串转整数处理）
	if v := os.Getenv("MYSQL_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Database.Port = port
		}
	}

	if v := os.Getenv("MYSQL_USER"); v != "" {
		cfg.Database.User = v
	}

	// 4. 密码兼容处理（⚠️ 注意覆盖顺序）
	// 这里同时兼容了 Docker 官方镜像常用的 MYSQL_ROOT_PASSWORD 和自定义的 MYSQL_PASSWORD。
	// 因为 MYSQL_PASSWORD 的判断在后面，如果两个环境变量同时存在，
	// MYSQL_PASSWORD 会覆盖 MYSQL_ROOT_PASSWORD 的值。
	if v := os.Getenv("MYSQL_ROOT_PASSWORD"); v != "" {
		cfg.Database.Password = v
	}
	if v := os.Getenv("MYSQL_PASSWORD"); v != "" {
		cfg.Database.Password = v
	}

	if v := os.Getenv("MYSQL_DATABASE"); v != "" {
		cfg.Database.DBName = v
	}
}

// LoadLocalDev 提供“开发友好型”的配置文件加载机制。
// 它实现了优雅降级逻辑：当本地配置文件缺失时，不会导致程序崩溃，而是自动回退到默认配置。
// 返回值说明：
//   - Config: 成功加载的配置，或回退使用的默认配置。
//   - bool: 标志位。true 表示未找到配置文件，使用了默认配置；false 表示成功加载了指定文件。
//   - error: 仅在发生非“文件不存在”的致命错误（如格式解析错误、权限不足）时返回。
func LoadLocalDev(filename string) (Config, bool, error) {
	// 1. 尝试正常加载指定的配置文件
	cfg, err := Load(filename)
	if err == nil {
		// 加载成功，返回解析后的配置，标志位为 false（未使用默认配置），无错误
		return cfg, false, nil
	}

	// 2. 处理“文件不存在”的特殊情况
	// 使用 errors.Is 进行判断，以兼容底层错误被包装（Wrapping）的情况
	if errors.Is(err, os.ErrNotExist) {
		// 文件不存在属于预期内的情况（例如开发者刚 clone 项目，还未创建本地配置）
		// 此时返回默认本地配置，标志位为 true，无错误
		return DefaultLocalConfig(), true, nil
	}

	// 3. 处理其他致命错误
	// 如果错误不是“文件不存在”（如 YAML 语法错误、读取权限被拒绝等），
	// 则视为真正的系统级错误，返回空配置、false 标志位，并将原始错误抛给上层处理
	return Config{}, false, err
}

// DefaultLocalConfig 给"找不到配置文件"时兜底用。
// 默认连本地 MySQL（root/123456/feedsystem）。
func DefaultLocalConfig() Config {
	cfg := Config{
		Server: ServerConfig{
			Port: 8080,
		},
		Database: DatabaseConfig{
			Host:     "localhost",
			Port:     3306,
			User:     "root",
			Password: "123456",
			DBName:   "feedsystem",
		},
	}
	ApplyEnvOverrides(&cfg)
	return cfg
}
