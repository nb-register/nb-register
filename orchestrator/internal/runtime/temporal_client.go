package runtime

import (
	"context"
	"log"

	temporalclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
)

func (s *orchestratorServer) workflowOptions(workflowID string) temporalclient.StartWorkflowOptions {
	return temporalclient.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: s.taskQueue,
	}
}

func newTemporalClient(cfg orchestratorConfig) (temporalclient.Client, func(), error) {
	if cfg.TemporalDevServer {
		options := testsuite.DevServerOptions{
			CachedDownload: testsuite.CachedDownload{
				Version: cfg.TemporalDevServerVersion,
				DestDir: cfg.TemporalDevServerCache,
			},
			ClientOptions: &temporalclient.Options{
				Namespace: cfg.TemporalNamespace,
			},
			DBFilename: cfg.TemporalDevServerDB,
			EnableUI:   cfg.TemporalDevServerUI,
			UIPort:     cfg.TemporalDevServerUIPort,
			LogLevel:   cfg.TemporalDevServerLog,
		}
		server, err := testsuite.StartDevServer(context.Background(), options)
		if err != nil {
			return nil, nil, err
		}
		log.Printf("Temporal dev server listening on %s", server.FrontendHostPort())
		client := server.Client()
		return client, func() {
			client.Close()
			if err := server.Stop(); err != nil {
				log.Printf("Temporal dev server stop failed: %v", err)
			}
		}, nil
	}

	client, err := temporalclient.Dial(temporalclient.Options{
		HostPort:  cfg.TemporalAddr,
		Namespace: cfg.TemporalNamespace,
	})
	if err != nil {
		return nil, nil, err
	}
	return client, client.Close, nil
}
