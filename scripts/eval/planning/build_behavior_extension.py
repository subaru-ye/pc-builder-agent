"""Explicitly versioned offline behavior cases on the reviewed 172-part catalog.

Oracles exercise real Screening state application and Builder tool execution;
they do not measure whether a live model independently makes these decisions.
"""
import argparse
import copy
import hashlib
import json
from pathlib import Path

from build_snapshot_pair import ROOT, load_pinned, text, tool


def field(value=None, status='active', strength=None, kind=None):
    result = {'status': status}
    if value is not None: result['value'] = value
    if strength: result['strength'] = strength
    if kind: result['kind'] = kind
    return result


def operation(name, value=None, kind='fact', strength='must', action='set'):
    result = {'op': action, 'field': name, 'kind': kind, 'strength': strength, 'evidence': 'stated'}
    if action != 'remove': result['value'] = value
    return result


def message(phrase, operations, expect=None, next_action='confirm', builder=None):
    for op in operations: op['quote'] = phrase
    result = {'kind': 'message', 'text': phrase, 'screen_oracle': {
        'operations': operations, 'next_action': next_action,
        'reply': '已更新当前信息，可以继续检索比较。'},
        'expect': {'versions': 0, **(expect or {})}}
    if builder: result['builder_oracle'] = builder
    return result


def finish(reply, outcome='collect'):
    return text({'outcome': outcome, 'reply': reply, 'issues': [], 'assessments': [], 'assumptions': []})


def build_cases():
    workload = '本地Whisper转写'
    first_fields = {'budget_cny': field(status='unknown'), 'use_case.resolution': field(status='unknown'),
                    'free.workload': field(workload, kind='fact'),
                    'free.offline': field('音频不上传云端', kind='constraint', strength='must'),
                    'free.avoid_brand': field('不要英伟达显卡', kind='constraint', strength='must')}
    search = [tool('search_local', {'category': 'gpu', 'query': 'Radeon'}),
              finish('可以先比较本地转写方案；预算未知，不假设购买上限。')]
    flexible = {'id': 'B2-001', 'title': '未知预算、未预设工作负载与多条独立条件进入规划并可分别撤销',
                'source': '用户反馈的自由需求机制；人工多轮oracle，目录为完整冻结172件', 'steps': [
        message('做本地Whisper转写，音频不上传云端，不要英伟达显卡，预算还没定。', [
            operation('free.workload', workload), operation('free.offline', '音频不上传云端', 'constraint'),
            operation('free.avoid_brand', '不要英伟达显卡', 'constraint')], {'fields': first_fields}),
        {'kind': 'confirm', 'builder_oracle': search, 'expect': {'versions': 0, 'outcome': 'collect',
            'builder_calls': 2, 'require_tools': ['search_local'], 'fields': first_fields,
            'model_input_contains': ['free.workload', 'free.offline', 'free.avoid_brand'],
            'reply_forbidden': ['请先提供预算', '请选择预设用途']}},
        message('取消显卡品牌限制，继续比较；音频仍然不上传云端。', [
            operation('free.avoid_brand', action='remove')], {'outcome': 'collect', 'fields': {
                **first_fields, 'free.avoid_brand': field(status='removed')}, 'require_tools': ['search_local']},
            next_action='plan', builder=[tool('search_local', {'category': 'gpu', 'query': 'RTX'}),
                                        finish('品牌限制已撤销，继续比较适合本地转写的显卡。')]),
        {'kind': 'refresh', 'expect': {'versions': 0, 'builder_calls': 0, 'fields': {
            'free.avoid_brand': field(status='removed'), 'free.offline': first_fields['free.offline']}}}
    ]}
    owned_fields = {'existing_parts': field(['cpu', 'motherboard']), 'owned_parts': field(status='unknown'),
                    'free.owned_cpu': field('5600', kind='fact'), 'free.owned_board': field('B550M', kind='fact'),
                    'budget_cny': field(status='unknown')}
    owned = {'id': 'B2-002', 'title': '已有件简称先检索方向，再只澄清影响升级的主板细节',
             'source': '用户CPU升级反馈的变体；人工oracle，不将简称伪装为已核对型号', 'steps': [
        message('手里有个5600和B550M，预算暂未定，先看看升级方向。', [
            operation('existing_parts', ['cpu', 'motherboard']), operation('free.owned_cpu', '5600'),
            operation('free.owned_board', 'B550M')], {'fields': owned_fields}),
        {'kind': 'confirm', 'builder_oracle': [
            tool('search_local', {'category': 'cpu', 'query': '5600'}),
            tool('search_local', {'category': 'motherboard', 'query': 'B550M'}),
            finish('先比较AM4处理器升级方向；B550M的品牌和完整型号尚未确定。')], 'expect': {
                'versions': 0, 'outcome': 'collect', 'builder_calls': 3, 'fields': owned_fields,
                'require_tools': ['search_local'], 'search_candidates': {'cpu-r5-5600': True},
                'model_input_contains': ['free.owned_cpu', 'free.owned_board']}},
        message('预算6000，只先换CPU，其他配件尽量不动。', [operation('budget_cny', 6000, 'constraint'),
            operation('priority', ['cpu'], 'constraint', 'prefer'),
            operation('free.preserve_other_parts', '其他配件尽量不动', 'constraint', 'prefer')], {
                'outcome': 'clarify', 'fields': {**owned_fields, 'budget_cny': field(6000)},
                'require_tools': ['search_local'], 'reply_forbidden': ['你的预算是多少', '你已有的CPU是什么']},
            next_action='plan', builder=[tool('search_local', {'category': 'cpu', 'query': 'Ryzen 7 5700X'}),
                finish('已找到AM4升级候选。为核对BIOS支持，需要确认B550M的品牌和完整型号。', 'clarify')]),
        {'kind': 'refresh', 'expect': {'versions': 0, 'builder_calls': 0,
            'fields': {'free.owned_cpu': field('5600'), 'budget_cny': field(6000)}}}
    ]}
    failed = {'id': 'B2-003', 'title': '语义检索和正文读取不可用后保留本地检索进展并继续',
              'source': '人工故障注入：无Embedder、未登记网页请求由离线transport拒绝', 'steps': [
        message('预算7000，先比较视频转写处理器，顺便看一下官网资料。', [
            operation('budget_cny', 7000, 'constraint'), operation('free.workload', '视频转写')]),
        {'kind': 'confirm', 'builder_oracle': [
            tool('search_semantic', {'query': '视频转写处理器', 'category': 'cpu'}),
            tool('search_local', {'category': 'cpu', 'query': '9950X3D'}),
            tool('read_page', {'url': 'https://hardware.eval.invalid/unavailable-spec'}),
            finish('本地候选和资料已保留；这次网页读取失败，可以继续比较或换一份公开资料。')], 'expect': {
                'versions': 0, 'outcome': 'collect', 'builder_calls': 4,
                'require_tools': ['search_semantic', 'search_local', 'read_page'],
                'model_input_contains': ['语义检索当前不可用', '资料服务暂时不可用或超时'],
                'search_candidates': {'cpu-r9-9950x3d': True},
                'candidate_specs': {'cpu-r9-9950x3d': {'socket': 'AM5', 'tdp_w': 170}}}},
        message('那先用已有资料继续比较。', [], {'outcome': 'collect', 'builder_calls': 2,
            'require_tools': ['search_local'], 'forbid_tools': ['search_web', 'read_page'],
            'model_input_contains': ['previous_proposal'], 'fields': {'budget_cny': field(7000)}},
            next_action='plan', builder=[tool('search_local', {'category': 'cpu', 'query': 'Ryzen 9'}),
                                        finish('继续基于已保存的本地规格比较处理器。')]),
        {'kind': 'refresh', 'expect': {'versions': 0, 'builder_calls': 0}}
    ]}
    return [flexible, owned, failed]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    source = ROOT / 'internal/planningeval/testdata/data-pair-20260915/new'
    prior = json.loads((source / 'provenance.json').read_text(encoding='utf-8'))
    base = load_pinned(source / 'suite.json', prior['suite_sha256'])
    suite = {'version': 'planning-behavior-20260915', 'provenance': 'Explicit offline behavior oracles, full frozen 172-part catalog; not live model quality.',
             'catalog': copy.deepcopy(base['catalog']), 'pages': {}, 'cases': build_cases()}
    raw = (json.dumps(suite, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
    args.out.mkdir(parents=True, exist_ok=False)
    (args.out / 'suite.json').write_bytes(raw)
    provenance = {'suite_sha256': hashlib.sha256(raw).hexdigest(), 'catalog_source_suite_sha256': prior['suite_sha256'],
                  'database_snapshot_sha256': prior['database_snapshot_sha256'],
                  'generator_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  'synthetic_changes': ['All model decisions are authored oracles',
                                        'Missing Embedder and unregistered fixture URL deliberately exercise tool failures']}
    (args.out / 'provenance.json').write_bytes((json.dumps(provenance, ensure_ascii=False, indent=2) + '\n').encode('utf-8'))
    print('Built', len(suite['cases']), 'behavior cases without database or provider calls')


if __name__ == '__main__': main()
