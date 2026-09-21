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

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/domain"
	"github.com/jobscout/jobscout/internal/scraper"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type NotifySnapshot struct {
	Pending []domain.Job
	All     []domain.Job
	Lists   domain.Lists
}

func TestScrapeProviderTaskQueue(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"LINKEDIN", config.LinkedInScrapeTaskQueue},
		{"DICE", config.ApifyScrapeTaskQueue},
		{"INDEED", config.ApifyScrapeTaskQueue},
		{"", config.TaskQueue},
	}
	for _, tc := range cases {
		if got := ScrapeProviderTaskQueue(tc.source); got != tc.want {
			t.Errorf("ScrapeProviderTaskQueue(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

func TestScrapeProviderActivityName(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"LINKEDIN", ActivityScrapeProviderLinkedIn},
		{"DICE", ActivityScrapeProviderDice},
		{"INDEED", ActivityScrapeProviderIndeed},
	}
	for _, tc := range cases {
		if got := ScrapeProviderActivityName(tc.source); got != tc.want {
			t.Errorf("ScrapeProviderActivityName(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}
}

func TestRegisterMain_OmitsScrapeProviderActivity(t *testing.T) {
	rec := &registryRecorder{}
	RegisterMain(rec, &Activities{})
	if rec.hasWorkflow("ProviderScrapeWorkflow") {
		t.Errorf("ProviderScrapeWorkflow must not be registered; registered %v", rec.workflows)
	}
	for _, name := range []string{
		ActivityScrapeProviderLinkedIn,
		ActivityScrapeProviderDice,
		ActivityScrapeProviderIndeed,
	} {
		if rec.hasActivity(name) {
			t.Errorf("main worker must not own %s; registered %v", name, rec.activities)
		}
	}
	if !rec.hasWorkflow("ScrapeWorkflow") {
		t.Errorf("main worker must register ScrapeWorkflow; registered %v", rec.workflows)
	}
	if !rec.hasActivity(ActivityListDueProviders) {
		t.Errorf("main worker must register %s; registered %v", ActivityListDueProviders, rec.activities)
	}
}

func TestRegisterApifyScrapeActivities(t *testing.T) {
	rec := &registryRecorder{}
	RegisterApifyScrapeActivities(rec, &Activities{})
	if !rec.hasActivity(ActivityScrapeProviderDice) || !rec.hasActivity(ActivityScrapeProviderIndeed) {
		t.Errorf("apify queue must register dice and indeed; registered %v", rec.activities)
	}
	if rec.hasActivity(ActivityScrapeProviderLinkedIn) {
		t.Errorf("apify queue must not register linkedin; registered %v", rec.activities)
	}
}

func TestRegisterLinkedInScrapeActivities(t *testing.T) {
	rec := &registryRecorder{}
	RegisterLinkedInScrapeActivities(rec, &Activities{})
	if !rec.hasActivity(ActivityScrapeProviderLinkedIn) {
		t.Errorf("linkedin queue must register linkedin; registered %v", rec.activities)
	}
	if rec.hasActivity(ActivityScrapeProviderDice) {
		t.Errorf("linkedin queue must not register dice; registered %v", rec.activities)
	}
}

func TestRegister_ScrapeAndNotifyAreProductionPath(t *testing.T) {
	rec := &registryRecorder{}
	Register(rec, &Activities{})

	if rec.hasWorkflow("MainWorkflow") {
		t.Errorf("MainWorkflow must not be the production path; registered %v", rec.workflows)
	}
	if !rec.hasWorkflow("ScrapeWorkflow") {
		t.Errorf("worker must register ScrapeWorkflow; registered %v", rec.workflows)
	}
	if rec.hasWorkflow("ProviderScrapeWorkflow") {
		t.Errorf("ProviderScrapeWorkflow must not be registered; registered %v", rec.workflows)
	}
	if !rec.hasWorkflow("NotifyWorkflow") {
		t.Errorf("worker must register NotifyWorkflow; registered %v", rec.workflows)
	}
	if !rec.hasWorkflow("FilterPendingWorkflow") {
		t.Errorf("worker must register FilterPendingWorkflow; registered %v", rec.workflows)
	}
	if !rec.hasWorkflow("ProcessPendingWorkflow") {
		t.Errorf("worker must register ProcessPendingWorkflow; registered %v", rec.workflows)
	}
	if !rec.hasWorkflow("ProcessJobWorkflow") {
		t.Errorf("worker must register ProcessJobWorkflow; registered %v", rec.workflows)
	}
	if !rec.hasActivity(ActivityListDueProviders) {
		t.Errorf("worker must register %s; registered %v", ActivityListDueProviders, rec.activities)
	}
	for _, name := range []string{
		ActivityScrapeProviderLinkedIn,
		ActivityScrapeProviderDice,
		ActivityScrapeProviderIndeed,
	} {
		if !rec.hasActivity(name) {
			t.Errorf("worker must register %s; registered %v", name, rec.activities)
		}
	}
	if !rec.hasActivity(ActivityClaimNotificationBatch) {
		t.Errorf("worker must register %s; registered %v", ActivityClaimNotificationBatch, rec.activities)
	}
	if !rec.hasActivity(ActivitySendNotification) {
		t.Errorf("worker must register %s; registered %v", ActivitySendNotification, rec.activities)
	}
	if !rec.hasActivity(ActivityFinishNotificationBatch) {
		t.Errorf("worker must register %s; registered %v", ActivityFinishNotificationBatch, rec.activities)
	}
	if !rec.hasActivity(ActivityNotificationStatus) {
		t.Errorf("worker must register %s; registered %v", ActivityNotificationStatus, rec.activities)
	}
	for _, required := range []string{
		ActivityWakeFilterPending,
		ActivityWakeProcessPending,
		ActivityLoadNextPendingJob,
		ActivityLoadNextNeedsDetailJob,
		ActivityFilterPendingJob,
		ActivityLoadProcessJob,
		ActivityLoadFilterLists,
		ActivityGetJobDescription,
		ActivitySaveJobFilterResult,
	} {
		if !rec.hasActivity(required) {
			t.Errorf("process-job activity %s must be registered; registered %v", required, rec.activities)
		}
	}

	for _, stub := range []string{
		"smart_filter",
		"duplicate_remover",
		"basic_filter",
		"job_detailer",
		"advanced_filter",
		"notifier",
	} {
		if rec.hasActivity(stub) {
			t.Errorf("stub pipeline stage %s must not be the production path; registered %v", stub, rec.activities)
		}
	}
}

func TestScrapeTick_StoresJobsWithoutSmartFilter(t *testing.T) {
	env, started, probe := newWorkflowEnv()

	env.ExecuteWorkflow(ScrapeWorkflow, scraper.TickInput{JobSource: "LINKEDIN"})

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
	assertActivityNames(t, *started, ActivityListDueProviders, ActivityScrapeProviderLinkedIn, ActivityWakeFilterPending)
}

func TestScrapeTick_LinkedInSearchActivityRunsOnce(t *testing.T) {
	env, started, probe := newWorkflowEnv()
	probe.scrapeErr = errors.New("linkedin search failed")

	env.ExecuteWorkflow(ScrapeWorkflow, scraper.TickInput{JobSource: "LINKEDIN"})

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
	assertActivityNames(t, *started, ActivityListDueProviders, ActivityScrapeProviderLinkedIn, ActivityWakeFilterPending)
}

func TestScrapeTick_ScrapeActivityAllowsFortyFiveMinutes(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	probe := &activityProbe{}
	registerProbeActivities(env, probe)
	var timeout time.Duration
	var heartbeat time.Duration
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		if info.ActivityType.Name == ActivityScrapeProviderLinkedIn {
			timeout = info.StartToCloseTimeout
			heartbeat = info.HeartbeatTimeout
		}
	})

	env.ExecuteWorkflow(ScrapeWorkflow, scraper.TickInput{JobSource: "LINKEDIN"})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ScrapeTick error: %v", err)
	}
	if timeout != 45*time.Minute {
		t.Errorf("Scrape StartToCloseTimeout = %s, want 45m", timeout)
	}
	if heartbeat != 2*time.Minute {
		t.Errorf("Scrape HeartbeatTimeout = %s, want 2m", heartbeat)
	}
}

func TestNotifyTick_CompletesWithoutLinkedInSearchOrWebhook(t *testing.T) {
	env, started, probe := newWorkflowEnv()

	env.ExecuteWorkflow(NotifyWorkflow)

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
	assertActivityNames(t, *started, ActivityNotificationStatus, ActivityClaimNotificationBatch)
}

func TestNotifyTick_ClaimsReadyJobsWithoutFilteringOrDetailGET(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	probe.snapshot = NotifySnapshot{
		Pending: []domain.Job{job},
		All:     []domain.Job{job},
		Lists: domain.Lists{
			TitleInclude: []string{"Help Desk"},
			DescInclude:  []string{"computer"},
		},
	}
	probe.claim = ClaimBatch{
		Jobs: []ClaimedJob{{ID: job.ID, JobURL: job.JobURL}},
	}

	env.ExecuteWorkflow(NotifyWorkflow)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("NotifyTick error: %v", err)
	}
	if probe.detailGet != 0 {
		t.Errorf("Notify must not GET descriptions; detail calls=%d", probe.detailGet)
	}
	if probe.apply != 0 {
		t.Errorf("Notify must not filter or update review state; apply calls=%d", probe.apply)
	}
	want := "there are 1 new jobs ready for review:\n" + job.JobURL
	if probe.lastMessage != want {
		t.Errorf("POST message = %q, want %q", probe.lastMessage, want)
	}
}

func TestProcessJobWorkflow_EmptyBodyStaysNeedsDetailAndThrottles(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	job.DetailAttempts = 1
	probe.processJob = ProcessJob{Job: job, State: domain.StateNeedsDetail, JobSource: "LINKEDIN"}
	probe.filterLists = domain.Lists{TitleInclude: []string{"help desk"}}
	probe.detailResults = []DetailFetchResult{{}}

	env.ExecuteWorkflow(ProcessJobWorkflow, job.ID)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ProcessJobWorkflow error: %v", err)
	}
	var result ProcessJobResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if !result.Throttle {
		t.Error("empty body must return Throttle=true")
	}
	assertLastDecision(t, probe, domain.StateNeedsDetail, "", 2)
}

func TestProcessJobWorkflow_SuccessfulGETSavesDescriptionAndThrottles(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	probe.processJob = ProcessJob{Job: job, State: domain.StateNeedsDetail, JobSource: "LINKEDIN"}
	probe.filterLists = domain.Lists{
		TitleInclude: []string{"help desk"}, DescInclude: []string{"computer"},
	}
	probe.detailResults = []DetailFetchResult{{Description: "computer troubleshooting"}}

	env.ExecuteWorkflow(ProcessJobWorkflow, job.ID)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ProcessJobWorkflow error: %v", err)
	}
	var result ProcessJobResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if !result.Throttle {
		t.Error("successful source GET must return Throttle=true")
	}
	assertLastDecision(t, probe, domain.StateReady, "", 0)
	if got := probe.applied[0].Decision.Description; got != "computer troubleshooting" {
		t.Errorf("saved description = %q, want fetched body", got)
	}
}

func TestProcessJobWorkflow_GETActivityDoesNotRetryErrors(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	probe.processJob = ProcessJob{Job: job, State: domain.StateNeedsDetail, JobSource: "LINKEDIN"}
	probe.filterLists = domain.Lists{TitleInclude: []string{"help desk"}}
	probe.detailErr = errors.New("unexpected activity failure")

	env.ExecuteWorkflow(ProcessJobWorkflow, job.ID)

	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("unexpected GET activity error must fail the child")
	}
	if probe.detailGet != 1 {
		t.Errorf("GET activity attempts = %d, want 1", probe.detailGet)
	}
}

func TestProcessJobWorkflow_ThirdDetailFailureRejects(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	job.DetailAttempts = 2
	probe.processJob = ProcessJob{Job: job, State: domain.StateNeedsDetail, JobSource: "LINKEDIN"}
	probe.filterLists = domain.Lists{TitleInclude: []string{"help desk"}}
	probe.detailResults = []DetailFetchResult{{FetchFailed: true}}

	env.ExecuteWorkflow(ProcessJobWorkflow, job.ID)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ProcessJobWorkflow error: %v", err)
	}
	assertLastDecision(t, probe, domain.StateRejected, domain.ReasonDetailFailed, 3)
}

func TestProcessJobWorkflow_LockBusyRetriesEightThenLeavesNeedsDetail(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	probe.processJob = ProcessJob{Job: job, State: domain.StateNeedsDetail, JobSource: "LINKEDIN"}
	probe.filterLists = domain.Lists{TitleInclude: []string{"help desk"}}
	probe.detailResults = make([]DetailFetchResult, processJobLockMaxRetries)
	for i := range probe.detailResults {
		probe.detailResults[i].LockBusy = true
	}

	env.ExecuteWorkflow(ProcessJobWorkflow, job.ID)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ProcessJobWorkflow error: %v", err)
	}
	if probe.detailGet != processJobLockMaxRetries {
		t.Errorf("detail GET calls = %d, want %d", probe.detailGet, processJobLockMaxRetries)
	}
	if probe.apply != 0 {
		t.Errorf("lock contention persisted %d decisions, want zero", probe.apply)
	}
	var result ProcessJobResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("workflow result: %v", err)
	}
	if result.Throttle {
		t.Error("exhausted lock retries must return Throttle=false")
	}
}

func TestProcessJobWorkflow_UnsupportedSourceRejectsWithoutGET(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	probe.processJob = ProcessJob{Job: job, State: domain.StateNeedsDetail, JobSource: "INDEED"}
	probe.filterLists = domain.Lists{}

	env.ExecuteWorkflow(ProcessJobWorkflow, job.ID)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ProcessJobWorkflow error: %v", err)
	}
	if probe.detailGet != 0 {
		t.Errorf("unsupported source caused %d GETs, want zero", probe.detailGet)
	}
	assertLastDecision(t, probe, domain.StateRejected, domain.ReasonUnsupportedSource, 0)
}

func TestProcessJobWorkflow_NonNeedsDetailDoesNothing(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	probe.processJob = ProcessJob{
		Job: domainJobNeedingDescription(), State: domain.StatePending, JobSource: "LINKEDIN",
	}

	env.ExecuteWorkflow(ProcessJobWorkflow, probe.processJob.Job.ID)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ProcessJobWorkflow error: %v", err)
	}
	if probe.listLoads != 0 || probe.detailGet != 0 || probe.apply != 0 {
		t.Errorf("non-needs_detail job did work: %+v", probe)
	}
}

func TestProcessJobWorkflow_DoesNotRunCheapFilters(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	job := domainJobNeedingDescription()
	job.Title = "Registered Nurse"
	probe.processJob = ProcessJob{Job: job, State: domain.StateNeedsDetail, JobSource: "LINKEDIN"}
	probe.filterLists = domain.Lists{TitleInclude: []string{"help desk"}, DescInclude: []string{"computer"}}
	probe.detailResults = []DetailFetchResult{{Description: "computer troubleshooting"}}

	env.ExecuteWorkflow(ProcessJobWorkflow, job.ID)

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("ProcessJobWorkflow error: %v", err)
	}
	if probe.groupLoads != 0 {
		t.Errorf("detail child must not load duplicate groups, loads=%d", probe.groupLoads)
	}
	assertLastDecision(t, probe, domain.StateReady, "", 0)
}

func TestNotifyTick_DetailGETRunsOnce(t *testing.T) {
	t.Skip("superseded: Notify no longer fetches descriptions")
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

	env.ExecuteWorkflow(NotifyWorkflow)

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
	assertActivityContains(t, *started, "load_jobs_for_filtering", ActivityGetJobDescription, ActivitySaveJobFilterResult)
}

func TestNotifyTick_DetailGETOncePerJobThenReady(t *testing.T) {
	t.Skip("superseded: pending processing moved out of Notify")
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

	env.ExecuteWorkflow(NotifyWorkflow)

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
		if in.Decision.State != domain.StateReady {
			t.Errorf("job %d state = %q, want ready after successful detail fetch", i, in.Decision.State)
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
	}

	env.ExecuteWorkflow(NotifyWorkflow)

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
		"there are 2 new jobs ready for review:\n" +
		older + "\n" +
		newer
	if probe.lastMessage != wantMsg {
		t.Errorf("POST message =\n%q\nwant\n%q", probe.lastMessage, wantMsg)
	}
	if probe.finish != 0 {
		t.Errorf("HTTP 200 must keep claim markers, cleanup calls=%d", probe.finish)
	}
	if len(probe.finished) != 0 {
		t.Errorf("HTTP 200 must keep dedupe markers, got cleanup calls %+v", probe.finished)
	}
	if len(probe.finished) == 1 {
		if len(probe.finished[0].IDs) != 2 || probe.finished[0].IDs[0] != 8 || probe.finished[0].IDs[1] != 9 {
			t.Errorf("finish IDs = %v, want claimed [8 9]", probe.finished[0].IDs)
		}
	}
	assertActivityNames(t, *started, ActivityNotificationStatus, ActivityClaimNotificationBatch, ActivitySendNotification)
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

	env.ExecuteWorkflow(NotifyWorkflow)

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
	assertActivityNames(t, *started, ActivityNotificationStatus, ActivityClaimNotificationBatch)
}

func TestNotifyTick_InactiveSkipsClaimAndWebhook(t *testing.T) {
	env, started, probe := newWorkflowEnv()
	probe.notifyStatus = ResolveNotificationStatus(true, false)
	probe.claim = ClaimBatch{
		Jobs: []ClaimedJob{{ID: 1, JobURL: "https://example.test/jobs/1"}},
	}

	env.ExecuteWorkflow(NotifyWorkflow)

	if !env.IsWorkflowCompleted() {
		t.Fatal("NotifyTick must complete when notifications are inactive")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("inactive notify must not fail: %v", err)
	}
	if probe.claimCalls != 0 {
		t.Errorf("inactive notify must not claim jobs, claim calls=%d", probe.claimCalls)
	}
	if probe.webhook != 0 {
		t.Errorf("inactive notify must not POST, webhook calls=%d", probe.webhook)
	}
	assertActivityNames(t, *started, ActivityNotificationStatus)
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
		Jobs: []ClaimedJob{{ID: job.ID, JobURL: job.JobURL}},
	}
	probe.webhookErr = errors.New("webhook HTTP 500")

	env.ExecuteWorkflow(NotifyWorkflow)

	if !env.IsWorkflowCompleted() {
		t.Fatal("NotifyTick must complete after a single failed webhook POST")
	}
	if err := env.GetWorkflowError(); err == nil || !strings.Contains(err.Error(), probe.webhookErr.Error()) {
		t.Fatalf("NotifyTick must fail with the webhook error after cleanup, got %v", err)
	}
	if probe.webhook != 1 {
		t.Errorf("webhook POST activity retry policy MaximumAttempts must be 1, got %d attempts", probe.webhook)
	}
	if probe.finish != 1 || len(probe.finished) != 1 {
		t.Errorf("non-200/transport must clear the claimed jobs, got %+v", probe.finished)
	}
	if len(probe.finished) == 1 && (len(probe.finished[0].IDs) != 1 || probe.finished[0].IDs[0] != job.ID) {
		t.Errorf("finish IDs = %v, want claimed [%d]", probe.finished[0].IDs, job.ID)
	}
}

func TestNotifyTick_PassersBecomeReadyWithoutWebhook(t *testing.T) {
	t.Skip("superseded: pending processing moved out of Notify")
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

	env.ExecuteWorkflow(NotifyWorkflow)

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
	if len(probe.applied) != 1 || probe.applied[0].Decision.State != "ready" {
		t.Errorf("passers must become ready, got %+v", probe.applied)
	}
}

func TestScrapeTick_PassesForceAndCompletesWhenSkipped(t *testing.T) {
	env, started, probe := newWorkflowEnv()
	probe.listForced = true
	probe.listSources = []string{}

	env.ExecuteWorkflow(ScrapeWorkflow, scraper.TickInput{Force: true})

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
		t.Errorf("ScrapeTick must pass force to list_due_providers, got %+v", probe.lastIn)
	}
	if probe.scrape != 0 {
		t.Errorf("empty due-set must not scrape providers, got %d", probe.scrape)
	}
	assertActivityNames(t, *started, ActivityListDueProviders, ActivityWakeFilterPending)
}

func TestScrapeTick_OneProviderSuccessCompletesWhenSiblingFails(t *testing.T) {
	env, started, probe := newWorkflowEnv()
	probe.listForced = true
	probe.listSources = []string{"LINKEDIN", "DICE"}
	probe.scrapeBySource = map[string]scraper.Result{
		"LINKEDIN": {Status: "ok", JobSource: "LINKEDIN", ScrapedCount: 3, SavedCount: 2},
	}
	probe.scrapeErrBySource = map[string]error{
		"DICE": errors.New("apify actor failed"),
	}

	env.ExecuteWorkflow(ScrapeWorkflow, scraper.TickInput{Force: true})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("parent must complete when one child fails: %v", err)
	}
	var got scraper.Result
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("read ScrapeTick result: %v", err)
	}
	if got.Status != "error" {
		t.Errorf("combined status = %q, want error when any provider fails", got.Status)
	}
	if got.ScrapedCount != 3 || got.SavedCount != 2 {
		t.Errorf("successful sibling counts must survive, got scraped=%d saved=%d", got.ScrapedCount, got.SavedCount)
	}
	if probe.scrape != 2 {
		t.Errorf("both providers must scrape as parallel activities, scrape calls=%d", probe.scrape)
	}
	if probe.wake != 1 {
		t.Errorf("wake must run once after all scrapes, wake=%d", probe.wake)
	}
	if len(*started) != 4 {
		t.Errorf("started activities = %v, want 4 entries", *started)
	}
	assertActivityContains(t, *started,
		ActivityListDueProviders,
		ActivityScrapeProviderLinkedIn,
		ActivityScrapeProviderDice,
		ActivityWakeFilterPending,
	)
}

func TestScrapeTick_ErrorStatusActivityFailsButParentCompletes(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	probe.listForced = true
	probe.listSources = []string{"INDEED"}
	probe.scrapeBySource = map[string]scraper.Result{
		"INDEED": {Status: "error", JobSource: "INDEED", Error: "actor timed out"},
	}

	env.ExecuteWorkflow(ScrapeWorkflow, scraper.TickInput{Force: true})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("parent must complete when scrape_provider fails: %v", err)
	}
	var got scraper.Result
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("read result: %v", err)
	}
	if got.Status != "error" {
		t.Errorf("status = %q, want error", got.Status)
	}
}

func TestScrapeTick_SkippedActivityCompletesGreen(t *testing.T) {
	env, _, probe := newWorkflowEnv()
	probe.listForced = true
	probe.listSources = []string{"INDEED"}
	probe.scrapeBySource = map[string]scraper.Result{
		"INDEED": {Status: "skipped", JobSource: "INDEED", Error: "apify advisory lock busy"},
	}

	env.ExecuteWorkflow(ScrapeWorkflow, scraper.TickInput{Force: true})

	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("skipped must keep parent green: %v", err)
	}
	var got scraper.Result
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatalf("read result: %v", err)
	}
	if got.Status != "skipped" {
		t.Errorf("status = %q, want skipped", got.Status)
	}
}

func newWorkflowEnv() (*testsuite.TestWorkflowEnvironment, *[]string, *activityProbe) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	probe := &activityProbe{
		notifyStatus: ResolveNotificationStatus(true, true),
	}
	registerProbeActivities(env, probe)
	var started []string
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		started = append(started, info.ActivityType.Name)
	})
	return env, &started, probe
}

func registerProbeActivities(env *testsuite.TestWorkflowEnvironment, probe *activityProbe) {
	env.RegisterActivityWithOptions(probe.ListDueProviders, activity.RegisterOptions{Name: ActivityListDueProviders})
	env.RegisterActivityWithOptions(probe.ScrapeProvider, activity.RegisterOptions{Name: ActivityScrapeProviderLinkedIn})
	env.RegisterActivityWithOptions(probe.ScrapeProvider, activity.RegisterOptions{Name: ActivityScrapeProviderDice})
	env.RegisterActivityWithOptions(probe.ScrapeProvider, activity.RegisterOptions{Name: ActivityScrapeProviderIndeed})
	env.RegisterActivityWithOptions(probe.LoadNotifySnapshot, activity.RegisterOptions{Name: "load_jobs_for_filtering"})
	env.RegisterActivityWithOptions(probe.LoadProcessJob, activity.RegisterOptions{Name: ActivityLoadProcessJob})
	env.RegisterActivityWithOptions(probe.LoadFilterLists, activity.RegisterOptions{Name: ActivityLoadFilterLists})
	env.RegisterActivityWithOptions(probe.FetchJobDescription, activity.RegisterOptions{Name: ActivityGetJobDescription})
	env.RegisterActivityWithOptions(probe.ApplyJobDecision, activity.RegisterOptions{Name: ActivitySaveJobFilterResult})
	env.RegisterActivityWithOptions(probe.ClaimNotifyBatch, activity.RegisterOptions{Name: ActivityClaimNotificationBatch})
	env.RegisterActivityWithOptions(probe.NotifyWebhook, activity.RegisterOptions{Name: ActivitySendNotification})
	env.RegisterActivityWithOptions(probe.FinishNotifyBatch, activity.RegisterOptions{Name: ActivityFinishNotificationBatch})
	env.RegisterActivityWithOptions(probe.NotificationStatus, activity.RegisterOptions{Name: ActivityNotificationStatus})
	env.RegisterActivityWithOptions(probe.WakeFilterPending, activity.RegisterOptions{Name: ActivityWakeFilterPending})
	env.RegisterActivityWithOptions(probe.WakeProcessPending, activity.RegisterOptions{Name: ActivityWakeProcessPending})
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

func assertLastDecision(t *testing.T, probe *activityProbe, state, reason string, attempts int) {
	t.Helper()
	if len(probe.applied) != 1 {
		t.Fatalf("saved decisions = %d, want 1: %+v", len(probe.applied), probe.applied)
	}
	got := probe.applied[0].Decision
	if got.State != state || got.RejectReason != reason || got.DetailAttempts != attempts {
		t.Errorf("saved decision = %+v, want state=%q reason=%q attempts=%d", got, state, reason, attempts)
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
	scrape            int
	webhook           int
	detailGet         int
	apply             int
	groupLoads        int
	listLoads         int
	claimCalls        int
	finish            int
	wake              int
	wakeProcess       int
	statusCalls       int
	scrapeErr         error
	scrapeErrBySource map[string]error
	scrapeBySource    map[string]scraper.Result
	listSources       []string
	listForced        bool
	wakeErr           error
	wakeProcessErr    error
	detailErr         error
	webhookErr        error
	result            scraper.Result
	lastIn            scraper.TickInput
	lastSource        string
	lastMessage       string
	snapshot          NotifySnapshot
	applied           []ApplyJobDecisionInput
	claim             ClaimBatch
	finished          []FinishNotifyBatchInput
	processJob        ProcessJob
	duplicateGroup    []domain.Job
	filterLists       domain.Lists
	detailResults     []DetailFetchResult
	notifyStatus      NotificationStatus
}

func (p *activityProbe) ListDueProviders(_ context.Context, in scraper.TickInput) ([]string, error) {
	p.lastIn = in
	if p.listForced {
		return p.listSources, nil
	}
	if in.JobSource != "" {
		return []string{in.JobSource}, nil
	}
	return nil, nil
}

func (p *activityProbe) ScrapeProvider(_ context.Context, source string) (scraper.Result, error) {
	p.scrape++
	p.lastSource = source
	if err, ok := p.scrapeErrBySource[source]; ok && err != nil {
		return scraper.Result{}, err
	}
	if p.scrapeErr != nil {
		return scraper.Result{}, p.scrapeErr
	}
	if res, ok := p.scrapeBySource[source]; ok {
		return res, nil
	}
	if p.result.Status != "" {
		return p.result, nil
	}
	src := source
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

func (p *activityProbe) LoadProcessJob(context.Context, int64) (ProcessJob, error) {
	return p.processJob, nil
}

func (p *activityProbe) LoadFilterLists(context.Context) (domain.Lists, error) {
	p.listLoads++
	return p.filterLists, nil
}

func (p *activityProbe) WakeFilterPending(context.Context) error {
	p.wake++
	return p.wakeErr
}

func (p *activityProbe) WakeProcessPending(context.Context) error {
	p.wakeProcess++
	return p.wakeProcessErr
}

func (p *activityProbe) FetchJobDescription(_ context.Context, _ DetailFetchInput) (DetailFetchResult, error) {
	p.detailGet++
	if p.detailErr != nil {
		return DetailFetchResult{}, p.detailErr
	}
	if p.detailGet <= len(p.detailResults) {
		return p.detailResults[p.detailGet-1], nil
	}
	return DetailFetchResult{Description: "computer troubleshooting on windows"}, nil
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

func (p *activityProbe) NotificationStatus(context.Context) (NotificationStatus, error) {
	p.statusCalls++
	return p.notifyStatus, nil
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

func (r *registryRecorder) RegisterWorkflowWithOptions(_ interface{}, options workflow.RegisterOptions) {
	r.workflows = append(r.workflows, options.Name)
}

func (r *registryRecorder) RegisterActivityWithOptions(_ interface{}, options activity.RegisterOptions) {
	r.activities = append(r.activities, options.Name)
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
