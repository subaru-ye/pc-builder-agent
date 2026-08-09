package main

// 兼容层保留 cmd/builds 原有内部函数名；确定性渲染实现统一位于 internal/presenter，
// 供 CLI 与产品 API 复用，避免两份金额和 diff 口径。

import (
	"encoding/json"

	"github.com/subaru-ye/pc-builder-agent/internal/presenter"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type wireSSD = presenter.WireSSD
type wireSel = presenter.WireSelection
type wireDraft = presenter.WireDraft

type buildRow struct{ presenter.BuildRow }

func decodeBuild(build store.BuildVersion, spec json.RawMessage) (buildRow, error) {
	row, err := presenter.DecodeBuild(build, spec)
	return buildRow{BuildRow: row}, err
}

func decodeBuilds(builds []store.BuildVersion) ([]buildRow, error) {
	rows, err := presenter.DecodeBuilds(builds)
	if err != nil {
		return nil, err
	}
	out := make([]buildRow, len(rows))
	for i, row := range rows {
		out[i] = buildRow{BuildRow: row}
	}
	return out, nil
}

func (r buildRow) skus() []string { return r.SKUs() }
func (r buildRow) budgetCNY() int { return r.BudgetCNY() }

func renderSessions(sessions []store.SessionSummary) string {
	return presenter.RenderSessions(sessions)
}

func renderList(session string, rows []buildRow) string {
	values := make([]presenter.BuildRow, len(rows))
	for i, row := range rows {
		values[i] = row.BuildRow
	}
	return presenter.RenderList(session, values)
}

func renderDiff(from, to buildRow) (string, error) {
	return presenter.RenderDiff(from.BuildRow, to.BuildRow)
}

func renderExport(row buildRow, names map[string]string) (string, error) {
	return presenter.RenderExport(row.BuildRow, names)
}

func parseFen(value string) (int, bool) { return presenter.ParseFen(value) }
func signedFen(fen int) string          { return presenter.SignedFen(fen) }
