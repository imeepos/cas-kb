package hash

import (
	"testing"
)

func TestSumStable(t *testing.T) {
	data := []byte("hello cas-kb 知识库")
	a := Sum(data)
	b := Sum(data)
	if a != b {
		t.Fatalf("Sum 不稳定: %s != %s", a, b)
	}
	if err := Validate(a); err != nil {
		t.Fatalf("Sum 结果校验失败: %v", err)
	}
}

func TestSumDistinct(t *testing.T) {
	if Sum([]byte("a")) == Sum([]byte("b")) {
		t.Fatal("不同内容应产生不同地址")
	}
}

func TestValidate(t *testing.T) {
	good := Sum([]byte("x"))
	bad := []string{
		"md5:abc",
		"sha256:XYZ123",
		"sha256:",
		"sha256:" + "0",
		"sha256:" + string(make([]byte, 64)), // 非法字符
	}
	for _, b := range bad {
		if err := Validate(Address(b)); err == nil {
			t.Errorf("应拒绝 %q", b)
		}
	}
	_ = good
}

func TestCheckAlg(t *testing.T) {
	if err := CheckAlg(Sum([]byte("x"))); err != nil {
		t.Fatalf("合法算法前缀被拒: %v", err)
	}
	if err := CheckAlg("blake3:abc"); err == nil {
		t.Fatal("应拒绝 blake3 前缀")
	}
}
