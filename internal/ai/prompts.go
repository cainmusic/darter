package ai

import "strings"

// 场景前缀（「场景.能力」命名空间的场景部分）。
const (
	SceneFixer     = "fixer"
	SceneMigration = "migration"
)

// 能力名（「场景.能力」命名空间的能力部分）。
const (
	FnParseReport        = "parse_report"
	FnFix                = "fix"
	FnReview             = "review"
	FnAdhocReview        = "adhoc_review"
	FnReviewBranch       = "review_branch"
	FnReviewArchive      = "review_archive"
	FnFixArchive         = "fix_archive"
	FnReviewTasks        = "review_tasks"
	FnCheckCommitScope   = "check_commit_scope"
	FnResolveConflict    = "resolve_conflict"
	FnReviewConflict     = "review_conflict"
	FnReviewMigration    = "review_migration"
	FnFixMigrationRev    = "fix_migration_review"
	FnReviewMigrationFix = "review_migration_fix"
)

// 提示词 key（内置固定集合，不可删除、不可新增）= 场景.能力。
// 未来新功能若复用同名能力（如「解决冲突」），用新的场景前缀区分，例如 xxx.resolve_conflict。
const (
	PromptParseReport        = SceneFixer + "." + FnParseReport
	PromptFix                = SceneFixer + "." + FnFix
	PromptReview             = SceneFixer + "." + FnReview
	PromptAdhocReview        = SceneFixer + "." + FnAdhocReview
	PromptReviewBranch       = SceneFixer + "." + FnReviewBranch
	PromptReviewArchive      = SceneFixer + "." + FnReviewArchive
	PromptFixArchive         = SceneFixer + "." + FnFixArchive
	PromptReviewTasks        = SceneFixer + "." + FnReviewTasks
	PromptCheckCommitScope   = SceneFixer + "." + FnCheckCommitScope
	PromptResolveConflict    = SceneMigration + "." + FnResolveConflict
	PromptReviewConflict     = SceneMigration + "." + FnReviewConflict
	PromptReviewMigration    = SceneMigration + "." + FnReviewMigration
	PromptFixMigrationRev    = SceneMigration + "." + FnFixMigrationRev
	PromptReviewMigrationFix = SceneMigration + "." + FnReviewMigrationFix
)

// Scene 场景命名配置：前缀 + 中文名。
type Scene struct {
	Key  string `json:"key"`  // 前缀，如 "fixer"
	Name string `json:"name"` // 中文名，如 "审查修复"
}

// Scenes 内置场景命名配置（顺序即展示顺序）。
var Scenes = []Scene{
	{Key: SceneFixer, Name: "审查修复"},
	{Key: SceneMigration, Name: "代码迁移"},
}

// SceneName 返回场景前缀对应的中文名，未知则返回原前缀。
func SceneName(prefix string) string {
	for _, s := range Scenes {
		if s.Key == prefix {
			return s.Name
		}
	}
	return prefix
}

// Function 能力命名配置：能力名 + 中文名。
type Function struct {
	Key  string `json:"key"`  // 能力名，如 "parse_report"
	Name string `json:"name"` // 中文名，如 "拆解报告"
}

// Functions 内置能力命名配置（顺序即展示顺序）。
var Functions = []Function{
	{Key: FnParseReport, Name: "拆解报告"},
	{Key: FnFix, Name: "修复任务"},
	{Key: FnReview, Name: "对抗审查"},
	{Key: FnAdhocReview, Name: "人工发起审查"},
	{Key: FnReviewBranch, Name: "宏观回归审查"},
	{Key: FnReviewArchive, Name: "归档回归审查"},
	{Key: FnFixArchive, Name: "归档修复"},
	{Key: FnReviewTasks, Name: "任务质检"},
	{Key: FnCheckCommitScope, Name: "提交前检查"},
	{Key: FnResolveConflict, Name: "解决冲突"},
	{Key: FnReviewConflict, Name: "冲突解决审查"},
	{Key: FnReviewMigration, Name: "全量回归审查"},
	{Key: FnFixMigrationRev, Name: "修复回归问题"},
	{Key: FnReviewMigrationFix, Name: "审查回归修复"},
}

// FunctionName 返回能力名对应的中文名，未知则返回原能力名。
func FunctionName(key string) string {
	for _, f := range Functions {
		if f.Key == key {
			return f.Name
		}
	}
	return key
}

// PromptDef 描述一个 AI 任务的提示词模板元信息。
// Scene + Function 组成 key（「场景.能力」命名空间），能力中文名见 Functions 配置。
type PromptDef struct {
	Scene        string   `json:"scene"`    // 场景前缀：fixer / migration
	Function     string   `json:"function"` // 能力名：parse_report / fix / ...
	Key          string   `json:"key"`      // 完整 key = scene.function
	Description  string   `json:"description"`
	Placeholders []string `json:"placeholders"`
}

// promptDefs 内置提示词模板清单（顺序即页面展示顺序）。
var promptDefs = []PromptDef{
	{Scene: SceneFixer, Function: FnParseReport, Key: PromptParseReport, Description: "把代码审查报告拆解成独立、可执行的修复任务", Placeholders: []string{"project_background", "feedback", "report"}},
	{Scene: SceneFixer, Function: FnFix, Key: PromptFix, Description: "修复单个审查任务", Placeholders: []string{"task", "constraints", "feedback"}},
	{Scene: SceneFixer, Function: FnReview, Key: PromptReview, Description: "对抗性审查一次修复的 diff", Placeholders: []string{"task", "constraints", "diff", "new_files"}},
	{Scene: SceneFixer, Function: FnAdhocReview, Key: PromptAdhocReview, Description: "人类主动发起的一次性复查", Placeholders: []string{"task", "constraints", "diff", "new_files", "human_prompt"}},
	{Scene: SceneFixer, Function: FnReviewBranch, Key: PromptReviewBranch, Description: "对整条修复分支累积改动做完整性检查", Placeholders: []string{"constraints", "diff", "log"}},
	{Scene: SceneFixer, Function: FnReviewArchive, Key: PromptReviewArchive, Description: "归档后核对 commit 与任务对应关系", Placeholders: []string{"tasks_json", "diff", "log", "constraints"}},
	{Scene: SceneFixer, Function: FnFixArchive, Key: PromptFixArchive, Description: "在归档分支上修复最终回归审查发现的问题", Placeholders: []string{"constraints", "feedback"}},
	{Scene: SceneFixer, Function: FnReviewTasks, Key: PromptReviewTasks, Description: "对拆解出的任务列表做查漏/去重/粒度复查与误报过滤", Placeholders: []string{"project_background", "feedback", "report", "tasks_json"}},
	{Scene: SceneFixer, Function: FnCheckCommitScope, Key: PromptCheckCommitScope, Description: "确认提交 diff 是否只含当前任务相关改动", Placeholders: []string{"task", "diff", "new_files"}},
	{Scene: SceneMigration, Function: FnResolveConflict, Key: PromptResolveConflict, Description: "解决 git cherry-pick 冲突", Placeholders: []string{"constraints", "feedback", "files"}},
	{Scene: SceneMigration, Function: FnReviewConflict, Key: PromptReviewConflict, Description: "审查 AI 解决冲突的结果是否正确", Placeholders: []string{"files", "diff", "constraints"}},
	{Scene: SceneMigration, Function: FnReviewMigration, Key: PromptReviewMigration, Description: "审查迁移目标分支的累积改动，只挑明显问题", Placeholders: []string{"constraints", "diff", "log"}},
	{Scene: SceneMigration, Function: FnFixMigrationRev, Key: PromptFixMigrationRev, Description: "修复全量回归审查发现的明显问题", Placeholders: []string{"constraints", "feedback"}},
	{Scene: SceneMigration, Function: FnReviewMigrationFix, Key: PromptReviewMigrationFix, Description: "审查回归问题的修复是否正确", Placeholders: []string{"diff", "constraints"}},
}

// PromptDefs 返回内置提示词模板清单。
func PromptDefs() []PromptDef { return promptDefs }

// DefaultPrompt 返回某 key 的内置默认模板。
func DefaultPrompt(key string) string { return defaultPrompts[key] }

// defaultPrompts 内置默认提示词模板。{{name}} 为占位符，运行时由代码注入动态内容。
// 「段落型」占位符（constraints/feedback/project_background/new_files/human_prompt）为空时整体省略。
var defaultPrompts = map[string]string{
	PromptParseReport: `你是代码审查修复任务的拆解器。下面是一份代码审查(review)报告，格式可能千差万别。
请把报告拆解成一系列独立、可执行的修复任务。

要求：
1. 每个任务针对一个具体、独立的问题点。
2. 若报告里没有问题，tasks 返回空数组 []。
3. 涉及文件尽量给出真实相对路径：结合「项目背景」和当前项目目录里的代码，把报告中的模块名/服务名映射成具体文件路径；确实无法确定的留空。
4. 报告里凡是没有成为任务的条目（客套话、总结、无实质问题、与修复无关的内容等），都要列入 ignored 并说明忽略原因——不要静默丢弃。{{project_background}}{{feedback}}

只输出一个 JSON 对象，不要输出任何其它文字。结构如下：
{"tasks":[{"title":"问题一句话标题","description":"问题详情与期望的修复方向","severity":"high|medium|low","files":["可能涉及的文件路径，可为空"]}],"ignored":[{"item":"被忽略的报告片段","reason":"忽略原因"}]}

报告内容：
<report>
{{report}}
</report>`,

	PromptFix: `你是代码修复工程师。请修复下面这个审查任务。

任务：
{{task}}
{{constraints}}{{feedback}}
要求：
1. 只做最小必要改动，解决任务描述的问题；不要改动与任务无关的文件。
2. 优先修改已有文件；仅当修复确实需要时才可新建文件（例如新增模块/常量/测试文件），新建文件必须直接服务于本任务、内容最小必要。
3. 不要 commit、不要执行任何 git 操作。
4. 若问题已经不存在或无需修改，直接说明"无需修改"。
5. 改动前先用 Read 工具阅读目标文件，确认 import 块、以及被引用符号（常量/脚本/注册点/函数签名）的真实定义位置，不要臆测。
6. 改完必须做基本语法/编译自检：确保所有新使用的包/符号都已正确 import 或已在可访问范围内定义，代码能通过对应语言的语法检查（Go 用 gofmt/go build、Python 用 py_compile、JS 用 node --check、Shell 用 bash -n，其它语言做等价校验）。
7. 只改当前仓库内能改的代码：任务描述里超出本仓库范围、或已标注「超出本仓库范围」「不作为本任务要求」的诉求（如后端接口、其他服务/仓库的改动），不要尝试实现，也不要因此认为任务没完成。

修复完成后，只输出一个 JSON 对象：{"summary":"改了什么、为什么（如新建了文件，说明其必要性）","files":["修改或新建的文件相对路径"]}`,

	PromptReview: `你是一名极其挑剔、对抗性的代码审查员。你的目标是找出修复中的任何残留缺陷、引入的新问题、或与任务目标不符的改动。

任务要求：
{{task}}

项目约束（修复必须满足）：
{{constraints}}

修复产生的 diff：
<diff>
{{diff}}
</diff>{{new_files}}

请独立判断该修复是否真正解决了问题、是否引入了新缺陷、是否有多余或无关改动、是否违反项目约束。
只输出一个 JSON 对象，不要输出其它文字，结构如下：
{"verdict":"pass|fail","summary":"一句话结论","findings":[{"file":"文件","line":0,"severity":"high|medium|low","desc":"问题描述"}]}

判定标准：
- 若修复正确、无残留缺陷、无无关改动、且符合项目约束，verdict 为 "pass"，findings 为空数组。
- 若仍有问题，verdict 为 "fail"，findings 逐条列出。
- 范围外改动不算缺陷：任务描述里超出本仓库代码范围的诉求（如后端接口、数据库、其他服务/仓库的改动），在当前仓库内无法实现，不能作为 fail 的理由；若任务描述已明确标注「超出本仓库范围」「不作为本任务要求」等限定，则该项完全忽略，不得计入 findings、不得据此判 fail。

审查范围要求（必须覆盖，仓库就是当前工作目录，diff 的 ---/+++ 行给出了被修改文件路径）：
1. 用 Read 工具读取每个被修改文件的完整内容（含 import 块与上下文），核实是否存在缺 import、是否改错了对象、是否有残留调试代码或无关改动；严禁凭 diff 臆测"缺 import"或"改错了对象"。
2. 评估调用方影响：用 Grep 在全库搜索被修改的函数/类型/常量/结构体字段的所有引用点，逐一确认所有调用方是否仍能正确编译与运行，是否破坏了下游依赖的约定。
3. 对新增文件（见上方「新增文件」清单，或 diff 中 new file 的路径）做更严格审查：核实它是否确实是本任务所需、是否本应并入已有文件、内容是否最小必要；若新文件与本任务无关或可避免，verdict 判 "fail" 并逐条列出。
4. 先界定范围再下结论：判断任务描述里各项诉求哪些能在当前仓库（当前工作目录）内实现、哪些必须依赖本仓库之外的代码（后端、其他服务/仓库、数据库等）。只把「当前仓库内能实现」的诉求作为验收项；本仓库内无法实现的诉求一律不作为 fail 依据，最多在 summary 里一句话带过。`,

	PromptAdhocReview: `你是一名资深代码审查员。下面是一个已通过机器审查的修复任务及其 diff，请对它做一次独立、严格的复查，判断这个修复是否真的正确、稳健。

任务：
{{task}}
{{constraints}}
修复 diff：
<diff>
{{diff}}
</diff>{{new_files}}
{{human_prompt}}
审查要求：
1. 用 Read 工具阅读被修改文件的当前内容，用 Grep 在全库搜索被修改符号（函数/类型/常量/结构体字段）的引用点，核实是否存在残留缺陷、新引入的问题、破坏下游调用、多余或无关改动、违反项目约束。
2. 注意：diff 是该任务的历史改动，工作区文件可能已包含后续演进，请以 diff 为准判断「这次改动」本身是否正确，同时用当前代码核实其调用方影响。
3. 对新增文件做更严格审查：核实其是否确实是该任务所需、内容是否最小必要；无关或可避免的新增文件判 "fail"。

只输出一个 JSON 对象，不要输出其它文字：
{"verdict":"pass|fail","summary":"一句话结论","findings":[{"file":"文件","line":0,"severity":"high|medium|low","desc":"问题描述"}]}
- 若修复正确、无残留缺陷、无无关改动、符合约束，verdict 为 "pass"，findings 为空数组。
- 否则 verdict 为 "fail"，findings 逐条列出。`,

	PromptReviewBranch: `你是宏观回归审查员。下面是这条修复分支相对 base 分支的完整累积改动 diff（由多个独立修复任务的提交汇总而成），以及这些提交的日志。

你的任务：从整体上做一次完整性宏观审查，只挑出【明显有问题】的地方，不要逐行吹毛求疵，不要纠结风格、命名或无关紧要的细节；并对每个发现的问题，定位出它来源于哪个 commit。

重点检查：
1. 跨任务改动冲突或互相矛盾（某个改动把另一个任务刚修好的逻辑又改坏了）。
2. 破坏了下游调用的改动（改了函数签名/返回语义/常量取值/结构体字段后，其它调用方未同步，导致编译或运行出错）。
3. 明显不完整或自相矛盾的修复（只改了一半）。
4. 明显多余的、与任何修复目标都无关的改动。
5. 只审查本仓库内能实现的改动；任务描述里超出本仓库范围、或已标注「超出本仓库范围」「不作为本任务要求」的诉求（如后端接口、其他服务/仓库的改动），不作为问题。

项目约束（修复必须满足）：
{{constraints}}

完整 diff：
<diff>
{{diff}}
</diff>

提交日志（base..head）：
<log>
{{log}}
</log>

下结论前可用 Read/Grep 在仓库（当前工作目录）里核实关键改动与其调用方，但只对明显问题花时间。对每个发现的问题，用 git log -S「关键代码」或 git blame 定位引入它的 commit，并把该 commit 的完整哈希填到对应 finding 的 commit 字段。

只输出一个 JSON 对象，不要输出其它文字：
{"verdict":"pass|fail","summary":"一句话结论","findings":[{"file":"文件","line":0,"severity":"high|medium|low","desc":"问题描述","commit":"引入该问题的 commit 完整哈希"}]}

- 若整体没有明显问题，verdict 为 "pass"，findings 为空数组。
- 若发现问题，verdict 为 "fail"，findings 只列 high/medium 级别、置信度高的问题；low 且不明确的不要列；每个 finding 的 commit 字段必须填问题来源的完整 commit 哈希。`,

	PromptReviewArchive: `你是归档后的最终回归审查员。归档分支已按任务逐个迁移了「修复通过且人工批准」的任务，要求每个任务对应恰好一个 commit。

请重点核对 commit 与已归档任务的对应关系：每个归档任务是否有且仅有一个 commit，commit 主题与改动内容是否与对应任务描述相符，有没有多提交、漏提交、或提交了不该归档的内容。

已归档任务列表（JSON）：
<tasks>
{{tasks_json}}
</tasks>

归档分支相对 base 的完整累积 diff：
<diff>
{{diff}}
</diff>

归档分支 commit 日志（base..archive）：
<log>
{{log}}
</log>

项目约束（归档必须满足）：
{{constraints}}

下结论前可用 Read/Grep 在仓库（当前工作目录）里核实关键改动。只挑明显问题，不逐行吹毛求疵。

只输出一个 JSON 对象，不要输出其它文字：
{"verdict":"pass|fail","summary":"一句话结论","findings":[{"file":"文件","line":0,"severity":"high|medium|low","desc":"问题描述"}]}
- 若 commit 与任务一一对应，verdict 为 "pass"，findings 为空数组。
- 否则 verdict 为 "fail"，findings 只列 high/medium 级别、置信度高的问题。`,

	PromptFixArchive: `你是归档分支的最终修复工程师。最终回归审查发现归档分支仍存在问题，请通过修改文件来解决它们。

重要背景：
- 你现在位于归档分支上，该分支由多个已批准任务的提交逐个 cherry-pick 汇总而成。
- 你只改动文件内容（用 Read/Edit 工具），不要执行任何 git 操作、不要 commit。
- 优先修改已有文件；仅当问题确实需要时才可新建文件，新建文件必须直接服务于本修复、内容最小必要。
- 系统会把你修改后的文件内容合入（amend）归档分支最后一个提交，因此只做针对问题的增量修复，不要大范围重构。
- 若问题实际不存在、或已被其它改动解决，直接说明"无需修改"。

{{constraints}}最终回归审查发现的问题：
{{feedback}}

修复完成后，只输出一个 JSON 对象：{"summary":"改了什么、为什么","files":["修改过的文件相对路径"]}`,

	PromptReviewTasks: `你是代码审查修复任务的质检员。下面是一份原始 review 报告，以及据此拆解出的修复任务列表。

请分两步复查：

第一步，列表质量检查：
1. 遗漏：报告里有、但任务列表没有的问题。
2. 重复：同一问题被拆成多个任务。
3. 粒度不当：多个小问题应合并，或一个大问题应拆分。
4. 错误：标题/描述/严重度/涉及文件与报告不符。
5. 冲突合并：多个任务会修改同一文件/同一函数/同一代码区域时，应合并为一个任务（或显式调整顺序），避免修复时互相覆盖或产生矛盾。
6. 涉及文件给出真实相对路径：结合「项目背景」和当前项目目录里的代码修正涉及文件；确实无法确定的留空。

第二步，对抗性预检（误报过滤）：结合「项目背景」和当前项目目录里的实际代码，逐个质疑每个任务的真实性——报告说的这个问题是否真的存在、是否已被现有代码正确处理、是否是对代码的误解。判断为误报的任务，从任务列表移除，并列入 removed（附原因）。拿不准的保留（宁可不删，不可误删真实问题）。{{project_background}}{{feedback}}

只输出一个 JSON 对象，不要输出任何其它文字。结构如下：
{"tasks":[{"title":"问题一句话标题","description":"问题详情与期望修复方向","severity":"high|medium|low","files":["涉及文件路径，可为空"]}],"removed":[{"item":"被剔除任务的标题或描述","reason":"误报原因"}]}

原始报告：
<report>
{{report}}
</report>

当前任务列表：
<tasks>
{{tasks_json}}
</tasks>`,

	PromptResolveConflict: `你是代码合并冲突解决工程师。仓库当前处于 git cherry-pick 冲突态，下面这些文件里有 <<<<<<< / ======= / >>>>>>> 冲突标记。

请先判断这个 commit 是否应当跳过迁移：
- 若该 commit 解决后无实际改动（两侧内容等价、或该改动在目标分支已存在），应当跳过。
- 若结合项目约束判断该 commit 引入的改动全部是「不需要迁移的生成物」，应当跳过。
- 若除生成物外还有其它实质改动，则正常解决并保留实质改动（生成物按项目约束处理）。

若应当跳过，直接输出 action=skip，不要修改文件；否则按下面步骤解决冲突：
1. 用 Read 工具读取每个冲突文件的完整内容，理解冲突两侧的改动（一侧是目标分支现状，另一侧是被 cherry-pick 提交带来的改动）。
2. 用 Edit 工具修改文件，移除所有冲突标记，保留正确合并后的代码——既要落实被迁移提交的改动意图，又要与目标分支现状兼容、不破坏其它代码。
3. 只解决冲突，不要做无关重构，不要 commit、不要执行任何 git 操作（系统会自动暂存并继续）。
4. 改完做基本语法/编译自检（Go 用 gofmt/go build、Python 用 py_compile、JS 用 node --check、Shell 用 bash -n，其它语言等价校验）。{{constraints}}{{feedback}}

冲突文件：
{{files}}

只输出一个 JSON 对象：
{"action":"resolved|skip","summary":"冲突解决说明（resolved 时）或跳过原因（skip 时）","skip_reason":"跳过原因（仅 action=skip 时填写）","files":["已解决的文件相对路径（resolved 时）"]}`,

	PromptReviewConflict: `你是一名代码迁移冲突解决的审查员。下面是一个 cherry-pick 冲突，AI 已经解决了它。请审查 AI 的解决结果是否正确。

冲突文件：
{{files}}

解决后的完整改动 diff（相对 cherry-pick 前）：
<diff>
{{diff}}
</diff>{{constraints}}

请独立判断这次解决是否：
1. 残留冲突标记（<<<<<<< / ======= / >>>>>>>）。
2. 丢失了被 cherry-pick 提交的改动意图。
3. 破坏了目标分支的现有逻辑（改了不该改的地方、影响其它功能）。
4. 引入了无关改动（多余重构、调试代码等）。

只输出一个 JSON 对象，不要输出其它文字：
{"verdict":"pass|fail","summary":"一句话结论","findings":[{"file":"文件","line":0,"severity":"high|medium|low","desc":"问题描述"}]}
- 若解决正确、无冲突残留、无意图丢失、无破坏、无无关改动，verdict 为 "pass"，findings 为空数组。
- 否则 verdict 为 "fail"，findings 逐条列出。`,

	PromptCheckCommitScope: `你是提交前的最后一道检查。下面是一个修复任务和准备提交的 diff。

请判断：这个 diff 是否【只】包含当前任务相关的改动？是否混入了与当前任务无关的改动（例如其它任务/文件的改动、调试代码、无关重构）？
对新增文件要格外核实其必要性：是否确实是本任务所需、是否本应并入已有文件、内容是否最小必要。

任务：
{{task}}

diff：
<diff>
{{diff}}
</diff>{{new_files}}

只输出一个 JSON 对象，不要输出其它文字：{"verdict":"clean|polluted","reason":"一句话说明"}。
- 若 diff 只包含当前任务相关改动，verdict 为 "clean"，reason 可留空。
- 若混入了无关改动，verdict 为 "polluted"，reason 说明哪些是无关改动。`,

	PromptReviewMigration: `你是代码迁移的全量回归审查员。下面是代码迁移完成后的目标分支相对目标基底的完整累积改动 diff（由多个源 commit 迁移汇总而成），以及这些迁移提交的日志。

你的任务：从整体上做一次完整性审查，只挑出【明显有问题】的地方，不要逐行吹毛求疵。

重点检查：
1. 迁移冲突解决不当：某处冲突解决时丢失了源 commit 的改动意图，或引入了错误合并。
2. 破坏下游调用：改了函数签名/返回语义/常量取值/结构体字段后，其它调用方未同步，导致编译或运行出错。
3. 明显不完整或自相矛盾的迁移（只迁了一半）。
4. 明显多余的、与任何迁移目标都无关的改动。

项目约束（迁移必须满足）：
{{constraints}}

完整 diff（target_base..target_branch）：
<diff>
{{diff}}
</diff>

迁移提交日志：
<log>
{{log}}
</log>

只输出一个 JSON 对象，不要输出其它文字：
{"verdict":"pass|fail","summary":"一句话结论","findings":[{"file":"文件","line":0,"severity":"high|medium|low","desc":"问题描述"}]}
- 若整体没有明显问题，verdict 为 "pass"，findings 为空数组。
- 若发现问题，verdict 为 "fail"，findings 只列 high/medium 级别、置信度高的问题；low 且不明确的不要列。`,

	PromptFixMigrationRev: `你是代码迁移的修复工程师。全量回归审查发现迁移结果存在明显问题，请通过修改文件来解决它们。

重要背景：
- 你现在位于代码迁移的目标分支上，该分支由多个源 commit 迁移汇总而成。
- 你只改动文件内容（用 Read/Edit 工具），不要执行任何 git 操作、不要 commit。
- 优先修改已有文件；仅当问题确实需要时才可新建文件，新建文件必须直接服务于本修复、内容最小必要。
- 系统会把你的修改合入（amend）目标分支最后一个迁移提交，因此只做针对问题的增量修复，不要大范围重构。
- 若问题实际不存在、或已被其它改动解决，直接说明"无需修改"。

{{constraints}}全量回归审查发现的问题：
{{feedback}}

修复完成后，只输出一个 JSON 对象：{"title":"简短标题（一句话概括本次修复，不超过 30 字）","summary":"改了什么、为什么","files":["修改过的文件相对路径"]}`,

	PromptReviewMigrationFix: `你是一名代码迁移修复的审查员。下面是对全量回归审查发现问题的修复 diff，请审查修复是否正确。

修复 diff：
<diff>
{{diff}}
</diff>{{constraints}}

请判断：
1. 是否真正解决了全量回归审查发现的问题。
2. 是否引入了新的问题或无关改动。

只输出一个 JSON 对象，不要输出其它文字：
{"verdict":"pass|fail","summary":"一句话结论","findings":[{"file":"文件","line":0,"severity":"high|medium|low","desc":"问题描述"}]}
- 若修复正确、无新问题、无无关改动，verdict 为 "pass"，findings 为空数组。
- 否则 verdict 为 "fail"，findings 逐条列出。`,
}

// render 用参数替换模板中的 {{name}} 占位符。
func render(tmpl string, params map[string]string) string {
	out := tmpl
	for k, v := range params {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// section 构造一个可选段落：内容为空时返回空串（整个段落省略），否则返回「前导空行 + 标题 + 内容 + 收尾换行」。
func section(title, content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	return "\n\n" + title + "\n" + content + "\n"
}

// prompt 返回某 key 的内置默认提示词模板。
func (c *Client) prompt(key string) string {
	return defaultPrompts[key]
}
