package store

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/testdb"
)

// M6-A:schema v6 门禁——v5 库(schema_version=5)在实现升级到 6 后必须拒开,
// 并指引重建;不做自动迁移(同 M4 v5 先例)。
func TestVectorSchemaV6GateRejectsV5Library(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.NewSQLite(t)
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟 v5 存量库:版本行退回 5 后关闭,再打开必须被拒
	db := st.(*SQLite)
	if _, err := db.db.ExecContext(ctx, "UPDATE meta SET value = '5' WHERE key = 'schema_version'"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Open(ctx, dsn)
	if err == nil {
		t.Fatal("v5 旧库应被拒绝打开(不做自动迁移)")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("错误应指向 schema_version 不匹配: %v", err)
	}
	if !strings.Contains(err.Error(), "重新执行 kb init") {
		t.Fatalf("错误应指引重建: %v", err)
	}
}

// M6-A:vecshard/vecroot 与 index 同待遇走 gzip 透明压缩——
// 落库字节带 0x01 前缀(库文件级验证),Get 返回逻辑字节与写入逐字节一致,
// 解码往返无损(vecroot 固定 64 槽、体积恒小于阈值,走同一条 compressible
// 判定,行为与小的 indexroot 一致:不触发压缩,读回原样)。
func TestVectorShardGzipCompressionCompat(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.NewSQLite(t)
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// 构造 >64KB 的 vecshard:200 条 × 64 维(每条 base64 ≈ 344 字节)
	items := make([]object.VecItem, 0, 200)
	v64 := make([]float32, 64)
	for i := range v64 {
		v64[i] = float32(i) / 64
	}
	vec := object.EncodeVecBase64(v64)
	for i := 0; i < 200; i++ {
		items = append(items, object.VecItem{
			Path: "dir/note-" + strings.Repeat("p", 20) + string(rune('a'+i%26)),
			Vec:  vec,
		})
	}
	sh := &object.VecShard{Kind: object.KindVecShard, Model: "nomic-embed-text", Dim: 64, Items: items}
	logical, err := object.EncodeVecShard(sh)
	if err != nil {
		t.Fatal(err)
	}
	if len(logical) < compressMinSize {
		t.Fatalf("测试向量应超过压缩阈值: %d < %d", len(logical), compressMinSize)
	}
	addr, err := s.Put(ctx, object.KindVecShard, logical)
	if err != nil {
		t.Fatal(err)
	}
	got, kind, err := s.Get(ctx, addr)
	if err != nil || kind != object.KindVecShard || !bytes.Equal(got, logical) {
		t.Fatalf("压缩对象读回应与逻辑字节一致: kind=%s err=%v", kind, err)
	}
	back, err := object.DecodeVecShard(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Items) != 200 || back.Dim != 64 || back.Model != "nomic-embed-text" {
		t.Fatalf("压缩往返应无损: %+v", back)
	}
	rv, err := object.DecodeVecBase64(back.Items[0].Vec)
	if err != nil || len(rv) != 64 {
		t.Fatalf("向量解码往返失败: %v", err)
	}

	// 库文件级验证:裸读 objects.data 首字节应为 gzip 标志 0x01
	raw, err := sql.Open("sqlite", strings.TrimPrefix(strings.TrimPrefix(dsn, "sqlite:"), "file:"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var stored []byte
	if err := raw.QueryRowContext(ctx, "SELECT data FROM objects WHERE addr = ?", string(addr)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) < 2 || stored[0] != compressFlagGzip {
		t.Fatalf("大 vecshard 落库应带 gzip 前缀: 首字节=%v len=%d", stored[0], len(stored))
	}
	if len(stored) >= len(logical) {
		t.Fatalf("压缩应产生空间收益: stored=%d logical=%d", len(stored), len(logical))
	}
}
