"""Version the snapshot-11 current-catalog regressions using the existing runner and frozen records.

No provider calls. Hand-authored decisions prove execution and feasible witnesses,
not autonomous model success. Historical results and expectations are not edited.
Compared with the r2 (snapshot 9) revision: c5 exercises the new ITX SFX PSU rule,
c6 exercises the new office iGPU scenario (cpu-r5-4600g), and c7 is rebuilt around
the recorded cooler-capacity gap because all motherboard memory-speed fields were
completed in the 2026-09-16 data update (the old unknown-frequency premise is gone).
"""
import copy
import hashlib
import json
import subprocess
from pathlib import Path

from build_snapshot_pair import ROOT, catalog, load_pinned, text, tool
from build_behavior_extension import build_cases, field, operation, message

OUT = ROOT / "internal/planningeval/testdata/current-178-20260917"
FREEZE = ROOT / "artifacts/current-178-freeze-20260917"
SAVED = ROOT / "internal/planning/testdata/complete_proposal_recording.json"
CPU_RECORDED = ROOT / "internal/planningeval/testdata/current-123-20260915-r2/cpu-recorded/suite.json"
LIMITATIONS = ["Offline authored decisions or frozen real responses, not a fresh model quality score.",
               "Current-catalog variants are separate from original historical results.",
               "Existing compatibility rules do not cover all physical fit, BIOS, noise or performance evidence.",
               "RX 6600/7600 and 5600G/5700G intake is deferred; see docs/data/数据获取与发布规则.md §5.4.",
               "10 coolers still have unknown cooling_capacity_w after buyer recheck; scenarios keep unknown honestly."]


def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def source_commit():
    return subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()


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
                source="当前快照11目录（126件active）；明确人工协议轨迹，不是新真实模型回答。", steps=steps, **extra)


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
    itx_sfx = draft("current-itx-sfx", cpu="cpu-r5-7600x", gpu=None, motherboard="mb-msi-b650i-edge",
                    memory="mem-kingston-beast-16-5200", psu="psu-cm-v850sfx-gold-white", **{"case": "case-coolermaster-nr200p"})
    c5 = case(5, "ITX机箱电源形态规则：ATX电源不兼容，换SFX电源后通过", [
        initial(9000, [operation("size_pref", "itx", "constraint")], use="general", phrase="预算9000，普通办公，必须ITX小机箱。"),
        dict(kind="confirm", builder_oracle=outputs(itx, [assessment("size_pref", evidence=["local:mb-msi-b650i-edge", "local:case-coolermaster-nr200p"])],
                 ["所选电源为ATX，机箱仅支持SFX/SFX-L电源形态，安装不兼容。"]),
             expect=dict(versions=0, outcome="proposal", missing_prices=0, issues_contain=["电源形态"],
                         require_tools=["search_local_batch", "evaluate"], reply_forbidden=["全部兼容", "已正式交付"])),
        message("电源换成酷冷至尊V SFX Gold 850W白色，其他保持不变，继续校验。", [],
                dict(versions=1, outcome="ready", validation="pass", selected_parts={"psu": "psu-cm-v850sfx-gold-white"},
                     fields={"size_pref": field("itx"), "budget_cny": field(9000)}),
                next_action="plan", builder=outputs(itx_sfx, [assessment("size_pref", evidence=["local:mb-msi-b650i-edge", "local:case-coolermaster-nr200p"])])), refresh(1)])
    office = draft("current-office-igpu", cpu="cpu-r5-4600g", gpu=None, motherboard="mb-msi-b550m-pro-vdh-wifi",
                   ssd=[dict(sku="ssd-crucial-bx500-1tb", quantity=1)])
    c6 = case(6, "4000元办公核显方案（无独显、CPU自带核显）及3000元预算不足时的续聊", [
        initial(4000, use="general"), confirm(office, budget_ceiling_cny="4000"),
        message("预算只有3000了，按现有要求继续看看。", [operation("budget_cny", 3000, "constraint")],
                dict(versions=1, outcome="proposal", issues_any=["预算", "budget"], fields={"budget_cny": field(3000)},
                     reply_forbidden=["已经正式交付"]), next_action="plan", builder=outputs(office, issues=["当前候选总价3134.8元，超过3000元预算。"])), refresh(1)])
    gap = draft("current-thermal-gap", cooler="cooler-noctua-nh-u12s")
    resolved = copy.deepcopy(gap)
    resolved["selection"]["cooler"] = "cooler-deepcool-ag400"
    c7 = case(7, "散热器解热容量未知保留方案，换有容量数据备选后重新校验", [
        initial(7500), dict(kind="confirm", builder_oracle=outputs(gap, issues=["所选散热器散热能力字段缺失，无法判定温控余量；可换有容量数据的备选。"]),
            expect=dict(versions=0, outcome="proposal", issues_any=["散热", "解热"],
                        reply_forbidden=["全部兼容", "温控保证", "已经正式交付"])),
        message("改用有散热容量数据的备选散热器，继续校验。", [], dict(versions=1, outcome="ready", validation="pass",
                model_input_contains=["previous_proposal"], selected_parts={"cooler": "cooler-deepcool-ag400"}),
                next_action="plan", builder=outputs(resolved)), refresh(1)])
    return [c1, c2, c3, c4, c5, c6, c7] + build_cases()


def save_suite(name, suite, sources):
    target = OUT / name
    write(target / "suite.json", suite)
    write(target / "provenance.json", dict(suite_sha256=sha(target / "suite.json"), database_snapshot_sha256=SNAPSHOT_SHA,
          generator_sha256=sha(Path(__file__)), source_files={str(p.relative_to(ROOT)).replace("\\", "/"): sha(p) for p in sources},
          source_commit=source_commit(), snapshot_id=11, active_parts=126,
          limitations=LIMITATIONS))


SNAPSHOT_SHA = json.loads((FREEZE / "manifest.json").read_text())["sha256"]


def main():
    assert not OUT.exists(), "Create an explicit new revision instead of overwriting fixtures"
    data = load_pinned(FREEZE / "database-snapshot.json", SNAPSHOT_SHA)
    current = catalog(data, 11)
    assert len(current["candidates"]) == 126 and all(c["price_cny"] for c in current["candidates"])
    write(OUT / "catalog.json", current)
    saved = json.loads(SAVED.read_bytes())["result"]
    suite = dict(version="current-178-mechanisms-20260917", provenance="完整快照11当前目录（126件active）；r178纳入ITX SFX电源规则与办公核显场景，判分要求不降低。", catalog=current, pages={}, cases=current_cases(saved))
    save_suite("mechanisms", suite, [SAVED, FREEZE / "manifest.json"])
    for name, source in [("screening-recorded", ROOT / "artifacts/planning-screening-full-recheck-recorded-suite-20260915/suite.json"),
                         ("thermal-recorded", ROOT / "artifacts/planning-local-reference-recorded-20260915-r1/suite.json"),
                         ("cpu-recorded", CPU_RECORDED)]:
        recorded = json.loads(source.read_bytes())
        recorded["version"] = "current-178-" + name + "-20260917"
        recorded["catalog"] = current
        recorded["provenance"] += " Current snapshot-11 126-part catalog variant; original answers and assertions retained."
        save_suite(name, recorded, [source])
    write(OUT / "budget.json", dict(model_requests_limit=12, model_requests_used=0, external_search_limit=3,
          external_search_used=0, page_read_limit=6, page_read_used=0, embedding_requests=0, buyer_requests=0,
          policy="Only explicit registered live plan may spend; failures count; stop at cumulative limit without new batches."))
    print("Frozen 10 mechanism cases (incl. ITX SFX and office iGPU), 20 Screening recordings and 1 Builder failure on the snapshot-11 126-part catalog")


if __name__ == "__main__":
    main()
