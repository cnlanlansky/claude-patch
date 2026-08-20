# Claude Code Windows 多模型接入需求说明

> 状态：需求基线（纯 Go 产品实现已落地；本文未勾选或未附真实证据的项目仍不视为验收完成）
> 决策基线：2026-08-15
> Claude Code profile：2.1.233 已有历史 profile；2.1.237 已通过精确 profile、隔离启动与受控 fake Provider 行为证据，发布前仍须完成第 23 节所列的人工 Windows 对等验收
> OpenCode CLI 调研样本：1.18.18
> sub2api 源码样本：0.1.176
> Claude Code 源码资料：2026-03-31 公开快照，仅作定位线索，不视为当前官方源码
> 官方资料核对日期：2026-08-15

## 1. 文档目的

本项目为 Windows 上已安装的 Claude Code 追加一组经过项目验证的第三方模型，同时保留 Claude Code 原生界面、原生命令、原生模型和日常使用习惯。

本文定义产品需求、已确认边界和仍需真实环境完成的验收证据。当前实现已迁移为纯 Go 单程序；仍不得因实现困难擅自改用另一套命令或聊天客户端，也不得把单元测试覆盖等同于真实 Provider 或原生界面 smoke。

本文的规范性条目是验收目标，不等于完成声明；当前代码事实以 [`architecture.md`](architecture.md) 为准，只有附有相应证据的验收项才能标记完成。

### 1.1 需求来源与冲突裁决

1. 本文是详细需求与决策基线；用户最初描述用于说明动机和候选方向，与本文冲突时以本文已冻结的详细决定为准。
2. 需求、可信资料、源码线索和真实 API 结果必须分层记录；源码具备某能力，不等于线上服务已经提供该能力。
3. 对话中提供的测试凭据只用于后续获准的真实验证，不属于需求内容，不得写入本文、源码、fixture、日志或发布包。

| 最初描述中的说法 | 本文采用的精确定义 |
|---|---|
| 轻量“插件” | 产品形态是轻量本地工具，不预设 Claude Code 官方插件 API 能完成目标 |
| Windows、Linux、macOS | 长期只支持 Windows；首版只支持 Windows x64 |
| 所有模型支持 `max`，不支持就自动降级 | 不伪造支持，也不自动降级；按当前模型协议发送用户选择，不支持时保留服务商原始错误 |
| 不修改用户配置和文件 | 本工具不主动编辑 Claude/OpenCode 数据或 Claude executable；Claude Code 自身正常持久化仍被允许；工具只管理自己的 `config.json`、同目录 `claude.cmd`、当前用户 PATH 项和两个桌面设置注册表值 |
| 可以 hook Claude 或采用其他方式 | 已冻结为 launcher 新建子进程的内存适配；原生行为验收闸门失败就停止，不静默换成兼容客户端或项目命令 |
| 开发时不要断开当前会话 | 不附加、不修改、不重启任何已运行 Claude 进程；所有实验只发生在本工具新建的独立子进程 |

## 2. 产品定位

### 2.1 首批用户

首批用户是能够自行准备 API Key、理解服务商原始错误和修改本地配置文件的技术用户。

### 2.2 核心体验

用户先启动管理程序并显式安装命令代理，再从现有 CMD/PowerShell 运行 `claude` 后：

1. Claude Code 原来的 Opus、Sonnet 等模型仍在原生 `/model` 列表中并可正常使用；
2. 项目模型追加在同一个原生 `/model` 列表中；
3. 用户继续使用原生 `/model`、`/fast`、`/effort`、`/context`、状态栏和子 Agent；
4. 不提供 `/router-model`、`/router-fast`、`/router-status` 或任何其他项目 slash command；
5. Claude Code 原来如何选择、记住和恢复默认模型，本工具就保持该逻辑；
6. 用户可以在多个终端中同时运行彼此隔离的 Claude Code 会话。

### 2.3 当前交付物

当前交付物是单个 `claude-patch.exe`、同目录 `config.json` 和用户显式安装后生成的 `claude.cmd`。无参数入口只启动原生管理 GUI 与管理 Router；`claude [...]` 才在当前终端创建、patch、resume 并等待 Claude child。项目不包含安装器或自动升级器。

## 3. 已冻结的产品决定

以下决定已在需求访谈中确认，后续设计不得静默恢复被否决方案：

1. **平台仅限 Windows**：项目长期不承诺 Linux 或 macOS。
2. **只使用原生命令**：不得用项目 slash command、外部控制面或兼容聊天客户端替代原生 `/model`、`/fast`、`/effort`。
3. **采用新子进程内存适配**：launcher 启动用户现有 Claude Code 后，只修改这个新子进程的内存状态；不得修改磁盘上的 Claude 可执行文件，也不得注入已经运行的 Claude 进程。
4. **失败即停止**：内存适配无法稳定满足需求时，原生行为验收闸门失败并报告证据；不得自动改成隔离副本、项目命令、fork 或其他降级方案。
5. **固定支持 v2.1.233、v2.1.237**：Claude binary 必须先通过 Windows x64 PE、`.bun` 映射、精确 package identity、版本专属 EXE SHA-256、唯一适配 marker 和 suspended capability probe；其他版本或能力/marker 不完整时安全停止启动。
6. **原生模型保留**：Opus、Sonnet 等原生模型仍显示、可选，并直接连接 Anthropic。
7. **内置项目模型目录**：本版本内置已经确认的服务商和模型；用户可以在管理页显式新增或修改 Provider/模型，但本工具不会从远端目录自动发现、研究或接入陌生模型。
8. **不做上游可用性检查**：本地先按第 22 条过滤明显未配置的 Provider，但不向上游预检 Key 有效性、额度、地区或模型在线状态；请求发出后，服务商返回什么错误就原样显示什么。
9. **不自动降低 effort**：用户请求的推理强度不受支持时，保留服务商原始错误，不自动改成较低等级。
10. **fast 失败不重试**：服务商拒绝或限流 fast 请求时，原样显示错误，不自动按普通速度重放当前请求。
11. **Claude 数据沿用原机制**：继续使用用户原来的 Claude 设置、登录、聊天、缓存和恢复目录；允许 Claude Code 自己正常写入。本工具不得主动编辑这些文件。
12. **配置放在程序旁边**：本工具配置和真实 API Key 放在本工具程序目录中；管理 Router 热加载保存后的配置，已打开 Claude child 的 picker rows 不热更新，重新运行 `claude` 后完整生效。
13. **每命令会话一个本地 Router**：每个 `claude-patch.exe claude [...]` 进程在进程内持有独立 Router、session token、Provider Key 快照和 Claude child；管理页可以通过本机临时 registry 查看非敏感 session 元数据，但不能调用或停止其他 Router 的 session。
14. **原始上游错误**：401、403、429、502 等服务商错误不翻译、不包装、不改写。
15. **所有项目模型真实测试**：当前项目目录中的每个项目模型都执行真实 API 核心测试；难以稳定触发的失败路径同时使用本地 fixture。
16. **上下文上限取可信资料**：上下文窗口来自官方文档、官方模型目录或经验证资料，不通过向每个模型发送接近上限的海量输入来证明。
17. **最小介入原则**：只实现第三方模型接入所必需的差异；Claude Code 已有的默认模型、持久化、重试、历史、界面、错误处理和其他行为一律沿用，不在本工具重复实现或接管。
18. **首版仅支持 Windows x64**：Windows ARM64 不进入首版范围。
19. **配置文件为 `config.json`**：位于本工具可执行程序旁边。
20. **管理入口不启动 Claude**：无参数只启动单实例原生 GUI 与管理 Router；`--background` 只进入托盘；Claude child 只能由 `claude [...]` 创建。
21. **绿色命令代理**：目录固定为 `claude-patch.exe`、`config.json` 和显式安装后生成的 `claude.cmd`；只提供安装与卸载，不提供修复功能、Git Bash shim 或旧 `%LOCALAPPDATA%\ClaudePatch` 迁移兼容。
22. **Provider 可用性**：`opencode-free` 在有效 URL 且 `auth: none` 时无需 Key；其他 Provider 必须同时具备有效 HTTP(S) URL 与非占位 Key。未配置 Provider 的模型不进入 `/model`、`/v1/models` 或消息路由。
23. **管理页免登录**：管理 Router 只绑定 loopback 并拒绝非 loopback Host，管理页面与管理 API 直接访问；Claude `/v1/*` 会话接口仍必须验证每 session 随机 token。
24. **命令沿用当前终端**：正式 GUI 子系统 EXE 在命令模式附着父控制台，Claude child 继承标准句柄，不创建新的 Console 窗口。

## 4. 产品目标

### 4.1 核心目标

1. 原生 `/model` 同时展示原生模型与项目模型。
2. 模型切换后，请求目的地、上下文窗口、自动 compact、effort、fast 和子 Agent 状态原子同步。
3. 项目模型上的原生 `/fast` 永远不得切换到 Opus 或其他模型。
4. 原生 `/effort` 的用户选择按当前模型协议发送；不支持时保留上游错误。
5. 所有新建子 Agent 强制继承父会话创建时的模型和 effort，包括原生模型、项目模型、内置 Agent 和自定义 Agent。
6. 原生模型请求不经过本地 Router，继续直连 Anthropic。
7. 项目模型请求只经过当前命令会话自己的 loopback Router。
8. 当前已经运行的其他 Claude 会话不受安装、启动、切换、退出或故障影响。
9. 多个本工具命令会话可并行运行，Router、端口、session token 和 Claude child 互不干扰。
10. 当前 Claude Code 版本只有通过能力探针后才能启动真实会话。

### 4.2 成功结果

用户通过本工具打开 Claude Code 后，可以：

- 在原生 `/model` 中选择原生模型或项目模型；
- 在同一对话里跨服务商切换并保留 Claude Code 原生历史；
- 使用原生 `/fast`，且项目模型 ID 前后不变；
- 使用原生 `/effort`，并看到服务商真实接受或拒绝的结果；
- 使用原生 `/context` 和状态栏查看当前模型对应的上下文状态；
- 创建子 Agent，并确认其模型和 effort 与父会话创建时状态一致；
- 同时运行多个命令会话，关闭后不遗留 Router 元数据或受管 child；
- 继续让普通 Claude Code 按其自身机制读写设置和会话数据。

## 5. 非目标

本项目不包含：

1. Linux 或 macOS 支持；
2. 项目自有 slash command 或聊天终端（浏览器管理页只负责 Provider、模型与 session 管理）；
3. 修改、替换或复制用户磁盘上的 Claude Code 可执行文件；
4. 修改已经运行的 Claude 进程；
5. 运行时自动发现、验证或接入远端目录中的服务商或模型；用户显式填写的自定义 Provider/模型仍由用户负责其协议兼容性；
6. 运行时自动研究、测试并接入 OpenCode 新模型；
7. 自研任意私有协议都能接入的通用网关；
8. Provider 注册、充值、额度管理、价格提醒或账单保护；
9. 向上游预检 Key 有效性、额度、地区或模型健康状态；本地 URL/非占位 Key 完整性过滤除外；
10. 自动降低 effort；
11. fast 失败后的自动标准速度重试；
12. 自动换模型或跨 Provider 故障转移；
13. 修改 Claude、OpenCode、系统代理、证书或系统级环境配置；用户显式安装/卸载命令代理所需的当前用户 PATH，以及两个桌面设置的 HKCU 值除外；
14. 关闭、绕过或自动修改 Windows 安全保护；
15. 对所有项目模型发送接近各自上下文上限的昂贵验证请求；
16. 远程下发可执行适配代码。

## 6. 模型目录

### 6.1 原生模型

Claude Code 自带的 Opus、Sonnet 等模型继续由 Claude Code 自己管理：

- 原生 `/model` 中继续显示；
- 使用用户原有 Claude 登录和账号权限；
- 请求直接发往 Anthropic，不经过项目本地 Router；
- `/fast`、`/effort`、上下文、计费和错误语义保持 Claude Code 原样；
- 唯一有意改变的行为是第 11 节定义的子 Agent 强制继承。

### 6.2 sub2api

- Base URL：`https://i.bug.pics`
- 本版本模型：
  - `gpt-5.6-sol`
  - `gpt-5.6-terra`
  - `gpt-5.6-luna`
- 优先协议：Anthropic Messages
- 上游账号类型：OpenAI OAuth。
- 当前资料存在必须在真实 Provider 验收中闭环的差异：sub2api 0.1.176 模型资源记录三个 GPT-5.6 模型的 1,050,000 max input 与 128,000 max output；OpenCode 1.18.18 的 Codex OAuth 适配则把三个模型限制为 500,000 context、372,000 max input、128,000 max output。
- 当前源码证据只确认 OAuth GPT-5.6 的 effort 候选为 `none/low/medium/high/xhigh`，不确认 `max`。因此在 `i.bug.pics` 真实账号验证完成前，本工具不得把 1.05M 或 `max` 标成已确认能力；最终内置 profile 取线上路径可重复验证的有效上限。

本地 sub2api 源码已经实现 Anthropic Messages 到 OpenAI Responses/Chat Completions 的转换、SSE、tools、thinking、usage、错误归一化、`count_tokens` 和 fast/effort 转换。本项目优先复用线上部署已验证的能力，不复制 GPT 大型转换链。

源码能力不等于 `i.bug.pics` 当前部署契约；三个模型、普通请求、compact、`count_tokens` 和 priority 都必须用真实账号验证。

### 6.3 DeepSeek

- Anthropic Base URL：`https://api.deepseek.com/anthropic`
- 本版本模型：
  - `deepseek-v4-flash`
  - `deepseek-v4-pro`
- 当前官方资料：1,000,000 context、384,000 max output。

官方 Anthropic 兼容入口优先。已知限制包括：部分 Anthropic headers 被忽略，`thinking.budget_tokens`、`tool_result.is_error` 和 `service_tier` 不受支持，MCP/code-execution 专用块不受支持。官方页面只说明 `output_config` 会处理 `effort`，没有公布可接受档位，因此 `max` 仍属于真实 API 待验证能力。

DeepSeek 不提供本项目语义下已确认的同模型 fast 通道。`messages/count_tokens` 是否可用必须通过真实凭据验证，不能由匿名 401 推断。

### 6.4 OpenCode Go

- Base URL：`https://opencode.ai/zen/go/v1`
- 当前发行目录固定为 5 个模型；OpenCode runtime `/models` 的额外结果不自动接入：
  - Chat Completions：`deepseek-v4-flash`、`mimo-v2.5`、`hy3`、`deepseek-v4-pro`；
  - Anthropic Messages：`minimax-m3`。
- 新模型等待 Claude Patch 升级并完成协议与行为验证后再纳入。

### 6.5 OpenCode Zen 免费模型

- Base URL：`https://opencode.ai/zen/v1`
- 当前项目保留三个 Free 模型：
  - `deepseek-v4-flash-free`
  - `big-pickle`
  - `mimo-v2.5-free`
- 当前主协议：Chat Completions。

“免费”只描述本版本取样时 OpenCode 目录中的零价格状态，不构成永久免费、匿名可用或免账单承诺。本工具不在运行时监控价格变化。

### 6.6 OpenCode 协议确认方法

开发阶段使用两类证据确认 OpenCode 模型路由：

1. 静态分析 OpenCode CLI 源码和 Models.dev catalog 的 `provider.npm`；
2. 将本机 OpenCode CLI 请求重定向到一次性 loopback fixture，抓取真实 endpoint、body 和鉴权 header，不发送付费推理请求。

当前已验证映射：

| Catalog SDK | 上游协议 |
|---|---|
| `@ai-sdk/openai` | OpenAI Responses |
| `@ai-sdk/openai-compatible` | OpenAI Chat Completions |
| `@ai-sdk/anthropic` | Anthropic Messages |
| `@ai-sdk/google` | Google 原生协议，本版本不因目录出现而自动支持 |

不得依据模型名称猜协议。

### 6.7 Claude 侧模型标识

项目模型使用 `claude-router/<服务商>/<上游模型 ID>` 作为 Claude 侧稳定 ID，并映射回服务商真实 ID。菜单继续使用友好的 `display_name`。

## 7. 工具配置

### 7.1 位置与生效时机

1. 配置文件固定为本工具可执行程序旁边的 `config.json`；
2. 真实 API Key 可以直接写入该本地配置；
3. 配置只由本工具读取，不写入 Claude 或 OpenCode 配置；
4. 配置文件不存在时，使用随本工具版本内置的当前 Provider/model catalog；Sub2API 地址和需要认证的 Provider Key 仅使用占位值，不含真实凭据，首次通过管理页保存设置时才创建 `config.json`；
5. 管理 Router 保存后立即采用新配置；已打开 Claude child 的 picker rows 是启动快照，不热更新，重新运行 `claude` 后完整生效；
6. 多个命令会话各自在启动时读取同一份配置快照。

### 7.2 可配置范围

用户可以在管理页新增、修改或停用 Provider 与模型。当前配置字段支持：

- Provider label、Base URL、API Key、认证方式与默认上游协议；
- 模型 Claude 侧 ID、显示名称、Provider、上游模型 ID；
- 模型级协议覆盖、context、fast profile 与启停状态；
- 模型顺序。

自定义项只按用户填写的契约路由；本工具不自动探测其能力、验证在线状态或把远端目录中的未知模型自动写入配置。

### 7.3 校验

1. 配置语法损坏、必需字段缺失、模型 ID 重复或字段类型错误时，指出具体位置并停止启动；
2. Key 是否真实有效不在启动时验证；占位或空 Key 只会让非 `opencode-free` Provider 保持“未配置”，其模型不暴露；
3. Base URL 必须是无凭据、query 和 fragment 的绝对 HTTP(S) URL；`.invalid` 占位域名不算已配置；
4. 管理 API 不回显 Key，只返回 `hasApiKey` 与派生的 `configured`，并拒绝客户端伪造这两个只读字段；
5. 用户填写的 context、fast 与协议只做结构/范围校验，不做线上能力探测；
6. 文件名与字段结构已冻结为当前 `config.json` schema，不得改变其位置、生效时机或安全边界。

## 8. 原生 `/model`

### 8.1 列表

1. 原生 picker 保留 Claude Code 自带模型；
2. 只有 `enabled` 且所属 Provider 已配置的项目模型才追加到同一 picker；同一过滤规则也用于 `/v1/models` 与消息路由；
3. 默认目录包含 13 个候选模型，但缺省只有 `opencode-free` 已配置，因此默认只追加它的 3 个模型；
4. 不从远端目录自动加入陌生新模型；用户显式配置的自定义项除外；
5. `display_name` 使用模型配置中的友好 label；context 与 fast 通过 picker row 的独立字段注入；
6. 本地“已配置”只表示 URL/Key 结构完整，不代表 Key、额度、地区或模型在线状态已通过上游预检。

### 8.2 默认模型与持久化

1. Claude Code 原来如何决定启动模型，本工具就如何决定；
2. Claude Code 是否记住最后选择，本工具不干预；
3. 即使最后选择的项目模型影响下一次普通 `claude` 启动，本工具也不在退出时恢复旧模型；
4. Claude 原生写入设置或会话数据属于允许行为。

### 8.3 切换

1. 切换必须原子更新模型、路由、context、compact、effort、fast 和子 Agent 创建状态；
2. 切换失败时保持原完整状态；
3. 已经运行的请求和子 Agent 不应中途换模型；
4. 切换影响下一次主会话请求和之后新建的子 Agent；
5. 当前模型下线、Key 无效或服务商故障时不自动换模型，服务商错误原样显示，由用户使用原生 `/model` 选择。

## 9. 原生 `/fast`

### 9.1 产品语义

`/fast` 表示请求当前模型可用的同模型低延迟或高优先级通道。

硬性规则：

1. `/fast on` 和 `/fast off` 不得改变当前模型 ID、Provider 或 effort；
2. 原生 Anthropic 模型保持 Claude Code 原有 fast 行为；
3. 项目模型有已验证 priority 能力时，映射真实服务等级；
4. 项目模型没有同质量 fast 能力时，保持当前模型和 effort，按标准速度运行并显示一次原生提示；
5. 不得通过降低 effort、缩短输出或换模型伪装 fast；
6. Provider 拒绝或限流 fast 时，原始错误直接结束当前请求；不自动标准速度重试，fast 开关之后保持什么状态沿用 Claude Code 自身逻辑；
7. 已输出文本、thinking 或 tool call 后绝不重放。

### 9.2 可行性难点

Claude Code 2.1.233 的原生 `/fast` 会在不支持模型上于 HTTP 请求之前切换 Opus。公开 Skills、hooks 和插件 API 均不能覆盖内置命令或通用 AppState。因此必须在新 Claude 子进程内控制原生命令状态；普通 sidecar 改写 HTTP model 字段不满足要求。

## 10. 原生 `/effort`

1. 用户通过原生 `/effort` 选择推理强度；
2. Router 按当前模型协议将该值发送到明确对应的上游字段；
3. 不支持的值不自动降低；
4. 服务商返回 400 或其他错误时原样显示；
5. 配置可以声明该模型支持的等级和协议字段，但不能把自动降级冒充为原请求；
6. 原生界面显示当前请求 effort；没有成功应用时不得显示成已接受；
7. compact 使用什么 effort 沿用 Claude Code 自身逻辑；本工具只确保它看到当前模型的正确能力。

## 11. 子 Agent 继承

1. 所有新建子 Agent 都继承直接父会话启动该 Agent 时的模型和 effort；
2. 该规则同时适用于原生模型和项目模型；
3. Agent frontmatter、Agent Tool 显式参数、内置 Explore/Plan 默认值和 `CLAUDE_CODE_SUBAGENT_MODEL` 不得静默覆盖；
4. Team 子进程的 model/effort 参数也必须来自直接父会话快照；
5. 继承 model/effort 的 resolver、实际请求边界和 Team launcher 任一 marker 缺失或不一致时，child 在 resume 前失败关闭；
6. 不允许回退到 Haiku、Opus、Sonnet、Provider 默认模型或其他 effort；
7. 子 Agent 使用继承模型对应的 context 和 compact 阈值。

这项规则是原生模型行为唯一明确允许改变的部分。

## 12. 上下文与原生 compact

### 12.1 能力来源

1. context、max input 和 max output 来自本工具版本内置的可信资料；
2. 用户配置可在 schema 允许的范围内覆盖，结构合法时按配置执行；本工具不把用户覆盖值冒充为已验证官方能力；
3. 不从模型 ID、`display_name` 或 `[1m]` 后缀猜测非 1M 窗口；
4. 不通过向每个线上模型发送接近上限的输入来证明窗口；
5. 窗口切换、剩余量和 compact 行为使用本地 fixture 做确定性边界验证。

### 12.2 动态切换

1. 原生 `/context`、剩余上下文、超限预警和自动 compact 必须随当前模型切换；
2. 200K/256K 模型不得显示或使用 1M；
3. 切到更大窗口时立即采用新上限；
4. 切到更小窗口且当前历史装不下时，先自动执行 Claude 原生 compact；
5. compact 成功后完成切换并提示一次；
6. compact 失败时保持原模型和原历史；
7. compact 阈值和安全余量沿用 Claude Code 自身计算；本工具只同步当前模型的正确窗口，不另造一套公式。

### 12.3 跨服务商历史

1. 正常跨服务商切换沿用 Claude Code 自身历史，不额外提示；
2. 若旧服务商专属 thinking/signature 等内部块无法被新服务商接受，则自动使用旧模型执行 Claude 原生 compact；
3. compact 成功后切换，并事后提示发生过历史压缩；
4. compact 失败时保持旧模型，不发送半转换历史；
5. Router 不自行静默删除或伪造 reasoning/signature。

## 13. 请求路由与协议

### 13.1 路由

- 原生 Claude 模型：直接连接 Anthropic；
- sub2api：优先 Anthropic Messages；
- DeepSeek：优先官方 Anthropic compatibility；
- OpenCode Go/Zen：按第 6 节已验证的 per-model 协议；
- 路由依据是项目模型 profile，不按裸模型名猜测。

### 13.2 本地 Router

每个 `claude-patch.exe claude [...]` 命令进程在进程内持有一个 loopback Router，并监督一个 Claude child：

1. 只监听 `127.0.0.1` 的随机端口并拒绝非 loopback Host；
2. 使用每 session 32 字节随机本地 token；
3. 持有项目模型 API Key，不把 Key 注入 Claude child 或工具环境；
4. 支持 `/v1/models`、`/v1/messages/count_tokens`、`/v1/messages`、SSE streaming、取消和项目模型所需转换；
5. `count_tokens` 透明转发给对应 Provider，不伪造 token 数或自研估算器；
6. 不处理原生 Anthropic 模型流量；
7. 不在命令会话之间共享 token、模型快照或请求；本机管理页只读取 registry 中的非敏感 session 元数据。

### 13.3 必须保留的语义

至少正确处理：

- system block 顺序；
- messages 与多种 content block；
- tool definitions、`tool_choice`；
- `tool_use`、`tool_result` 和 ID 关联；
- thinking/reasoning block；OpenCode Free 与 OpenCode Go 的 Chat Completions 使用 `reasoning_content` 与 Anthropic `thinking` 做无签名往返；
- effort 字段；
- streaming delta 和事件顺序；
- usage、stop reason；
- cache control；
- 用户取消；
- Provider 原始错误状态和正文；
- Claude Code 新字段的前向转发。

对 Anthropic 格式上游尽量开放转发。对非 Anthropic 上游只能显式转换已支持语义；不得无提示丢弃关键内容。

### 13.4 Streaming 与重试

1. Anthropic 上游在不需要 server Web Search 聚合时逐块转发并主动 flush；协议转换或 server Web Search 响应会在 16 MiB 上限内聚合；
2. 用户取消时传播到上游；
3. 已输出任何内容后不自动重放；
4. fast 失败不重试；
5. 本工具不新增普通请求重试策略；Claude Code 原来如何重试就继续如何重试；
6. Router 保留上游状态、正文和可安全透传的响应头，使 Claude Code 按原机制处理错误。

## 14. 新子进程内存适配

### 14.1 边界

1. launcher 定位用户当前 `claude` 可执行程序；
2. 启动一个新的、受 launcher 管理的 Claude 子进程；
3. 只修改该子进程内存，不改磁盘文件；
4. 不附加到启动工具之前已经运行的 Claude；
5. 不使用固定文件 offset 或固定源码行号；每个已支持版本均使用独立唯一 marker，版本结构变化时安全拒绝；
6. 不关闭 Windows Defender、EDR 或系统保护；
7. 普通权限失败时，展示系统返回的原因和用户可手动采取的权限操作，工具不自动修改安全设置。

### 14.2 能力探针

启动真实会话前至少验证：

- 原生 `/model` 注册表和选择状态可安全控制；
- 原生 `/fast` 可保持当前项目模型；
- 原生 `/effort` 可读取并随请求传递；
- 每模型 context 和 compact 状态可动态更新；
- 子 Agent 统一模型解析点可强制继承；
- 原生模型可保持直连 Anthropic；
- 项目模型可路由到当前命令会话的 Router；
- 所有目标位置唯一匹配并通过前后置结构校验；
- patch 后自检能恢复读取预期状态。

任何必要探针失败：

1. 不启动真实 Claude 会话；
2. 报告 Claude binary hash、版本、失败探针和缺失能力；
3. 不尝试替代命令、磁盘副本、原地 patch 或兼容客户端；
4. 当前只接受已支持 profile 的适配 marker；其他版本或 marker 不匹配时安全拒绝。

### 14.3 版本边界

当前发行版只适配 Windows x64 Claude Code v2.1.233、v2.1.237。每个版本使用独立 package identity、EXE SHA-256 和 13 个独立 marker profile；测试使用官方 npm Windows x64 候选包，写入系统临时目录；候选 child 可在隔离 smoke 中 patch、resume 并执行 `--version`，`--self-check` 则始终保持 suspended，完成后 terminate/close，不 resume。

启动时仍执行 package identity、版本专属 EXE SHA-256、PE、`.bun` 映射、唯一 marker 和内存回读校验。v2.1.233、v2.1.237 以外的版本不保证兼容；package identity、hash、marker、映像路径、映射内容或内存权限任一不匹配，均在 child resume 前安全拒绝。

后续适配新版本时，以版本号最高的已支持 profile 的 13 个逻辑 patch 点为语义模板，逐点迁移到新 bundle；先完成独立 profile、静态唯一性和 replacement 语义对照，再做最小 patch/resume smoke 与用户指定的行为验证。不得以反复扩展 TTY、fake Provider 或全量交互实验代替源码级迁移；所有临时取证仅放系统临时目录并在完成后清理。

## 15. Windows 进程生命周期

1. 永久只支持 Windows，当前仅支持 x64；
2. 无参数和 `--background` 管理入口用当前用户命名 mutex 保证单实例；重复普通启动只唤起已有窗口，重复后台启动静默退出；
3. 每次 `claude [...]` 创建独立命令进程、进程内 Router、随机端口/session token 和一个 suspended Claude child，不受管理入口单实例限制；
4. Claude child 加入 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` Job；命令进程异常退出或关闭 Job 时，Windows 终止其 child；
5. 命令正常结束、GUI 真正退出或托盘明确退出时，Runtime 先停止并关闭自己创建的全部 Claude child，再停止 Router；管理入口随后终止固定名称 Job Object 中主动加入的本工具命令进程。命令入口正常退出只关闭自己的 Job 句柄，不广播兄弟会话；Job 成员关系不依赖 EXE 文件名，因此改名后的本工具仍可清理。重复 Stop 幂等，Router 注册竞态不得遗漏 child；
6. Claude child 正常退出后，命令进程移除自己的 registry 记录、停止 Router、关闭句柄并透传退出码；
7. Router 与 launcher 属于同一 Go 进程，不存在独立 sidecar 的父进程探测或崩溃重启；本地失败结束当前命令会话，不自动重放；
8. registry 枚举发现 Router PID 已不存在或记录损坏时，删除对应陈旧元数据；
9. 不使用系统常驻服务；管理 GUI 仅在用户显式开启开机启动后通过当前用户 Run 项登录启动，并可从托盘明确退出；
10. 替换 EXE 不得覆盖同目录 `config.json`；卸载命令代理默认保留 EXE 与配置文件；
11. 原生 GUI 只提供命令代理安装和卸载入口，不要求用户手工编辑 Claude 或 OpenCode 文件；
12. 二进制名称固定为 `claude-patch.exe`；双击只打开管理程序，`--background` 只在托盘启动管理 Router；
13. “登录 Windows 后启动”默认关闭，“关闭窗口后隐藏到托盘”默认开启；两项只在用户点击时写入本工具自己的 HKCU 注册表值；
14. GUI 子系统 EXE 的命令模式附着父控制台，Claude child 继承标准输入、输出和错误句柄，不创建新 Console 窗口。

## 16. 凭据、安全与隐私

### 16.1 凭据

1. 项目模型真实 Key 可保存在程序旁边的工具配置中；
2. 配置文件不得进入源码仓库、fixture、快照、日志或发布包；
3. 本工具不检查或修改 `config.json` 的 Windows 文件权限；文件放在哪里、谁能读取由用户管理；
4. Router 读取真实 Key；Claude child 只看到本地 session token；
5. 空 Key、占位 Key 或无效 URL 会让非 `opencode-free` Provider 保持未配置，其模型不暴露；结构完整但实际无效的 Key 由服务商在请求时返回原始错误；
6. 不弹出隐藏输入框，不自动读取 OpenCode auth 文件；除用户显式安装/卸载命令代理所需的当前用户 PATH 外，不修改其他系统环境变量。

### 16.2 Claude 与 OpenCode 数据

1. 本工具使用 Claude Code 原来的配置和数据位置；
2. Claude Code 正常写入 settings、登录刷新、history、projects、sessions、cache 和 file history 属于允许行为；
3. 本工具自身不得主动编辑这些文件；
4. 本工具不得读取或修改 OpenCode `auth.json`、配置或安装；开发阶段只把 OpenCode CLI 当作协议取证样本；
5. 当前已运行 Claude 会话的环境、模型、凭据和连接不得变化。

### 16.3 日志

禁止持久记录：

- Authorization、API Key 和本地会话令牌；
- 完整 prompt、tool result 或源码正文；
- 完整自定义 headers；
- URL query token；
- Provider 可能回显的凭据。

当前版本不写磁盘日志；本地错误只显示在调用终端或 GUI 错误框。不得持久化 prompt、响应正文、API Key、session token 或完整上游错误正文。

服务商错误在当前 Claude 交互中原样显示，但 Router 不得把完整错误正文额外持久化到诊断日志。

## 17. 错误与回退

### 17.1 上游错误

Provider 返回的 HTTP 状态和正文原样传给 Claude Code：

- 不翻译；
- 不添加 Provider/模型前缀；
- 不将 401 猜成缺 key；
- 不将 429 猜成固定原因；
- 不自动换模型；
- 不自动降低 effort；
- 不自动把 fast 改回标准速度重试。

### 17.2 本地错误

工具自己产生的错误必须明确、可行动：

- 配置文件位置和字段错误；
- Claude binary 未找到；
- 能力探针失败；
- 内存访问权限不足；
- loopback 端口失败；
- 转换层不支持某个 Claude content block；
- Router、registry 或 child 清理失败。

### 17.3 允许的自动动作

仅允许：

1. 切到更小 context 前由 Claude 原生机制执行 compact；
2. 跨服务商历史不兼容时由 Claude 原生机制处理历史；
3. registry 枚举时删除 Router PID 已不存在或内容损坏的陈旧元数据。

本工具不自动重启 Router，不重放已经产生文本或 tool call 的请求。

## 18. 原生行为验收闸门

### 18.1 四个核心闭环

发布或完整能力声明前必须以真实 Claude 交互证明：

1. **原生 fast**：在每个项目模型上执行原生 `/fast`，模型 ID 不变；
2. **动态 context**：200K、256K、1M、1,048,576、1.05M context 能在同一 Claude 对话中正确切换，原生 `/context`、剩余量和 compact 同步；
3. **子 Agent 继承**：内置与自定义子 Agent 均强制继承父模型和 effort；
4. **进程边界**：只修改本工具新建 Claude 子进程的内存；不改 Claude binary，不影响已运行会话；Claude child 退出后当前命令会话的 Router 随进程停止。

同时验证原生模型继续直连 Anthropic，项目模型走当前命令会话的 Router。

### 18.2 闸门结论

- 四项全部通过并留存证据：可以声明对应原生能力完成；
- 任一项无法稳定复现：停止对应发布声明，并报告失败探针、复现步骤和证据；
- 不允许用项目命令、磁盘 patch、隔离 Claude 副本或自有客户端把失败包装成成功。

## 19. 测试与验收

### 19.1 真实 API 范围

本版本默认目录共 13 个候选项目模型；真实 API 验收范围覆盖全部候选模型，但运行时只暴露已配置 Provider 下启用的模型：

- Sub2API：3 个；
- DeepSeek：2 个；
- OpenCode Go：5 个；
- OpenCode Free：3 个。

每个模型都执行真实 API 核心清单：

1. 最短文本 streaming；
2. thinking/reasoning；
3. 强制 tool call；
4. tool result continuation；
5. 当前配置声明的 effort；
6. fast：有正式能力才做 standard/fast 配对；无能力则验证不换模型；
7. `count_tokens`：仅在项目声明支持时验证；
8. 用户取消；
9. usage、stop reason 和原始错误传播；
10. 正常规模上下文请求。

不对每个模型发送接近最大 context 的海量输入。

### 19.2 本地 fixture

本地 fixture 验收范围：

- 200K、256K、1M、1,048,576、1.05M 模型切换；
- 大历史切小窗口的自动 compact；
- 跨 Provider 私有 reasoning/signature 不兼容；
- SSE 长 thinking、ping 和断流；
- 首字节前失败与已输出后失败；
- 多 tool call ID；
- 取消传播；
- 401、403、429、502 原样透传；
- fast 拒绝且不重试；
- effort 不支持且不降级；
- Router 停止、拒绝停止后的新 session，以及 registry 陈旧记录清理；
- 多命令会话隔离和关闭清理。

### 19.3 原生界面验收

- [ ] `/model` 保留原生模型，并只追加已配置 Provider 下启用的项目模型；默认配置只追加 3 个 OpenCode Free 模型；
- [ ] 项目模型显示配置中的友好 label，并使用对应的 context 与 fast profile；
- [ ] 默认模型与记忆逻辑保持 Claude 原样；
- [ ] 原生模型请求直连 Anthropic；
- [ ] 项目模型请求进入当前命令会话的 Router；
- [ ] `/fast` 不改变项目模型 ID；
- [ ] `/effort` 不受支持时上游错误原样显示；
- [ ] `/context` 随模型切换显示正确窗口；
- [ ] 不存在任何项目 slash command。

### 19.4 子 Agent 验收

至少覆盖原生 Anthropic、sub2api、DeepSeek、OpenCode Go 和 OpenCode Zen：

- [ ] 普通 Agent 继承直接父会话启动时的 model 和 effort；
- [ ] Explore/Plan 等内置 Agent 不静默切其他模型；
- [ ] frontmatter 和显式 Agent 参数不能覆盖强制继承；
- [ ] Team 子进程的 model/effort 参数来自直接父会话；
- [ ] 继承 marker 或 mapped 校验失败时 child 不 resume，不降级。

### 19.5 配置与生命周期验收

- [ ] 配置损坏时指出字段并停止启动；
- [ ] 未配置 Provider 不阻止管理程序或 Claude 启动，但其模型不进入 picker 或路由；
- [ ] 管理页正确区分 `hasApiKey` 与 `configured`，并且不回显 Key；
- [ ] 保存配置后管理 Router 立即采用新配置，已打开 Claude child 的 picker rows 保持启动快照；
- [ ] 多命令会话使用独立进程内 Router、端口、session token 和 child；
- [ ] Claude child 退出后当前命令进程停止 Router、移除 registry 记录并透传退出码；
- [ ] 命令进程异常退出时，kill-on-close Job 终止其 Claude child；
- [ ] registry 枚举能清理 Router PID 已不存在或内容损坏的陈旧记录；
- [ ] 本工具不主动写 Claude/OpenCode 配置；
- [ ] Claude 自身正常数据写入不被误报为污染；
- [ ] 当前已运行 Claude 会话不受影响；
- [ ] 安装只添加本工具自身文件和启动入口，不修改 Claude/OpenCode 文件或系统级网络与环境配置；
- [ ] 开机启动、关闭到托盘、托盘明确退出及管理入口单实例行为符合默认值，并且只管理本工具拥有的 HKCU 值；
- [ ] 更新不覆盖 `config.json`，也不干扰已经运行的 Claude 会话；
- [ ] 卸载只移除本工具自身内容，默认保留 `config.json` 并明确显示位置，仅在用户显式选择时删除；
- [ ] Windows x64 用户可通过单一、文档化入口完成安装、更新和卸载，无需手工编辑 Claude 或 OpenCode 文件。

### 19.6 版本验收

- [x] 2.1.233、2.1.237 各自通过 package identity、EXE SHA-256、PE、`.bun`、唯一 marker、suspended capability probe 与隔离 `--version` smoke；
- [x] 其他 Claude 版本不因 PE probe 或相似 marker 通过而自动启用；
- [x] marker、package identity、EXE SHA-256 或结构不匹配时在 child resume 前拒绝；
- [x] v2.1.233、v2.1.237 的候选 binary 必须匹配对应 profile 的固定 SHA，且仍经过 package identity、PE、`.bun` 与 marker 门禁；
- [ ] 探针失败时停止，不启用替代命令或其他客户端；
- [ ] 不修改或替换用户 Claude executable。

## 20. 非功能要求

1. **Windows-only**：不为抽象跨平台而增加当前产品不需要的层。
2. **原生体验**：所有模型控制都在 Claude Code 原生命令和原生状态位置完成。
3. **轻量**：每命令会话一个进程内短生命周期 Router，无独立 sidecar 或系统常驻服务。
4. **失败关闭**：内存适配与能力探针不确定时不启动真实会话。
5. **状态原子性**：模型及其派生状态不得出现 UI 与实际上游不一致。
6. **错误真实性**：上游错误不润色，本地错误提供精确证据。
7. **安全**：loopback、每 session token、凭据不进入 Claude child 或工具环境、默认不写磁盘日志。
8. **可维护性**：项目模型目录和兼容数据随本工具版本更新；不运行时猜测未来模型。
9. **版本边界明确**：当前只支持 v2.1.233、v2.1.237；未来版本必须重新提取 marker、package identity 与 EXE SHA-256 并完成独立验证，不因版本号或 PE probe 通过而自动启用。
10. **无副作用重放**：已产生输出或 tool call 后不自动重试。

## 21. 已确认事实

1. Claude Code 2.1.233 可向自定义 base URL 发送自定义模型名；gateway discovery 只消费 `id` 和 `display_name`。
2. gateway discovery 会保留内置模型并追加符合过滤条件的 gateway 模型。
3. 仅设置自定义 base URL 会使原生模型请求也经过该地址；要实现原生模型直连 Anthropic，必须在客户端状态/路由层按模型分流。
4. 内置 slash command 优先于同名 Skill；Skills 不能覆盖 `/model`、`/fast`、`/effort`。
5. 公开 hooks 不是通用 AppState middleware，不能替换这些内置命令。
6. 未发现公开的每模型动态 context/auto-compact 注入 API 或客户端命令处理器 API。
7. 原生 `/fast` 在不支持模型上可能先切 Opus，HTTP sidecar 介入太晚。
8. 当前 Windows `claude` 样本是 Bun 编译的 PE x64 单文件。
9. Windows 对 launcher 自建子进程提供调试和内存 API；普通用户通常可控制同一用户启动的子进程，实际仍须探针验证。
10. OpenCode `/models` 只返回瘦模型清单，不含 context、free、协议、effort 或 fast 完整能力。
11. OpenCode CLI 依据 catalog 的 SDK 元数据选择 Responses、Chat、Messages 或其他协议，不按模型名猜测。
12. OpenCode runtime 出现、catalog 缺失的模型不会被当前 CLI 安全调用。
13. sub2api 源码能力不能直接代表 `i.bug.pics` 部署行为。
14. DeepSeek 当前没有确认的同模型 fast，`messages/count_tokens` 仍需真实凭据验证。

## 22. 风险登记

| 风险 | 影响 | 当前要求 |
|---|---|---|
| Claude 内部布局变化 | 内存适配失效 | 每个支持版本的独立 marker + package identity/EXE SHA-256/PE/.bun/唯一性校验 + suspended probe；失败关闭 |
| Windows 安全策略阻止内存访问 | 无法启动项目会话 | 显示系统原因和手动权限建议；不绕过保护 |
| 原生模型与项目模型需要不同目的地 | 单一 base URL 会错误转发原生流量 | 内存层按当前模型分流；闸门必须证明 |
| 用户覆盖错误能力 | context 或协议错误 | 做 schema/范围校验，按合法配置执行，上游错误不改写 |
| 工具配置明文保存 key | 本地凭据泄露 | 明确由用户管理文件位置和权限；本工具不检查或修改 ACL，日志默认不落盘 |
| OpenCode 目录过期 | 新模型暂不可用 | 随工具版本更新，不运行时自动猜测 |
| 跨 Provider 私有 reasoning block | 新 Provider 拒绝历史 | 旧模型原生 compact，失败保持旧状态 |
| 命令进程或 Router 异常退出 | 当前请求与会话结束 | 不重放、不自动重启；Job 关闭时终止受管 Claude child |
| Claude 自己持久化项目模型 | 普通 `claude` 下次可能不认识 | 明确接受并沿用 Claude 原生逻辑，不退出恢复 |
| 所有子 Agent 强制继承 | 改变部分原生 Agent 默认选模 | 已冻结的产品规则，必须明确测试 |

## 23. 尚需真实环境闭环的事实

1. 2.1.233 的 13 个独立 marker 在原生 `/model`、`/fast`、context 与项目模型 client 路由上的完整交互行为；静态唯一性、suspended patch 和 `--version` smoke 不能替代人工命令验收。
2. 2.1.237 已通过独立 13 点 profile、静态唯一性、隔离 patch/resume 与 `/model`、`/fast`、`/effort`、`/context` 的 loopback fake Provider 证据；普通 Agent、内置 Agent、自定义 Agent 与 Team 的替换字节已逐项对照 2.1.233 的父 model / effort 继承语义，但其真实 child lineage 与双版本人工对等矩阵仍须在发布前完成。
3. 原生模型直连 Anthropic、项目模型走当前命令会话 Router 的真实流量分流。
4. Windows 安全机制、ASLR、CFG、npm 与 Bun 安装来源对新子进程内存适配的实际影响。
5. `i.bug.pics` 当前账号可见模型、普通/compact effort、`count_tokens` 和 priority。
6. DeepSeek Anthropic compatibility 的真实 tool/stream/effort 与 `count_tokens` 行为。
7. 当前默认目录中的 OpenCode Go 5 个、OpenCode Free 3 个模型的真实协议、tool、effort、stream、reasoning 和错误行为。
8. Claude 原生历史在各服务商之间切换时，哪些私有 block 会实际触发拒绝。
9. 多命令会话、控制台继承、关闭清理、托盘和两个桌面 toggle 的人工 Windows 验收。

## 24. 需求访谈状态

当前产品决策 frontier 已清空。二进制名称固定为 `claude-patch.exe`，命令代理固定为同目录 `claude.cmd`，配置固定为同目录 `config.json`；只提供安装与卸载，不保留待实现的命名或目录决策。

## 25. 官方参考资料

- Claude Code 模型配置：<https://code.claude.com/docs/en/model-config>
- Claude Code Fast Mode：<https://code.claude.com/docs/en/fast-mode>
- Claude Code Gateway Protocol：<https://code.claude.com/docs/en/llm-gateway-protocol>
- Claude Code Gateway 连接：<https://code.claude.com/docs/en/llm-gateway-connect>
- Claude Code CLI：<https://code.claude.com/docs/en/cli-reference>
- Claude Code Hooks：<https://code.claude.com/docs/en/hooks>
- Claude Code Skills：<https://code.claude.com/docs/en/skills>
- DeepSeek API：<https://api-docs.deepseek.com/>
- DeepSeek Anthropic API：<https://api-docs.deepseek.com/guides/anthropic_api>
- OpenCode Zen：<https://opencode.ai/docs/zen/>
- OpenCode Go：<https://opencode.ai/docs/go/>
- OpenCode Models catalog：<https://models.opencode.ai/api.json>

## 26. 交付边界

纯 Go 产品实现已经落地，但以下能力在对应真实交互证据闭环前不得对外宣称完成：

- 2.1.233 或 2.1.237 的全部 Claude Code 原生命令均已兼容；
- `/fast` 在每个已配置项目模型上安全可用；
- context 可在同一会话严格动态切换并正确触发原生 compact；
- 2.1.233 或 2.1.237 的所有子 Agent 已通过真实 child 流量证明强制继承模型与 effort；
- 全部候选 Provider/模型均已通过真实 API 验收；
- 支持 Claude Code v2.1.233、v2.1.237 以外的 Windows 版本。

验收失败时应保留可重复证据，不得用项目命令、磁盘 patch 或其他客户端包装成“近似成功”。
