"""Build reviewed offline protocol fixtures from immutable saved records.

This does not call models or search, and never reads the changing collection data.
Outputs are labeled hand-authored oracles unless copied from a saved model result.
"""
import copy
import hashlib
import html
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / 'internal/planningeval/testdata'
OUT.mkdir(parents=True, exist_ok=True)
load = lambda p: json.loads((ROOT / p).read_text(encoding='utf-8'))
saved_path = 'internal/planning/testdata/complete_proposal_recording.json'
upgrade_path = 'internal/producthttp/testdata/cpu_upgrade_recording.json'
external_path = 'internal/planning/testdata/5900x_registration_recording.json'
saved = load(saved_path)['result']
upgrade = load(upgrade_path)['candidate']
external = load(external_path)
draft = saved['draft']
if isinstance(draft, str):
    draft = json.loads(draft)
updraft = copy.deepcopy(draft)
updraft['selection']['cpu'] = upgrade['id']
updraft['rationale']['cpu'] = '离线回放：优先升级处理器，其他配件保持不变。'

def text(value):
    return {'role':'model','parts':[{'text':json.dumps(value,ensure_ascii=False)}]}

def tool(action,payload):
    return {'role':'model','parts':[{'functionCall':{'id':'oracle-'+action,'name':'planning_action','args':{'action':action,'payload':json.dumps(payload,ensure_ascii=False)}}}]}

def finish(d=draft,outcome='proposal',issues=None,assessments=None):
    return text({'outcome':outcome,'reply':'已比较候选，具体取舍见方案。','draft':d,'issues':issues or [],
                 'assessments':assessments if assessments is not None else [{'field':'budget_cny','status':'met','explanation':'以工具实际报价核对预算','evidence':[]}],
                 'assumptions':[]})

def build(d=draft,issues=None,assessments=None):
    return [tool('search_local',{'category':'cpu'}),tool('evaluate',{'draft':d}),finish(d,issues=issues,assessments=assessments)]

def op(field,value=None,strength='must',kind=None,action='set',quote=None,scope=None):
    out={'op':action,'field':field}
    if action not in ['remove','restore']:
        out.update(value=value,strength=strength)
    if kind:out['kind']=kind
    if quote:out['quote']=quote
    if scope:out['scope']=scope
    return out

def msg(s,ops,expect,next='collect',builder=None,reply='已记录本轮有效需求。'):
    for x in ops:
        x.setdefault('quote',s)
    out={'kind':'message','text':s,'screen_oracle':{'next_action':next,'reply':reply,'operations':ops},'expect':expect}
    if builder:out['builder_oracle']=builder
    return out

def field(value=None,status='active',strength=None,kind=None):
    f={'status':status}
    if value is not None:f['value']=value
    if strength:f['strength']=strength
    if kind:f['kind']=kind
    return f

def initial(extra=None,budget=7000):
    return msg('预算'+str(budget)+'，用于视频剪辑', [op('budget_cny',budget),op('use_case.type','productivity',kind='fact')]+(extra or []),
               {'versions':0,'builder_calls':0,'fields':{'budget_cny':field(budget)}},next='confirm')

def confirm(outputs=None,**expect):
    return {'kind':'confirm','builder_oracle':outputs or build(),'expect':{'versions':1,'outcome':'ready','validation':'pass','require_tools':['search_local','evaluate'],**expect}}

def case(id,title,steps,source='用户真实反馈场景；模型操作/工具轨迹为人工 oracle，配件取保存快照'):
    return {'id':id,'title':title,'source':source,'steps':steps}

cases=[]
cases.append(case('P2-001','升级直接执行、其他配件保留、刷新和幂等',[
    initial(),confirm(),
    msg('把处理器换更好的，预算还很充足啊，其他配件尽量不动',
        [op('priority',['cpu']),op('free.preserve_other_parts','其他配件尽量不动','prefer','constraint')],
        {'versions':2,'outcome':'ready','cpu':upgrade['id'],'preserve_other_parts':True,'require_tools':['search_local','evaluate'],
         'builder_calls':3,'model_input_contains':['base_draft','cpu-r5-5600','previous_proposal'],
         'fields':{'budget_cny':field(7000),'free.preserve_other_parts':field('其他配件尽量不动',strength='prefer')},
         'reply_forbidden':['你的预算是多少','现有CPU型号是什么']},next='plan',builder=build(updraft,assessments=[
             {'field':'budget_cny','status':'met','explanation':'报价在预算内','evidence':[]},
             {'field':'priority','status':'met','explanation':'优先更换处理器','evidence':['local:'+upgrade['id']]},
             {'field':'free.preserve_other_parts','status':'met','explanation':'其他配件保持','evidence':['local:'+upgrade['id']]}])),
    {'kind':'refresh','expect':{'versions':2,'builder_calls':0}},
    {'kind':'retry','expect':{'versions':2,'builder_calls':0}}
]))
cases.append(case('P2-002','预算修改保留偏好、撤销不复活、备选和临时例外',[
    msg('帮朋友装机，预算8000，2K游戏，尽量安静，显卡优先英伟达',
        [op('recipient','朋友',kind='context'),op('budget_cny',8000),op('use_case.type','gaming',kind='fact'),
         op('use_case.resolution','2K',kind='fact'),op('noise_pref','silent','prefer'),op('brand_pref.gpu','nvidia','prefer')],
        {'versions':0,'builder_calls':0},next='confirm'),
    msg('预算改6000',[op('budget_cny',6000)],{'versions':0,'fields':{'budget_cny':field(6000),'noise_pref':field('silent',strength='prefer'),'brand_pref.gpu':field('nvidia'),'recipient':field('朋友')}}),
    msg('取消显卡品牌偏好',[op('brand_pref.gpu',action='remove')],{'versions':0,'fields':{'brand_pref.gpu':field(status='removed')}}),
    msg('如果换4K会怎样，先不改',[op('use_case.resolution','4K',action='alternative')],{'versions':0,'alternatives':1,'fields':{'use_case.resolution':field('2K'),'brand_pref.gpu':field(status='removed')}}),
    msg('这次临时用AMD显卡',[op('brand_pref.gpu','amd','prefer',scope='temporary')],{'versions':0,'fields':{'brand_pref.gpu':field('amd')}}),
    msg('恢复之前的显卡要求',[op('brand_pref.gpu',action='restore')],{'versions':0,'fields':{'brand_pref.gpu':field(status='removed'),'noise_pref':field('silent')}}),
    {'kind':'refresh','expect':{'versions':0,'builder_calls':0,'fields':{'brand_pref.gpu':field(status='removed'),'recipient':field('朋友')}}}
]))
cases.append(case('P2-003','素材2K改1080p，清理重复notes，不写入显示分辨率',[
    msg('预算7000，主要剪2K视频',[op('budget_cny',7000),op('use_case.type','productivity',kind='fact'),op('free.workload_resolution','2K素材',kind='fact'),op('notes','2K视频素材',kind='context')],
        {'versions':0,'fields':{'free.workload_resolution':field('2K素材')}},next='confirm'),
    msg('素材大约1080p，之前2K不算',[op('free.workload_resolution','1080p素材',kind='fact'),op('notes',action='remove')],
        {'versions':0,'fields':{'free.workload_resolution':field('1080p素材'),'notes':field(status='removed'),'budget_cny':field(7000),'use_case.resolution':field(status='unknown')}}),
    {'kind':'refresh','expect':{'versions':0,'fields':{'free.workload_resolution':field('1080p素材'),'notes':field(status='removed')}}}
]))
noise_assess=[{'field':'budget_cny','status':'met','explanation':'报价在预算内','evidence':[]},
              {'field':'noise_pref','status':'unknown','explanation':'没有整机噪声实测','evidence':[]}]
cases.append(case('P2-004','必须静音进入规划，证据不足保存方案，放宽后交付',[
    msg('预算6000，剪4K视频，必须静音',[op('budget_cny',6000),op('free.workload_resolution','4K素材',kind='fact'),op('noise_pref','silent')],
        {'versions':0,'fields':{'noise_pref':field('silent',strength='must')}},next='confirm'),
    confirm(build(issues=['整机噪声缺少实测'],assessments=noise_assess),versions=0,outcome='proposal',issues_contain=['整机噪声'],fields={'noise_pref':field('silent',strength='must')},model_input_contains=['noise_pref','must']),
    msg('静音尽量满足即可，继续选配',[op('noise_pref','silent','prefer')],
        {'versions':1,'outcome':'ready','validation':'pass','require_tools':['evaluate'],'fields':{'noise_pref':field('silent',strength='prefer')}},
        next='plan',builder=build(assessments=noise_assess)),
    {'kind':'refresh','expect':{'versions':1,'outcome':'ready','builder_calls':0}}
]))
# The saved final model output is replayed verbatim. Calls before it are an oracle.
saved_final={k:saved[k] for k in ['outcome','reply','draft','issues','assessments','assumptions']}
cases.append(case('P2-005','真实proposal空问题列表自动交付',[
    initial(),confirm([tool('search_local',{'category':'cpu'}),tool('evaluate',{'draft':draft}),text(saved_final)]),
    {'kind':'retry','expect':{'versions':1,'builder_calls':0}},
    {'kind':'refresh','expect':{'versions':1,'builder_calls':0}}
],source=saved_path+'；末次模型输出原样复用，其前工具调用为人工 oracle'))

ext=copy.deepcopy(external['candidate'])
page=external['page']
ext['specs']={'socket':'AM4','tdp_w':105,'has_igpu':False}
# Reconstruct supported request as in the existing registration replay; not a claim about original args.
ext['unknown']=[]
ext['evidence']=['source-2']
ext['field_evidence']={k:'source-2' for k in ['model','specs.socket','specs.tdp_w','specs.has_igpu','price_cny']}
ext['field_quotes']={'model':ext['model'],'specs.socket':'AM4接口','specs.tdp_w':'105W 功率','specs.has_igpu':'不支持核显','price_cny':page['text']}
ed=copy.deepcopy(draft);ed['selection']['cpu']=ext['id']
url='https://hardware.eval.invalid/5900x'
webops=[tool('search_web',{'query':'Ryzen 9 5900X 官方规格 AM4'}),
        tool('read_page',{'url':url}),tool('register_candidate',ext),
        tool('evaluate',{'draft':ed}),finish(ed,issues=['外部候选缺价格和芯片组支持证据'])]
cases.append(case('P2-006','外部规格来源入候选，偶然网页价格不能进入报价',[
    initial(),confirm(webops,versions=0,outcome='proposal',validation='',require_tools=['search_web','read_page','register_candidate','evaluate'],
        issues_contain=['外部候选缺价格'],missing_prices=1,candidate_specs={ext['id']:{'socket':'AM4','tdp_w':105,'has_igpu':False}})
],source=external_path+'；规格摘录为已保存原文，操作为重建 oracle；网页包装为离线夹具'))

cases.append(case('P2-007','明确询价只用本地快照，不联网查价',[
    initial(),confirm(),
    msg('看看现在价格，按已有参考价继续给方案',[],
        {'versions':2,'outcome':'ready','forbid_tools':['search_web','read_page'],'require_tools':['search_local','evaluate']},
        next='plan',builder=build())
]))
cases.append(case('P2-008','预算和游戏分辨率未知仍可讨论，不编造',[
    msg('想玩游戏，预算还没定，先说说选择方向',[op('use_case.type','gaming',kind='fact')],
        {'versions':0,'builder_calls':0,'fields':{'budget_cny':field(status='unknown'),'use_case.resolution':field(status='unknown')}},reply='可以先比较游戏类型与升级方向。'),
    {'kind':'refresh','expect':{'versions':0,'fields':{'budget_cny':field(status='unknown')}}}
]))
cases.append(case('P2-009','面板编辑与聊天同样保留其他状态',[
    msg('预算8000，尽量安静',[op('budget_cny',8000),op('noise_pref','silent','prefer')],{'versions':0},next='confirm'),
    {'kind':'edit','edit':[op('budget_cny',6000)],'expect':{'versions':0,'builder_calls':0,'fields':{'budget_cny':field(6000),'noise_pref':field('silent',strength='prefer')}}},
    msg('预算改7000',[op('budget_cny',7000)],{'versions':0,'builder_calls':0,'fields':{'budget_cny':field(7000),'noise_pref':field('silent',strength='prefer')}}),
    {'kind':'refresh','expect':{'versions':0,'fields':{'budget_cny':field(7000),'noise_pref':field('silent',strength='prefer')}}}
]))
cases.append(case('P2-010','已有配置中的备选讨论不执行、不替换当前要求',[
    initial(),confirm(),
    msg('如果换更好的CPU会怎样，先别执行',[op('priority',['cpu'],action='alternative')],
        {'versions':1,'builder_calls':0,'alternatives':1,'fields':{'priority':field(status='unknown')}},reply='可以比较升级方向，当前方案保持不变。'),
    {'kind':'refresh','expect':{'versions':1,'builder_calls':0}}
]))
# A deliberately conflicting motherboard is a labeled synthetic mutation of a saved candidate.
bad=copy.deepcopy(next(c for c in saved['candidates'] if c['category']=='motherboard'))
bad['id']='eval-only-am5-board';bad['model']='Synthetic AM5 socket conflict fixture';bad['specs']['socket']='AM5'
bad_draft=copy.deepcopy(draft);bad_draft['selection']['motherboard']=bad['id']
cases.append(case('P2-011','模型错误ready不能掩盖真实插槽冲突',[
    initial(),confirm([tool('search_local',{'category':'motherboard'}),tool('evaluate',{'draft':bad_draft}),finish(bad_draft,'ready'),finish(bad_draft,issues=['CPU和主板插槽不兼容'])],
        versions=0,outcome='proposal',validation='fail',issues_contain=['插槽'],require_tools=['evaluate'])
]))
cases.append(case('P2-012','实际校验失败后工具循环修正并交付',[
    initial(),confirm([tool('search_local',{'category':'motherboard'}),tool('evaluate',{'draft':bad_draft}),tool('search_local',{'category':'motherboard'}),tool('evaluate',{'draft':draft}),finish()],
        model_input_contains=['SOCKET_MATCH','fail'])
]))
selected_ids={c['id'] for c in saved['candidates']}
selected_evidence=[e for e in saved['evidence'] if (e.get('candidate_id') or e['title'].split(' · ')[0]) in selected_ids]
suite={'version':'planning-v2.0-offline','provenance':'Saved user inputs/products/pages plus explicit hand-authored model oracles. Not live model accuracy; not representative catalog completion rate.',
       'catalog':{'date':saved['quote']['snapshot_date'],'candidates':saved['candidates']+[upgrade,bad],'evidence':selected_evidence},
       'pages':{url:'<html><body><pre>'+html.escape(page['text'])+'</pre></body></html>',
                'search:Ryzen 9 5900X 官方规格 AM4':json.dumps({'organic_results':[{'title':'保存的5900X规格资料','link':url,'snippet':'AM4 / 105W；正文来源见fixture provenance'}]},ensure_ascii=False)},
       'cases':cases}
(OUT/'v2.0.json').write_bytes((json.dumps(suite,ensure_ascii=False,indent=2)+'\n').encode('utf-8'))
manifest={'sources':{p:hashlib.sha256((ROOT/p).read_bytes()).hexdigest() for p in [saved_path,upgrade_path,external_path]},
          'suite_sha256':hashlib.sha256((OUT/'v2.0.json').read_bytes()).hexdigest(),
          'synthetic_changes':['eval-only-am5-board socket mutation','fixture URL and HTML wrapper','all Screening incremental operations and tool trajectories except saved final P2-005']}
(OUT/'provenance.json').write_bytes((json.dumps(manifest,ensure_ascii=False,indent=2)+'\n').encode('utf-8'))
print('Built',len(cases),'cases with',sum(len(c['steps']) for c in cases),'steps')
