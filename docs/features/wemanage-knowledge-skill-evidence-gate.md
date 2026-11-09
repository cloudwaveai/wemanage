# We-Manage 参考 HelixHarness：证据化知识沉淀设计

日期：2026-11-09。状态：功能提议，未排期。范围：仅文档参考与设计建议；不修改网关代码、不改 We-Manage 运行行为、不删除或改写正式来源。

## 参考对象

- 仓库：https://github.com/didi/helixharness
- 协议：Apache-2.0
- 本次阅读范围：`README.zh-CN.md`、`docs/user-guide.zh-CN.md`、`docs/workflow-architecture.md`、`helixharness/workflows/single/{propose,apply,archive}.md`、`helixharness/templates/{spec,tasks,verify-report}.md.tpl`、`helixharness/skills/{tdd,code-review}/SKILL.md`、`helixharness/reference/loading-protocol.md`、`helixharness-platform/README.md`。

HelixHarness 是面向 Codex / Claude Code 的研发工作流插件，核心链路为 `Propose → Apply → Archive`；可选 platform 用于观测、评测和改进 HelixHarness 框架本身。它不是 We-Manage 这类组织用量与知识管理产品，因此只借鉴方法论，不照搬产品形态。

## 可参考的核心机制

| HelixHarness 机制 | 它解决的问题 | 对 We-Manage 的启发 |
|---|---|---|
| `spec.md` / `tasks.md` / `verify-report.md` 三件套 | 把一次变更拆成需求契约、可执行任务和验证证据 | 知识/Skill 草稿也应有“证据契约”，而不是只有正文和来源列表 |
| Archive 才沉淀知识，且展示候选 diff 后由人确认 | 防止模型自动把一次性结论写成组织知识 | We-Manage 已有人工审核/发布；可把发布前检查做成确定性 gate |
| fixed point、HEAD、scope sha256、receipt | 防止评审后内容漂移导致结论失效 | We-Manage 可用来源快照、模型指纹、规则指纹再加“发布前完整性校验” |
| Standards / Spec 双轴 Review | 避免工程规范问题掩盖需求实现问题 | 知识/Skill 审核可拆成“证据轴”和“适用性轴” |
| TDD / Verification / Evidence Receipt | 要求结论能回到实际验证命令和结果 | 每条知识/Skill 应写清验证方式、停止条件和人工确认点 |
| Platform 的 Case / Run / Judge / evolution | 用冻结样本评测框架改进是否有效 | We-Manage 可先做轻量 calibrated case，而不是上整套评测平台 |

## 不建议照搬的部分

- 不引入插件市场、宿主适配、Codex/Claude Code hooks 等插件机制；We-Manage 是集中式多用户系统。
- 不直接部署 `helixharness-platform`；其 FastAPI + Worker Pool + Case/Run/Judge 对当前阶段过重。
- 不照搬 Git-centric 的 review scope；We-Manage 的对象是日报快照、知识候选、资产版本和权限范围，不是仓库 commit。
- 不把 HelixHarness 的研发流程当成 We-Manage 的产品边界；它不能替代 We-Manage 已识别的缺口：真实费用渠道、全量日志覆盖、长来源分段、全员共享权限、UI 现场验收。

## 建议落地：知识/Skill 证据卡

后续若实现，可把现有知识/Skill 草稿升级为带证据契约的卡片。字段建议：

| 字段 | 说明 |
|---|---|
| `claim` | 一条可判断真假的知识主张或 Skill 适用结论 |
| `scope` | 适用项目、人员范围、时间范围、客户/内部边界 |
| `evidence` | 逐字引文、来源快照 ID、来源日期、来源版本 |
| `status` | proposed / current / historical / resolved / conflicted / unknown |
| `verification` | 已执行的验证、未执行的验证、人工核查入口 |
| `stop_conditions` | 不适用情形、失败时停止条件、需要人工确认的节点 |
| `review` | 审核人、审核时间、判断依据、是否发布、版本号 |
| `privacy_scope` | private / project / organization；继承最严格来源权限 |

该结构只借鉴 HelixHarness 的证据化交付思想；具体表结构、API 和 UI 需后续单独设计与验收。

## 建议落地：发布前 gate

在人工审核前增加确定性检查，减少“模型像真的但不可复用”的风险：

1. 引文逐字匹配来源快照，不能匹配时标记 `citation_unverified`，不自动判假。
2. 来源仍可访问且未转为私密/隐藏；任一输入收紧权限时，派生知识/Skill 同步隐藏。
3. 项目归属与来源人员一致；不把模型提取名自动当成正式项目。
4. Skill 流程图与文字共用同一结构化数据；无循环、无不可达节点、无缺失结束条件。
5. 同输入、同规则、同模型可复用结果；新增来源形成新版本并保留差异。
6. 明确区分建议、实际执行、测试通过、上线、业务验收；不生成完成百分比或个人绩效结论。

## 建议的第一步验证

选择一个真实且证据完整的项目作为 calibrated case：

- 冻结输入日报快照、模型版本、整理规则版本。
- 人工标注应沉淀的知识主题、未闭环问题、方法候选和不应沉淀的内容。
- 运行自动整理，比较事实覆盖率、引文准确率、误合并率、私密泄漏风险。
- 只把通过 gate 且人工确认的结果进入组织资产。

这一步比直接建设评测平台更符合当前阶段；若后续整理质量成为主要瓶颈，再考虑引入更完整的 Case/Run/Judge 机制。

## 边界

- 本文是参考与设计建议，不代表上述 schema、gate 或 calibrated case 已实现。
- 引文匹配不等于语义正确；事实状态、适用条件和方法仍需人工审核。
- We-Manage 仍只反映已采集且有权查看的记录；不覆盖网关外工作或未采集内容。
- 后续如复制 HelixHarness 代码片段或文档，需要遵守其 Apache-2.0 许可并保留 NOTICE/归属说明；当前仅借鉴方法，不复制代码。
