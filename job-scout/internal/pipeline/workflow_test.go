package pipeline

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/domain"
	"github.com/jobscout/jobscout/internal/scraper"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
)

func TestRegister_ScrapeAndNotifyAreProductionPath(t *testing.T) {
	rec := &registryRecorder{}
	Register(rec, &Activities{})

	if rec.hasWorkflow("MainWorkflow") {
		t.Errorf("MainWorkflow must not be the production path; registered %v", rec.workflows)
	}
	if !rec.hasWorkflow("ScrapeTick") {
		t.Errorf("worker must register ScrapeTick; registered %v", rec.workflows)
	}
	if !rec.hasWorkflow("NotifyTick") {
		t.Errorf("worker must register NotifyTick; registered %v", rec.workflows)
	}
	if !rec.hasActivity("Scrape") {
		t.Errorf("worker must register Scrape activity; registered %v", rec.activities)
	}
	if !rec.hasActivity("LoadNotifySnapshot") {
		t.Errorf("worker must register LoadNotifySnapshot; registered %v", rec.activities)
	}
	if !rec.hasActivity("FetchJobDescription") {
		t.Errorf("worker must register FetchJobDescription (LinkedIn detail GET); registered %v", rec.activities)
	}
	if !rec.hasActivity("ApplyJobDecision") {
		t.Errorf("worker must register ApplyJobDecision; registered %v", rec.activities)
	}
	if !rec.hasActivity("ClaimNotifyBatch") {
		t.Errorf("worker must register ClaimNotifyBatch; registered %v", rec.activities)
	}
	if !rec.hasActivity("NotifyWebhook") {
		t.Errorf("worker must register NotifyWebhook; registered %v", rec.activities)
	}
	if !rec.hasActivity("FinishNotifyBatch") {
		t.Errorf("worker must register FinishNotifyBatch; registered %v", rec.activities)
	}

	for _, stub := range []string{
		"SmartFilter",
		"DuplicateRemover",
		"BasicFilter",
		"Detailer",
		"AdvancedFilter",
		"Notifier",
	} {
		if rec.hasActivity(stub) {
			t.Errorf("stub pipeline stage %s must not be the production path; registered %v", stub, rec.activities)
		}
	}
}

func TestScrapeTick_StoresJobsWithoutSmartFilter(t *testing.T) {
	env, started, probe := newWorkflowEnv()

	env.ExecuteWorkflow(ScrapeTick, scraper.TickInput{JobSource: "LINKEDIN"})

	if !env.IsWorkflowCompleted() {
		t.Fatal("ScrapeTick must complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ScrapeTick must complete without workflow error: %v", err)
	}

	var got scraper.Result
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("read ScrapeTick result: %v", err)
	}
	want := scraper.Result{Status: "ok", JobSource: "LINKEDIN", ScrapedCount: 2, SavedCount: 2}
	if got != want {
		t.Errorf("ScrapeTick result = %+v, want %+v", got, want)
	}
	if probe.scrape != 1 {
		t.Errorf("ScrapeTick must fetch and store jobs once, scrape calls=%d", probe.scrape)
	}
	assertActivityNames(t, *started, "Scrape")
}

func TestScrapeTick_LinkedInSearchActivityRunsOnce(t *testing.T) {
	env, started, probe := newWorkflowEnv()
	probe.scrapeErr = errors.New("linkedin search failed")

	env.ExecuteWorkflow(ScrapeTick, scraper.TickInput{JobSource: "LINKEDIN"})

	if !env.IsWorkflowCompleted() {
		t.Fatal("ScrapeTick must complete after a single failed LinkedIn GET")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ScrapeTick must complete without a workflow error after one failed LinkedIn GET: %v", err)
	}

	var got scraper.Result
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("read ScrapeTick result: %v", err)
	}
	if got.Status != "error" || got.JobSource != "LINKEDIN" || got.Error == "" {
		t.Errorf("want error-status scrape result, got %+v", got)
	}
	if probe.scrape != 1 {
		t.Errorf("LinkedIn search GET activity retry policy MaximumAttempts must be 1, got %d attempts", probe.scrape)
	}
	assertActivityNames(t, *started, "Scrape")
}

func TestScrapeTick_ScrapeActivityAllowsFortyFiveMinutes(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(&activityProbe{})
	var timeout time.Duration
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		if info.ActivityType.Name == "Scrape" {
			timeout = info.StartToCloseTimeout
		}
	})

	env.ExecuteWorkflow(ScrapeTick, scraper.TickInput{JobSource: "LINKEDIN"})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ScrapeTick error: %v", err)
	}
	if timeout != 45*time.Minute {
		t.Errorf("Scrape StartToCloseTimeout = %s, want 45m", timeout)
	}
}

func TestNotifyTick_CompletesWithoutLinkedInSearchOrWebhook(t *testing.T) {
	env, started, probe := newWorkflowEnv()

	env.ExecuteWorkflow(NotifyTick)

	if !env.IsWorkflowCompleted() {
		t.Fatal("NotifyTick must complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("NotifyTick must complete without workflow error: %v", err)
	}
	if probe.scrape != 0 {
		t.Errorf("NotifyTick must not call LinkedIn search, scrape calls=%d", probe.scrape)
	}
	if probe.webhook != 0 {
		t.Errorf("NotifyTick must not POST to Home Assistant, webhook calls=%d", probe.webhook)
	}
	if probe.detailGet != 0 {
		t.Errorf("empty pending set must not GET LinkedIn job detail, detail calls=%d", probe.detailGet)
	}
	assertActivityNames(t, *started, "LoadNotifySnapshot", "ClaimNotifyBatch")
}

func TestNotifyTick_DetailGETRunsOnce(t *testing.T) {
	env, started, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	probe.snapshot = NotifySnapshot{
		Pending: []domain.Job{job},
		All:     []domain.Job{job},
		Lists: domain.Lists{
			TitleInclude: []string{"Help Desk"},
			DescInclude:  []string{"computer"},
		},
	}
	probe.detailErr = errors.New("linkedin detail failed")

	env.ExecuteWorkflow(NotifyTick)

	if !env.IsWorkflowCompleted() {
		t.Fatal("NotifyTick must complete after a single failed LinkedIn detail GET")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("NotifyTick must complete without a workflow error after one failed detail GET: %v", err)
	}
	if probe.scrape != 0 {
		t.Errorf("NotifyTick must not call LinkedIn search, scrape calls=%d", probe.scrape)
	}
	if probe.webhook != 0 {
		t.Errorf("NotifyTick must not POST to Home Assistant, webhook calls=%d", probe.webhook)
	}
	if probe.detailGet != 1 {
		t.Errorf("LinkedIn detail GET activity retry policy MaximumAttempts must be 1, got %d attempts", probe.detailGet)
	}
	if probe.apply != 1 {
		t.Errorf("failed detail GET must persist one decision, apply calls=%d", probe.apply)
	}
	if len(probe.applied) != 1 || probe.applied[0].Decision.State != "pending" || probe.applied[0].Decision.DetailAttempts != 1 {
		t.Errorf("first detail failure must leave job pending with detail_attempts=1, got %+v", probe.applied)
	}
	assertActivityContains(t, *started, "LoadNotifySnapshot", "FetchJobDescription", "ApplyJobDecision")
}

func TestNotifyTick_DetailGETOncePerJobThenEligible(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	jobA := domainJobNeedingDescription()
	jobB := domainJobNeedingDescription()
	jobB.ID = 11
	jobB.Company = "Globex"
	jobB.JobURL = "https://www.linkedin.com/jobs/view/11/"
	probe.snapshot = NotifySnapshot{
		Pending: []domain.Job{jobA, jobB},
		All:     []domain.Job{jobA, jobB},
		Lists: domain.Lists{
			TitleInclude: []string{"Help Desk"},
			DescInclude:  []string{"computer"},
		},
	}

	env.ExecuteWorkflow(NotifyTick)

	if !env.IsWorkflowCompleted() {
		t.Fatal("NotifyTick must complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("NotifyTick error: %v", err)
	}
	if probe.detailGet != 2 {
		t.Errorf("one detail GET per job per notify run, got %d", probe.detailGet)
	}
	if probe.webhook != 0 {
		t.Errorf("NotifyTick must not POST to Home Assistant, webhook calls=%d", probe.webhook)
	}
	if len(probe.applied) != 2 {
		t.Fatalf("applied = %d, want 2", len(probe.applied))
	}
	for i, in := range probe.applied {
		if in.Decision.State != domain.StateEligible {
			t.Errorf("job %d state = %q, want eligible after successful detail fetch", i, in.Decision.State)
		}
		if in.Decision.Description != "computer troubleshooting on windows" {
			t.Errorf("job %d description = %q, want fetched text persisted", i, in.Decision.Description)
		}
	}
}

func TestNotifyTick_ClaimsBatchPostsOnceAndMarksNotified(t *testing.T) {
	env, started, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	job.Description = "computer troubleshooting on windows"
	older := "https://www.linkedin.com/jobs/view/older/"
	newer := "https://www.linkedin.com/jobs/view/newer/"
	probe.snapshot = NotifySnapshot{
		Pending: []domain.Job{job},
		All:     []domain.Job{job},
		Lists: domain.Lists{
			TitleInclude: []string{"Help Desk"},
			DescInclude:  []string{"computer"},
		},
	}
	probe.claim = ClaimBatch{
		Jobs: []ClaimedJob{
			{ID: 8, JobURL: older},
			{ID: 9, JobURL: newer},
		},
		Counts: domain.MessageCounts{
			Total:        5,
			TitleCompany: 0,
			Description:  0,
			RemoteLie:    0,
			Duplicate:    0,
			DetailFailed: 0,
			Pending:      4,
			Eligible:     0,
			Notifying:    2,
			Notified:     0,
		},
	}

	env.ExecuteWorkflow(NotifyTick)

	if !env.IsWorkflowCompleted() {
		t.Fatal("NotifyTick must complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("NotifyTick error: %v", err)
	}
	if probe.webhook != 1 {
		t.Errorf("claimed batch must POST Home Assistant once, webhook calls=%d", probe.webhook)
	}
	wantMsg := "" +
		"Job alert summary:\n" +
		"  5 job postings scraped\n" +
		" -0 dont match companies or titles\n" +
		" -0 dont match descriptions\n" +
		" -0 are lying about remote\n" +
		" -0 duplicate title/company\n" +
		" -0 lost due to unforseen circumstances\n" +
		"\n" +
		"2 new jobs to check out\n" +
		"*************\n" +
		older + "\n" +
		newer
	if probe.lastMessage != wantMsg {
		t.Errorf("POST message =\n%q\nwant\n%q", probe.lastMessage, wantMsg)
	}
	if probe.finish != 1 {
		t.Errorf("HTTP 200 must finish the claimed batch once, finish calls=%d", probe.finish)
	}
	if len(probe.finished) != 1 || probe.finished[0].State != domain.StateNotified {
		t.Errorf("HTTP 200 must set notified, got %+v", probe.finished)
	}
	if len(probe.finished) == 1 {
		if len(probe.finished[0].IDs) != 2 || probe.finished[0].IDs[0] != 8 || probe.finished[0].IDs[1] != 9 {
			t.Errorf("finish IDs = %v, want claimed [8 9]", probe.finished[0].IDs)
		}
	}
	assertActivityNames(t, *started, "LoadNotifySnapshot", "ApplyJobDecision", "ClaimNotifyBatch", "NotifyWebhook", "FinishNotifyBatch")
}

func TestNotifyTick_EmptyClaimDoesNotPost(t *testing.T) {
	env, started, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	job.Description = "computer troubleshooting on windows"
	probe.snapshot = NotifySnapshot{
		Pending: []domain.Job{job},
		All:     []domain.Job{job},
		Lists: domain.Lists{
			TitleInclude: []string{"Help Desk"},
			DescInclude:  []string{"computer"},
		},
	}

	env.ExecuteWorkflow(NotifyTick)

	if !env.IsWorkflowCompleted() {
		t.Fatal("NotifyTick must complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("NotifyTick error: %v", err)
	}
	if probe.claimCalls != 1 {
		t.Errorf("NotifyTick must claim after filters, claim calls=%d", probe.claimCalls)
	}
	if probe.webhook != 0 {
		t.Errorf("zero claimed rows must not POST, webhook calls=%d", probe.webhook)
	}
	if probe.finish != 0 {
		t.Errorf("zero claimed rows must not finish a batch, finish calls=%d", probe.finish)
	}
	assertActivityNames(t, *started, "LoadNotifySnapshot", "ApplyJobDecision", "ClaimNotifyBatch")
}

func TestNotifyTick_WebhookFailureFinishesBatchThenFailsWorkflow(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	job.Description = "computer troubleshooting on windows"
	probe.snapshot = NotifySnapshot{
		Pending: []domain.Job{job},
		All:     []domain.Job{job},
		Lists: domain.Lists{
			TitleInclude: []string{"Help Desk"},
			DescInclude:  []string{"computer"},
		},
	}
	probe.claim = ClaimBatch{
		Jobs:   []ClaimedJob{{ID: job.ID, JobURL: job.JobURL}},
		Counts: domain.MessageCounts{Total: 1, Notifying: 1},
	}
	probe.webhookErr = errors.New("webhook HTTP 500")

	env.ExecuteWorkflow(NotifyTick)

	if !env.IsWorkflowCompleted() {
		t.Fatal("NotifyTick must complete after a single failed webhook POST")
	}
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), probe.webhookErr.Error()) {
		t.Fatalf("NotifyTick must fail with the webhook error after cleanup, got %v", err)
	}
	if probe.webhook != 1 {
		t.Errorf("webhook POST activity retry policy MaximumAttempts must be 1, got %d attempts", probe.webhook)
	}
	if probe.finish != 1 || len(probe.finished) != 1 || probe.finished[0].State != domain.StateEligible {
		t.Errorf("non-200/transport must return claimed jobs to eligible, got %+v", probe.finished)
	}
	if len(probe.finished) == 1 && (len(probe.finished[0].IDs) != 1 || probe.finished[0].IDs[0] != job.ID) {
		t.Errorf("finish IDs = %v, want claimed [%d]", probe.finished[0].IDs, job.ID)
	}
}

func TestNotifyTick_PassersBecomeEligibleWithoutWebhook(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	job.Description = "computer troubleshooting on windows"
	probe.snapshot = NotifySnapshot{
		Pending: []domain.Job{job},
		All:     []domain.Job{job},
		Lists: domain.Lists{
			TitleInclude: []string{"Help Desk"},
			DescInclude:  []string{"computer"},
		},
	}

	env.ExecuteWorkflow(NotifyTick)

	if !env.IsWorkflowCompleted() {
		t.Fatal("NotifyTick must complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("NotifyTick error: %v", err)
	}
	if probe.detailGet != 0 {
		t.Errorf("job with description must not GET detail, calls=%d", probe.detailGet)
	}
	if probe.webhook != 0 {
		t.Errorf("empty claim must not POST Home Assistant, webhook calls=%d", probe.webhook)
	}
	if len(probe.applied) != 1 || probe.applied[0].Decision.State != "eligible" {
		t.Errorf("passers must become eligible, got %+v", probe.applied)
	}
}

func TestScrapeTick_PassesForceAndCompletesWhenSkipped(t *testing.T) {
	env, started, probe := newWorkflowEnv()
	probe.result = scraper.Result{Status: "skipped"}

	env.ExecuteWorkflow(ScrapeTick, scraper.TickInput{Force: true})

	if !env.IsWorkflowCompleted() {
		t.Fatal("ScrapeTick must complete when no provider is due")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("empty due-set must complete without workflow error: %v", err)
	}
	var got scraper.Result
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("read ScrapeTick result: %v", err)
	}
	if got.Status != "skipped" {
		t.Errorf("empty due-set result status = %q, want skipped", got.Status)
	}
	if !probe.lastIn.Force {
		t.Errorf("ScrapeTick must pass force to the scrape activity, got %+v", probe.lastIn)
	}
	if probe.scrape != 1 {
		t.Errorf("Scrape activity MaximumAttempts must stay 1, got %d", probe.scrape)
	}
	assertActivityNames(t, *started, "Scrape")
}

func newWorkflowEnv() (*testsuite.TestWorkflowEnvironment, *[]string, *activityProbe) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	probe := &activityProbe{}
	env.RegisterActivity(probe)
	var started []string
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		started = append(started, info.ActivityType.Name)
	})
	return env, &started, probe
}

func assertActivityNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("started activities = %v, want %v", got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("started activities = %v, want %v", got, want)
			return
		}
	}
}

func assertActivityContains(t *testing.T, got []string, want ...string) {
	t.Helper()
	for _, name := range want {
		if !containsName(got, name) {
			t.Errorf("started activities = %v, want to include %q", got, name)
		}
	}
}

func domainJobNeedingDescription() domain.Job {
	return domain.Job{
		ID:        10,
		Title:     "IT Help Desk",
		Company:   "Acme",
		JobURL:    "https://www.linkedin.com/jobs/view/10/",
		CreatedAt: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC),
	}
}

type activityProbe struct {
	scrape      int
	webhook     int
	detailGet   int
	apply       int
	claimCalls  int
	finish      int
	scrapeErr   error
	detailErr   error
	webhookErr  error
	result      scraper.Result
	lastIn      scraper.TickInput
	lastMessage string
	snapshot    NotifySnapshot
	applied     []ApplyJobDecisionInput
	claim       ClaimBatch
	finished    []FinishNotifyBatchInput
}

func (p *activityProbe) Scrape(_ context.Context, in scraper.TickInput) (scraper.Result, error) {
	p.scrape++
	p.lastIn = in
	if p.scrapeErr != nil {
		return scraper.Result{}, p.scrapeErr
	}
	if p.result.Status != "" {
		return p.result, nil
	}
	src := in.JobSource
	if src == "" {
		src = "LINKEDIN"
	}
	return scraper.Result{Status: "ok", JobSource: src, ScrapedCount: 2, SavedCount: 2}, nil
}

func (p *activityProbe) DuplicateRemover(_ context.Context) error { return nil }
func (p *activityProbe) BasicFilter(_ context.Context) error      { return nil }
func (p *activityProbe) Detailer(_ context.Context) error         { return nil }
func (p *activityProbe) AdvancedFilter(_ context.Context) error   { return nil }
func (p *activityProbe) SmartFilter(_ context.Context) error      { return nil }
func (p *activityProbe) Notifier(_ context.Context) error         { return nil }

func (p *activityProbe) PostWebhook(_ context.Context) error {
	p.webhook++
	return nil
}

func (p *activityProbe) LoadNotifySnapshot(context.Context) (NotifySnapshot, error) {
	return p.snapshot, nil
}

func (p *activityProbe) FetchJobDescription(_ context.Context, _ string) (string, error) {
	p.detailGet++
	if p.detailErr != nil {
		return "", p.detailErr
	}
	return "computer troubleshooting on windows", nil
}

func (p *activityProbe) ApplyJobDecision(_ context.Context, in ApplyJobDecisionInput) error {
	p.apply++
	p.applied = append(p.applied, in)
	return nil
}

func (p *activityProbe) ClaimNotifyBatch(context.Context) (ClaimBatch, error) {
	p.claimCalls++
	return p.claim, nil
}

func (p *activityProbe) NotifyWebhook(_ context.Context, message string) error {
	p.webhook++
	p.lastMessage = message
	return p.webhookErr
}

func (p *activityProbe) FinishNotifyBatch(_ context.Context, in FinishNotifyBatchInput) error {
	p.finish++
	p.finished = append(p.finished, in)
	return nil
}

type registryRecorder struct {
	workflows  []string
	activities []string
}

func (r *registryRecorder) RegisterWorkflow(w interface{}) {
	r.workflows = append(r.workflows, funcBaseName(w))
}

func (r *registryRecorder) RegisterActivity(a interface{}) {
	t := reflect.TypeOf(a)
	if t != nil && t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
		for i := 0; i < t.NumMethod(); i++ {
			r.activities = append(r.activities, t.Method(i).Name)
		}
		return
	}
	r.activities = append(r.activities, funcBaseName(a))
}

func (r *registryRecorder) hasWorkflow(name string) bool {
	return containsName(r.workflows, name)
}

func (r *registryRecorder) hasActivity(name string) bool {
	return containsName(r.activities, name)
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func funcBaseName(fn any) string {
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.Kind() != reflect.Func {
		return fmt.Sprintf("%T", fn)
	}
	name := runtime.FuncForPC(v.Pointer()).Name()
	name = strings.TrimSuffix(name, "-fm")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}
