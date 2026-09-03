package object

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
)

// 向量分量的规范二进制承载(M6-A,DESIGN §3/§7.3):
// JSON 浮点文本在不同实现间存在精度与格式歧义(指数形态、尾数零),
// 文本承载会破坏「同一逻辑对象跨平台逐字节一致」的根基。因此 vec 字段
// 采用二进制承载:全部 float32 分量按 little-endian 逐个拼接成字节串,
// 再整体 base64(StdEncoding)为一个 JSON 字符串。字节序固定 little-endian,
// 与 CPU 字节序无关;float32 为 IEEE 754 精确可逆表示,编解码零损失。

// EncodeVecBase64 把 float32 向量编码为规范 base64 字符串:
// 每个分量 4 字节 little-endian(IEEE 754 位型),顺序拼接后 StdEncoding。
func EncodeVecBase64(v []float32) string {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return base64.StdEncoding.EncodeToString(b)
}

// DecodeVecBase64 解析 EncodeVecBase64 的产物;长度不是 4 的倍数即报错。
func DecodeVecBase64(s string) ([]float32, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("object: 向量 base64 解码失败: %w", err)
	}
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("object: 向量字节长度 %d 不是 4 的倍数", len(b))
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out, nil
}
