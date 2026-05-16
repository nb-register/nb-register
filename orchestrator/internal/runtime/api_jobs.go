package runtime

import (
	"context"
	"orchestrator/internal/contracts"
	"orchestrator/internal/jobprojection"
	"orchestrator/pb"
	"strings"
	"time"
)

func (s *orchestratorServer) GetJob(ctx context.Context, req *pb.GetJobRequest) (*pb.GetJobResponse, error) {
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return &pb.GetJobResponse{ErrorMessage: "job_id is required"}, nil
	}

	snapshot, err := s.jobStore.GetSnapshot(ctx, jobID)
	if err != nil {
		return &pb.GetJobResponse{ErrorMessage: err.Error()}, nil
	}

	s.withWorkflowProgress(ctx, snapshot)
	return &pb.GetJobResponse{Snapshot: snapshot}, nil
}

func (s *orchestratorServer) ListJobs(ctx context.Context, req *pb.ListJobsRequest) (*pb.ListJobsResponse, error) {
	snapshots, err := s.jobStore.ListSnapshots(ctx, jobprojection.ListFilter{
		Limit:     int(req.GetLimit()),
		Status:    req.GetStatus(),
		Action:    req.GetAction(),
		AccountID: req.GetAccountId(),
	})
	if err != nil {
		return &pb.ListJobsResponse{ErrorMessage: err.Error()}, nil
	}

	for _, snapshot := range snapshots {
		s.withWorkflowProgress(ctx, snapshot)
	}
	return &pb.ListJobsResponse{Snapshots: snapshots}, nil
}

func (s *orchestratorServer) WatchJob(req *pb.WatchJobRequest, stream pb.OrchestratorService_WatchJobServer) error {
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return stream.Send(&pb.WatchJobResponse{ErrorMessage: "job_id is required"})
	}

	lastSent := req.GetAfterEventId()
	send := func() (bool, error) {
		snapshot, err := s.jobStore.GetSnapshot(stream.Context(), jobID)
		if err != nil {
			return false, stream.Send(&pb.WatchJobResponse{ErrorMessage: err.Error()})
		}
		s.withWorkflowProgress(stream.Context(), snapshot)
		if snapshot.GetEventId() > lastSent {
			if err := stream.Send(&pb.WatchJobResponse{Snapshot: snapshot}); err != nil {
				return false, err
			}
			lastSent = snapshot.GetEventId()
		}
		return snapshotIsRunning(snapshot), nil
	}

	running, err := send()
	if err != nil || !running {
		return err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			running, err := send()
			if err != nil || !running {
				return err
			}
		}
	}
}

func (s *orchestratorServer) withWorkflowProgress(ctx context.Context, snapshot *pb.JobSnapshot) {
	if snapshot == nil || !snapshotIsRunning(snapshot) {
		return
	}
	job := snapshot.GetJob()
	workflowID, ok := contracts.WorkflowID(job.GetAction(), job.GetJobId())
	if ok && s.temporal != nil {
		query, err := s.temporal.QueryWorkflow(ctx, workflowID, "", workflowProgressQueryName)
		if err == nil {
			var progress WorkflowProgress
			if err := query.Get(&progress); err == nil {
				jobprojection.ApplyProgress(snapshot, &progress)
			}
		}
	}
}

func snapshotIsRunning(snapshot *pb.JobSnapshot) bool {
	return snapshot != nil && strings.EqualFold(strings.TrimSpace(snapshot.GetJob().GetStatus()), "RUNNING")
}
