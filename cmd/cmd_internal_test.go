package cmd

import (
	"errors"
	"net"
	"testing"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/sqs"
	"github.com/aws/smithy-go"
)

// The SQS queues and SNS subscriptions are created before the HTTP server
// binds. When the bind failed they were left behind in AWS, two queues and two
// subscriptions per attempt, which a crash loop multiplies.
func TestFailedStartTearsDownTheListener(t *testing.T) {
	t.Parallel()
	busy, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = busy.Close() })
	_, port, err := net.SplitHostPort(busy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	var listener *sqs.Listener
	cmd := newCommand("testing", "start-failure", func(ch chan events.Event) (*sqs.Listener, error) {
		l, err := sqs.NewListener(ch)
		listener = l
		return l, err
	})
	cmd.SetArgs([]string{"--http.port", port})
	if err := cmd.Execute(); err == nil {
		t.Fatal("start on a busy port should fail")
	}
	if listener == nil {
		t.Fatal("listener was never created")
	}

	// Subscribing again writes to the subscription the listener made, which
	// SNS only reports missing if the failed start tore it down.
	err = listener.ListenChunk(t.Context(), "KTLX")
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NotFound" {
		_ = listener.Stop()
		t.Fatalf("the listener's subscription outlived the failed start: %v", err)
	}
}
