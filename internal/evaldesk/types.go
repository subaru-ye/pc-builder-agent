// Package evaldesk serves a read-only, local projection of saved evaluation evidence.
package evaldesk

type Usage struct {
	ModelCalls      int64 `json:"modelCalls"`
	EmbeddingCalls  int64 `json:"embeddingCalls"`
	UsageResponses  int64 `json:"usageResponses"`
	InputTokens     int64 `json:"inputTokens"`
	OutputTokens    int64 `json:"outputTokens"`
	TotalTokens     int64 `json:"totalTokens"`
	MeasuredRecords int   `json:"measuredRecords"`
	TotalRecords    int   `json:"totalRecords"`
	Complete        bool  `json:"complete"`
}
type Metrics struct {
	Passed         int      `json:"passed"`
	Recorded       int      `json:"recorded"`
	Expected       *int     `json:"expected"`
	Failed         int      `json:"failed"`
	DataErrors     int      `json:"dataErrors"`
	ExecutionRate  *float64 `json:"executionRate"`
	AllPassedCases int      `json:"allPassedCases"`
	CaseCount      int      `json:"caseCount"`
	AllPassedRate  *float64 `json:"allPassedRate"`
	Repeats        *int     `json:"repeats"`
	Usage          *Usage   `json:"usage"`
	DurationMS     int64    `json:"durationMs"`
}
type ModelVersion struct {
	Role     string  `json:"role"`
	Model    string  `json:"model"`
	Provider *string `json:"provider"`
}
type Versions struct {
	Suite                 *string        `json:"suite"`
	SuiteHash             *string        `json:"suiteHash"`
	SnapshotDate          *string        `json:"snapshotDate"`
	DataFingerprint       *string        `json:"dataFingerprint"`
	DataFingerprintSource *string        `json:"dataFingerprintSource"`
	PromptVersion         *string        `json:"promptVersion"`
	PromptSnapshot        bool           `json:"promptSnapshot"`
	Commit                *string        `json:"commit"`
	Dirty                 *bool          `json:"dirty"`
	Binary                *string        `json:"binary"`
	SourceFingerprint     *string        `json:"sourceFingerprint"`
	Grader                *string        `json:"grader"`
	Models                []ModelVersion `json:"models"`
}
type RunSummary struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	CreatedAt      *string  `json:"createdAt"`
	Status         string   `json:"status"`
	Verified       bool     `json:"verified"`
	Notes          []string `json:"notes"`
	Versions       Versions `json:"versions"`
	Original       Metrics  `json:"original"`
	Current        *Metrics `json:"current"`
	RegradedTrials *int     `json:"regradedTrials"`
}
type RunsResponse struct {
	Runs          []RunSummary `json:"runs"`
	Warnings      []string     `json:"warnings"`
	CurrentGrader string       `json:"currentGrader"`
}
type CaseScore struct {
	Passed   int  `json:"passed"`
	Total    int  `json:"total"`
	Expected *int `json:"expected"`
}
type CaseComparison struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Stage           string     `json:"stage"`
	Status          string     `json:"status"`
	Content         string     `json:"content"`
	OriginalA       *CaseScore `json:"originalA"`
	OriginalB       *CaseScore `json:"originalB"`
	CurrentA        *CaseScore `json:"currentA"`
	CurrentB        *CaseScore `json:"currentB"`
	OutputChanges   int        `json:"outputChanges"`
	UsageA          *Usage     `json:"usageA"`
	UsageB          *Usage     `json:"usageB"`
	CallsDelta      *int64     `json:"callsDelta"`
	TokensDelta     *int64     `json:"tokensDelta"`
	DurationDeltaMS *int64     `json:"durationDeltaMs"`
}
type Condition struct {
	Key       string  `json:"key"`
	Label     string  `json:"label"`
	Baseline  *string `json:"baseline"`
	Candidate *string `json:"candidate"`
	State     string  `json:"state"`
}

// ChangeSummary explains evidence in ordinary language; Conditions retain exact identifiers.
type ChangeDetail struct {
	Label     string  `json:"label"`
	Baseline  *string `json:"baseline"`
	Candidate *string `json:"candidate"`
}
type ChangeSummary struct {
	Key       string         `json:"key"`
	Label     string         `json:"label"`
	State     string         `json:"state"`
	Summary   string         `json:"summary"`
	Baseline  *string        `json:"baseline"`
	Candidate *string        `json:"candidate"`
	Details   []ChangeDetail `json:"details"`
	Notes     []string       `json:"notes"`
}
type MetricsDelta struct {
	ExecutionRate  *float64 `json:"executionRate"`
	AllPassedRate  *float64 `json:"allPassedRate"`
	ModelCalls     *int64   `json:"modelCalls"`
	EmbeddingCalls *int64   `json:"embeddingCalls"`
	TotalTokens    *int64   `json:"totalTokens"`
	DurationMS     *int64   `json:"durationMs"`
}
type ComparedMetrics struct {
	Scope     string       `json:"scope"`
	Baseline  *Metrics     `json:"baseline"`
	Candidate *Metrics     `json:"candidate"`
	Delta     MetricsDelta `json:"delta"`
}
type CompareResponse struct {
	Baseline        RunSummary       `json:"baseline"`
	Candidate       RunSummary       `json:"candidate"`
	Mode            string           `json:"mode"`
	StrictReason    *string          `json:"strictReason"`
	CurrentGrader   string           `json:"currentGrader"`
	Conditions      []Condition      `json:"conditions"`
	ChangeSummaries []ChangeSummary  `json:"changeSummaries"`
	Notices         []string         `json:"notices"`
	Counts          map[string]int   `json:"counts"`
	Metrics         ComparedMetrics  `json:"metrics"`
	Cases           []CaseComparison `json:"cases"`
}
type Failure struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
	Veto   bool   `json:"veto"`
}
type Verdict struct {
	Passed    bool      `json:"passed"`
	DataError bool      `json:"dataError"`
	Failures  []Failure `json:"failures"`
}
type Turn struct {
	Turn         int       `json:"turn"`
	Output       string    `json:"output"`
	ModelOutputs []string  `json:"modelOutputs"`
	Failures     []Failure `json:"failures"`
}
type Trial struct {
	Seed       int      `json:"seed"`
	Original   Verdict  `json:"original"`
	Current    *Verdict `json:"current"`
	Attempts   *int     `json:"attempts"`
	Usage      *Usage   `json:"usage"`
	DurationMS int64    `json:"durationMs"`
	Error      *string  `json:"error"`
	Turns      []Turn   `json:"turns"`
	Selection  *string  `json:"selection"`
}
type Input struct {
	Turn     int    `json:"turn"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
}
type CaseSide struct {
	RunID       string   `json:"runId"`
	CaseID      string   `json:"caseId"`
	Title       string   `json:"title"`
	Stage       string   `json:"stage"`
	InputFrozen bool     `json:"inputFrozen"`
	Inputs      []Input  `json:"inputs"`
	Trials      []Trial  `json:"trials"`
	Notes       []string `json:"notes"`
}
type CaseResponse struct {
	CaseID    string    `json:"caseId"`
	Baseline  *CaseSide `json:"baseline"`
	Candidate *CaseSide `json:"candidate"`
}

type CommitSummary struct {
	Hash        *string `json:"hash"`
	Subject     *string `json:"subject"`
	CommittedAt *string `json:"committedAt"`
	Status      string  `json:"status"`
}
type CommitFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Label  string `json:"label"`
}
type CommitDetails struct {
	CommitSummary
	Files          []CommitFile `json:"files"`
	FilesAvailable bool         `json:"filesAvailable"`
	FilesTruncated bool         `json:"filesTruncated"`
	Notes          []string     `json:"notes"`
}
type TimelineEntry struct {
	Run             RunSummary    `json:"run"`
	FrozenCaseCount *int          `json:"frozenCaseCount"`
	Commit          CommitSummary `json:"commit"`
}
type TimelineResponse struct {
	Items    []TimelineEntry `json:"items"`
	Warnings []string        `json:"warnings"`
	Notes    []string        `json:"notes"`
}
type FrozenCaseInput struct {
	Turn               int      `json:"turn"`
	Input              string   `json:"input"`
	InputKind          string   `json:"inputKind"`
	Expected           string   `json:"expected"`
	ExpectationSummary []string `json:"expectationSummary"`
}
type FrozenCase struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	Stage          string            `json:"stage"`
	ContentHash    *string           `json:"contentHash"`
	RecordedTrials int               `json:"recordedTrials"`
	PlannedTrials  *int              `json:"plannedTrials"`
	Inputs         []FrozenCaseInput `json:"inputs"`
	Notes          []string          `json:"notes"`
}
type ProvenanceResponse struct {
	Run             RunSummary    `json:"run"`
	FrozenCaseCount *int          `json:"frozenCaseCount"`
	Cases           []FrozenCase  `json:"cases"`
	Commit          CommitDetails `json:"commit"`
	Notes           []string      `json:"notes"`
}
