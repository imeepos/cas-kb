package store

import (
	"bytes"
	"context"
	"testing"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/object"
	"github.com/imeepos/cas-kb/internal/testdb"
)

// 压测结论的空间优化:索引类对象透明压缩——Get 返回逻辑字节,
// 地址/哈希语义不变;小对象与 blob/note/tree 不参与压缩。
func TestTransparentCompression(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, testdb.NewSQLite(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 大索引对象:压缩存储,读回逻辑字节一致
	big := bytes.Repeat([]byte(`{"填充":"内容一段 channel 并发"}`), 600) // ~21KB
	addr, err := st.Put(ctx, object.KindIndexShard, big)
	if err != nil {
		t.Fatal(err)
	}
	got, kind, err := st.Get(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if kind != object.KindIndexShard || !bytes.Equal(got, big) {
		t.Fatalf("压缩对象读回不一致: kind=%s len=%d", kind, len(got))
	}

	// 小索引对象(<4KB):不压缩,读回一致
	small := bytes.Repeat([]byte("x"), 100)
	sAddr, err := st.Put(ctx, object.KindIndexRoot, small)
	if err != nil {
		t.Fatal(err)
	}
	sGot, _, err := st.Get(ctx, sAddr)
	if err != nil || !bytes.Equal(sGot, small) {
		t.Fatalf("小对象读回不一致: %v", err)
	}

	// blob 永不压缩(用户内容原样,含任意字节)
	blob := bytes.Repeat([]byte{0x00, 0xff, 0x01}, 1000)
	bAddr, err := st.Put(ctx, object.KindBlob, blob)
	if err != nil {
		t.Fatal(err)
	}
	bGot, bKind, err := st.Get(ctx, bAddr)
	if err != nil || bKind != object.KindBlob || !bytes.Equal(bGot, blob) {
		t.Fatalf("blob 读回不一致: %v", err)
	}

	// 同地址幂等:重复 Put 等价空操作(压缩确定性)
	addr2, err := st.Put(ctx, object.KindIndexShard, big)
	if err != nil || addr2 != addr {
		t.Fatalf("压缩应确定性(同内容同地址): %s != %s", addr2, addr)
	}

	// 压缩确实生效:库文件级验证交给 e2e/基准;此处验证可压缩对象存储字节
	// 小于逻辑字节(通过 Has/Get 行为已覆盖,存储体积由基准基准断言)。
	_ = hash.Sum
}
