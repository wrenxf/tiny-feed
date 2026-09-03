package main

import (
	"log"
	"os"
	"tiny-feed/internal/config"
	"tiny-feed/internal/db"

	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("未找到.env文件,继续启动")
	}

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.yaml"
	}
	log.Printf("正在加载配置: %s", configPath)
	cfg, userDefault, err := config.LoadLocalDev(configPath)
	if err != nil {
		log.Fatalf("加载配置失败:%v", err)
	}
	if userDefault {
		log.Printf("配置文件%s不存在,使用默认本地配置", configPath)
	} else {
		log.Printf("已从文件加载配置:%s", configPath)
	}

	sqlDB, err := db.NewDB(cfg.Database)
	if err != nil {
		log.Fatalf("连接数据库失败:%v", err)
	}
	if err = db.AutoMigrate(sqlDB); err != nil {
		log.Fatalf("数据库迁移失败:%v", err)
	}
	defer db.CloseDB(sqlDB)

	log.Printf("服务已启动，监听端口：%d", cfg.Server.Port)
}
