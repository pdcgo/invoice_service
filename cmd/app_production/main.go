package main

import (
	"context"
	"os"

	"github.com/pdcgo/san_collection/san_caches"
	"github.com/pdcgo/san_collection/san_config"
	"github.com/pdcgo/shared/configs"
	"github.com/pdcgo/shared/db_connect"
	"github.com/pdcgo/shared/pkg/cloud_logging"
	"github.com/redis/go-redis/v9"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

func NewDatabase(cfg *configs.AppConfig) (*gorm.DB, error) {
	return db_connect.NewProductionDatabase("invoice_service", &cfg.Database)
}

func NewRedisDatabase(cfg *configs.AppConfig) *redis.Client {
	return db_connect.NewRedisDatabase(&cfg.RedisConfig)
}

func NewCacheManager(client *redis.Client) san_caches.CacheManager {
	return san_caches.NewRedisCacheManager(client)
}

func NewProjectConfig() *san_config.ProjectConfig {
	return &san_config.ProjectConfig{ProjectID: os.Getenv("GOOGLE_CLOUD_PROJECT")}
}

func NewApp(
	serviceApiFunc ServiceApiFunc,
	syncLegacyFunc SyncLegacyFunc,
) *cli.Command {
	return &cli.Command{
		Name:   "run",
		Action: cli.ActionFunc(serviceApiFunc),
		Commands: []*cli.Command{
			{
				Name:   "sync-legacy",
				Action: cli.ActionFunc(syncLegacyFunc),
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "host",
						Aliases: []string{"h"},
						Value:   "http://localhost:8080",
					},
					&cli.StringFlag{
						Name:    "username",
						Aliases: []string{"u"},
					},
				},
			},
		},
	}
}

func main() {
	if os.Getenv("DISABLE_CLOUD_LOGGING") == "" {
		cloud_logging.SetCloudLoggingDefault()
	}

	app, err := InitializeApp()
	if err != nil {
		panic(err)
	}

	err = app.Run(context.Background(), os.Args)
	if err != nil {
		panic(err)
	}
}
