package main

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"outlookimapservice/pb"
)

type EmailService struct {
	pb.UnimplementedEmailServiceServer
	store   *MailboxStore
	watcher *MailWatcher
}

func (s *EmailService) GetEmail(ctx context.Context, request *pb.GetEmailRequest) (*pb.GetEmailResponse, error) {
	mailbox, err := s.store.AcquireEmail(ctx, request.GetExcludeEmailAddresses())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &pb.GetEmailResponse{
		EmailAddress: mailbox.GetEmailAddress(),
		Password:     mailbox.GetPassword(),
		Mailbox:      mailbox,
	}, nil
}

func (s *EmailService) MarkEmailStatus(ctx context.Context, request *pb.MarkEmailStatusRequest) (*pb.MarkEmailStatusResponse, error) {
	mailbox, err := s.store.MarkEmailStatus(ctx, request.GetEmailAddress(), request.GetStatus(), request.GetLastError())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &pb.MarkEmailStatusResponse{Mailbox: mailbox}, nil
}

func (s *EmailService) UpsertMailbox(ctx context.Context, request *pb.UpsertEmailMailboxRequest) (*pb.UpsertEmailMailboxResponse, error) {
	mailbox, err := s.store.UpsertMailbox(ctx, request.GetMailbox())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pb.UpsertEmailMailboxResponse{Mailbox: mailbox}, nil
}

func (s *EmailService) ListMailboxes(ctx context.Context, request *pb.ListEmailMailboxesRequest) (*pb.ListEmailMailboxesResponse, error) {
	mailboxes, err := s.store.ListMailboxes(ctx, request.GetStatus(), request.GetLimit())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.ListEmailMailboxesResponse{Mailboxes: mailboxes}, nil
}

func (s *EmailService) WaitForEmail(ctx context.Context, request *pb.WaitForEmailRequest) (*pb.WaitForEmailResponse, error) {
	timeoutSeconds := request.GetTimeoutSeconds()
	if timeoutSeconds <= 0 {
		timeoutSeconds = 300
	}
	issuedAfter := float64(request.GetIssuedAfterUnix())
	if otp, ok := s.watcher.ConsumeCachedOTP(request.GetEmailAddress(), request.GetSubjectKeyword(), issuedAfter); ok {
		return &pb.WaitForEmailResponse{Found: true, ContentExtracted: otp}, nil
	}
	if err := s.watcher.PollForEmail(ctx, request.GetEmailAddress()); err != nil {
		return nil, waitError(ctx, err)
	}
	if otp, ok := s.watcher.ConsumeCachedOTP(request.GetEmailAddress(), request.GetSubjectKeyword(), issuedAfter); ok {
		return &pb.WaitForEmailResponse{Found: true, ContentExtracted: otp}, nil
	}

	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	for time.Now().Before(deadline) {
		sleepFor := time.Duration(s.watcher.pollInterval) * time.Second
		if remaining := time.Until(deadline); remaining < sleepFor {
			sleepFor = remaining
		}
		if sleepFor > 0 {
			timer := time.NewTimer(sleepFor)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, status.Error(codes.Canceled, "request cancelled")
			case <-timer.C:
			}
		}
		if err := s.watcher.PollForEmail(ctx, request.GetEmailAddress()); err != nil {
			return nil, waitError(ctx, err)
		}
		if otp, ok := s.watcher.ConsumeCachedOTP(request.GetEmailAddress(), request.GetSubjectKeyword(), issuedAfter); ok {
			return &pb.WaitForEmailResponse{Found: true, ContentExtracted: otp}, nil
		}
	}
	return &pb.WaitForEmailResponse{Found: false}, nil
}

func waitError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return status.Error(codes.Canceled, "request cancelled")
	}
	return status.Error(codes.Internal, err.Error())
}
