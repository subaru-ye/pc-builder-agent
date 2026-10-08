import type { ReqV2RunSummary } from "./reqv2";

export const runPurposes = [
  { key: "models", label: "模型对比" },
  { key: "repair", label: "修复验证" },
  { key: "certification", label: "认证检查" },
  { key: "regression", label: "程序回归" },
  { key: "grading", label: "评分核对" },
  { key: "diagnostic", label: "消融与诊断" },
  { key: "unknown", label: "未标注目的" },
] as const;
export type RunPurpose = typeof runPurposes[number]["key"];
interface Experiment {
  id: string; purpose: RunPurpose; title: string; question: string; note: string; reference: string;
  members: { name: string; label: string; baseline?: boolean }[];
}
export interface RunExperiment extends Omit<Experiment, "members"> {
  runs: { run: ReqV2RunSummary; label: string; baseline?: boolean }[];
}

// 人工核对的历史实验索引；只按准确目录名关联，不按模型名、时间邻近或名称前缀猜测目的。
// 分组不参与评分或可比性判断；Flash 首跑在模型对比与修复验收中复用同一条记录。
const experiments: Experiment[] = [
  {
    id: "models-20260924", purpose: "models", title: "初筛模型对比 · 09-24",
    question: "更换模型能否改善需求抽取和多轮对话？",
    note: "开发集 + 校准集，每题 3 次；Flash 复用修复验收首跑。小样本结果不代表模型通用排名。",
    reference: "docs/eval/requirement-v2/模型对比结果-20260924.md",
    members: [
      { name: "screening-fix2-live-devcal-r3-20260924", label: "Flash · 基线（复用）", baseline: true },
      { name: "modelcmp-pro-devcal-r3-20260924", label: "Pro · 候选" },
      { name: "modelcmp-kimi-devcal-r3-20260924", label: "Kimi · 候选" },
      { name: "modelcmp-glm-devcal-r3-20260924", label: "GLM · 候选" },
    ],
  },
  {
    id: "evidence-fix", purpose: "repair", title: "证据来源与已有配件修复",
    question: "修复后能否消除证据误标和已有配件错写？",
    note: "两批均为开发集 + 校准集，每题 3 次；包含提示词与服务端守卫修改。",
    reference: "docs/changes/screening-v2-evidence-grounding-fix.md",
    members: [
      { name: "screening-fix-live-devcal-r3-20260924", label: "首轮修复", baseline: true },
      { name: "screening-fix2-live-devcal-r3-20260924", label: "补齐撤销授权" },
    ],
  },
  {
    id: "certification", purpose: "certification", title: "候选方案正式认证 · 09-24",
    question: "冻结候选是否满足发布门槛与保留集边界？",
    note: "不同批次分别检查可用性、重复稳定性与保留集；保留原始未通过结果，不合并成一个分数。",
    reference: "docs/eval/requirement-v2/认证产物索引-20260924.md",
    members: [
      { name: "spec6-cert-live-a-devcal-r1-20260924", label: "A · 可用性检查" },
      { name: "spec6-cert-live-b-devcal-r3-20260924", label: "B · 开发 / 校准稳定性" },
      { name: "spec6-cert-live-c-holdout-r3-20260924", label: "C · 保留集验证" },
      { name: "spec6-cert-deterministic-20260924", label: "程序行为检查" },
    ],
  },
  {
    id: "precertification", purpose: "certification", title: "认证前置检查 · 09-24",
    question: "评估环境与候选是否具备正式认证条件？",
    note: "单次模型测试与确定性套件分别查看；这批未执行保留集认证。",
    reference: "docs/eval/requirement-v2/预认证-20260924.md",
    members: [
      { name: "spec6-prereq-live-devcal-20260924", label: "开发 / 校准预检查" },
      { name: "spec6-prereq-deterministic-20260924", label: "程序行为预检查" },
    ],
  },
  {
    id: "cert-diagnostics", purpose: "diagnostic", title: "修复前候选的换模型与消融诊断",
    question: "失败与模型选择或被消融的规则是否有关？",
    note: "D 为 Qwen 换模型诊断，E 为校准集消融；使用修复前代码，独立于最新模型对比。",
    reference: "docs/eval/requirement-v2/认证产物索引-20260924.md",
    members: [
      { name: "spec6-cert-live-d-swap-qwen-max-devcal-r3-20260924", label: "D · Qwen 换模型诊断" },
      { name: "spec6-cert-live-e-ablation-cal-r3-20260924", label: "E · 单因素消融" },
    ],
  },
  {
    id: "grader-checks", purpose: "grading", title: "评分器稳定性与版本核对",
    question: "同一冻结输出重放是否稳定，评分版本变化影响哪些结论？",
    note: "回放与重判不调用模型；结果反映评分核对，不能当作新模型测试。",
    reference: "docs/eval/requirement-v2/预认证-20260924.md；认证产物索引-20260924.md",
    members: [
      { name: "spec6-cert-baseline-v1-regrade-v4-20260924", label: "旧基线按新规则重判" },
      { name: "spec6-cert-replay-v4-stability-20260924", label: "认证评分稳定性" },
      { name: "spec6-prereq-replay-v4-20260924", label: "同版本评分稳定性" },
      { name: "spec6-prereq-replay-20260924", label: "跨版本重判" },
    ],
  },
  {
    id: "builder-gate", purpose: "regression", title: "确认条件与选配准入回归",
    question: "需求更新、确认和启动选配的程序行为是否符合合同？",
    note: "只检查确定性程序行为，不评价模型输出质量。",
    reference: "docs/changes/requirement-confirmation-builder-gate-v2.md",
    members: ["spec4-strict-validation-212310", "spec4-frozen-constraints-204934", "spec4-consistency-zero-model-191407", "spec4-zero-model-final-175620", "spec4-zero-model-173639", "spec4-zero-model-173626", "spec4-zero-model-173251", "spec4-zero-model-173214"].map(name => ({ name, label: "确定性回归" })),
  },
  {
    id: "authorization", purpose: "repair", title: "初筛建议值与接受授权迭代",
    question: "接受、询问和拒绝建议时，是否正确更新需求？",
    note: "按迭代批次保留运行结果；冻结门槛状态以各自报告为准。",
    reference: "docs/eval/requirement-v2/README.md · Screening 收集 v2 验收",
    members: [
      { name: "spec3-live-20260923", label: "初次验证", baseline: true },
      { name: "spec3-rework-live-20260923", label: "首轮返工" },
      { name: "spec3-rework2-live-20260923", label: "第二轮返工" },
      { name: "spec3-final-live-20260923", label: "最终验证" },
    ],
  },
  {
    id: "authorization-replays", purpose: "grading", title: "授权边界历史输出回放",
    question: "历史输出在冻结评分规则下能否复现？",
    note: "不新增模型调用；按来源批次核对历史输出。",
    reference: "docs/eval/requirement-v2/README.md · Screening 收集 v2 验收",
    members: ["spec3-live-20260923-replay-20260923T060539Z", "spec3-live-20260923-replay-20260923T062240Z", "spec3-rework-live-20260923-replay-20260923T064836Z", "spec3-rework2-live-20260923-replay-20260923T071444Z", "spec3-rework2-live-20260923-replay-20260923T072620Z"].map(name => ({ name, label: "冻结输出回放" })),
  },
  {
    id: "initial-regression", purpose: "regression", title: "初筛状态机初始回归",
    question: "需求状态机在初始候选上是否符合合同？",
    note: "零模型检查，模型层不参与评分。",
    reference: "artifacts/reqv2/spec3-deterministic-20260923/plan.json",
    members: [{ name: "spec3-deterministic-20260923", label: "初始确定性检查" }],
  },
];

export function groupRunExperiments(runs: ReqV2RunSummary[]): RunExperiment[] {
  const byName = new Map(runs.map(run => [run.dir_name, run]));
  const known = new Set<string>();
  const groups: RunExperiment[] = experiments.flatMap(({ members, ...experiment }) => {
    const entries = members.flatMap(({ name, ...member }) => {
      const run = byName.get(name);
      if (!run) return [];
      known.add(name);
      return [{ run, ...member }];
    });
    return entries.length ? [{ ...experiment, runs: entries }] : [];
  });
  const unknown = runs.filter(run => !known.has(run.dir_name));
  if (unknown.length) groups.push({ id: "unclassified", purpose: "unknown", title: "尚未标注测试目的", question: "这些记录尚未关联到已核对的实验。", note: "保留原始结果；补充实验依据后再分类。", reference: "", runs: unknown.map(run => ({ run, label: "目的未记录" })) });
  return groups.sort((a, b) => newest(b).localeCompare(newest(a)));
}

function newest(group: RunExperiment) {
  return group.runs.reduce((latest, { run }) => run.created_at && run.created_at > latest ? run.created_at : latest, "");
}
