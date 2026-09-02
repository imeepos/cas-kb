package main

// kb doctor 单元测试(T49):名字统一含 Doctor。
// 覆盖:健康库全 ok 退出 0 / 悬垂对象 warn 不拦 / 坏 DSN fail 退出 1 /
// --check 单项与 --list-checks / --json 契约 / serve 探活三态 /
// config 与 gc-protect 的状态映射 / 凭据绝不回显。

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/imeepos/cas-kb/internal/store"
	"github.com/imeepos/cas-kb/internal/view"
)

// silenceServe 把 doctorServeAddr 指到当前无监听的回环端口并注册恢复,
// 隔离本机 8787 环境对「全 ok」断言的干扰;返回注入的地址。
func silenceServe(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	old := doctorServeAddr
	doctorServeAddr = addr
	t.Cleanup(func() { doctorServeAddr = old })
	return addr
}

// doctorRowOf 跑单项检查并返回其唯一一行。
func doctorRowOf(t *testing.T, ctx context.Context, name string) view.DoctorRow {
	t.Helper()
	rows, code := runDoctor(ctx, map[string]bool{name: true})
	if len(rows) != 1 {
		t.Fatalf("单项 %s 应只有一行,得到 %d", name, len(rows))
	}
	_ = code
	return rows[0]
}

// TestDoctorHealthyAllOK:健康库六项全 ok,退出码 0。
func TestDoctorHealthyAllOK(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	silenceServe(t)
	out, err := captureStdout(t, func() error { return cmdDoctor(ctx, nil) })
	if err != nil {
		t.Fatalf("健康库 doctor 不应报错: %v", err)
	}
	if !strings.Contains(out, "doctor: 6 ok, 0 warn, 0 fail") {
		t.Fatalf("健康库应输出 6 ok 汇总,got %q", out)
	}
	rows, code := runDoctor(ctx, nil)
	if code != 0 {
		t.Fatalf("健康库退出码应 0,得到 %d", code)
	}
	if len(rows) != len(doctorChecks) {
		t.Fatalf("应跑全部 %d 项,得到 %d", len(doctorChecks), len(rows))
	}
	for _, r := range rows {
		if r.Status != view.DoctorStatusOK {
			t.Fatalf("%s 应 ok,得到 %s(%s)", r.Check, r.Status, r.Detail)
		}
	}
}

// TestDoctorDanglingObjectsWarnNotFail:reset 制造悬垂/未达对象后,
// fsck 项应 warn(信息非错误,学 git fsck --dangling),退出码仍 0。
func TestDoctorDanglingObjectsWarnNotFail(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	silenceServe(t)
	setNote(t, "a", "A1")
	setNote(t, "b", "B")
	setNote(t, "c", "C")
	firstShort := oldestShort(t) // log 最新在前,末行即首提交
	if err := cmdReset(ctx, []string{firstShort}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	rows, code := runDoctor(ctx, nil)
	if code != 0 {
		t.Fatalf("仅 warn 不应拦退出码,得到 %d", code)
	}
	for _, r := range rows {
		want := view.DoctorStatusOK
		if r.Check == "fsck" {
			want = view.DoctorStatusWarn
		}
		if r.Status != want {
			t.Fatalf("%s 应 %s,得到 %s(%s)", r.Check, want, r.Status, r.Detail)
		}
	}
	if !strings.Contains(rows[1].Detail, "悬垂/未达对象") {
		t.Fatalf("fsck warn 详情应说明悬垂对象,got %q", rows[1].Detail)
	}
}

// TestDoctorBadDSNFail:坏 DSN 时 storage fail(依赖存储的 fsck 跟随失败),
// 退出码 1。
func TestDoctorBadDSNFail(t *testing.T) {
	ctx := context.Background()
	silenceServe(t)
	t.Setenv("KB_DSN", "sqlite:") // 空路径:store 打开响亮失败,形态核对同判非法
	rows, code := runDoctor(ctx, nil)
	if code != 1 {
		t.Fatalf("坏 DSN 应退出码 1,得到 %d", code)
	}
	byName := map[string]view.DoctorRow{}
	for _, r := range rows {
		byName[r.Check] = r
	}
	if byName["storage"].Status != view.DoctorStatusFail {
		t.Fatalf("storage 应 fail,得到 %s(%s)", byName["storage"].Status, byName["storage"].Detail)
	}
	if byName["fsck"].Status != view.DoctorStatusFail {
		t.Fatalf("存储不可用时 fsck 应 fail,得到 %s(%s)", byName["fsck"].Status, byName["fsck"].Detail)
	}
	if !strings.Contains(byName["config"].Detail, "路径为空") {
		t.Fatalf("config 应指出 DSN 形态非法,got %q", byName["config"].Detail)
	}
}

// TestDoctorCheckSelectionAndList:--check 单项、多项(注册表顺序)、
// 未知检查名报错、--list-checks 全量列举。
func TestDoctorCheckSelectionAndList(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	silenceServe(t)

	rows, code := runDoctor(ctx, map[string]bool{"fsck": true})
	if code != 0 || len(rows) != 1 || rows[0].Check != "fsck" {
		t.Fatalf("--check fsck 应只跑一项: rows=%v code=%d", rows, code)
	}
	rows, _ = runDoctor(ctx, map[string]bool{"version": true, "storage": true})
	if len(rows) != 2 || rows[0].Check != "storage" || rows[1].Check != "version" {
		t.Fatalf("多 --check 仍应按注册表顺序输出: %v", rows)
	}
	if err := cmdDoctor(ctx, []string{"--check", "nope"}); err == nil {
		t.Fatal("未知检查名应报错")
	}
	var names []string
	for _, c := range doctorChecks {
		names = append(names, c.name)
	}
	out, err := captureStdout(t, func() error { return cmdDoctor(ctx, []string{"-l"}) })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSpace(out), "\n"); len(got) != len(names) {
		t.Fatalf("-l 应列举全部 %d 项,got %q", len(names), out)
	}
	for i, n := range names {
		if !strings.Contains(out, n) {
			t.Fatalf("-l 输出缺 %s:%q", n, out)
		}
		if i > 0 && strings.Index(out, names[i-1]) > strings.Index(out, n) {
			t.Fatalf("-l 应按注册表顺序:%q", out)
		}
	}
}

// TestDoctorJSONContract:--json 输出 [{check,status,detail}] 数组,
// 行序同注册表,status 三档枚举;与 --check 可组合。
func TestDoctorJSONContract(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	silenceServe(t)
	out, err := captureStdout(t, func() error { return cmdDoctor(ctx, []string{"--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var rows []view.DoctorRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("--json 应为可解析数组: %v:%q", err, out)
	}
	if len(rows) != len(doctorChecks) {
		t.Fatalf("应 %d 行,得到 %d", len(doctorChecks), len(rows))
	}
	for i, r := range rows {
		if r.Check != doctorChecks[i].name {
			t.Fatalf("第 %d 行应 %s,得到 %s", i, doctorChecks[i].name, r.Check)
		}
		switch r.Status {
		case view.DoctorStatusOK, view.DoctorStatusWarn, view.DoctorStatusFail:
		default:
			t.Fatalf("status 非法: %q", r.Status)
		}
		if r.Detail == "" {
			t.Fatalf("%s 的 detail 不应为空", r.Check)
		}
	}
	out, err = captureStdout(t, func() error { return cmdDoctor(ctx, []string{"--json", "--check", "version"}) })
	if err != nil {
		t.Fatal(err)
	}
	var one []view.DoctorRow
	if err := json.Unmarshal([]byte(out), &one); err != nil || len(one) != 1 || one[0].Check != "version" {
		t.Fatalf("--json --check version 应单行: %v:%q", err, out)
	}
}

// TestDoctorServeProbeThreeStates:serve 探活三态——
// 未运行 = ok;同库真实实例 = ok;schema/后端不符 = warn。
func TestDoctorServeProbeThreeStates(t *testing.T) {
	ctx := context.Background()
	initRepo(t)

	// 态 1:未运行 = ok(明确「未运行」不是错误)
	silenceServe(t)
	r := doctorRowOf(t, ctx, "serve")
	if r.Status != view.DoctorStatusOK || !strings.Contains(r.Detail, "未运行") {
		t.Fatalf("未运行应 ok 且说明未运行,得到 %s(%s)", r.Status, r.Detail)
	}

	// 态 2:同库真实实例 = ok(backend/schema 一致)
	base, stop := startAPIServe(t, "")
	defer stop()
	doctorServeAddr = strings.TrimPrefix(base, "http://")
	r = doctorRowOf(t, ctx, "serve")
	if r.Status != view.DoctorStatusOK || !strings.Contains(r.Detail, "实例健康") {
		t.Fatalf("同库实例应 ok,得到 %s(%s)", r.Status, r.Detail)
	}

	// 态 3a:schema 版本不符 = warn
	fakeSchema := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"backend":"sqlite","schema_version":%d,"project":"default"}`, store.DBSchemaVersion+1)
	}))
	defer fakeSchema.Close()
	doctorServeAddr = strings.TrimPrefix(fakeSchema.URL, "http://")
	r = doctorRowOf(t, ctx, "serve")
	if r.Status != view.DoctorStatusWarn || !strings.Contains(r.Detail, "schema 版本不一致") {
		t.Fatalf("schema 不符应 warn,得到 %s(%s)", r.Status, r.Detail)
	}

	// 态 3b:后端不符 = warn
	fakeBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"backend":"postgres","schema_version":%d,"project":"default"}`, store.DBSchemaVersion)
	}))
	defer fakeBackend.Close()
	doctorServeAddr = strings.TrimPrefix(fakeBackend.URL, "http://")
	r = doctorRowOf(t, ctx, "serve")
	if r.Status != view.DoctorStatusWarn || !strings.Contains(r.Detail, "后端不一致") {
		t.Fatalf("后端不符应 warn,得到 %s(%s)", r.Status, r.Detail)
	}
}

// TestDoctorConfigChecks:config 的状态映射——非法形态 fail、目标不存在
// warn、fail 盖过 warn;令牌已设置只报「值不回显」。
func TestDoctorConfigChecks(t *testing.T) {
	ctx := context.Background()
	initRepo(t)
	silenceServe(t)

	r := doctorRowOf(t, ctx, "config")
	if r.Status != view.DoctorStatusOK {
		t.Fatalf("默认配置应 ok,得到 %s(%s)", r.Status, r.Detail)
	}

	t.Setenv("KB_PROJECT", "ghost-project")
	r = doctorRowOf(t, ctx, "config")
	if r.Status != view.DoctorStatusWarn || !strings.Contains(r.Detail, "kb project create ghost-project") {
		t.Fatalf("项目不存在应 warn 并给可行动建议,得到 %s(%s)", r.Status, r.Detail)
	}

	t.Setenv("KB_PROJECT", "")
	t.Setenv("KB_GC_PROTECT", "oof")
	t.Setenv("KB_UPDATE_REPO", "just-a-name")
	r = doctorRowOf(t, ctx, "config")
	if r.Status != view.DoctorStatusFail {
		t.Fatalf("fail 应盖过 warn,得到 %s(%s)", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, "无法识别") || !strings.Contains(r.Detail, "owner/name") {
		t.Fatalf("应逐项列出问题,got %q", r.Detail)
	}

	t.Setenv("KB_UPDATE_REPO", "")
	t.Setenv("KB_SERVE_TOKEN", "super-secret-token-value")
	r = doctorRowOf(t, ctx, "config")
	if !strings.Contains(r.Detail, "已设置(值不回显)") {
		t.Fatalf("令牌应只报已设置,got %q", r.Detail)
	}
	if strings.Contains(r.Detail, "super-secret-token-value") {
		t.Fatal("令牌值绝不能出现在输出")
	}
}

// TestDoctorGCProtectUnwritableWarn:gc 保护开启但当前目录不可写 = warn。
func TestDoctorGCProtectUnwritableWarn(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限约束,跳过")
	}
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	t.Chdir(dir)
	t.Setenv("KB_GC_PROTECT", "on")
	r := doctorRowOf(t, ctx, "gc-protect")
	if r.Status != view.DoctorStatusWarn || !strings.Contains(r.Detail, "不可写") {
		t.Fatalf("目录不可写应 warn,得到 %s(%s)", r.Status, r.Detail)
	}
}
