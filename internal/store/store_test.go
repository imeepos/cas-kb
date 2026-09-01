package store

import (
	"context"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/testdb"
)

// openTest 打开一个独立测试库(每个用例独立新库;PostgreSQL,需 KB_TEST_DSN)。
func openTest(t *testing.T) *PG {
	ctx := context.Background()
	s, err := openPostgres(ctx, testdb.New(t))
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestM1_PutIdempotent(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	data := []byte("same content for idempotency test")
	a1, err := s.Put(ctx, object.KindBlob, data)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s.Put(ctx, object.KindBlob, data)
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 {
		t.Fatalf("重复 Put 地址不一致: %s != %s", a1, a2)
	}
	rows := 0
	err = s.List(ctx, func(info ObjectInfo) error {
		if info.Addr == a1 {
			rows++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("幂等 Put 后应只有一行,实际 %d", rows)
	}
}

func TestM1_GetRoundtripAndKind(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	want := []byte("byte-perfect roundtrip 内容")
	addr, err := s.Put(ctx, object.KindBlob, want)
	if err != nil {
		t.Fatal(err)
	}
	got, kind, err := s.Get(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("Get 字节不一致: got %q want %q", got, want)
	}
	if kind != object.KindBlob {
		t.Fatalf("kind 错误: got %s", kind)
	}
}

func TestM1_GetNotFound(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	addr := hash.Sum([]byte("definitely-not-stored-xyz"))
	if _, _, err := s.Get(ctx, addr); err != ErrNotFound {
		t.Fatalf("期望 ErrNotFound,got %v", err)
	}
}

func TestM1_TamperDetectable(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	addr, err := s.Put(ctx, object.KindBlob, []byte("original bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE objects SET data = $2 WHERE addr = $1",
		string(addr), []byte("tampered bytes!")); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Get(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if hash.Sum(got) == addr {
		t.Fatal("篡改后重算哈希应与地址不匹配")
	}
}

func TestM1_SchemaVersionMismatchRejects(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.New(t)
	s, err := openPostgres(ctx, dsn)
	if err != nil {
		t.Fatalf("打开存储失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.pool.Exec(ctx, "UPDATE meta SET value = '999' WHERE key = 'schema_version'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, "UPDATE meta SET value = '1' WHERE key = 'schema_version'")
	})
	if _, err := openPostgres(ctx, dsn); err == nil {
		t.Fatal("schema_version 不匹配时应拒绝打开")
	}
}
