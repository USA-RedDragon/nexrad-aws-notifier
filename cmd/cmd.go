package cmd

import (
	"context"
	"errors"
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
	return newCommand(version, commit, sqs.NewListener)
}

type listenerFactory func(chan events.Event) (*sqs.Listener, error)

func newCommand(version, commit string, newListener listenerFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "nexrad-aws-notifier",
		Version: fmt.Sprintf("%s - %s", version, commit),
		Annotations: map[string]string{
			"version": version,
			"commit":  commit,
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	loader := config.New(cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg, err := loader.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		return run(cmd, cfg, newListener)
	}
	return cmd
}

// tracingShutdownTimeout bounds the final span flush on exit.
const tracingShutdownTimeout = 5 * time.Second

func run(cmd *cobra.Command, config *config.Config, newListener listenerFactory) error {
	slog.Info("nexrad-aws-notifier", "version", cmd.Annotations["version"], "commit", cmd.Annotations["commit"])

	shutdownTracing, err := setupTracing(cmd.Context(), config, cmd.Annotations["version"])
	if err != nil {
		return fmt.Errorf("failed to set up tracing: %w", err)
	}
	stopTracing := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), tracingShutdownTimeout)
		defer cancel()
		if err := shutdownTracing(ctx); err != nil {
			return fmt.Errorf("failed to shut down tracing: %w", err)
		}
		return nil
	}

	// Initialize the websocket event bus
	eventBus := events.NewEventBus()
	slog.Info("Event bus started")

	eventChannel := eventBus.GetChannel()
	sqsListener, err := newListener(eventChannel)
	if err != nil {
		return errors.Join(fmt.Errorf("failed to create SQS listener: %w", err), stopTracing())
	}
	slog.Info("SQS listener started")

	slog.Info("Starting HTTP server")
	server := server.NewServer(&config.HTTP, eventChannel, sqsListener)

	teardown := func() error {
		errGrp := errgroup.Group{}

		errGrp.Go(func() error {
			return server.Stop()
		})

		errGrp.Go(func() error {
			return sqsListener.Stop()
		})

		err := errGrp.Wait()
		// We always want to close the event channel before exiting
		close(eventChannel)
		// Last, so spans from the shutdown itself are flushed too.
		return errors.Join(err, stopTracing())
	}

	err = server.Start(cmd.Context())
	if err != nil {
		return errors.Join(fmt.Errorf("failed to start HTTP server: %w", err), teardown())
	}

	stop := func(_ os.Signal) {
		slog.Info("Shutting down")

		err := teardown()
		if err != nil {
			slog.Error("Shutdown error", "error", err.Error())
			os.Exit(1)
		}
		slog.Info("Shutdown complete")
	}

	if cmd.Annotations["version"] == "testing" {
		doneChannel := make(chan struct{})
		go func() {
			slog.Info("Sleeping for 5 seconds")
			time.Sleep(5 * time.Second)
			slog.Info("Sending SIGTERM")
			stop(syscall.SIGTERM)
			doneChannel <- struct{}{}
		}()
		<-doneChannel
	} else {
		shutdown.AddWithParam(stop)
		shutdown.Listen(syscall.SIGINT, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGQUIT)
	}

	return nil
}
