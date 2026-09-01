package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/store"
)

// dumpWithHeaderVersion 生成源库备份并把 header 行的 schema_version 改为 v
// (模拟其他版本 kb 导出的备份;对象行与当前格式逐字节一致)。
func dumpWithHeaderVersion(t *testing.T, v int) []byte {
	t.Helper()
	ctx := context.Background()
	s, _ := freshStore(t)
	if err := s.ProjectCreate(ctx, "alpha", "演示项目"); err != nil {
		t.Fatal(err)
	}
	r := Open(s, Config{Project: "alpha", Now: func() int64 { return fixedTime }})
	if _, _, err := r.SetNote(ctx, "go/channel", NoteInput{Title: "Chan", Body: "cs"}, "add"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := DumpLibrary(ctx, s, &buf); err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitN(buf.Bytes(), []byte("\n"), 2)
	var h map[string]any
	if err := json.Unmarshal(lines[0], &h); err != nil {
		t.Fatal(err)
	}
	h["schema_version"] = v
	nh, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	return append(append(nh, '\n'), lines[1]...)
}

// M4 复盘:跨版本恢复——v4 备份(对象编码与当前兼容)可恢复并记录来源版本;
// v3 及更旧(对象编码不兼容)与比当前更新的版本拒绝并给出可行动提示。
func TestRestoreCrossVersion(t *testing.T) {
	ctx := context.Background()

	// 1) v4 备份:恢复成功,记录来源版本,数据可读
	s4, _ := freshStore(t)
	stats, err := RestoreLibrary(ctx, s4, bytes.NewReader(dumpWithHeaderVersion(t, 4)), true)
	if err != nil {
		t.Fatalf("v4 备份应可恢复: %v", err)
	}
	if stats.FromSchemaVersion != 4 {
		t.Fatalf("应记录来源版本 4: %+v", stats)
	}
	r4 := Open(s4, Config{Project: "alpha"})
	ref, err := r4.Note(ctx, "go/channel")
	if err != nil || ref.Note.Meta.Title != "Chan" {
		t.Fatalf("恢复后数据应可读: %v %+v", err, ref)
	}

	// 2) v3 备份:对象编码不兼容,拒绝
	old, _ := freshStore(t)
	if _, err := RestoreLibrary(ctx, old, bytes.NewReader(dumpWithHeaderVersion(t, 3)), true); err == nil || !strings.Contains(err.Error(), "最低支持") {
		t.Fatalf("v3 备份应拒绝: %v", err)
	}

	// 3) 比当前更新的备份:拒绝并指引升级 kb
	future, _ := freshStore(t)
	if _, err := RestoreLibrary(ctx, future, bytes.NewReader(dumpWithHeaderVersion(t, store.DBSchemaVersion+1)), true); err == nil || !strings.Contains(err.Error(), "升级 kb") {
		t.Fatalf("未来版本备份应拒绝: %v", err)
	}
}
