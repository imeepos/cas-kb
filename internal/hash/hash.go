// Package hash 定义内容寻址地址:sha256 校验和 + 算法前缀。
package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// 算法前缀,为未来升级 BLAKE3 等留门。地址格式:algo + ":" + 小写 hex。
const (
	// PrefixSha256 是当前使用的哈希算法前缀。
	PrefixSha256 = "sha256:"
	// HexLen 是 sha256 十六进制小写串长度。
	HexLen = sha256.Size * 2
)

// Address 是内容寻址地址,形如 "sha256:<64位小写hex>"。
type Address string

// CheckAlg 校验地址的算法前缀,不匹配返回错误。
func CheckAlg(a Address) error {
	if len(a) <= len(PrefixSha256) || a[:len(PrefixSha256)] != PrefixSha256 {
		return fmt.Errorf("hash: 不支持的地址算法前缀: %q", a)
	}
	return nil
}

// Validate 校验地址格式合法(前缀 + 64 位小写 hex),且算法前缀匹配。
func Validate(a Address) error {
	if err := CheckAlg(a); err != nil {
		return err
	}
	hexPart := a[len(PrefixSha256):]
	if len(hexPart) != HexLen {
		return fmt.Errorf("hash: 地址 hex 长度 %d,期望 %d: %q", len(hexPart), HexLen, a)
	}
	for _, c := range hexPart {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return fmt.Errorf("hash: 地址含非法字符 %q: %q", string(c), a)
		}
	}
	return nil
}

// Sum 计算规范化字节的地址。addr = "sha256:" + hex(sha256(data))。
func Sum(data []byte) Address {
	sum := sha256.Sum256(data)
	return Address(PrefixSha256 + hex.EncodeToString(sum[:]))
}
