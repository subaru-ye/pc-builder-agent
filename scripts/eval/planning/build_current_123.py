"""Version current-catalog regressions using existing runner and frozen records.

No provider calls. Hand-authored decisions prove execution and feasible witnesses,
not autonomous model success. Historical results and expectations are not edited.
"""
import copy
import hashlib
import json
from pathlib import Path

from build_snapshot_pair import ROOT, catalog, load_pinned, text, tool
from build_behavior_extension import build_cases, field, operation, message

OUT = ROOT / "internal/planningeval/testdata/current-123-20260915-r2"
FREEZE = ROOT / "artifacts/current-123-freeze-20260915"
SNAPSHOT_SHA = "12b00273edafcbe7ff7d7e78441167ec23ab61b91a31735222ec647bd0de6986"
SAVED = ROOT / "internal/planning/testdata/complete_proposal_recording.json"


def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def draft(name, **changes):
    parts = dict(cpu="cpu-r5-5600", gpu="gpu-gb-5060-windforce", motherboard="mb-msi-b550m-pro-vdh-wifi",
                 memory="mem-crucial-ballistix-16-3200", ssd=[dict(sku="ssd-zhitai-ti600-1tb", quantity=1)],
                 psu="psu-msi-mag-a650bn", case="case-asus-prime-ap201", cooler="cooler-deepcool-ag400")
    parts.update(changes)
    return dict(schema_version=1, build_ref=name, requirement_ref="current", selection=parts,
                rationale={c: "离线构造的当前目录候选，以实际报价和现有规则核验；无性能实测承诺。" for c in parts})


def finish(d, assessments=None, issues=None):
    return text(dict(outcome="proposal", reply="已保留当前选配与核验结果。", draft=d, issues=issues or [],
                     assessments=assessments or [], assumptions=[]))


def outputs(d, assessments=None, issues=None):
    queries = [dict(category=c, limit=24) for c in d["selection"]]
    final = finish(d, assessments, issues)
    return [tool("search_local_batch", dict(queries=queries)), tool("evaluate", dict(draft=d)), final, copy.deepcopy(final)]


def assessment(name, status="met", explanation="以当前选件和工具核验为依据。", evidence=None):
    return dict(field=name, status=status, explanation=explanation, evidence=evidence or [])


def initial(budget, extra=None, use="productivity", phrase=None):
    return message(phrase or f"预算{budget}，用于{'普通办公' if use == 'general' else '视频剪辑'}。",
                   [operation("budget_cny", budget, "constraint"), operation("use_case.type", use)] + (extra or []),
                   dict(builder_calls=0))


def confirm(d, versions=1, assessments=None, issues=None, **expect):
    return dict(kind="confirm", builder_oracle=outputs(d, assessments, issues),
                expect=dict(versions=versions, outcome="ready", validation="pass", missing_prices=0,
                            require_tools=["search_local_batch", "evaluate"], **expect))


def refresh(versions):
    return dict(kind="refresh", expect=dict(versions=versions, builder_calls=0))


def case(number, title, steps, **extra):
    return dict(id=f"C123-{number:03}", title=title,
                source="当前123件完整目录；明确人工协议轨迹，不是新真实模型回答。", steps=steps, **extra)


def current_cases(saved):
    base = draft("current-am4")
    upgraded = draft("current-upgrade", cpu="cpu-r7-5700x")
    prefs = [operation("noise_pref", "silent", "constraint", "prefer")]
    c1 = case(1, "首次确认交付、直接升级CPU、降预算保留有效需求与旧版本", [
        initial(7500, prefs, phrase="预算7500，用于视频剪辑，尽量安静。"), confirm(base, budget_ceiling_cny="7500"),
        message("直接把CPU升级为Ryzen 7 5700X，其他配件尽量不动。", [operation("free.keep_others", "其他配件尽量不动", "constraint", "prefer")],
                dict(versions=2, outcome="ready", cpu_changed=True, preserve_other_parts=True,
                     require_tools=["search_local_batch", "evaluate"], fields={"budget_cny": field(7500), "noise_pref": field("silent", strength="prefer")},
                     model_input_contains=["base_draft", "cpu-r5-5600"], reply_forbidden=["预算是多少", "CPU是什么"]),
                next_action="plan", builder=outputs(upgraded)),
        message("预算降到6500，其他要求保留，直接调整配置。", [operation("budget_cny", 6500, "constraint")],
                dict(versions=3, outcome="ready", budget_ceiling_cny="6500", selected_parts={"gpu": base["selection"]["gpu"]},
                     fields={"budget_cny": field(6500), "noise_pref": field("silent", strength="prefer")}),
                next_action="plan", builder=outputs(base)), refresh(3), dict(kind="retry", expect=dict(versions=3, builder_calls=0))])
    old = saved["draft"]
    c2 = case(2, "旧版含四件退库配件，读取历史并更换为当前有价候选", [
        refresh(1), message("预算7000，沿用原需求，把退库配件换成当前有报价的配件，直接继续选配。",
            [operation("budget_cny", 7000, "constraint"), operation("use_case.type", "productivity")],
            dict(versions=2, outcome="ready", missing_prices=0, budget_ceiling_cny="7000",
                 model_input_contains=["base_draft", "unresolved_base_ids", "gpu-sapphire-6600-pulse"],
                 search_candidates={"gpu-sapphire-6600-pulse": False, "mem-gskill-ripjawsv-32-3200": False,
                                    "ssd-crucial-p3plus-1tb": False, "case-montech-air-903-max": False,
                                    "gpu-gb-5060-windforce": True},
                 reply_forbidden=["预算是多少", "原配置是什么"]), next_action="plan", builder=outputs(base)), refresh(2)],
        previous_build=dict(selection=dict(schema_version=1, build_ref=old["build_ref"], parts=old["selection"]),
                            requirement=dict(schema_version=1, budget_cny=7000, use_case=dict(type="productivity")),
                            source="complete_proposal_recording.json的历史草稿、规格、报价与校验；不进入当前检索目录。", snapshot=saved))
    owned_cpu = dict(category="cpu", model="AMD Ryzen 5 5600", quantity=1)
    owned_memory = dict(category="memory", model="G.Skill Ripjaws V 32GB (2x16GB) DDR4-3200 CL16", quantity=1)
    c3 = case(3, "准确已有退库内存保留需求，允许明确改成新购内存后继续", [
        initial(6000, [operation("budget_basis", "new_purchase", "constraint"), operation("owned_parts", [owned_cpu, owned_memory])],
                phrase=f"用于视频剪辑，已有{owned_cpu['model']}和{owned_memory['model']}各一件，新增采购预算6000。"),
        dict(kind="confirm", builder_oracle=[tool("search_local", dict(category="memory", query="Ripjaws V 32GB")),
            text(dict(outcome="clarify", reply="已知CPU和内存型号已保留；当前目录没有这套内存，暂缺重新核验所需资料，可补充来源或选择新购内存。", issues=[], assessments=[], assumptions=[]))],
            expect=dict(versions=0, outcome="clarify", fields={"owned_parts": field([owned_cpu, owned_memory])},
                        search_candidates={"mem-gskill-ripjawsv-32-3200": False}, model_input_contains=["owned_candidates", owned_memory["model"]])),
        message("那内存也买新的，只保留已有AMD Ryzen 5 5600，新增采购预算仍为6000，继续选配。",
                [operation("owned_parts", [owned_cpu])], dict(versions=1, outcome="ready", purchase_budget=True,
                fields={"budget_cny": field(6000), "budget_basis": field("new_purchase"), "owned_parts": field([owned_cpu])}),
                next_action="plan", builder=outputs(base)), refresh(1)])
    c4 = case(4, "必须静音无测量证据则保留候选，改为软偏好后继续", [
        initial(7500, [operation("noise_pref", "silent", "constraint")], phrase="预算7500，用于视频剪辑，必须静音。"),
        dict(kind="confirm", builder_oracle=outputs(base, [assessment("noise_pref", "unknown", "当前资料没有整机噪声测量，不能保证静音。")], ["必须静音缺少整机噪声测量证据。"]),
             expect=dict(versions=0, outcome="proposal", issues_any=["noise_pref", "噪"], validation="pass",
                         reply_forbidden=["全部兼容", "静音保证", "已正式交付"])),
        message("静音改成尽量即可，预算和其他用途不变，继续。", [operation("noise_pref", "silent", "constraint", "prefer")],
                dict(versions=1, outcome="ready", fields={"budget_cny": field(7500), "noise_pref": field("silent", strength="prefer")}),
                next_action="plan", builder=outputs(base)), refresh(1)])
    itx = draft("current-itx", cpu="cpu-r5-7600x", gpu=None, motherboard="mb-msi-b650i-edge",
                memory="mem-kingston-beast-16-5200", **{"case": "case-coolermaster-nr200p"})
    c5 = case(5, "ITX候选有报价但电源安装规格缺失，不能据12条规则宣称完整兼容", [
        initial(9000, [operation("size_pref", "itx", "constraint")], use="general", phrase="预算9000，普通办公，必须ITX小机箱。"),
        dict(kind="confirm", builder_oracle=outputs(itx, [assessment("size_pref", evidence=["local:mb-msi-b650i-edge", "local:case-coolermaster-nr200p"])],
                 ["电源与ITX机箱的安装尺寸缺少规格，尚未核实；现有规则未覆盖这一项。"]),
             expect=dict(versions=0, outcome="proposal", missing_prices=0, issues_contain=["安装尺寸"],
                         require_tools=["search_local_batch", "evaluate"], reply_forbidden=["全部兼容", "已正式交付"])), refresh(0)])
    office = draft("current-office", cpu="cpu-i5-12600k", gpu=None, motherboard="mb-msi-h610m-g-ddr4",
                   ssd=[dict(sku="ssd-crucial-bx500-1tb", quantity=1)])
    c6 = case(6, "4000元办公有价方案及3000元预算不足时的续聊", [
        initial(4000, use="general"), confirm(office, budget_ceiling_cny="4000"),
        message("预算只有3000了，按现有要求继续看看。", [operation("budget_cny", 3000, "constraint")],
                dict(versions=1, outcome="proposal", issues_any=["预算", "budget"], fields={"budget_cny": field(3000)},
                     reply_forbidden=["已经正式交付"]), next_action="plan", builder=outputs(office, issues=["当前候选总价3975.12元，超过3000元预算。"])), refresh(1)])
    gap = draft("current-spec-gap", cpu="cpu-r5-7600x", motherboard="mb-gb-b650-aorus-elite-ax",
                memory="mem-kingston-beast-16-5200", **{"case": "case-fractal-pop-air"})
    resolved = copy.deepcopy(gap)
    resolved["selection"]["motherboard"] = "mb-msi-b650m-mortar-wifi"
    c7 = case(7, "主板频率未知保留方案，换有规格备选后重新校验", [
        initial(12000), dict(kind="confirm", builder_oracle=outputs(gap, issues=["主板内存频率上限未知，需核实或换有规格备选。"]),
            expect=dict(versions=0, outcome="proposal", issues_any=["MEMORY_SPEED", "频率"],
                        reply_forbidden=["全部兼容", "已经正式交付"])),
        message("改用资料完整的主板备选，继续校验。", [], dict(versions=1, outcome="ready", validation="pass",
                model_input_contains=["previous_proposal"], selected_parts={"motherboard": "mb-msi-b650m-mortar-wifi"}),
                next_action="plan", builder=outputs(resolved)), refresh(1)])
    return [c1, c2, c3, c4, c5, c6, c7] + build_cases()


def save_suite(name, suite, sources):
    target = OUT / name
    write(target / "suite.json", suite)
    write(target / "provenance.json", dict(suite_sha256=sha(target / "suite.json"), database_snapshot_sha256=SNAPSHOT_SHA,
          generator_sha256=sha(Path(__file__)), source_files={str(p.relative_to(ROOT)).replace("\\", "/"): sha(p) for p in sources},
          source_commit="6e7a9b8b2baaaf4f1bc3b314eb4cb6582a31fbbd", snapshot_id=9, active_parts=123,
          limitations=["Offline authored decisions or frozen real responses, not a fresh model quality score.",
                       "Current-catalog variants are separate from original historical results.",
                       "Existing compatibility rules do not cover all physical fit, BIOS, noise or performance evidence."]))


def main():
    assert not OUT.exists(), "Create an explicit new revision instead of overwriting fixtures"
    data = load_pinned(FREEZE / "database-snapshot.json", SNAPSHOT_SHA)
    current = catalog(data, 9)
    assert len(current["candidates"]) == 123 and all(c["price_cny"] for c in current["candidates"])
    write(OUT / "catalog.json", current)
    saved = json.loads(SAVED.read_bytes())["result"]
    suite = dict(version="current-123-mechanisms-20260915-r2", provenance="完整当前目录；r2补正人工输入引用、待解决回复和历史确认前提，判分要求不降低。", catalog=current, pages={}, cases=current_cases(saved))
    save_suite("mechanisms", suite, [SAVED, FREEZE / "manifest.json"])
    for name, source in [("screening-recorded", ROOT / "artifacts/planning-screening-full-recheck-recorded-suite-20260915/suite.json"),
                         ("thermal-recorded", ROOT / "artifacts/planning-local-reference-recorded-20260915-r1/suite.json")]:
        recorded = json.loads(source.read_bytes())
        recorded["version"] = "current-123-" + name + "-20260915"
        recorded["catalog"] = current
        recorded["provenance"] += " Current 123-part catalog variant; original answers and assertions retained."
        save_suite(name, recorded, [source])
    write(OUT / "budget.json", dict(model_requests_limit=12, model_requests_used=0, external_search_limit=3,
          external_search_used=0, page_read_limit=6, page_read_used=0, embedding_requests=0, buyer_requests=0,
          policy="Only explicit registered live plan may spend; failures count; stop at cumulative limit without new batches."))
    print("Frozen 10 mechanism cases, 20 Screening recordings and 1 Builder failure on the current 123-part catalog")


if __name__ == "__main__":
    main()
