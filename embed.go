// Package caskb 内嵌数据模型 DDL,保证迁移与 schema.sql 单一来源一致。
package caskb

import _ "embed"

// SchemaSQL 是数据库 DDL 权威来源(schema.sql)的字节内容。
//
//go:embed schema.sql
var SchemaSQL string
