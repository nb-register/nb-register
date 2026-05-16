package app

import (
	"context"
	"orchestrator/db"
	"orchestrator/internal/contracts"
	"orchestrator/pb"
	"strings"
)

func (s *orchestratorServer) GetJob(ctx context.Context, req *pb.GetJobRequest) (*pb.GetJobResponse, error) {
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return &pb.GetJobResponse{ErrorMessage: "job_id is required"}, nil
	}

	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return &pb.GetJobResponse{ErrorMessage: err.Error()}, nil
	}

	steps, err := s.jobStore.Steps(ctx, jobID)
	if err != nil {
		return &pb.GetJobResponse{ErrorMessage: err.Error()}, nil
	}

	return &pb.GetJobResponse{Job: jobToProto(job, steps)}, nil
}

func (s *orchestratorServer) GetWorkflowProgress(ctx context.Context, req *pb.GetWorkflowProgressRequest) (*pb.GetWorkflowProgressResponse, error) {
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return &pb.GetWorkflowProgressResponse{ErrorMessage: "job_id is required"}, nil
	}

	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return &pb.GetWorkflowProgressResponse{ErrorMessage: err.Error()}, nil
	}

	if !jobIsRunning(job) {
		return &pb.GetWorkflowProgressResponse{Progress: workflowProgressFromJob(job)}, nil
	}

	workflowID, ok := contracts.WorkflowID(job.Action, job.ID)
	if ok && s.temporal != nil {
		query, err := s.temporal.QueryWorkflow(ctx, workflowID, "", workflowProgressQueryName)
		if err == nil {
			var progress WorkflowProgress
			if err := query.Get(&progress); err == nil {
				return &pb.GetWorkflowProgressResponse{Progress: &progress}, nil
			}
		}
	}

	return &pb.GetWorkflowProgressResponse{Progress: workflowProgressFromJob(job)}, nil
}

func jobIsRunning(job *db.Job) bool {
	return job != nil && strings.EqualFold(strings.TrimSpace(job.Status), "RUNNING")
}

func workflowProgressFromJob(job *db.Job) *pb.WorkflowProgress {
	if job == nil {
		return nil
	}
	workflowID, _ := contracts.WorkflowID(job.Action, job.ID)
	stepName := strings.TrimSpace(job.LastStep)
	if stepName == "" {
		stepName = "created"
	}
	status := strings.ToLower(strings.TrimSpace(job.Status))
	if status == "" {
		status = "unknown"
	}
	return &pb.WorkflowProgress{
		JobId:         job.ID,
		Workflow:      workflowID,
		StepName:      stepName,
		Status:        status,
		ErrorMessage:  job.ErrorMessage,
		UpdatedAtUnix: job.UpdatedAt,
	}
}
