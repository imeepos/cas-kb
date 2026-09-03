package store

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"

	"github.com/imeepos/cas-kb/internal/object"
)

// 存储透明压缩(压测结论的空间优化):索引类对象是库体积膨胀的唯一大头
// (2000 条逐条写入 6.68GB,历史索引版本占 95%+),且规范 JSON 重复率高。
// 编码: 前缀 0x01 + gzip 字节;未压缩的旧数据/小对象不带前缀原样存储。
// 语义: 地址、哈希、Get 返回值均基于**逻辑字节**(解压后),对上层完全透明;
// 备份/恢复/pull/transfer 走 Get/Put,自动获得压缩收益且格式可移植。
// 仅 SQLite 后端启用;PostgreSQL 依赖 TOAST 压缩,不重复处理。

const (
	// compressFlagGzip 标记其后为 gzip 字节流。索引对象规范编码以 '{' 开头,
	// 不会与该前缀冲突;未压缩旧行无前缀,按原样读取。
	compressFlagGzip byte = 0x01
	// compressMinSize 是启用压缩的最小逻辑字节数。
	// 64KB:小分片跳过压缩 CPU(单条写入热路径),大分片(热词/大语料)
	// 才吃压缩收益——压测调优结论(BestSpeed 下小片压缩 CPU 占比过高)。
	compressMinSize = 64 * 1024
)

// compressionDisabled 允许实验性关闭新写入的压缩(Get 始终能解压既有数据,
// 因此开关只影响新写入,对已压缩数据安全)。KB_COMPRESS=off 时关闭。
var compressionDisabled = os.Getenv("KB_COMPRESS") == "off"

// compressible 报告该类型是否参与压缩。
// M6-A 起 vecroot/vecshard 与 indexroot/indexshard 同待遇:同为高重复率
// 规范 JSON 的索引类对象,是库体积膨胀大头,压缩收益同源。
func compressible(kind object.Kind) bool {
	switch kind {
	case object.KindIndexRoot, object.KindIndexShard,
		object.KindVecRoot, object.KindVecShard:
		return true
	}
	return false
}

// encodeObjectData 写入前编码:可压缩类型且足够大时加前缀压缩,否则原样。
func encodeObjectData(kind object.Kind, data []byte) []byte {
	if compressionDisabled || !compressible(kind) || len(data) < compressMinSize {
		return data
	}
	var buf bytes.Buffer
	buf.WriteByte(compressFlagGzip)
	// BestSpeed:写热路径上的 CPU 优先;JSON 高重复文本仍有多倍收益
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return data
	}
	if _, err := zw.Write(data); err != nil {
		return data // 压缩失败(不应发生):退回原样
	}
	if err := zw.Close(); err != nil {
		return data
	}
	if buf.Len() >= len(data)+1 {
		return data // 收益为负:原样
	}
	return buf.Bytes()
}

// decodeObjectData 读取后解码:识别前缀则解压,否则原样返回。
func decodeObjectData(kind object.Kind, data []byte) ([]byte, error) {
	if !compressible(kind) || len(data) < 2 || data[0] != compressFlagGzip {
		return data, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(data[1:]))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	return out, nil
}
