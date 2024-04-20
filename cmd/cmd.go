package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/config"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/server"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/sqs"
	"github.com/spf13/cobra"
	"github.com/ztrue/shutdown"
	"golang.org/x/sync/errgroup"
)

func NewCommand(version, commit string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "nexrad-aws-notifier",
		Version: fmt.Sprintf("%s - %s", version, commit),
		Annotations: map[string]string{
			"version": version,
			"commit":  commit,
		},
		RunE:          run,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	config.RegisterFlags(cmd)
	return cmd
}

func run(cmd *cobra.Command, _ []string) error {
	slog.Info("nexrad-aws-notifier", "version", cmd.Annotations["version"], "commit", cmd.Annotations["commit"])

	config, err := config.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize the websocket event bus
	eventBus := events.NewEventBus()
	slog.Info("Event bus started")

	sqsListener, err := sqs.NewListener(eventBus.GetChannel())
	if err != nil {
		return fmt.Errorf("failed to create SQS listener: %w", err)
	}
	slog.Info("SQS listener started")

	slog.Info("Starting HTTP server")
	server := server.NewServer(&config.HTTP, eventBus.GetChannel(), sqsListener)
	err = server.Start()
	if err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	stop := func(sig os.Signal) {
		slog.Info("Shutting down")

		errGrp := errgroup.Group{}

		if server != nil {
			errGrp.Go(func() error {
				return server.Stop()
			})
		}

		errGrp.Go(func() error {
			return sqsListener.Stop()
		})

		errGrp.Go(func() error {
			eventBus.Close()
			return nil
		})

		err := errGrp.Wait()
		if err != nil {
			slog.Error("Shutdown error", "error", err.Error())
			os.Exit(1)
		}
		slog.Info("Shutdown complete")
	}

	shutdown.AddWithParam(stop)

	if cmd.Annotations["version"] == "testing" {
		go func() {
			slog.Info("Sleeping for 5 seconds")
			time.Sleep(5 * time.Second)
			slog.Info("Sending SIGTERM")
			err = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
			if err != nil {
				slog.Error("Failed to send SIGTERM", "error", err.Error())
			}
		}()
	}

	shutdown.Listen(syscall.SIGINT, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT)

	return nil
}
