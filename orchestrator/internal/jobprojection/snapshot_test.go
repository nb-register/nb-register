package jobprojection

import (
	"testing"

	"orchestrator/db"
	"orchestrator/internal/contracts"
	"orchestrator/pb"
)

func TestBuildSnapshotProjectsJobProgressAndEventID(t *testing.T) {
	job := &db.Job{
		ID:        "job-1",
		AccountID: "account-1",
		Action:    contracts.ActionRegister,
		Status:    "RUNNING",
		LastStep:  "register_account",
		UpdatedAt: 10,
	}
	steps := []db.JobStep{
		{JobID: "job-1", StepName: "create_job", Status: "SUCCEEDED", UpdatedAt: 12},
		{JobID: "job-1", StepName: "register_account", Status: "RUNNING", UpdatedAt: 14},
	}

	snapshot := BuildSnapshot(job, steps)
	if snapshot.GetJob().GetJobId() != "job-1" {
		t.Fatalf("job id = %q; want job-1", snapshot.GetJob().GetJobId())
	}
	if got := snapshot.GetProgress().GetWorkflow(); got != "register-job-1" {
		t.Fatalf("workflow = %q; want register-job-1", got)
	}
	if got := snapshot.GetProgress().GetStatus(); got != "running" {
		t.Fatalf("progress status = %q; want running", got)
	}
	if got := snapshot.GetEventId(); got != 14 {
		t.Fatalf("event id = %d; want 14", got)
	}
}

func TestApplyProgressBumpsEventID(t *testing.T) {
	snapshot := &pb.JobSnapshot{EventId: 10}
	ApplyProgress(snapshot, &pb.WorkflowProgress{UpdatedAtUnix: 20, Status: "running"})

	if got := snapshot.GetProgress().GetStatus(); got != "running" {
		t.Fatalf("progress status = %q; want running", got)
	}
	if got := snapshot.GetEventId(); got != 20 {
		t.Fatalf("event id = %d; want 20", got)
	}
}
