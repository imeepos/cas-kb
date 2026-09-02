package store

import (
	"context"
	"errors"
	"testing"

	"github.com/imeepos/cas-kb/internal/testdb"
)

// MetaGet/MetaSet:库级 KV 读写(gc 保留水位等);键不存在返回 ErrNotFound。
func TestMetaGetSet(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, testdb.NewSQLite(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.MetaGet(ctx, "gc.keep_last"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("缺失键应 ErrNotFound: %v", err)
	}
	if err := st.MetaSet(ctx, "gc.keep_last", "50"); err != nil {
		t.Fatal(err)
	}
	if v, err := st.MetaGet(ctx, "gc.keep_last"); err != nil || v != "50" {
		t.Fatalf("读回不符: %q %v", v, err)
	}
	if err := st.MetaSet(ctx, "gc.keep_last", "0"); err != nil {
		t.Fatal(err)
	}
	if v, err := st.MetaGet(ctx, "gc.keep_last"); err != nil || v != "0" {
		t.Fatalf("覆盖写入不符: %q %v", v, err)
	}
	// 系统键不受影响(同一张表)
	if v, err := st.MetaGet(ctx, "schema_version"); err != nil || v == "" {
		t.Fatalf("schema_version 应可读: %q %v", v, err)
	}
}
