"""Read-only audit of all frozen v1.5 cases; never rewrites the old suite."""
import hashlib
import json
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
base = ROOT / 'internal/evalsuite/testdata'
manifest = json.loads((base / 'suites/v1.5.json').read_text(encoding='utf-8'))
out = ROOT / 'docs/eval/planning-v2'
out.mkdir(parents=True, exist_ok=True)
rows = []
specific = {
    'L3-102': '预算未知不再强制追问或拒绝；检查不编造金额，允许讨论方向。',
    'L3-103': '游戏分辨率未知不再固定追问；不得推断为已表达偏好。',
    'L4-203': '本地最低价只能作为证据反馈；必须进入模型规划，不能程序提前返回市场无解。',
    'L4-204': '目录外已有件允许查官网规格；不能偷换近似型号，不强制固定 data_unavailable。',
    'L4-205': '真实插槽/内存冲突必须保留；允许提出替换备选但不得修改用户已接受的保留件。',
    'L4-214': '缺预算可先讨论；不将固定字段清单作为准入条件。',
    'L4-215': '预算内交付与具体待解决分开统计，不把任意不交付都算正确。',
    'L5-309': '办公改游戏应保留其他状态；是否追问分辨率由必要性决定。',
    'L5-310': '预算撤销后保留未知且不复活；不强制立即重新询问。',
}
for entry in manifest['cases']:
    raw = (base / 'cases' / entry['file']).read_bytes()
    canonical = json.dumps(json.loads(raw), ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    canonical = canonical.replace('<', '\\u003c').replace('>', '\\u003e').replace('&', '\\u0026')
    actual = hashlib.sha256(canonical.encode('utf-8')).hexdigest()
    if actual != entry['sha256']:
        raise SystemExit('Frozen case hash mismatch: ' + entry['id'])
    c = json.loads(raw)
    id = c['id']
    stage = c.get('stage', 'build')
    if id in specific:
        disposition, change = 'rewrite_expectation', specific[id]
    elif id.startswith('L4-') and (c.get('expect') or {}).get('outcome') == 'clarify':
        disposition, change = 'rewrite_expectation', '保留已有件与预算口径场景；只问确实影响下一步的信息，不强制旧阻断结果。'
    elif stage == 'screening':
        disposition, change = 'migrate_state_assertions', '保留用户输入与纠正语义；spec/clarify 断言迁移为服务端增量状态、来源、强度和必要追问检查。'
    else:
        disposition, change = 'migrate_planning_assertions', '保留场景；改用产品 planning 执行与交付核验，取消固定工具顺序/修复器限制，保留真实兼容性和预算约束。'
    req = c.get('requirement') or {}
    notes = []
    if any(k in req for k in ['noise_pref','notes','size_pref','brand_pref']):
        notes.append('原文区分 must/prefer；不能根据旧结构默认强度。')
    if c.get('locked'):
        notes.append('保留用户明确锁定；区分“尽量不动”，不延续程序固定改单数量限制。')
    if req.get('budget_flex') is not None:
        notes.append('只有用户明确授权的预算弹性可保留。')
    if stage == 'build':
        notes.append('固定完整型号/规格/报价快照，不只固定价格日期；新数据另跑配对组。')
    rows.append({'id':id,'title':c['title'],'sha256':actual,'stage':stage,
                 'scenario':'retain','disposition':disposition,'data_sensitive':stage=='build',
                 'change':change,'notes':notes,'execution_migration':'audited_not_auto_converted'})
result={'source_suite':'v1.5','source_suite_sha256':hashlib.sha256((base/'suites/v1.5.json').read_bytes()).hexdigest(),
        'counts':dict(Counter(r['disposition'] for r in rows)),'cases':rows}
(out/'legacy-audit.json').write_bytes((json.dumps(result,ensure_ascii=False,indent=2)+'\n').encode('utf-8'))
lines=['# 旧 v1.5 评估集逐题审查','',
       '50 题均保留历史场景和哈希；没有修改旧题或历史成绩。以下是迁移判断，不表示 50 题已自动转成新协议或已通过新模型。新入口先执行单独登记的真实问题回归集。','',
       '| 用例 | 原场景 | 判定调整 | 数据依赖 |','| --- | --- | --- | --- |']
for r in rows:
    lines.append('| '+r['id']+' | '+r['title'].replace('|','/')+' | '+r['change']+' '+' '.join(r['notes'])+' | '+('目录/规格/价格' if r['data_sensitive'] else '会话与语义')+' |')
(out/'legacy-audit.md').write_bytes(('\n'.join(lines)+'\n').encode('utf-8'))
print('Verified and audited',len(rows),'frozen cases:',result['counts'])
