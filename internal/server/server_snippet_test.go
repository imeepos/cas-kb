package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/view"
)

// TestServeSearchSnippet T35/M4.2 验收:snippet=1 行内附带 snippet 字段、
// 缺省不带(契约不变,旧消费者零破坏)、仅字面 1 生效、带/不带 snippet 的
// 结果序列(path+score 等)完全一致(排序零变化)、响应逐字节确定。
func TestServeSearchSnippet(t *testing.T) {
	ts := newTestServer(t)

	// 基线:缺省响应不含 snippet 字段
	_, baseRaw := do(t, ts, http.MethodGet, "/api/v1/search?q=chan")
	if strings.Contains(string(baseRaw), "\"snippet\"") {
		t.Fatalf("缺省响应不得含 snippet 字段: %s", baseRaw)
	}
	var baseRows []view.SearchRow
	if err := json.Unmarshal(baseRaw, &baseRows); err != nil {
		t.Fatal(err)
	}
	if len(baseRows) != 1 || baseRows[0].Path != "go/concurrency/channel" {
		t.Fatalf("基线应命中 channel 1 条: %+v", baseRows)
	}

	// snippet=1:行内附带片段,命中词元以【】包裹
	_, snipRaw := do(t, ts, http.MethodGet, "/api/v1/search?q=chan&snippet=1")
	if !strings.Contains(string(snipRaw), "\"snippet\"") {
		t.Fatalf("snippet=1 应含 snippet 字段: %s", snipRaw)
	}
	var snipRows []view.SearchRow
	if err := json.Unmarshal(snipRaw, &snipRows); err != nil {
		t.Fatal(err)
	}
	if len(snipRows) != len(baseRows) {
		t.Fatalf("行数应一致: %d vs %d", len(snipRows), len(baseRows))
	}
	for i := range baseRows {
		b, s := baseRows[i], snipRows[i]
		if b.Path != s.Path || b.Slug != s.Slug || b.Addr != s.Addr || b.Title != s.Title ||
			b.Summary != s.Summary || b.Score != s.Score ||
			strings.Join(b.Tags, ",") != strings.Join(s.Tags, ",") {
			t.Fatalf("第 %d 行除 snippet 外应逐字段一致:\n基线 %+v\n片段 %+v", i, b, s)
		}
	}
	if !strings.Contains(snipRows[0].Snippet, "【chan】") {
		t.Fatalf("片段应标记命中词元: %q", snipRows[0].Snippet)
	}

	// 仅字面 1 生效:snippet=0 / snippet=true 均视为缺省(契约不变)
	for _, v := range []string{"0", "true"} {
		_, raw := do(t, ts, http.MethodGet, "/api/v1/search?q=chan&snippet="+v)
		if strings.Contains(string(raw), "\"snippet\"") {
			t.Fatalf("snippet=%s 不应启用片段: %s", v, raw)
		}
	}

	// 确定性:重复调用逐字节一致
	_, again := do(t, ts, http.MethodGet, "/api/v1/search?q=chan&snippet=1")
	if string(again) != string(snipRaw) {
		t.Fatal("snippet=1 响应应逐字节一致")
	}
}
