package main

// kb doctor —— 健康自检(T49,调研 best-practices-adoption §5):
// 形态学 brew doctor(检查可列举、可单独跑、warn 克制不拦),分级学
// git fsck(错误/警告/信息三档),退出码两档(有 fail ⇒ 1)。
// v1 六个检查项全部复用现成能力(store 打开门禁 / repo.FSCK / kb version /
// 环境变量核对 / GC 备份目录可写性 / serve /healthz),零新增诊断逻辑;
// doctor 只诊断不施治,修复永远指向可行动命令。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/imeepos/cas-kb/internal/hash"
	"github.com/imeepos/cas-kb/internal/repo"
	"github.com/imeepos/cas-kb/internal/store"
	"github.com/imeepos/cas-kb/internal/view"
)

// doctorEnv 是一次 doctor 运行的共享环境:存储最多打开一次,各检查按需取用。
// 打开失败会被记住(storage 检查报 fail),后续依赖存储的检查给出可行动
// 说明而不是重复报错。
type doctorEnv struct {
	ctx      context.Context
	s        store.Store
	r        *repo.Repo
	storeErr error
	opened   bool
}

// open 打开(含迁移)存储并构造仓库对象,结果缓存;失败原样返回。
func (e *doctorEnv) open() (store.Store, *repo.Repo, error) {
	if !e.opened {
		e.opened = true
		e.s, e.storeErr = openStore(e.ctx)
		if e.storeErr == nil {
			e.r = repo.Open(e.s, repo.Config{Project: projectName(), Branch: branchName()})
		}
	}
	return e.s, e.r, e.storeErr
}

// close 释放已打开的存储连接。
func (e *doctorEnv) close() {
	if e.s != nil {
		_ = e.s.Close()
	}
}

// doctorCheck 是注册表的一行:检查名即契约(--list-checks 列举、--check 选择)。
type doctorCheck struct {
	name string
	run  func(ctx context.Context, env *doctorEnv) view.DoctorRow
}

// doctorChecks 是检查项注册表(v1 六项);顺序即默认执行与输出顺序,
// 新增检查只在表尾追加,不破坏既有检查名。
var doctorChecks = []doctorCheck{
	{name: "storage", run: doctorCheckStorage},
	{name: "fsck", run: doctorCheckFSCK},
	{name: "version", run: doctorCheckVersion},
	{name: "config", run: doctorCheckConfig},
	{name: "gc-protect", run: doctorCheckGCProtect},
	{name: "serve", run: doctorCheckServe},
}

// doctorServeAddr 是 serve 检查的探活地址;测试经它注入临时实例。
var doctorServeAddr = defaultServeAddr

// doctorRow 便于构造检查行。
func doctorRow(check, status, detail string) view.DoctorRow {
	return view.DoctorRow{Check: check, Status: status, Detail: detail}
}

// runDoctor 按注册表顺序执行检查(selected 为 nil 跑全部;非 nil 只跑命中项,
// 顺序仍按注册表,输出稳定)。返回行序列与退出码;输出与退出码分离便于测试。
func runDoctor(ctx context.Context, selected map[string]bool) ([]view.DoctorRow, int) {
	env := &doctorEnv{ctx: ctx}
	defer env.close()
	rows := make([]view.DoctorRow, 0, len(doctorChecks))
	for _, c := range doctorChecks {
		if selected != nil && !selected[c.name] {
			continue
		}
		rows = append(rows, c.run(ctx, env))
	}
	return rows, doctorExitCode(rows)
}

// doctorExitCode 是退出码契约:任一 fail ⇒ 1,仅 ok/warn ⇒ 0(warn 不拦,
// 学 brew doctor 的克制);细分只存在于行内,退出码不做多档。
func doctorExitCode(rows []view.DoctorRow) int {
	for _, r := range rows {
		if r.Status == view.DoctorStatusFail {
			return 1
		}
	}
	return 0
}

// cmdDoctor 处理 kb doctor:解析旗标、按需列举检查名、执行并输出,
// 有 fail 时以退出码 1 结束。
func cmdDoctor(ctx context.Context, args []string) error {
	var checks []string
	listChecks := false
	jsonOut := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--check":
			if i+1 >= len(args) {
				return fmt.Errorf("doctor: --check 缺少检查名(-l 可列举全部)")
			}
			checks = append(checks, args[i+1])
			i++
		case "-l", "--list-checks":
			listChecks = true
		case "--json":
			jsonOut = true
		default:
			return fmt.Errorf("doctor: 未知参数 %q(用法: kb doctor [--json] [--check <name>…] [-l|--list-checks])", args[i])
		}
	}
	if listChecks {
		for _, c := range doctorChecks {
			fmt.Println(c.name)
		}
		return nil
	}
	var selected map[string]bool
	if len(checks) > 0 {
		selected = make(map[string]bool, len(checks))
		for _, name := range checks {
			found := false
			for _, c := range doctorChecks {
				if c.name == name {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("doctor: 未知检查名 %q(-l 列举全部)", name)
			}
			selected[name] = true
		}
	}
	rows, code := runDoctor(ctx, selected)
	if jsonOut {
		// 行契约复用 internal/view,机器可读出口与文本同源
		if err := printJSON(rows); err != nil {
			return err
		}
	} else {
		printDoctorText(rows)
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// printDoctorText 输出逐项「状态 + 检查名 + 人话详情」与末尾汇总行。
func printDoctorText(rows []view.DoctorRow) {
	okN, warnN, failN := 0, 0, 0
	for _, r := range rows {
		fmt.Printf("%-4s %-11s%s\n", r.Status, r.Check, r.Detail)
		switch r.Status {
		case view.DoctorStatusOK:
			okN++
		case view.DoctorStatusWarn:
			warnN++
		case view.DoctorStatusFail:
			failN++
		}
	}
	fmt.Printf("doctor: %d ok, %d warn, %d fail\n", okN, warnN, failN)
}

// doctorCheckStorage 打开 KB_DSN 后端(与其它命令同口径,含迁移)+ 库
// schema 门禁;打不开/版本不符 = fail。
func doctorCheckStorage(ctx context.Context, env *doctorEnv) view.DoctorRow {
	if _, _, err := env.open(); err != nil {
		return doctorRow("storage", view.DoctorStatusFail,
			fmt.Sprintf("库打不开:%v;核对 KB_DSN(连接串形态/网络/库名),schema 版本不符按提示处理旧库", err))
	}
	name, target := store.DescribeBackend(effectiveDSN())
	if _, err := env.s.MetaGet(ctx, "schema_version"); err != nil {
		return doctorRow("storage", view.DoctorStatusFail,
			fmt.Sprintf("库可打开但读不到 schema 版本:%v;疑似损坏,先 kb backup 留档", err))
	}
	return doctorRow("storage", view.DoctorStatusOK,
		fmt.Sprintf("后端 %s(%s),库 schema v%d,打开正常", name, target, store.DBSchemaVersion))
}

// doctorCheckFSCK 等价 kb fsck:完整性问题 = fail;悬垂/未达对象 = warn
// (信息非错误,学 git fsck --dangling;计数复用 GC 标记口径,零第二套实现)。
func doctorCheckFSCK(ctx context.Context, env *doctorEnv) view.DoctorRow {
	_, r, err := env.open()
	if err != nil {
		return doctorRow("fsck", view.DoctorStatusFail,
			"存储不可用,无法执行完整性检查(见 storage 项);先修复存储再重跑")
	}
	res, err := r.FSCK(ctx)
	if err != nil {
		return doctorRow("fsck", view.DoctorStatusFail, fmt.Sprintf("fsck 执行失败:%v", err))
	}
	if len(res.Problems) > 0 {
		var b strings.Builder
		for i, p := range res.Problems {
			if i == 3 {
				fmt.Fprintf(&b, " …(共 %d 个)", len(res.Problems))
				break
			}
			fmt.Fprintf(&b, " %s[%s]:%s;", view.ShortAddr(hash.Address(p.Addr)), p.Kind, p.Problem)
		}
		return doctorRow("fsck", view.DoctorStatusFail,
			fmt.Sprintf("发现 %d 个完整性问题:%s;处置前先 kb backup 留档", len(res.Problems), b.String()))
	}
	n, err := r.UnreachableCount(ctx)
	if err != nil {
		return doctorRow("fsck", view.DoctorStatusFail,
			fmt.Sprintf("对象完整但未达对象统计失败(疑似分支指向缺失的快照):%v;先 kb backup 再排查", err))
	}
	if n > 0 {
		return doctorRow("fsck", view.DoctorStatusWarn,
			fmt.Sprintf("%d 个悬垂/未达对象(信息非错误,下次 kb gc 会清扫)", n))
	}
	return doctorRow("fsck", view.DoctorStatusOK,
		fmt.Sprintf("检查 %d 个对象,完整无问题", res.Checked))
}

// doctorCheckVersion 报告 kb version 本体;仅信息,永不 fail;dev 构建
// 注明未注入版本号(沿 §8.4 口径,不参与比较)。
func doctorCheckVersion(_ context.Context, _ *doctorEnv) view.DoctorRow {
	v := fmt.Sprintf("kb %s(%s/%s,%s)", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	if version == "dev" {
		v += ";dev 构建(未注入版本号),仅信息"
	}
	return doctorRow("version", view.DoctorStatusOK, v)
}

// validateDSNForm 核对 DSN 形态(PostgreSQL URL / SQLite 路径)。只判合法性,
// 错误信息绝不回显连接串本身(url.Parse 的错误文本含原文,一律丢弃换语义说明);
// 合法时的展示统一走 store.DescribeBackend(host/database 或文件路径,无凭据段)。
func validateDSNForm(what, dsn string) error {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return fmt.Errorf("%s:postgres URL 解析失败", what)
		}
		if u.Host == "" {
			return fmt.Errorf("%s:缺少主机(host)", what)
		}
		return nil
	}
	p := strings.TrimPrefix(strings.TrimPrefix(dsn, "sqlite:"), "file:")
	if p == "" {
		return fmt.Errorf("%s:sqlite 库路径为空", what)
	}
	return nil
}

// doctorCheckConfig 逐个核对已设置的 KB_* 环境变量取值合法性:
// 非法形态 = fail;目标不存在/取值不可识别 = warn;未设置的默认项不提;
// 令牌类变量只报「已设置」,绝不回显值。
func doctorCheckConfig(ctx context.Context, env *doctorEnv) view.DoctorRow {
	status := view.DoctorStatusOK
	bump := func(s string) {
		if s == view.DoctorStatusFail || status == view.DoctorStatusFail {
			status = view.DoctorStatusFail
		} else if s == view.DoctorStatusWarn {
			status = view.DoctorStatusWarn
		}
	}
	var notes []string
	dsnNote := func(name, dsn string) {
		if err := validateDSNForm(name, dsn); err != nil {
			bump(view.DoctorStatusFail)
			notes = append(notes, err.Error())
			return
		}
		backend, _ := store.DescribeBackend(dsn)
		notes = append(notes, fmt.Sprintf("%s 形态合法(%s)", name, backend))
	}
	if v := os.Getenv("KB_DSN"); v != "" {
		dsnNote("KB_DSN", v)
	}
	if v := os.Getenv("KB_REMOTE_DSN"); v != "" {
		dsnNote("KB_REMOTE_DSN", v)
	}
	if v := os.Getenv("KB_TEST_DSN"); v != "" {
		dsnNote("KB_TEST_DSN(仅测试)", v)
	}
	if v := os.Getenv("KB_BRANCH"); v != "" {
		notes = append(notes, "KB_BRANCH 已设置(生效分支 "+v+")")
	}
	if v := os.Getenv("KB_PROJECT"); v != "" {
		s, _, err := env.open()
		switch {
		case err != nil:
			notes = append(notes, "KB_PROJECT 已设置但存储不可用,跳过存在性核对")
		default:
			if _, gerr := s.ProjectGet(ctx, v); gerr != nil {
				if errors.Is(gerr, store.ErrProjectNotFound) {
					bump(view.DoctorStatusWarn)
					notes = append(notes, fmt.Sprintf("KB_PROJECT 项目 %q 不存在;kb project create %s 可创建", v, v))
				} else {
					notes = append(notes, "KB_PROJECT 查询失败:"+gerr.Error())
				}
			} else {
				notes = append(notes, "KB_PROJECT 项目存在")
			}
		}
	}
	if v := os.Getenv("KB_GC_PROTECT"); v != "" {
		switch strings.ToLower(v) {
		case "on", "off", "0", "false":
			notes = append(notes, "KB_GC_PROTECT="+strings.ToLower(v))
		default:
			bump(view.DoctorStatusWarn)
			notes = append(notes, fmt.Sprintf("KB_GC_PROTECT 取值 %q 无法识别(按 on 处理);可用 on/off/0/false", v))
		}
	}
	if v := os.Getenv("KB_UPDATE_REPO"); v != "" {
		owner, name, ok := strings.Cut(v, "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			bump(view.DoctorStatusFail)
			notes = append(notes, "KB_UPDATE_REPO 应为 owner/name 形态")
		} else {
			notes = append(notes, "KB_UPDATE_REPO 形态合法")
		}
	}
	if os.Getenv(serveTokenEnv) != "" {
		notes = append(notes, "KB_SERVE_TOKEN 已设置(值不回显);serve 写端点已启用")
	}
	if os.Getenv("GITHUB_TOKEN") != "" {
		notes = append(notes, "GITHUB_TOKEN 已设置(值不回显);仅 kb update 使用")
	}
	if len(notes) == 0 {
		return doctorRow("config", view.DoctorStatusOK, "未设置任何 KB_* 环境变量,全部取默认(默认项不逐一提示)")
	}
	return doctorRow("config", status, strings.Join(notes, ";"))
}

// doctorCheckGCProtect 报告 KB_GC_PROTECT 开关态 + 分支表备份目录(gc 把
// branches-backup-*.json 写在当前工作目录)可写性;不可写 = warn。
func doctorCheckGCProtect(_ context.Context, _ *doctorEnv) view.DoctorRow {
	if !gcProtectOn() {
		return doctorRow("gc-protect", view.DoctorStatusOK,
			"KB_GC_PROTECT 已关闭:gc 清扫前不备份分支表(误删保护不生效;设 KB_GC_PROTECT=on 可开启)")
	}
	probe, err := os.CreateTemp(".", "doctor-gc-probe-*")
	if err != nil {
		return doctorRow("gc-protect", view.DoctorStatusWarn,
			fmt.Sprintf("GC 保护开启但当前目录不可写(%v),gc 清扫前将无法备份分支表;在可写目录运行 kb 或调整目录权限", err))
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return doctorRow("gc-protect", view.DoctorStatusOK,
		"GC 保护开启(on),当前目录可写,gc 清扫前可自动备份分支表")
}

// healthzRow 是 /healthz 响应的只读解析目标(字段与 server.healthRow 同款)。
type healthzRow struct {
	OK            bool   `json:"ok"`
	Backend       string `json:"backend"`
	SchemaVersion int    `json:"schema_version"`
	Project       string `json:"project"`
}

// doctorCheckServe 探活本机 serve:连接拒绝 = ok(明确「未运行」不是错误);
// 可达则 GET /healthz 核对 backend/schema_version 与本工具一致,不符 = warn。
func doctorCheckServe(_ context.Context, _ *doctorEnv) view.DoctorRow {
	u := "http://" + doctorServeAddr + "/healthz"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		reason := "连接被拒"
		if !strings.Contains(err.Error(), "connect: connection refused") {
			reason = "不可达(" + err.Error() + ")"
		}
		return doctorRow("serve", view.DoctorStatusOK,
			fmt.Sprintf("未检测到运行中的 kb serve(%s %s);未运行不是错误,需要时 kb serve 启动", doctorServeAddr, reason))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return doctorRow("serve", view.DoctorStatusWarn,
			fmt.Sprintf("%s 有服务但 /healthz 响应读取失败(%v);确认端口归属", doctorServeAddr, err))
	}
	if resp.StatusCode != http.StatusOK {
		return doctorRow("serve", view.DoctorStatusWarn,
			fmt.Sprintf("%s 有服务但 /healthz 返回 HTTP %d,不像健康的 kb serve;确认端口归属", doctorServeAddr, resp.StatusCode))
	}
	var h healthzRow
	if err := json.Unmarshal(body, &h); err != nil || !h.OK {
		return doctorRow("serve", view.DoctorStatusWarn,
			fmt.Sprintf("%s 有服务但 /healthz 不是 kb serve 探活响应;确认端口归属", doctorServeAddr))
	}
	var probs []string
	if name, _ := store.DescribeBackend(effectiveDSN()); name != h.Backend {
		probs = append(probs, fmt.Sprintf("后端不一致(实例 %s,本工具 %s)", h.Backend, name))
	}
	if h.SchemaVersion != store.DBSchemaVersion {
		probs = append(probs, fmt.Sprintf("库 schema 版本不一致(实例 v%d,本工具 v%d),实例可能是旧版二进制,建议重启", h.SchemaVersion, store.DBSchemaVersion))
	}
	if len(probs) > 0 {
		return doctorRow("serve", view.DoctorStatusWarn,
			fmt.Sprintf("kb serve 实例可达(项目 %s)但%s", h.Project, strings.Join(probs, ";")))
	}
	return doctorRow("serve", view.DoctorStatusOK,
		fmt.Sprintf("kb serve 实例健康(后端 %s,schema v%d,项目 %s)", h.Backend, h.SchemaVersion, h.Project))
}
