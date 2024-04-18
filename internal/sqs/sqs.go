package sqs

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sns"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/aws/aws-sdk-go/service/sts"
	"golang.org/x/sync/errgroup"
)

const (
	nexradArchiveTopicARN = "arn:aws:sns:us-east-1:811054952067:NewNEXRADLevel2Archive"
	nexradChunkTopicARN   = "arn:aws:sns:us-east-1:684042711724:NewNEXRADLevel2ObjectFilterable"
)

type Listener struct {
	eventChan                    chan events.Event
	archiveSites                 []string
	chunkSites                   []string
	awsSession                   *session.Session
	awsSqs                       *sqs.SQS
	awsSns                       *sns.SNS
	awsSts                       *sts.STS
	archiveQueueName             string
	archiveQueueURL              string
	chunkQueueName               string
	chunkQueueURL                string
	nexradChunkSubscriptionARN   string
	nexradArchiveSubscriptionARN string
	running                      bool
}

func (l *Listener) ensureChunkQueue() error {
	resp, err := l.awsSqs.GetQueueUrl(&sqs.GetQueueUrlInput{
		QueueName: aws.String(l.chunkQueueName),
	})
	if err != nil {
		// Queue does not exist, create it
		resp, err := l.awsSqs.CreateQueue(&sqs.CreateQueueInput{
			QueueName: aws.String(l.chunkQueueName),
		})
		if err != nil {
			return err
		}
		l.chunkQueueURL = *resp.QueueUrl
	} else {
		l.chunkQueueURL = *resp.QueueUrl
	}
	return nil
}

func (l *Listener) ensureArchiveQueue() error {
	resp, err := l.awsSqs.GetQueueUrl(&sqs.GetQueueUrlInput{
		QueueName: aws.String(l.archiveQueueName),
	})
	if err != nil {
		// Queue does not exist, create it
		resp, err := l.awsSqs.CreateQueue(&sqs.CreateQueueInput{
			QueueName: aws.String(l.archiveQueueName),
		})
		if err != nil {
			return err
		}
		l.archiveQueueURL = *resp.QueueUrl
	} else {
		l.archiveQueueURL = *resp.QueueUrl
	}
	return nil
}

func (l *Listener) ensureArchiveSubscription() error {
	callerID, err := l.awsSts.GetCallerIdentity(&sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}
	sqsARN := fmt.Sprintf("arn:aws:sqs:us-east-1:%s:%s", *callerID.Account, l.archiveQueueName)

	subs, err := l.awsSns.Subscribe(&sns.SubscribeInput{
		Protocol:              aws.String("sqs"),
		TopicArn:              aws.String(nexradArchiveTopicARN),
		Endpoint:              aws.String(sqsARN),
		ReturnSubscriptionArn: aws.Bool(true),
	})
	if err != nil {
		return err
	}
	l.nexradArchiveSubscriptionARN = *subs.SubscriptionArn
	_, err = l.awsSqs.SetQueueAttributes(&sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(l.archiveQueueURL),
		Attributes: map[string]*string{
			"Policy": aws.String(fmt.Sprintf(`{
				"Version": "2012-10-17",
				"Statement": [
					{
						"Effect": "Allow",
						"Principal": {
							"AWS": "*"
						},
						"Action": "sqs:SendMessage",
						"Resource": "%s",
						"Condition": {
							"ArnLike": {
								"aws:SourceArn": "%s"
							}
						}
					}
				]
			}`, sqsARN, nexradArchiveTopicARN)),
		},
	})
	return err
}

func (l *Listener) updateFilterPolicy() error {
	var sites []string
	for _, site := range l.chunkSites {
		if !slices.Contains(sites, site) {
			sites = append(sites, site)
		}
	}

	if len(sites) == 0 {
		sites = []string{"nonsense"}
	}
	jsonSites, err := json.Marshal(sites)
	if err != nil {
		return err
	}

	filterPolicy := fmt.Sprintf(`{
		"SiteID": %s
	}`, jsonSites)

	_, err = l.awsSns.SetSubscriptionAttributes(&sns.SetSubscriptionAttributesInput{
		SubscriptionArn: aws.String(l.nexradChunkSubscriptionARN),
		AttributeName:   aws.String("FilterPolicy"),
		AttributeValue:  aws.String(filterPolicy),
	})

	return err
}

func (l *Listener) ensureChunkSubscription() error {
	callerID, err := l.awsSts.GetCallerIdentity(&sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}
	sqsARN := fmt.Sprintf("arn:aws:sqs:us-east-1:%s:%s", *callerID.Account, l.chunkQueueName)

	subs, err := l.awsSns.Subscribe(&sns.SubscribeInput{
		Protocol:              aws.String("sqs"),
		TopicArn:              aws.String(nexradChunkTopicARN),
		Endpoint:              aws.String(sqsARN),
		ReturnSubscriptionArn: aws.Bool(true),
		Attributes: map[string]*string{
			"FilterPolicy": aws.String(`{
				"SiteID": ["nonsense"]
			}`),
		},
	})
	if err != nil {
		return err
	}
	l.nexradChunkSubscriptionARN = *subs.SubscriptionArn
	_, err = l.awsSqs.SetQueueAttributes(&sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(l.chunkQueueURL),
		Attributes: map[string]*string{
			"Policy": aws.String(fmt.Sprintf(`{
				"Version": "2012-10-17",
				"Statement": [
					{
						"Effect": "Allow",
						"Principal": {
							"AWS": "*"
						},
						"Action": "sqs:SendMessage",
						"Resource": "%s",
						"Condition": {
							"ArnLike": {
								"aws:SourceArn": "%s"
							}
						}
					}
				]
			}`, sqsARN, nexradChunkTopicARN)),
		},
	})
	return err
}

func (l *Listener) destroyArchiveSubscription() error {
	if l.nexradArchiveSubscriptionARN == "" {
		return nil
	}
	_, err := l.awsSns.Unsubscribe(&sns.UnsubscribeInput{
		SubscriptionArn: aws.String(l.nexradArchiveSubscriptionARN),
	})
	return err
}

func (l *Listener) destroyChunkSubscription() error {
	if l.nexradChunkSubscriptionARN == "" {
		return nil
	}
	_, err := l.awsSns.Unsubscribe(&sns.UnsubscribeInput{
		SubscriptionArn: aws.String(l.nexradChunkSubscriptionARN),
	})
	return err
}

func (l *Listener) destroyArchiveQueue() error {
	if l.archiveQueueURL == "" {
		return nil
	}
	_, err := l.awsSqs.DeleteQueue(&sqs.DeleteQueueInput{
		QueueUrl: aws.String(l.archiveQueueURL),
	})
	return err
}

func (l *Listener) destroyChunkQueue() error {
	if l.chunkQueueURL == "" {
		return nil
	}
	_, err := l.awsSqs.DeleteQueue(&sqs.DeleteQueueInput{
		QueueUrl: aws.String(l.chunkQueueURL),
	})
	return err
}

func NewListener(eventChan chan events.Event) (*Listener, error) {
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))
	config := aws.NewConfig().WithRegion("us-east-1")
	svc := sqs.New(sess, config)
	snsSvc := sns.New(sess, config)
	stsSvc := sts.New(sess, config)

	listener := &Listener{
		eventChan:        eventChan,
		archiveSites:     []string{},
		chunkSites:       []string{},
		awsSession:       sess,
		awsSqs:           svc,
		awsSns:           snsSvc,
		awsSts:           stsSvc,
		archiveQueueName: fmt.Sprintf("nexrad-aws-notifier-events-archive-%d", time.Now().Unix()),
		chunkQueueName:   fmt.Sprintf("nexrad-aws-notifier-events-chunk-%d", time.Now().Unix()),
		running:          true,
	}

	err := listener.ensureArchiveQueue()
	if err != nil {
		return nil, err
	}

	err = listener.ensureChunkQueue()
	if err != nil {
		_ = listener.destroyArchiveQueue()
		return nil, err
	}

	err = listener.ensureArchiveSubscription()
	if err != nil {
		_ = listener.destroyArchiveQueue()
		_ = listener.destroyChunkQueue()
		return nil, err
	}

	err = listener.ensureChunkSubscription()
	if err != nil {
		_ = listener.destroyArchiveQueue()
		_ = listener.destroyChunkQueue()
		_ = listener.destroyArchiveSubscription()
		return nil, err
	}

	go listener.runArchive()
	go listener.runChunk()

	return listener, nil
}

func (l *Listener) ListenChunk(station string) error {
	station = strings.ToUpper(station)
	if !slices.Contains(l.chunkSites, station) {
		l.chunkSites = append(l.chunkSites, station)
	}
	return l.updateFilterPolicy()
}

func (l *Listener) ListenArchive(station string) error {
	station = strings.ToUpper(station)
	if !slices.Contains(l.archiveSites, station) {
		l.archiveSites = append(l.archiveSites, station)
	}
	return l.updateFilterPolicy()
}

func (l *Listener) runArchive() {
	// Loop and poll the SQS queue
	for l.running {
		resp, err := l.awsSqs.ReceiveMessage(&sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(l.archiveQueueURL),
			MaxNumberOfMessages: aws.Int64(10),
			WaitTimeSeconds:     aws.Int64(20),
		})
		// Break early since it's likely the listener will stop
		// while waiting for messages
		if !l.running {
			break
		}
		if err != nil {
			slog.Warn("Error receiving message:", "error", err)
			continue
		}
		for _, msg := range resp.Messages {
			// Delete the message
			_, err := l.awsSqs.DeleteMessage(&sqs.DeleteMessageInput{
				QueueUrl:      aws.String(l.archiveQueueURL),
				ReceiptHandle: msg.ReceiptHandle,
			})
			if err != nil {
				slog.Warn("Error deleting message:", "error", err)
			}
			go l.onArchiveMessage(msg)
		}
	}
}

func (l *Listener) runChunk() {
	// Loop and poll the SQS queue
	for l.running {
		resp, err := l.awsSqs.ReceiveMessage(&sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(l.chunkQueueURL),
			MaxNumberOfMessages: aws.Int64(10),
			WaitTimeSeconds:     aws.Int64(20),
		})
		// Break early since it's likely the listener will stop
		// while waiting for messages
		if !l.running {
			break
		}
		if err != nil {
			slog.Warn("Error receiving message:", "error", err)
			continue
		}
		for _, msg := range resp.Messages {
			// Delete the message
			_, err := l.awsSqs.DeleteMessage(&sqs.DeleteMessageInput{
				QueueUrl:      aws.String(l.chunkQueueURL),
				ReceiptHandle: msg.ReceiptHandle,
			})
			if err != nil {
				slog.Warn("Error deleting message:", "error", err)
			}
			go l.onChunkMessage(msg)
		}
	}
}

func (l *Listener) onArchiveMessage(msg *sqs.Message) {
	var notification ArchiveNotification
	err := json.Unmarshal([]byte(*msg.Body), &notification)
	if err != nil {
		slog.Warn("Error unmarshalling message:", "error", err)
		return
	}
	var message ArchiveNotificationMessage
	err = json.Unmarshal([]byte(notification.Message), &message)
	if err != nil {
		slog.Warn("Error unmarshalling message:", "error", err)
		return
	}

	for _, record := range message.Records {
		// Key is yyyy/mm/dd/STATION/STATION_yyyymmdd_hhmmss_V06
		parts := strings.Split(record.S3.Object.Key, "/")
		if len(parts) < 4 {
			slog.Warn("Invalid key:", "key", record.S3.Object.Key)
			continue
		}
		station := parts[3]
		slog.Info("Received archive record", "station", station, "prefix", record.S3.Object.Key)

		l.eventChan <- events.NexradArchiveEvent{
			Station: station,
			Path:    record.S3.Object.Key,
		}
	}
}

func (l *Listener) onChunkMessage(msg *sqs.Message) {
	var notification ChunkNotification
	err := json.Unmarshal([]byte(*msg.Body), &notification)
	if err != nil {
		slog.Warn("Error unmarshalling message:", "error", err)
		return
	}
	site := notification.MessageAttributes["SiteID"].Value
	volume := notification.MessageAttributes["VolumeID"].Value
	chunk := notification.MessageAttributes["ChunkID"].Value
	l2Version := notification.MessageAttributes["L2Version"].Value
	chunkType := notification.MessageAttributes["ChunkType"].Value

	slog.Info("Received chunk record", "site", site, "volume", volume, "chunk", chunk, "chunkType", chunkType, "l2Version", l2Version)

	l.eventChan <- events.NexradChunkEvent{
		Station: site,
		Path:    fmt.Sprintf("%s/%s/%s/%s", site, volume, chunk, l2Version),
	}
}

func (l *Listener) Stop() error {
	l.running = false
	errGrp := errgroup.Group{}
	errGrp.SetLimit(2)
	errGrp.Go(func() error {
		return l.destroyChunkSubscription()
	})
	errGrp.Go(func() error {
		return l.destroyArchiveSubscription()
	})
	errGrp.Go(func() error {
		return l.destroyChunkQueue()
	})
	errGrp.Go(func() error {
		return l.destroyArchiveQueue()
	})
	return errGrp.Wait()
}
