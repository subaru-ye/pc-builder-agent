// Package db 嵌入版本化迁移文件(goose 编号迁移),
// 供 cmd/migrate 与数据库集成测试共用同一套迁移源。
package db

import "embed"

// Migrations db/migrations 下的全部编号迁移,按文件名顺序执行。
//
//go:embed migrations/*.sql
var Migrations embed.FS
