package sqs

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func archiveMessage(t *testing.T, keys ...string) types.Message {
	t.Helper()
	var message ArchiveNotificationMessage
	for _, key := range keys {
		var record ArchiveNotificationRecord
		record.S3.Object.Key = key
		message.Records = append(message.Records, record)
	}
	inner, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(ArchiveNotification{Message: string(inner)})
	if err != nil {
		t.Fatal(err)
	}
	return types.Message{Body: aws.String(string(body))}
}

// The caller closes the event channel as soon as Stop returns. A handler still
// holding a message used to send into it afterwards and panic the process, so
// Stop has to wait for every handler it started.
func TestStopWaitsForMessageHandlers(t *testing.T) {
	t.Parallel()
	eventChan := make(chan events.Event)
	done := make(chan struct{})
	l := &Listener{eventChan: eventChan, cancel: func() { close(done) }, done: done}
	l.running.Store(true)

	l.dispatch(l.onArchiveMessage, archiveMessage(t,
		"2026/09/07/KTLX/KTLX20260907_000000_V06",
		"2026/09/07/KTLX/KTLX20260907_000500_V06"))

	// The first record arriving means the handler is mid-message; give it a
	// moment to block sending the second.
	<-eventChan
	time.Sleep(50 * time.Millisecond)

	if err := l.Stop(); err != nil {
		t.Fatal(err)
	}
	close(eventChan)
	// Leave a stray send time to panic.
	time.Sleep(50 * time.Millisecond)
}
