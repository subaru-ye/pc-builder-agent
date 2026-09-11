package sharing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/subaru-ye/pc-builder-agent/internal/presenter"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type Store interface {
	CreateBuildShare(context.Context, store.CreateBuildShareParams) (store.BuildShare, bool, error)
	BuildSharesByOwner(context.Context, string, string, int) ([]store.BuildShare, error)
	RevokeBuildShareByID(context.Context, string, string, int, string) error
	RevokeBuildShareByToken(context.Context, string, []byte) error
	PublicBuildShare(context.Context, []byte) (store.BuildShare, error)
}

type Presenter interface {
	Build(context.Context, string, int) (presenter.BuildView, error)
	Markdown(context.Context, string, int) (string, error)
}

type Service struct {
	store     Store
	presenter Presenter
	codec     *TokenCodec
	webBase   *url.URL
}

func New(st Store, builds Presenter, codec *TokenCodec, publicWebBaseURL string) (*Service, error) {
	if st == nil || builds == nil || codec == nil {
		return nil, fmt.Errorf("sharing: store/presenter/token codec 不能为空")
	}
	base, err := url.Parse(publicWebBaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("sharing: PUBLIC_WEB_BASE_URL 无效")
	}
	return &Service{store: st, presenter: builds, codec: codec, webBase: base}, nil
}

type Share struct {
	SchemaVersion int        `json:"schema_version"`
	ID            string     `json:"id"`
	Version       int        `json:"version"`
	Token         string     `json:"token"`
	URL           string     `json:"url"`
	CreatedAt     time.Time  `json:"created_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
}

type ShareRecord struct {
	SchemaVersion int        `json:"schema_version"`
	ID            string     `json:"id"`
	Version       int        `json:"version"`
	CreatedAt     time.Time  `json:"created_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
}

func (s *Service) Create(ctx context.Context, ownerID, sessionID string, version int, requestID string) (Share, bool, error) {
	token := s.codec.Derive(ownerID, sessionID, version, requestID)
	record, created, err := s.store.CreateBuildShare(ctx, store.CreateBuildShareParams{
		OwnerID: ownerID, SessionID: sessionID, Version: version, PublicID: uuid.NewString(),
		ClientRequestID: requestID, TokenHash: HashToken(token),
	})
	if err != nil {
		return Share{}, false, err
	}
	shareURL := *s.webBase
	shareURL.Path = strings.TrimRight(shareURL.Path, "/") + "/share/" + token
	shareURL.RawQuery, shareURL.Fragment = "", ""
	return Share{SchemaVersion: 1, ID: record.PublicID, Version: record.Version, Token: token,
		URL: shareURL.String(), CreatedAt: record.CreatedAt, RevokedAt: record.RevokedAt}, created, nil
}

func (s *Service) List(ctx context.Context, ownerID, sessionID string, version int) ([]ShareRecord, error) {
	records, err := s.store.BuildSharesByOwner(ctx, ownerID, sessionID, version)
	if err != nil {
		return nil, err
	}
	out := make([]ShareRecord, 0, len(records))
	for _, record := range records {
		out = append(out, ShareRecord{SchemaVersion: 1, ID: record.PublicID, Version: record.Version,
			CreatedAt: record.CreatedAt, RevokedAt: record.RevokedAt})
	}
	return out, nil
}

func (s *Service) RevokeByID(ctx context.Context, ownerID, sessionID string, version int, publicID string) error {
	if _, err := uuid.Parse(publicID); err != nil {
		return store.ErrShareNotFound
	}
	return s.store.RevokeBuildShareByID(ctx, ownerID, sessionID, version, publicID)
}

func (s *Service) RevokeByToken(ctx context.Context, ownerID, token string) error {
	if !ValidToken(token) {
		return store.ErrShareNotFound
	}
	return s.store.RevokeBuildShareByToken(ctx, ownerID, HashToken(token))
}

type PublicUseCase struct {
	Type       schemas.UseCaseType `json:"type"`
	Titles     []string            `json:"titles"`
	Resolution *schemas.Resolution `json:"resolution"`
	FPSTarget  *int                `json:"fps_target"`
}

type PublicBrandPref struct {
	CPU schemas.CPUBrand `json:"cpu"`
	GPU schemas.GPUBrand `json:"gpu"`
}

type PublicRequirementSummary struct {
	KnownFields       *[]string          `json:"known_fields,omitempty"`
	BudgetCNY         string             `json:"budget_cny"`
	BudgetFlexPercent int                `json:"budget_flex_percent"`
	UseCase           PublicUseCase      `json:"use_case"`
	SizePref          schemas.SizePref   `json:"size_pref"`
	NoisePref         schemas.NoisePref  `json:"noise_pref"`
	BrandPref         PublicBrandPref    `json:"brand_pref"`
	ExistingParts     []schemas.Category `json:"existing_parts"`
	Priority          []schemas.Category `json:"priority"`
}

type PublicBuildSummary struct {
	SchemaVersion  int                      `json:"schema_version"`
	Version        int                      `json:"version"`
	ParentVersion  *int                     `json:"parent_version"`
	IntentLabel    string                   `json:"intent_label"`
	TotalCNY       string                   `json:"total_cny"`
	SnapshotDate   string                   `json:"snapshot_date"`
	PriceFreshness presenter.PriceFreshness `json:"price_freshness,omitempty"`
	OverallStatus  schemas.OverallStatus    `json:"overall_status"`
	CreatedAt      string                   `json:"created_at"`
}

type PublicValidationCheck struct {
	RuleID        schemas.RuleID   `json:"rule_id"`
	Outcome       schemas.Outcome  `json:"outcome"`
	Severity      schemas.Severity `json:"severity"`
	MissingFields []string         `json:"missing_fields"`
	Detail        string           `json:"detail"`
}

type PublicValidation struct {
	OverallStatus schemas.OverallStatus   `json:"overall_status"`
	Checks        []PublicValidationCheck `json:"checks"`
}

type PublicShareMeta struct {
	CreatedAt time.Time `json:"created_at"`
}

type PublicBuildView struct {
	Sources       []PublicSource           `json:"sources,omitempty"`
	SchemaVersion int                      `json:"schema_version"`
	Summary       PublicBuildSummary       `json:"summary"`
	Requirement   PublicRequirementSummary `json:"requirement"`
	Parts         []presenter.PartLine     `json:"parts"`
	Quote         presenter.QuoteView      `json:"quote"`
	Validation    PublicValidation         `json:"validation"`
	Disclaimers   []string                 `json:"disclaimers"`
	Share         PublicShareMeta          `json:"share"`
}

type PublicSource struct {
	URL        string `json:"url"`
	Title      string `json:"title"`
	CapturedAt string `json:"captured_at"`
}

func (s *Service) Public(ctx context.Context, token string) (PublicBuildView, error) {
	share, err := s.publicShare(ctx, token)
	if err != nil {
		return PublicBuildView{}, err
	}
	view, err := s.presenter.Build(ctx, share.SessionID, share.Version)
	if err != nil {
		return PublicBuildView{}, err
	}
	return toPublic(view, share.CreatedAt)
}

func (s *Service) PublicMarkdown(ctx context.Context, token string) (string, int, string, error) {
	share, err := s.publicShare(ctx, token)
	if err != nil {
		return "", 0, "", err
	}
	view, err := s.presenter.Build(ctx, share.SessionID, share.Version)
	if err != nil {
		return "", 0, "", err
	}
	markdown, err := s.presenter.Markdown(ctx, share.SessionID, share.Version)
	return markdown, share.Version, view.Quote.SnapshotDate, err
}

func (s *Service) publicShare(ctx context.Context, token string) (store.BuildShare, error) {
	if !ValidToken(token) {
		return store.BuildShare{}, store.ErrShareNotFound
	}
	return s.store.PublicBuildShare(ctx, HashToken(token))
}

func toPublic(view presenter.BuildView, createdAt time.Time) (PublicBuildView, error) {
	requirement, err := schemas.DecodeRequirementSpec(view.Requirement)
	var input schemas.PlanningInput
	known := []string(nil)
	var knownFields *[]string
	if json.Unmarshal(view.Requirement, &input) == nil && input.SchemaVersion == 2 {
		err = nil
		requirement = schemas.RequirementSpec{}
		known = []string{}
		read := func(key string, dst any) {
			if f := input.State.Fields[key]; f.Status == "active" {
				if json.Unmarshal(f.Value, dst) == nil {
					known = append(known, key)
				}
			}
		}
		read("budget_cny", &requirement.BudgetCNY)
		read("budget_flex", &requirement.BudgetFlex)
		read("use_case.type", &requirement.UseCase.Type)
		read("use_case.titles", &requirement.UseCase.Titles)
		read("use_case.resolution", &requirement.UseCase.Resolution)
		read("use_case.fps_target", &requirement.UseCase.FPSTarget)
		read("size_pref", &requirement.SizePref)
		read("noise_pref", &requirement.NoisePref)
		read("brand_pref.cpu", &requirement.BrandPref.CPU)
		read("brand_pref.gpu", &requirement.BrandPref.GPU)
		read("existing_parts", &requirement.ExistingParts)
		read("priority", &requirement.Priority)
		if requirement.UseCase.Type == "" {
			requirement.UseCase.Type = "unknown"
		}
		if requirement.SizePref == "" {
			requirement.SizePref = "unknown"
		}
		if requirement.NoisePref == "" {
			requirement.NoisePref = "unknown"
		}
		if requirement.BrandPref.CPU == "" {
			requirement.BrandPref.CPU = "unknown"
		}
		if requirement.BrandPref.GPU == "" {
			requirement.BrandPref.GPU = "unknown"
		}
		knownFields = &known
	}
	if err != nil {
		return PublicBuildView{}, fmt.Errorf("sharing: 公开需求摘要解码失败: %w", err)
	}
	titles := requirement.UseCase.Titles
	if titles == nil {
		titles = []string{}
	}
	existing, priority := requirement.ExistingParts, requirement.Priority
	if existing == nil {
		existing = []schemas.Category{}
	}
	if priority == nil {
		priority = []schemas.Category{}
	}
	var resolution *schemas.Resolution
	if requirement.UseCase.Resolution != "" {
		value := requirement.UseCase.Resolution
		resolution = &value
	}
	checks := make([]PublicValidationCheck, 0, len(view.Validation.Checks))
	for _, check := range view.Validation.Checks {
		missing := check.MissingFields
		if missing == nil {
			missing = []string{}
		}
		checks = append(checks, PublicValidationCheck{RuleID: check.RuleID, Outcome: check.Outcome,
			Severity: check.Severity, MissingFields: missing, Detail: check.Detail})
	}
	var snapshot struct {
		Evidence []PublicSource `json:"evidence"`
	}
	_ = json.Unmarshal(view.CandidateSnapshot, &snapshot)
	sources := []PublicSource{}
	for _, e := range snapshot.Evidence {
		if strings.HasPrefix(e.URL, "https://") {
			sources = append(sources, e)
		}
	}
	return PublicBuildView{
		Sources:       sources,
		SchemaVersion: 1,
		Summary: PublicBuildSummary{SchemaVersion: 1, Version: view.Summary.Version,
			ParentVersion: view.Summary.ParentVersion, IntentLabel: intentLabel(view.Summary.Intent),
			TotalCNY: view.Summary.TotalCNY, SnapshotDate: view.Summary.SnapshotDate,
			PriceFreshness: view.Summary.PriceFreshness,
			OverallStatus:  view.Summary.OverallStatus, CreatedAt: view.Summary.CreatedAt},
		Requirement: PublicRequirementSummary{KnownFields: knownFields, BudgetCNY: presenter.FormatFen(requirement.BudgetCNY * 100),
			BudgetFlexPercent: int(math.Round(requirement.BudgetFlex * 100)),
			UseCase: PublicUseCase{Type: requirement.UseCase.Type, Titles: titles, Resolution: resolution,
				FPSTarget: requirement.UseCase.FPSTarget},
			SizePref: requirement.SizePref, NoisePref: requirement.NoisePref,
			BrandPref:     PublicBrandPref{CPU: requirement.BrandPref.CPU, GPU: requirement.BrandPref.GPU},
			ExistingParts: existing, Priority: priority},
		Parts: view.Parts, Quote: view.Quote,
		Validation:  PublicValidation{OverallStatus: view.Validation.OverallStatus, Checks: checks},
		Disclaimers: view.Disclaimers, Share: PublicShareMeta{CreatedAt: createdAt},
	}, nil
}

func intentLabel(intent string) string {
	switch intent {
	case "整单生成":
		return "初始配置"
	case "swap_part":
		return "更换配件"
	case "adjust_budget":
		return "调整预算"
	case "change_constraint":
		return "调整需求"
	case "改单":
		return "配置调整"
	default:
		return "配置调整"
	}
}
