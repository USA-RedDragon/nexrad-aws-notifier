package sqs

import (
	"encoding/json"
	"testing"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// chunkBody is a notification as the chunk topic delivered it to the queue,
// signature elided. VolumeID and ChunkID are numbers in the message, though
// strings in the attributes.
const chunkBody = `{
  "Type": "Notification",
  "MessageId": "32e4ee51-fd7a-5d65-a74f-8d138ce6e928",
  "TopicArn": "arn:aws:sns:us-east-1:684042711724:NewNEXRADLevel2ObjectFilterable",
  "Message": "{\"S3Bucket\": \"unidata-nexrad-level2-chunks\", \"Key\": \"KTLX/399/20261009-043037-048-I\", \"SiteID\": \"KTLX\", \"DateTime\": \"2026-10-09T04:30:37\", \"VolumeID\": 399, \"ChunkID\": 48, \"ChunkType\": \"I\", \"L2Version\": \"V06\"}",
  "Timestamp": "2026-10-09T04:36:44.969Z",
  "SignatureVersion": "1",
  "Signature": "elided",
  "SigningCertURL": "https://sns.us-east-1.amazonaws.com/SimpleNotificationService-1e59c4574facfe41babdb2d652f8ebef.pem",
  "UnsubscribeURL": "https://sns.us-east-1.amazonaws.com/?Action=Unsubscribe&SubscriptionArn=arn:aws:sns:us-east-1:684042711724:NewNEXRADLevel2ObjectFilterable:db63f231-16c0-4826-ae1c-c217c75b0b5d",
  "MessageAttributes": {
    "SiteID": {
      "Type": "String",
      "Value": "KTLX"
    },
    "VolumeID": {
      "Type": "Number",
      "Value": "399"
    },
    "ChunkID": {
      "Type": "Number",
      "Value": "48"
    },
    "ChunkType": {
      "Type": "String",
      "Value": "I"
    },
    "L2Version": {
      "Type": "String",
      "Value": "V06"
    },
    "DateTime": {
      "Type": "String",
      "Value": "2026-10-09T04:30:37"
    }
  }
}`

// The message's VolumeID and ChunkID were decoded as strings, so every chunk
// logged "Error unmarshalling chunk message".
func TestChunkMessageDecodes(t *testing.T) {
	t.Parallel()
	var notification ChunkNotification
	if err := json.Unmarshal([]byte(chunkBody), &notification); err != nil {
		t.Fatal(err)
	}
	var message ChunkNotificationMessage
	if err := json.Unmarshal([]byte(notification.Message), &message); err != nil {
		t.Fatalf("real chunk message does not decode: %v", err)
	}
	want := ChunkNotificationMessage{
		S3Bucket:  "unidata-nexrad-level2-chunks",
		Key:       "KTLX/399/20261009-043037-048-I",
		SiteID:    "KTLX",
		DateTime:  "2026-10-09T04:30:37",
		VolumeID:  "399",
		ChunkID:   "48",
		ChunkType: "I",
		L2Version: "V06",
	}
	if message != want {
		t.Errorf("got  %+v\nwant %+v", message, want)
	}
}

func TestChunkMessageBecomesAnEvent(t *testing.T) {
	t.Parallel()
	eventChan := make(chan events.Event, 1)
	l := &Listener{eventChan: eventChan, done: make(chan struct{})}

	l.onChunkMessage(chunkMessage(chunkBody))

	want := events.NexradChunkEvent{
		Station:   "KTLX",
		Volume:    "399",
		Chunk:     "48",
		ChunkType: "I",
		L2Version: "V06",
		Name:      "20261009-043037-048-I",
		Path:      "KTLX/399/20261009-043037-048-I",
	}
	select {
	case got := <-eventChan:
		if got != events.Event(want) {
			t.Errorf("got  %+v\nwant %+v", got, want)
		}
	default:
		t.Fatal("no event emitted")
	}
}

func chunkMessage(body string) types.Message {
	return types.Message{Body: aws.String(body)}
}
