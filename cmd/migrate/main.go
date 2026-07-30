// P1 迁移 CLI:对 PG_DSN 指向的库执行 db/migrations 编号迁移(goose v3,嵌入二进制)。
// 运行方式(从仓库根):
//
//	go run ./cmd/migrate            # 等价于 up,应用全部未执行迁移
//	go run ./cmd/migrate up
//	go run ./cmd/migrate status     # 查看各迁移应用状态
//	go run ./cmd/migrate version    # 查看当前库版本
package main

import (
	"database/sql"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql 驱动注册(driver name: pgx)
	"github.com/pressly/goose/v3"

	"github.com/subaru-ye/pc-builder-agent/db"
	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
)

func main() {
	dotenv.Load(".env")

	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		log.Fatal("PG_DSN 未设置:复制 .env.example 为 .env,或显式导出 PG_DSN")
	}

	cmd := "up"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("设置 goose 方言失败: %v", err)
	}

	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatalf("打开数据库连接失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	switch cmd {
	case "up":
		err = goose.Up(conn, "migrations")
	case "status":
		err = goose.Status(conn, "migrations")
	case "version":
		err = goose.Version(conn, "migrations")
	default:
		log.Fatalf("未知子命令 %q,支持: up | status | version", cmd)
	}
	if err != nil {
		log.Fatalf("执行 %s 失败: %v", cmd, err)
	}
}
