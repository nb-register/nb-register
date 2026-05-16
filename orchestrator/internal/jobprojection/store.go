package jobprojection

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"orchestrator/db"
	"orchestrator/internal/contracts"
	"orchestrator/internal/jobstatus"
	"orchestrator/pb"
)

type Store struct {
	db *gorm.DB
}

type StepFailure struct {
	JobID        string
	StepName     string
	Status       string
	Recoverable  bool
	Retryable    bool
	ErrorMessage string
	Result       any
}

type ListFilter struct {
	Limit     int
	Status    string
	Action    string
	AccountID string
}

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Create(ctx context.Context, accountID, action string, params map[string]string) (*db.Job, error) {
	return s.CreateWithID(ctx, uuid.NewString(), accountID, action, params)
}

func (s *Store) CreateWithID(ctx context.Context, jobID, accountID, action string, params map[string]string) (*db.Job, error) {
	job := &db.Job{
		ID:        jobID,
		AccountID: accountID,
		Action:    action,
		Status:    jobstatus.Created,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(job).Error; err != nil {
			return err
		}
		if err := upsertParams(ctx, tx, jobID, params); err != nil {
			return err
		}
		return tx.First(job, "id = ?", jobID).Error
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Store) SetParams(ctx context.Context, jobID string, params map[string]string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return upsertParams(ctx, tx, jobID, params)
	})
}

func (s *Store) GetParam(ctx context.Context, jobID, key string) (string, bool, error) {
	var param db.JobParam
	result := s.db.WithContext(ctx).
		Where("job_id = ? AND key = ?", jobID, key).
		Limit(1).
		Find(&param)
	if result.Error != nil {
		return "", false, result.Error
	}
	if result.RowsAffected == 0 {
		return "", false, nil
	}
	return param.Value, true, nil
}

func (s *Store) DeleteParam(ctx context.Context, jobID, key string) error {
	return s.db.WithContext(ctx).Delete(&db.JobParam{}, "job_id = ? AND key = ?", jobID, key).Error
}

func (s *Store) Update(ctx context.Context, jobID, statusValue, errorMessage string, result any) {
	updates := map[string]any{
		"status":        statusValue,
		"recoverable":   statusValue == jobstatus.FailedRecoverable,
		"retryable":     statusValue == jobstatus.FailedRetryable,
		"error_message": errorMessage,
	}
	if result != nil {
		if b, err := json.Marshal(result); err == nil {
			updates["result_json"] = string(b)
		}
	}
	if err := s.db.WithContext(ctx).Model(&db.Job{}).Where("id = ?", jobID).Updates(updates).Error; err != nil {
		log.Printf("[orchestrator] update job failed job=%s: %v", jobID, err)
	}
}

func (s *Store) Get(ctx context.Context, jobID string) (*db.Job, error) {
	var job db.Job
	if err := s.db.WithContext(ctx).First(&job, "id = ?", jobID).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *Store) List(ctx context.Context, filter ListFilter) ([]db.Job, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	query := s.db.WithContext(ctx).Model(&db.Job{})
	if value := strings.TrimSpace(filter.Status); value != "" {
		query = query.Where("status = ?", value)
	}
	if value := strings.TrimSpace(filter.Action); value != "" {
		query = query.Where("action = ?", value)
	}
	if value := strings.TrimSpace(filter.AccountID); value != "" {
		query = query.Where("account_id = ?", value)
	}

	var jobs []db.Job
	if err := query.Order("updated_at DESC").Limit(limit).Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Store) Steps(ctx context.Context, jobID string) ([]db.JobStep, error) {
	var steps []db.JobStep
	if err := s.db.WithContext(ctx).Where("job_id = ?", jobID).Order("started_at ASC, step_name ASC").Find(&steps).Error; err != nil {
		return nil, err
	}
	return steps, nil
}

func (s *Store) GetSnapshot(ctx context.Context, jobID string) (*pb.JobSnapshot, error) {
	job, err := s.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	steps, err := s.Steps(ctx, jobID)
	if err != nil {
		return nil, err
	}
	return BuildSnapshot(job, steps), nil
}

func (s *Store) ListSnapshots(ctx context.Context, filter ListFilter) ([]*pb.JobSnapshot, error) {
	jobs, err := s.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return []*pb.JobSnapshot{}, nil
	}

	jobIDs := make([]string, 0, len(jobs))
	for i := range jobs {
		jobIDs = append(jobIDs, jobs[i].ID)
	}

	var steps []db.JobStep
	if err := s.db.WithContext(ctx).
		Where("job_id IN ?", jobIDs).
		Order("started_at ASC, step_name ASC").
		Find(&steps).Error; err != nil {
		return nil, err
	}

	stepsByJob := make(map[string][]db.JobStep, len(jobs))
	for i := range steps {
		stepsByJob[steps[i].JobID] = append(stepsByJob[steps[i].JobID], steps[i])
	}

	snapshots := make([]*pb.JobSnapshot, 0, len(jobs))
	for i := range jobs {
		job := jobs[i]
		snapshots = append(snapshots, BuildSnapshot(&job, stepsByJob[job.ID]))
	}
	return snapshots, nil
}

func (s *Store) RunAtomicStep(ctx context.Context, jobID, stepName string, recoverable bool, retryable bool, fn func() (any, error)) (any, error) {
	if err := s.StartStep(ctx, jobID, stepName, recoverable, retryable); err != nil {
		return nil, err
	}

	result, stepErr := fn()
	return result, s.CompleteStep(ctx, jobID, stepName, recoverable, retryable, result, stepErr)
}

func (s *Store) StartStep(ctx context.Context, jobID, stepName string, recoverable bool, retryable bool) error {
	startedAt := time.Now().Unix()
	start := db.JobStep{
		JobID:       jobID,
		StepName:    stepName,
		Status:      jobstatus.Running,
		Recoverable: recoverable,
		Retryable:   retryable,
		StartedAt:   startedAt,
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "job_id"}, {Name: "step_name"}},
			DoUpdates: clause.Assignments(map[string]any{
				"status":        jobstatus.Running,
				"recoverable":   recoverable,
				"retryable":     retryable,
				"error_message": "",
				"result_json":   "",
				"started_at":    startedAt,
				"completed_at":  int64(0),
			}),
		}).Create(&start).Error; err != nil {
			return err
		}
		return tx.Model(&db.Job{}).Where("id = ?", jobID).Updates(map[string]any{
			"status":        jobstatus.Running,
			"recoverable":   false,
			"retryable":     false,
			"last_step":     stepName,
			"error_message": "",
		}).Error
	})
}

func (s *Store) CompleteStep(ctx context.Context, jobID, stepName string, recoverable bool, retryable bool, result any, stepErr error) error {
	completedAt := time.Now().Unix()
	statusValue := jobstatus.Succeeded
	errorMessage := ""
	if stepErr != nil {
		statusValue = jobstatus.Failed(recoverable, retryable)
		errorMessage = stepErr.Error()
	}

	updates := map[string]any{
		"status":        statusValue,
		"recoverable":   recoverable,
		"retryable":     retryable,
		"error_message": errorMessage,
		"result_json":   MarshalStepResult(jobID, stepName, result),
		"completed_at":  completedAt,
	}
	if err := s.db.WithContext(ctx).Model(&db.JobStep{}).
		Where("job_id = ? AND step_name = ?", jobID, stepName).
		Updates(updates).Error; err != nil {
		log.Printf("[orchestrator] update step failed job=%s step=%s: %v", jobID, stepName, err)
	}

	if stepErr == nil {
		return nil
	}
	if err := s.db.WithContext(ctx).Model(&db.Job{}).Where("id = ?", jobID).Updates(map[string]any{
		"status":        statusValue,
		"recoverable":   recoverable,
		"retryable":     retryable,
		"last_step":     stepName,
		"error_message": errorMessage,
	}).Error; err != nil {
		log.Printf("[orchestrator] update failed job failed job=%s step=%s: %v", jobID, stepName, err)
	}
	return stepErr
}

func (s *Store) UpdateRunningStepData(ctx context.Context, jobID, stepName string, result any) {
	resultJSON := MarshalStepResult(jobID, stepName, result)
	if resultJSON == "" {
		return
	}
	if err := s.db.WithContext(ctx).Model(&db.JobStep{}).
		Where("job_id = ? AND step_name = ? AND status = ?", jobID, stepName, jobstatus.Running).
		Update("result_json", resultJSON).Error; err != nil {
		log.Printf("[orchestrator] update running step data failed job=%s step=%s: %v", jobID, stepName, err)
	}
}

func (s *Store) MarkStepFailed(ctx context.Context, input StepFailure) error {
	now := time.Now().Unix()
	step := db.JobStep{
		JobID:        input.JobID,
		StepName:     input.StepName,
		Status:       input.Status,
		Recoverable:  input.Recoverable,
		Retryable:    input.Retryable,
		ErrorMessage: input.ErrorMessage,
		CompletedAt:  now,
	}
	updates := map[string]any{
		"status":        input.Status,
		"recoverable":   input.Recoverable,
		"retryable":     input.Retryable,
		"error_message": input.ErrorMessage,
		"completed_at":  now,
	}
	if input.Result != nil {
		if resultJSON := MarshalStepResult(input.JobID, input.StepName, input.Result); resultJSON != "" {
			updates["result_json"] = resultJSON
		}
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "job_id"}, {Name: "step_name"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&step).Error
}

func ToProto(job *db.Job, steps []db.JobStep) *pb.Job {
	if job == nil {
		return nil
	}
	out := &pb.Job{
		JobId:        job.ID,
		AccountId:    job.AccountID,
		Action:       job.Action,
		Status:       job.Status,
		Recoverable:  job.Recoverable,
		Retryable:    job.Retryable,
		LastStep:     job.LastStep,
		ErrorMessage: job.ErrorMessage,
		ResultJson:   job.ResultJSON,
		CreatedAt:    job.CreatedAt,
		UpdatedAt:    job.UpdatedAt,
		Steps:        make([]*pb.JobStep, 0, len(steps)),
	}
	for i := range steps {
		out.Steps = append(out.Steps, &pb.JobStep{
			StepName:     steps[i].StepName,
			Status:       steps[i].Status,
			Recoverable:  steps[i].Recoverable,
			Retryable:    steps[i].Retryable,
			ErrorMessage: steps[i].ErrorMessage,
			ResultJson:   steps[i].ResultJSON,
			StartedAt:    steps[i].StartedAt,
			CompletedAt:  steps[i].CompletedAt,
		})
	}
	return out
}

func MarshalStepResult(jobID, stepName string, result any) string {
	if result == nil {
		return ""
	}
	b, err := json.Marshal(result)
	if err != nil {
		log.Printf("[orchestrator] marshal step result failed job=%s step=%s: %v", jobID, stepName, err)
		return ""
	}
	return string(b)
}

func BuildSnapshot(job *db.Job, steps []db.JobStep) *pb.JobSnapshot {
	if job == nil {
		return nil
	}
	progress := ProgressFromJob(job)
	return &pb.JobSnapshot{
		Job:      ToProto(job, steps),
		Progress: progress,
		EventId:  eventID(job, steps, progress),
	}
}

func ApplyProgress(snapshot *pb.JobSnapshot, progress *pb.WorkflowProgress) {
	if snapshot == nil || progress == nil {
		return
	}
	snapshot.Progress = progress
	if progress.GetUpdatedAtUnix() > snapshot.GetEventId() {
		snapshot.EventId = progress.GetUpdatedAtUnix()
	}
}

func ProgressFromJob(job *db.Job) *pb.WorkflowProgress {
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

func eventID(job *db.Job, steps []db.JobStep, progress *pb.WorkflowProgress) int64 {
	var id int64
	if job != nil {
		id = job.UpdatedAt
	}
	for i := range steps {
		if steps[i].UpdatedAt > id {
			id = steps[i].UpdatedAt
		}
	}
	if progress != nil && progress.GetUpdatedAtUnix() > id {
		id = progress.GetUpdatedAtUnix()
	}
	return id
}

func upsertParams(ctx context.Context, tx *gorm.DB, jobID string, params map[string]string) error {
	if len(params) == 0 {
		return nil
	}

	rows := make([]db.JobParam, 0, len(params))
	for key, value := range params {
		key = strings.TrimSpace(key)
		if key == "" || value == "" {
			continue
		}
		rows = append(rows, db.JobParam{JobID: jobID, Key: key, Value: value})
	}
	if len(rows) == 0 {
		return nil
	}

	return tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "job_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&rows).Error
}
