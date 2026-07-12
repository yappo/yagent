package benchmark

import (
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"yagent/internal/domain"
)

type RecordReportOptions struct {
	Inputs                        []string
	ProfileNames                  []string
	CaseIDs                       []string
	BaselineProfile               string
	MinPassRate                   *float64
	FailOnRegression              bool
	RuntimeAudit                  *RecordRuntimeAuditSummary
	RequireRuntimeAudit           bool
	MinRuntimeContext             int
	MaxRuntimeWarnings            *int
	MaxRuntimeRecommendations     *int
	RequireRuntimeLoaded          bool
	RequireRuntimeProbeOK         bool
	RequireRuntimeStructuredProbe bool
}

type RecordReport struct {
	GeneratedAt         time.Time                  `json:"generated_at"`
	Inputs              []string                   `json:"inputs,omitempty"`
	Records             int                        `json:"records"`
	BaselineProfile     string                     `json:"baseline_profile,omitempty"`
	RuntimeAudit        *RecordRuntimeAuditSummary `json:"runtime_audit,omitempty"`
	Groups              []RecordGroupSummary       `json:"groups"`
	FailedThresholds    []RecordGateFailure        `json:"failed_thresholds,omitempty"`
	RuntimeGateFailures []RecordRuntimeGateFailure `json:"runtime_gate_failures,omitempty"`
	Regressions         []RecordRegression         `json:"regressions,omitempty"`
}

type RecordGroupSummary struct {
	ProfileName                    string               `json:"profile_name"`
	RoutingProfile                 string               `json:"routing_profile,omitempty"`
	Model                          string               `json:"model,omitempty"`
	CaseID                         string               `json:"case_id"`
	CaseName                       string               `json:"case_name,omitempty"`
	Runs                           int                  `json:"runs"`
	Passes                         int                  `json:"passes"`
	PassRate                       float64              `json:"pass_rate"`
	Successes                      int                  `json:"successes"`
	SuccessRate                    float64              `json:"success_rate"`
	AvgDurationMS                  float64              `json:"avg_duration_ms"`
	AvgEvents                      float64              `json:"avg_events"`
	AvgToolCalls                   float64              `json:"avg_tool_calls"`
	AvgModelCalls                  float64              `json:"avg_model_calls"`
	AvgModelDurationMS             float64              `json:"avg_model_duration_ms"`
	AvgModelTransportAttempts      float64              `json:"avg_model_transport_attempts"`
	ModelTransportFailures         int                  `json:"model_transport_failures"`
	AvgModelTransportDurationMS    float64              `json:"avg_model_transport_duration_ms"`
	ModelUsageAvailable            int                  `json:"model_usage_available"`
	ModelUsageUnavailable          int                  `json:"model_usage_unavailable"`
	ModelUsageCoverage             float64              `json:"model_usage_coverage"`
	AvgModelInputTokens            float64              `json:"avg_model_input_tokens"`
	AvgModelOutputTokens           float64              `json:"avg_model_output_tokens"`
	AvgModelTotalTokens            float64              `json:"avg_model_total_tokens"`
	AvgModelCachedInputTokens      float64              `json:"avg_model_cached_input_tokens"`
	AvgModelReasoningTokens        float64              `json:"avg_model_reasoning_tokens"`
	ModelFallbacks                 int                  `json:"model_fallbacks"`
	ModelServers                   []string             `json:"model_servers,omitempty"`
	ModelNames                     []string             `json:"model_names,omitempty"`
	ModelAPIs                      []string             `json:"model_apis,omitempty"`
	ModelProfiles                  []string             `json:"model_profiles,omitempty"`
	AvgAgentStarts                 float64              `json:"avg_agent_starts"`
	AvgMutations                   float64              `json:"avg_mutations"`
	AvgPermissionRequests          float64              `json:"avg_permission_requests"`
	AvgDelegations                 float64              `json:"avg_delegations"`
	AvgHandoffs                    float64              `json:"avg_handoffs"`
	FailedEvents                   int                  `json:"failed_events"`
	VerificationFailures           int                  `json:"verification_failures"`
	AvgArtifacts                   float64              `json:"avg_artifacts"`
	AvgPlanNodes                   float64              `json:"avg_plan_nodes"`
	FailedExpectations             []string             `json:"failed_expectations,omitempty"`
	BaselineDelta                  *RecordBaselineDelta `json:"baseline_delta,omitempty"`
	PreflightDoctor                bool                 `json:"preflight_doctor,omitempty"`
	PreflightDoctorServer          string               `json:"preflight_doctor_server,omitempty"`
	PreflightDoctorModel           string               `json:"preflight_doctor_model,omitempty"`
	PreflightDoctorWarnings        int                  `json:"preflight_doctor_warnings,omitempty"`
	PreflightDoctorRecommendations int                  `json:"preflight_doctor_recommendations,omitempty"`
	PreflightRuntimeContextLength  int                  `json:"preflight_runtime_context_length,omitempty"`
	PreflightRuntimeQuantization   string               `json:"preflight_runtime_quantization,omitempty"`
}

type RecordBaselineDelta struct {
	PassRate             float64 `json:"pass_rate"`
	SuccessRate          float64 `json:"success_rate"`
	AvgDurationMS        float64 `json:"avg_duration_ms"`
	AvgEvents            float64 `json:"avg_events"`
	AvgToolCalls         float64 `json:"avg_tool_calls"`
	AvgModelCalls        float64 `json:"avg_model_calls"`
	AvgModelTotalTokens  float64 `json:"avg_model_total_tokens"`
	ModelUsageCoverage   float64 `json:"model_usage_coverage"`
	VerificationFailures int     `json:"verification_failures"`
}

type RecordGateFailure struct {
	ProfileName string  `json:"profile_name"`
	CaseID      string  `json:"case_id"`
	Metric      string  `json:"metric"`
	Got         float64 `json:"got"`
	Want        float64 `json:"want"`
}

type RecordRegression struct {
	ProfileName string `json:"profile_name"`
	CaseID      string `json:"case_id"`
	Metric      string `json:"metric"`
	Detail      string `json:"detail"`
}

type RecordRuntimeAuditSummary struct {
	ID                  string    `json:"id,omitempty"`
	ServerName          string    `json:"server_name,omitempty"`
	URL                 string    `json:"url,omitempty"`
	API                 string    `json:"api,omitempty"`
	Model               string    `json:"model,omitempty"`
	MatchedModel        string    `json:"matched_model,omitempty"`
	RuntimeLoaded       bool      `json:"runtime_loaded,omitempty"`
	RuntimeContext      int       `json:"runtime_context,omitempty"`
	RuntimeMaxContext   int       `json:"runtime_max_context,omitempty"`
	RuntimeQuantization string    `json:"runtime_quantization,omitempty"`
	ProbeRequested      bool      `json:"probe_requested,omitempty"`
	ProbeStructured     bool      `json:"probe_structured,omitempty"`
	ProbeOK             bool      `json:"probe_ok,omitempty"`
	Warnings            int       `json:"warnings,omitempty"`
	Problems            int       `json:"problems,omitempty"`
	Recommendations     int       `json:"recommendations,omitempty"`
	CreatedAt           time.Time `json:"created_at,omitempty"`
}

type RecordRuntimeGateFailure struct {
	Metric string `json:"metric"`
	Got    string `json:"got,omitempty"`
	Want   string `json:"want,omitempty"`
}

type junitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Time     string           `xml:"time,attr"`
	Suites   []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Time      string          `xml:"time,attr"`
	Timestamp string          `xml:"timestamp,attr,omitempty"`
	Cases     []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	ClassName string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

func LoadRecordFiles(paths []string) ([]ResultRecord, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("benchmark record input が必要です")
	}

	records := []ResultRecord{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("benchmark record file の読み込みに失敗しました: %w", err)
		}
		items, readErr := ReadRecords(file, path)
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		records = append(records, items...)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("benchmark record がありません")
	}
	return records, nil
}

func ReadRecords(r io.Reader, name string) ([]ResultRecord, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".csv":
		return ReadCSVRecords(r)
	default:
		return ReadJSONLRecords(r)
	}
}

func ReadJSONLRecords(r io.Reader) ([]ResultRecord, error) {
	decoder := json.NewDecoder(r)
	records := []ResultRecord{}
	for {
		var record ResultRecord
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("benchmark JSONL record のパースに失敗しました: %w", err)
		}
		records = append(records, record)
	}
	return records, nil
}

func ReadCSVRecords(r io.Reader) ([]ResultRecord, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("benchmark CSV record のパースに失敗しました: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	header := map[string]int{}
	for idx, name := range rows[0] {
		header[strings.TrimSpace(name)] = idx
	}

	records := make([]ResultRecord, 0, len(rows)-1)
	for idx, row := range rows[1:] {
		record, err := csvRecordFromRow(header, row)
		if err != nil {
			return nil, fmt.Errorf("benchmark CSV record line %d: %w", idx+2, err)
		}
		records = append(records, record)
	}
	return records, nil
}

func BuildRecordReport(records []ResultRecord, options RecordReportOptions) RecordReport {
	profileFilter := stringSet(options.ProfileNames)
	caseFilter := stringSet(options.CaseIDs)

	filtered := []ResultRecord{}
	for _, record := range records {
		if len(profileFilter) > 0 && !profileFilter[record.ProfileName] {
			continue
		}
		if len(caseFilter) > 0 && !caseFilter[record.CaseID] {
			continue
		}
		filtered = append(filtered, record)
	}

	groups := summarizeRecordGroups(filtered)
	report := RecordReport{
		GeneratedAt:     time.Now(),
		Inputs:          append([]string(nil), options.Inputs...),
		Records:         len(filtered),
		BaselineProfile: strings.TrimSpace(options.BaselineProfile),
		RuntimeAudit:    options.RuntimeAudit,
		Groups:          groups,
	}

	if options.MinPassRate != nil {
		for _, group := range groups {
			if group.PassRate < *options.MinPassRate {
				report.FailedThresholds = append(report.FailedThresholds, RecordGateFailure{
					ProfileName: group.ProfileName,
					CaseID:      group.CaseID,
					Metric:      "pass_rate",
					Got:         group.PassRate,
					Want:        *options.MinPassRate,
				})
			}
		}
	}

	if report.BaselineProfile != "" {
		report.Groups = attachBaselineDeltas(report.Groups, report.BaselineProfile)
		if options.FailOnRegression {
			report.Regressions = collectRecordRegressions(report.Groups)
		}
	}
	report.RuntimeGateFailures = evaluateRuntimeAuditGate(options)

	return report
}

func (report RecordReport) HasGateFailures() bool {
	return len(report.FailedThresholds) > 0 || len(report.RuntimeGateFailures) > 0 || len(report.Regressions) > 0
}

func WriteRecordReportJUnit(w io.Writer, report RecordReport) error {
	suite := junitTestSuite{
		Name:      "yagent benchmark report",
		Timestamp: report.GeneratedAt.UTC().Format(time.RFC3339),
	}
	for _, group := range report.Groups {
		details := recordGroupJUnitFailureDetails(report, group)
		testcase := junitTestCase{
			ClassName: "benchmark." + fallbackString(group.CaseID, "case"),
			Name:      strings.TrimSpace(group.ProfileName + " " + group.CaseID),
			Time:      junitSeconds(group.AvgDurationMS / 1000),
		}
		if len(details) > 0 {
			testcase.Failure = &junitFailure{
				Message: "benchmark group failed",
				Type:    "benchmark",
				Text:    strings.Join(details, "\n"),
			}
			suite.Failures++
		}
		suite.Tests++
		suite.Cases = append(suite.Cases, testcase)
	}
	if report.RuntimeAudit != nil || len(report.RuntimeGateFailures) > 0 {
		details := runtimeJUnitFailureDetails(report.RuntimeGateFailures)
		testcase := junitTestCase{
			ClassName: "benchmark.runtime",
			Name:      "runtime audit",
			Time:      "0.000",
		}
		if len(details) > 0 {
			testcase.Failure = &junitFailure{
				Message: "runtime audit gate failed",
				Type:    "runtime",
				Text:    strings.Join(details, "\n"),
			}
			suite.Failures++
		}
		suite.Tests++
		suite.Cases = append(suite.Cases, testcase)
	}
	suite.Time = junitSeconds(recordReportJUnitTime(report))
	suites := junitTestSuites{
		Tests:    suite.Tests,
		Failures: suite.Failures,
		Time:     suite.Time,
		Suites:   []junitTestSuite{suite},
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	encoder := xml.NewEncoder(w)
	encoder.Indent("", "  ")
	if err := encoder.Encode(suites); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func recordGroupJUnitFailureDetails(report RecordReport, group RecordGroupSummary) []string {
	details := []string{}
	if group.Runs > 0 && group.Passes < group.Runs {
		details = append(details, fmt.Sprintf("evaluation pass rate %.3f (%d/%d)", group.PassRate, group.Passes, group.Runs))
	}
	for _, expectation := range group.FailedExpectations {
		details = append(details, "expectation: "+expectation)
	}
	for _, failure := range report.FailedThresholds {
		if failure.ProfileName == group.ProfileName && failure.CaseID == group.CaseID {
			details = append(details, fmt.Sprintf("threshold %s got=%.3f want=%.3f", failure.Metric, failure.Got, failure.Want))
		}
	}
	for _, regression := range report.Regressions {
		if regression.ProfileName == group.ProfileName && regression.CaseID == group.CaseID {
			details = append(details, fmt.Sprintf("regression %s %s", regression.Metric, regression.Detail))
		}
	}
	return details
}

func runtimeJUnitFailureDetails(failures []RecordRuntimeGateFailure) []string {
	details := make([]string, 0, len(failures))
	for _, failure := range failures {
		details = append(details, fmt.Sprintf("%s got=%s want=%s", failure.Metric, fallbackString(failure.Got, "-"), fallbackString(failure.Want, "-")))
	}
	return details
}

func recordReportJUnitTime(report RecordReport) float64 {
	totalMS := 0.0
	for _, group := range report.Groups {
		totalMS += group.AvgDurationMS * float64(group.Runs)
	}
	return totalMS / 1000
}

func junitSeconds(value float64) string {
	return fmt.Sprintf("%.3f", value)
}

func evaluateRuntimeAuditGate(options RecordReportOptions) []RecordRuntimeGateFailure {
	failures := []RecordRuntimeGateFailure{}
	audit := options.RuntimeAudit
	if audit == nil {
		if options.RequireRuntimeAudit ||
			options.MinRuntimeContext > 0 ||
			options.MaxRuntimeWarnings != nil ||
			options.MaxRuntimeRecommendations != nil ||
			options.RequireRuntimeLoaded ||
			options.RequireRuntimeProbeOK ||
			options.RequireRuntimeStructuredProbe {
			failures = append(failures, RecordRuntimeGateFailure{
				Metric: "runtime_audit",
				Got:    "missing",
				Want:   "present",
			})
		}
		return failures
	}
	if audit.Problems > 0 {
		failures = append(failures, RecordRuntimeGateFailure{
			Metric: "runtime_problems",
			Got:    fmt.Sprint(audit.Problems),
			Want:   "0",
		})
	}
	if options.MinRuntimeContext > 0 && audit.RuntimeContext < options.MinRuntimeContext {
		failures = append(failures, RecordRuntimeGateFailure{
			Metric: "runtime_context",
			Got:    fmt.Sprint(audit.RuntimeContext),
			Want:   fmt.Sprintf(">=%d", options.MinRuntimeContext),
		})
	}
	if options.MaxRuntimeWarnings != nil && audit.Warnings > *options.MaxRuntimeWarnings {
		failures = append(failures, RecordRuntimeGateFailure{
			Metric: "runtime_warnings",
			Got:    fmt.Sprint(audit.Warnings),
			Want:   fmt.Sprintf("<=%d", *options.MaxRuntimeWarnings),
		})
	}
	if options.MaxRuntimeRecommendations != nil && audit.Recommendations > *options.MaxRuntimeRecommendations {
		failures = append(failures, RecordRuntimeGateFailure{
			Metric: "runtime_recommendations",
			Got:    fmt.Sprint(audit.Recommendations),
			Want:   fmt.Sprintf("<=%d", *options.MaxRuntimeRecommendations),
		})
	}
	if options.RequireRuntimeLoaded && !audit.RuntimeLoaded {
		failures = append(failures, RecordRuntimeGateFailure{
			Metric: "runtime_loaded",
			Got:    "false",
			Want:   "true",
		})
	}
	if options.RequireRuntimeProbeOK && !audit.ProbeOK {
		failures = append(failures, RecordRuntimeGateFailure{
			Metric: "runtime_probe_ok",
			Got:    "false",
			Want:   "true",
		})
	}
	if options.RequireRuntimeStructuredProbe && (!audit.ProbeStructured || !audit.ProbeOK) {
		got := "not_structured"
		if audit.ProbeStructured {
			got = "structured_failed"
		}
		failures = append(failures, RecordRuntimeGateFailure{
			Metric: "runtime_probe_structured",
			Got:    got,
			Want:   "structured_ok",
		})
	}
	return failures
}

type recordGroupKey struct {
	profileName    string
	routingProfile string
	model          string
	caseID         string
}

type recordGroupAccumulator struct {
	summary                RecordGroupSummary
	durationMS             int64
	events                 int
	toolCalls              int
	modelCalls             int
	modelDuration          int64
	modelTransportAttempts int
	modelTransportFailures int
	modelTransportDuration int64
	usageAvailable         int
	usageUnavailable       int
	inputTokens            int
	outputTokens           int
	totalTokens            int
	cachedTokens           int
	reasoningTokens        int
	agentStarts            int
	mutations              int
	permissionRequests     int
	delegations            int
	handoffs               int
	artifacts              int
	planNodes              int
	failedDetails          []string
}

func summarizeRecordGroups(records []ResultRecord) []RecordGroupSummary {
	accumulators := map[recordGroupKey]*recordGroupAccumulator{}
	for _, record := range records {
		key := recordGroupKey{
			profileName:    record.ProfileName,
			routingProfile: record.RoutingProfile,
			model:          record.Model,
			caseID:         record.CaseID,
		}
		acc, ok := accumulators[key]
		if !ok {
			acc = &recordGroupAccumulator{summary: RecordGroupSummary{
				ProfileName:    record.ProfileName,
				RoutingProfile: record.RoutingProfile,
				Model:          record.Model,
				CaseID:         record.CaseID,
				CaseName:       record.CaseName,
			}}
			accumulators[key] = acc
		}
		acc.summary.Runs++
		if record.Passed {
			acc.summary.Passes++
		}
		if record.Status == domain.RunStatusCompleted {
			acc.summary.Successes++
		}
		acc.durationMS += record.DurationMS
		acc.events += record.Events
		acc.toolCalls += record.ToolCalls
		acc.modelCalls += record.ModelCalls
		acc.modelDuration += record.ModelDurationMS
		transportAttempts := record.ModelTransportAttempts
		if transportAttempts == 0 && record.ModelCalls > 0 {
			transportAttempts = record.ModelCalls
		}
		acc.modelTransportAttempts += transportAttempts
		acc.modelTransportFailures += record.ModelTransportFailures
		transportDuration := record.ModelTransportDurationMS
		if transportDuration == 0 {
			transportDuration = record.ModelDurationMS
		}
		acc.modelTransportDuration += transportDuration
		usageAvailable := record.ModelUsageAvailable
		usageUnavailable := record.ModelUsageUnavailable
		if usageAvailable+usageUnavailable == 0 && record.ModelCalls > 0 {
			usageUnavailable = record.ModelCalls
		}
		acc.usageAvailable += usageAvailable
		acc.usageUnavailable += usageUnavailable
		acc.inputTokens += record.ModelInputTokens
		acc.outputTokens += record.ModelOutputTokens
		acc.totalTokens += record.ModelTotalTokens
		acc.cachedTokens += record.ModelCachedTokens
		acc.reasoningTokens += record.ModelReasoningTokens
		acc.summary.ModelFallbacks += record.ModelFallbacks
		acc.summary.ModelServers = appendUniqueStrings(acc.summary.ModelServers, record.ModelServers...)
		acc.summary.ModelNames = appendUniqueStrings(acc.summary.ModelNames, record.ModelNames...)
		acc.summary.ModelAPIs = appendUniqueStrings(acc.summary.ModelAPIs, record.ModelAPIs...)
		acc.summary.ModelProfiles = appendUniqueStrings(acc.summary.ModelProfiles, record.ModelProfiles...)
		acc.agentStarts += record.AgentStarts
		acc.mutations += record.Mutations
		acc.permissionRequests += record.PermissionRequests
		acc.delegations += record.Delegations
		acc.handoffs += record.Handoffs
		acc.summary.FailedEvents += record.FailedEvents
		acc.summary.VerificationFailures += record.VerificationFailures
		acc.artifacts += record.Artifacts
		acc.planNodes += record.PlanNodes
		acc.failedDetails = appendUniqueStrings(acc.failedDetails, record.FailedExpectationDetails...)
		if record.PreflightDoctor {
			acc.summary.PreflightDoctor = true
			if acc.summary.PreflightDoctorServer == "" {
				acc.summary.PreflightDoctorServer = record.PreflightDoctorServer
			}
			if acc.summary.PreflightDoctorModel == "" {
				acc.summary.PreflightDoctorModel = record.PreflightDoctorModel
			}
			if record.PreflightDoctorWarnings > acc.summary.PreflightDoctorWarnings {
				acc.summary.PreflightDoctorWarnings = record.PreflightDoctorWarnings
			}
			if record.PreflightDoctorRecommendations > acc.summary.PreflightDoctorRecommendations {
				acc.summary.PreflightDoctorRecommendations = record.PreflightDoctorRecommendations
			}
			if record.PreflightRuntimeContextLength > acc.summary.PreflightRuntimeContextLength {
				acc.summary.PreflightRuntimeContextLength = record.PreflightRuntimeContextLength
			}
			if acc.summary.PreflightRuntimeQuantization == "" {
				acc.summary.PreflightRuntimeQuantization = record.PreflightRuntimeQuantization
			}
		}
	}

	groups := make([]RecordGroupSummary, 0, len(accumulators))
	for _, acc := range accumulators {
		runs := float64(acc.summary.Runs)
		acc.summary.PassRate = float64(acc.summary.Passes) / runs
		acc.summary.SuccessRate = float64(acc.summary.Successes) / runs
		acc.summary.AvgDurationMS = float64(acc.durationMS) / runs
		acc.summary.AvgEvents = float64(acc.events) / runs
		acc.summary.AvgToolCalls = float64(acc.toolCalls) / runs
		acc.summary.AvgModelCalls = float64(acc.modelCalls) / runs
		acc.summary.AvgModelDurationMS = float64(acc.modelDuration) / runs
		acc.summary.AvgModelTransportAttempts = float64(acc.modelTransportAttempts) / runs
		acc.summary.ModelTransportFailures = acc.modelTransportFailures
		acc.summary.AvgModelTransportDurationMS = float64(acc.modelTransportDuration) / runs
		acc.summary.ModelUsageAvailable = acc.usageAvailable
		acc.summary.ModelUsageUnavailable = acc.usageUnavailable
		usageCalls := acc.usageAvailable + acc.usageUnavailable
		if usageCalls > 0 {
			acc.summary.ModelUsageCoverage = float64(acc.usageAvailable) / float64(usageCalls)
		}
		acc.summary.AvgModelInputTokens = float64(acc.inputTokens) / runs
		acc.summary.AvgModelOutputTokens = float64(acc.outputTokens) / runs
		acc.summary.AvgModelTotalTokens = float64(acc.totalTokens) / runs
		acc.summary.AvgModelCachedInputTokens = float64(acc.cachedTokens) / runs
		acc.summary.AvgModelReasoningTokens = float64(acc.reasoningTokens) / runs
		acc.summary.AvgAgentStarts = float64(acc.agentStarts) / runs
		acc.summary.AvgMutations = float64(acc.mutations) / runs
		acc.summary.AvgPermissionRequests = float64(acc.permissionRequests) / runs
		acc.summary.AvgDelegations = float64(acc.delegations) / runs
		acc.summary.AvgHandoffs = float64(acc.handoffs) / runs
		acc.summary.AvgArtifacts = float64(acc.artifacts) / runs
		acc.summary.AvgPlanNodes = float64(acc.planNodes) / runs
		sort.Strings(acc.failedDetails)
		acc.summary.FailedExpectations = acc.failedDetails
		groups = append(groups, acc.summary)
	}

	sort.Slice(groups, func(i, j int) bool {
		if groups[i].CaseID != groups[j].CaseID {
			return groups[i].CaseID < groups[j].CaseID
		}
		if groups[i].ProfileName != groups[j].ProfileName {
			return groups[i].ProfileName < groups[j].ProfileName
		}
		if groups[i].RoutingProfile != groups[j].RoutingProfile {
			return groups[i].RoutingProfile < groups[j].RoutingProfile
		}
		return groups[i].Model < groups[j].Model
	})
	return groups
}

func attachBaselineDeltas(groups []RecordGroupSummary, baselineProfile string) []RecordGroupSummary {
	baselines := map[string]RecordGroupSummary{}
	for _, group := range groups {
		if group.ProfileName == baselineProfile {
			baselines[group.CaseID] = group
		}
	}

	out := make([]RecordGroupSummary, 0, len(groups))
	for _, group := range groups {
		if baseline, ok := baselines[group.CaseID]; ok && group.ProfileName != baselineProfile {
			delta := RecordBaselineDelta{
				PassRate:             group.PassRate - baseline.PassRate,
				SuccessRate:          group.SuccessRate - baseline.SuccessRate,
				AvgDurationMS:        group.AvgDurationMS - baseline.AvgDurationMS,
				AvgEvents:            group.AvgEvents - baseline.AvgEvents,
				AvgToolCalls:         group.AvgToolCalls - baseline.AvgToolCalls,
				AvgModelCalls:        group.AvgModelCalls - baseline.AvgModelCalls,
				AvgModelTotalTokens:  group.AvgModelTotalTokens - baseline.AvgModelTotalTokens,
				ModelUsageCoverage:   group.ModelUsageCoverage - baseline.ModelUsageCoverage,
				VerificationFailures: group.VerificationFailures - baseline.VerificationFailures,
			}
			group.BaselineDelta = &delta
		}
		out = append(out, group)
	}
	return out
}

func collectRecordRegressions(groups []RecordGroupSummary) []RecordRegression {
	regressions := []RecordRegression{}
	for _, group := range groups {
		if group.BaselineDelta == nil {
			continue
		}
		if group.BaselineDelta.PassRate < 0 {
			regressions = append(regressions, RecordRegression{
				ProfileName: group.ProfileName,
				CaseID:      group.CaseID,
				Metric:      "pass_rate",
				Detail:      fmt.Sprintf("delta=%.3f", group.BaselineDelta.PassRate),
			})
		}
		if group.BaselineDelta.VerificationFailures > 0 {
			regressions = append(regressions, RecordRegression{
				ProfileName: group.ProfileName,
				CaseID:      group.CaseID,
				Metric:      "verification_failures",
				Detail:      fmt.Sprintf("delta=%d", group.BaselineDelta.VerificationFailures),
			})
		}
	}
	return regressions
}

func csvRecordFromRow(header map[string]int, row []string) (ResultRecord, error) {
	get := func(name string) string {
		idx, ok := header[name]
		if !ok || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}
	parseInt := func(name string) (int, error) {
		value := get(name)
		if value == "" {
			return 0, nil
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("%s=%q は integer ではありません", name, value)
		}
		return parsed, nil
	}
	parseInt64 := func(name string) (int64, error) {
		value := get(name)
		if value == "" {
			return 0, nil
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s=%q は integer ではありません", name, value)
		}
		return parsed, nil
	}

	passed := false
	if value := get("passed"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return ResultRecord{}, fmt.Errorf("passed=%q は bool ではありません", value)
		}
		passed = parsed
	}
	preflightDoctor := false
	if value := get("preflight_doctor"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return ResultRecord{}, fmt.Errorf("preflight_doctor=%q は bool ではありません", value)
		}
		preflightDoctor = parsed
	}
	preflightProbeStructured := false
	if value := get("preflight_probe_structured"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return ResultRecord{}, fmt.Errorf("preflight_probe_structured=%q は bool ではありません", value)
		}
		preflightProbeStructured = parsed
	}
	preflightProbeOK := false
	if value := get("preflight_probe_ok"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return ResultRecord{}, fmt.Errorf("preflight_probe_ok=%q は bool ではありません", value)
		}
		preflightProbeOK = parsed
	}

	var generatedAt time.Time
	if value := get("generated_at"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return ResultRecord{}, fmt.Errorf("generated_at=%q は RFC3339 ではありません", value)
		}
		generatedAt = parsed
	}

	runIndex, err := parseInt("run_index")
	if err != nil {
		return ResultRecord{}, err
	}
	durationMS, err := parseInt64("duration_ms")
	if err != nil {
		return ResultRecord{}, err
	}
	events, err := parseInt("events")
	if err != nil {
		return ResultRecord{}, err
	}
	toolCalls, err := parseInt("tool_calls")
	if err != nil {
		return ResultRecord{}, err
	}
	modelCalls, err := parseInt("model_calls")
	if err != nil {
		return ResultRecord{}, err
	}
	modelFallbacks, err := parseInt("model_fallbacks")
	if err != nil {
		return ResultRecord{}, err
	}
	modelDurationMS, err := parseInt64("model_duration_ms")
	if err != nil {
		return ResultRecord{}, err
	}
	modelTransportAttempts, err := parseInt("model_transport_attempts")
	if err != nil {
		return ResultRecord{}, err
	}
	modelTransportFailures, err := parseInt("model_transport_failures")
	if err != nil {
		return ResultRecord{}, err
	}
	modelTransportDurationMS, err := parseInt64("model_transport_duration_ms")
	if err != nil {
		return ResultRecord{}, err
	}
	modelUsageAvailable, err := parseInt("model_usage_available")
	if err != nil {
		return ResultRecord{}, err
	}
	modelUsageUnavailable, err := parseInt("model_usage_unavailable")
	if err != nil {
		return ResultRecord{}, err
	}
	modelInputTokens, err := parseInt("model_input_tokens")
	if err != nil {
		return ResultRecord{}, err
	}
	modelOutputTokens, err := parseInt("model_output_tokens")
	if err != nil {
		return ResultRecord{}, err
	}
	modelTotalTokens, err := parseInt("model_total_tokens")
	if err != nil {
		return ResultRecord{}, err
	}
	modelCachedTokens, err := parseInt("model_cached_input_tokens")
	if err != nil {
		return ResultRecord{}, err
	}
	modelReasoningTokens, err := parseInt("model_reasoning_tokens")
	if err != nil {
		return ResultRecord{}, err
	}
	agentStarts, err := parseInt("agent_starts")
	if err != nil {
		return ResultRecord{}, err
	}
	mutations, err := parseInt("mutations")
	if err != nil {
		return ResultRecord{}, err
	}
	permissionRequests, err := parseInt("permission_requests")
	if err != nil {
		return ResultRecord{}, err
	}
	delegations, err := parseInt("delegations")
	if err != nil {
		return ResultRecord{}, err
	}
	handoffs, err := parseInt("handoffs")
	if err != nil {
		return ResultRecord{}, err
	}
	failedEvents, err := parseInt("failed_events")
	if err != nil {
		return ResultRecord{}, err
	}
	verificationFailures, err := parseInt("verification_failures")
	if err != nil {
		return ResultRecord{}, err
	}
	artifacts, err := parseInt("artifacts")
	if err != nil {
		return ResultRecord{}, err
	}
	planNodes, err := parseInt("plan_nodes")
	if err != nil {
		return ResultRecord{}, err
	}
	preflightWarnings, err := parseInt("preflight_doctor_warnings")
	if err != nil {
		return ResultRecord{}, err
	}
	preflightRecommendations, err := parseInt("preflight_doctor_recommendations")
	if err != nil {
		return ResultRecord{}, err
	}
	preflightContextLength, err := parseInt("preflight_runtime_context_length")
	if err != nil {
		return ResultRecord{}, err
	}

	return ResultRecord{
		GeneratedAt:                    generatedAt,
		ProfileName:                    get("profile"),
		RoutingProfile:                 get("routing_profile"),
		Model:                          get("model"),
		CaseID:                         get("case_id"),
		CaseName:                       get("case_name"),
		RunIndex:                       runIndex,
		Passed:                         passed,
		Status:                         domain.RunStatus(get("status")),
		Phase:                          domain.RunPhase(get("phase")),
		DurationMS:                     durationMS,
		Events:                         events,
		ToolCalls:                      toolCalls,
		ModelCalls:                     modelCalls,
		ModelFallbacks:                 modelFallbacks,
		ModelDurationMS:                modelDurationMS,
		ModelTransportAttempts:         modelTransportAttempts,
		ModelTransportFailures:         modelTransportFailures,
		ModelTransportDurationMS:       modelTransportDurationMS,
		ModelUsageAvailable:            modelUsageAvailable,
		ModelUsageUnavailable:          modelUsageUnavailable,
		ModelInputTokens:               modelInputTokens,
		ModelOutputTokens:              modelOutputTokens,
		ModelTotalTokens:               modelTotalTokens,
		ModelCachedTokens:              modelCachedTokens,
		ModelReasoningTokens:           modelReasoningTokens,
		AgentStarts:                    agentStarts,
		Mutations:                      mutations,
		PermissionRequests:             permissionRequests,
		Delegations:                    delegations,
		Handoffs:                       handoffs,
		FailedEvents:                   failedEvents,
		VerificationFailures:           verificationFailures,
		Artifacts:                      artifacts,
		PlanNodes:                      planNodes,
		ToolNames:                      splitCSVList(get("tool_names")),
		ModelServers:                   splitCSVList(get("model_servers")),
		ModelNames:                     splitCSVList(get("model_names")),
		ModelAPIs:                      splitCSVList(get("model_apis")),
		ModelProfiles:                  splitCSVList(get("model_profiles")),
		FailedExpectationDetails:       splitCSVList(get("failed_expectations")),
		PreflightDoctor:                preflightDoctor,
		PreflightDoctorServer:          get("preflight_doctor_server"),
		PreflightDoctorModel:           get("preflight_doctor_model"),
		PreflightDoctorWarnings:        preflightWarnings,
		PreflightDoctorRecommendations: preflightRecommendations,
		PreflightRuntimeContextLength:  preflightContextLength,
		PreflightRuntimeQuantization:   get("preflight_runtime_quantization"),
		PreflightProbeStructured:       preflightProbeStructured,
		PreflightProbeOK:               preflightProbeOK,
	}, nil
}

func stringSet(items []string) map[string]bool {
	out := map[string]bool{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out[item] = true
		}
	}
	return out
}

func appendUniqueStrings(items []string, values ...string) []string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		seen := false
		for _, item := range items {
			if item == value {
				seen = true
				break
			}
		}
		if !seen {
			items = append(items, value)
		}
	}
	return items
}

func splitCSVList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, "|")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
