# T35/T36 质量审查门禁报告

## 审查范围

本审查在独立 worktree "/Users/imeepos/ext512/ymm-001/cas-kb-t35-t36-review"、分支 "review/t35-t36-gate" 完成；仅复核 "feature/search-snippet" (T35) 与 "research/merge-design" (T36)，未修改 main、未 push/merge，也未修改两个 feature worktree。审查基准为 main 的 "6b150ab"。

## Git 提交与工作树证据

- T35 HEAD："d335ae8" (T35d)，提交链为 "e20c4bb" → "3ee4f22" → "92f3571" → "d335ae8"；相对 main 的变更集中在 snippet 实现、CLI/API 接入、测试及同步文档/e2e。
- T36 HEAD："72aa8ab"，提交信息为 "docs(research): 三方合并(merge)设计调研——pull --merge、条目级三方判定、冲突中间态与里程碑建议(T36)"；相对 main 仅新增 "docs/research/merge-design.md" (387 行)。
- 三个 worktree 检查时均为干净工作树；本审查 worktree 起点为 main，审查期间仅计划新增本报告。
- "git worktree list --porcelain" 确认 T35、T36 均为独立 worktree，目标分支分别为 "feature/search-snippet"、"research/merge-design"。

## T35 结论：通过

T35 测试真实覆盖了“snippet 只影响展示”的红线：

- "internal/view/snippet_test.go" 覆盖中英/CJK 2-gram 词源合并、英文整词边界、无正文命中、窗口吸附、rune 边界、合法 UTF-8、与 tokenizer 语义一致及重复调用确定性。
- "cmd/kb/search_snippet_test.go" 覆盖 CLI "--snippet" 文本缩进行、"--json --snippet" 可选字段、缺省 JSON 向后兼容、过滤片段行后命中行序列完全一致、标题命中无标记及逐字节确定性。
- "internal/server/server_snippet_test.go" 覆盖 API "snippet=1"、仅字面 "1" 生效、缺省字段不出现、除 snippet 外逐字段一致、响应逐字节确定。
- 执行 "go test ./..." (T35) 全绿；另执行 snippet 定向包测试，全绿。

因此 T35 的 CLI/API 面、CJK/rune 行为、确定性，以及排序/命中集合零变化红线均有可执行测试支撑，结论为 **PASS**。

## T36 结论：通过（文档研究交付）

"docs/research/merge-design.md" 已确认包含：

- 动机与现状，以及现有 pull 的 fast-forward/分叉行为与缺口；
- LCA 基准算法、只沿 parents 不信任 Time 的纪律及多 LCA 处理；
- 笔记级/条目级三方粒度、目录递归、冲突分类与 Merkle 剪枝；
- 双亲合并快照、无冲突落库、中间态、"merge --continue"、"merge --abort" 与冻结纪律；
- base/ours/theirs 的 "sha256:" 地址示例和完整演算；
- 9 条风险与开放问题（超过至少 5 条）；
- ROADMAP M5 两批次建议、范围、验收标准与命令。

全文是文档改动，未发现代码越界；T36 文档门禁结论为 **PASS**。

## 原 T36 验收失败根因与修正版

原设计草案使用 "grep -Fq '动机\\|现状' docs/research/merge-design.md" 一类写法时失败："-F" 将 pattern 当作固定字符串，因此会寻找字面量反斜杠和竖线，不会把 "\\|" 当作 alternation。若去掉 "-F"，在 GNU/BSD grep 上才是基本正则 alternation；但这又依赖正则语义，不符合本门禁要求的稳定逐个固定字符串检查。复现结果：固定模式状态 1，基本正则状态 0。

稳定修正版（每项独立 "grep -Fq"，无 BSD grep 的 "\\|"）：

    for term in '动机与现状' 'LCA(最近公共祖先快照)' '条目级三方判定' '冲突中间态' '两个 parents' '中间态' '--abort' 'base' 'ours' 'theirs' '风险与开放问题' 'ROADMAP 里程碑'; do grep -Fq -- "$term" docs/research/merge-design.md || exit 1; done

实跑该命令退出码为 0。T36 原建议的 "go test ./internal/repo/ -run Merge -v" 在当前 T36 纯文档分支上不是合适的文档门禁：它可能匹配不到新增 merge 实现测试；文档交付应采用上述内容断言，并另行记录全量测试结果。

## 合并顺序与剩余风险

建议先合并 T35，再决定是否立项并实施 T36 的 M5 设计：T35 是已实现、已测试的展示增量，可独立进入主线；T36 是研究文档，不应与未实现的 merge 能力混淆。若负责人批准 M5，按 T36 §5 先做 repo 内核 A，再做 CLI/中间态/e2e B，并把 DESIGN、ROADMAP、usage、CHANGELOG 同步纳入实现验收。

剩余风险包括：LCA 大 DAG 的开销、多 LCA/蟹状历史策略、批量冲突裁决 UX、"-stage" 与 "-merge" 冻结交互、跨项目语义、"gc --keep-last" 水位、"kb log" 第二亲链展示、中间态 backup/restore 与 serve 边界、以及 HTTP API 是否暴露合并状态。另有一个审查注意项：T36 文档示例中的地址均明确为示意地址，不能直接当作可调用对象地址。
