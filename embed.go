// Package caskb 内嵌数据模型 DDL,保证迁移与 schema.sql 单一来源一致。
package caskb

import _ "embed"

// SchemaSQL 是 PostgreSQL DDL 权威来源(schema.sql)的字节内容。
//
//go:embed schema.sql
var SchemaSQL string

// SchemaSQLiteSQL 是 SQLite 后端 DDL(schema_sqlite.sql)的字节内容,
// 与 schema.sql 保持语义镜像(表/约束/播种/版本一致),两处同步演进。
//
//go:embed schema_sqlite.sql
var SchemaSQLiteSQL string
