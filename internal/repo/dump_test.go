package repo

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// M3.9:整库备份/恢复 roundtrip——嵌套条目、项目与分支描述全部还原。
func TestDumpRestoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t)
	if err := s.ProjectCreate(ctx, "alpha", "演示项目"); err != nil {
		t.Fatal(err)
	}
	r := Open(s, Config{Project: "alpha", Now: func() int64 { return fixedTime }})
	if _, _, err := r.SetNote(ctx, "go/conc/channel", NoteInput{Title: "Channel", Body: "chan 语义"}, "add"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "root", NoteInput{Title: "Root", Body: ""}, "add"); err != nil {
		t.Fatal(err)
	}
	if err := s.BranchDescribe(ctx, "alpha", "main", "工作线"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	stats, err := DumpLibrary(ctx, s, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Objects == 0 || stats.Projects != 2 || stats.Branches != 1 {
		t.Fatalf("备份计数不符: %+v", stats)
	}

	// 恢复到全新库
	if err := s.Wipe(ctx); err != nil {
		t.Fatal(err)
	}
	rstats, err := RestoreLibrary(ctx, s, bytes.NewReader(buf.Bytes()), false)
	if err != nil {
		t.Fatal(err)
	}
	if rstats.Objects != stats.Objects || rstats.Projects != stats.Projects || rstats.Branches != stats.Branches {
		t.Fatalf("恢复计数不符: %+v vs %+v", rstats, stats)
	}

	// 内容逐项还原
	notes, err := r.ListNotes(ctx, "")
	if err != nil || len(notes) != 2 {
		t.Fatalf("恢复后应有 2 条: %v %v", notes, err)
	}
	if notes[0].Path != "go/conc/channel" || notes[0].Note.Meta.Title != "Channel" {
		t.Fatalf("嵌套条目未还原: %+v", notes)
	}
	branches, err := s.BranchListAll(ctx)
	if err != nil || len(branches) != 1 || branches[0].Description != "工作线" {
		t.Fatalf("分支描述未还原: %+v %v", branches, err)
	}
	ps, err := s.ProjectStats(ctx)
	if err != nil || len(ps) != 2 {
		t.Fatalf("项目数不符: %+v %v", ps, err)
	}
	fs, err := r.FSCK(ctx)
	if err != nil || len(fs.Problems) != 0 {
		t.Fatalf("恢复后 fsck 应通过: %+v %v", fs.Problems, err)
	}
}

// M3.9:损坏的备份(对象字节被篡改)必须在导入时被哈希校验拒绝。
func TestRestoreRejectsCorruptBackup(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t)
	r := Open(s, Config{Now: func() int64 { return fixedTime }})
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A", Body: "body-a"}, "add"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := DumpLibrary(ctx, s, &buf); err != nil {
		t.Fatal(err)
	}
	// 正文在备份中以 base64 存在("body-a" → "Ym9keS1h");替换为同长度但内容不同的片段
	if !strings.Contains(buf.String(), "Ym9keS1h") {
		t.Fatal("测试前提失效: 备份中未找到目标 base64 片段")
	}
	corrupt := strings.Replace(buf.String(), "Ym9keS1h", "Ym9keS1i", 1)
	if err := s.Wipe(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreLibrary(ctx, s, strings.NewReader(corrupt), false); err == nil {
		t.Fatal("篡改后的备份应被哈希校验拒绝")
	}
}

// M3.9:非空库默认拒绝恢复,--force 覆盖。
func TestRestoreRefusesNonEmptyUnlessForced(t *testing.T) {
	ctx := context.Background()
	s, _ := freshStore(t)
	r := Open(s, Config{Now: func() int64 { return fixedTime }})
	if _, _, err := r.SetNote(ctx, "a", NoteInput{Title: "A"}, "add"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := DumpLibrary(ctx, s, &buf); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SetNote(ctx, "b", NoteInput{Title: "B"}, "add b"); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreLibrary(ctx, s, bytes.NewReader(buf.Bytes()), false); err == nil {
		t.Fatal("非空库不加 force 应拒绝")
	} else if !strings.Contains(err.Error(), "非空") {
		t.Fatalf("错误应说明非空: %v", err)
	}
	if _, err := RestoreLibrary(ctx, s, bytes.NewReader(buf.Bytes()), true); err != nil {
		t.Fatalf("force 恢复应成功: %v", err)
	}
	notes, err := r.ListNotes(ctx, "")
	if err != nil || len(notes) != 1 || notes[0].Path != "a" {
		t.Fatalf("force 恢复后应只剩备份内容: %v %v", notes, err)
	}
}
