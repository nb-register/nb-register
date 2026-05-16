package app

import (
	"log"
	"net"

	temporalworker "go.temporal.io/sdk/worker"
	"google.golang.org/grpc"
	"orchestrator/pb"
)

func Run() {
	log.Println("Initializing Orchestrator API...")

	cfg := loadOrchestratorConfig()
	deps, err := newOrchestratorDependencies(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize orchestrator dependencies: %v", err)
	}
	defer deps.Close()

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	server := newOrchestratorServer(cfg, deps)

	temporalWorker := temporalworker.New(deps.temporal, server.taskQueue, temporalworker.Options{})
	registerTemporalWorker(temporalWorker, server)
	go func() {
		if err := temporalWorker.Run(temporalworker.InterruptCh()); err != nil {
			log.Fatalf("Temporal worker failed: %v", err)
		}
	}()

	grpcServer := grpc.NewServer()
	pb.RegisterOrchestratorServiceServer(grpcServer, server)

	log.Printf("Orchestrator gRPC API listening on %s", cfg.ListenAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
