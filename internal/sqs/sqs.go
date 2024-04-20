package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/google/uuid"
	"github.com/puzpuzpuz/xsync/v3"
	"golang.org/x/sync/errgroup"
)

const (
	nexradArchiveTopicARN = "arn:aws:sns:us-east-1:811054952067:NewNEXRADLevel2Archive"
	nexradChunkTopicARN   = "arn:aws:sns:us-east-1:684042711724:NewNEXRADLevel2ObjectFilterable"
)

type Listener struct {
	eventChan                    chan events.Event
	archiveSites                 *xsync.MapOf[string, uint]
	chunkSites                   *xsync.MapOf[string, uint]
	awsSqs                       *sqs.Client
	awsSns                       *sns.Client
	awsSts                       *sts.Client
	archiveQueueName             string
	archiveQueueURL              string
	chunkQueueName               string
	chunkQueueURL                string
	nexradChunkSubscriptionARN   string
	nexradArchiveSubscriptionARN string
	running                      bool
}

func (l *Listener) ensureChunkQueue() error {
	resp, err := l.awsSqs.GetQueueUrl(context.TODO(), &sqs.GetQueueUrlInput{
		QueueName: aws.String(l.chunkQueueName),
	})
	if err != nil {
		// Queue does not exist, create it
		resp, err := l.awsSqs.CreateQueue(context.TODO(), &sqs.CreateQueueInput{
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
	resp, err := l.awsSqs.GetQueueUrl(context.TODO(), &sqs.GetQueueUrlInput{
		QueueName: aws.String(l.archiveQueueName),
	})
	if err != nil {
		// Queue does not exist, create it
		resp, err := l.awsSqs.CreateQueue(context.TODO(), &sqs.CreateQueueInput{
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
	callerID, err := l.awsSts.GetCallerIdentity(context.TODO(), &sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}
	sqsARN := fmt.Sprintf("arn:aws:sqs:us-east-1:%s:%s", *callerID.Account, l.archiveQueueName)

	subs, err := l.awsSns.Subscribe(context.TODO(), &sns.SubscribeInput{
		Protocol:              aws.String("sqs"),
		TopicArn:              aws.String(nexradArchiveTopicARN),
		Endpoint:              aws.String(sqsARN),
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		return err
	}
	l.nexradArchiveSubscriptionARN = *subs.SubscriptionArn
	_, err = l.awsSqs.SetQueueAttributes(context.TODO(), &sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(l.archiveQueueURL),
		Attributes: map[string]string{
			"Policy": fmt.Sprintf(`{
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
			}`, sqsARN, nexradArchiveTopicARN),
		},
	})
	return err
}

func (l *Listener) updateFilterPolicy(ctx context.Context) error {
	var sites []string
	l.chunkSites.Range(func(key string, val uint) bool {
		if !slices.Contains(sites, key) && val > 0 {
			sites = append(sites, key)
		}
		return true
	})

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

	_, err = l.awsSns.SetSubscriptionAttributes(ctx, &sns.SetSubscriptionAttributesInput{
		SubscriptionArn: aws.String(l.nexradChunkSubscriptionARN),
		AttributeName:   aws.String("FilterPolicy"),
		AttributeValue:  aws.String(filterPolicy),
	})

	return err
}

func (l *Listener) ensureChunkSubscription() error {
	callerID, err := l.awsSts.GetCallerIdentity(context.TODO(), &sts.GetCallerIdentityInput{})
	if err != nil {
		return err
	}
	sqsARN := fmt.Sprintf("arn:aws:sqs:us-east-1:%s:%s", *callerID.Account, l.chunkQueueName)

	subs, err := l.awsSns.Subscribe(context.TODO(), &sns.SubscribeInput{
		Protocol:              aws.String("sqs"),
		TopicArn:              aws.String(nexradChunkTopicARN),
		Endpoint:              aws.String(sqsARN),
		ReturnSubscriptionArn: true,
		Attributes: map[string]string{
			"FilterPolicy": `{
				"SiteID": ["nonsense"]
			}`,
		},
	})
	if err != nil {
		return err
	}
	l.nexradChunkSubscriptionARN = *subs.SubscriptionArn
	_, err = l.awsSqs.SetQueueAttributes(context.TODO(), &sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(l.chunkQueueURL),
		Attributes: map[string]string{
			"Policy": fmt.Sprintf(`{
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
			}`, sqsARN, nexradChunkTopicARN),
		},
	})
	return err
}

func (l *Listener) destroyArchiveSubscription() error {
	if l.nexradArchiveSubscriptionARN == "" {
		return nil
	}
	_, err := l.awsSns.Unsubscribe(context.TODO(), &sns.UnsubscribeInput{
		SubscriptionArn: aws.String(l.nexradArchiveSubscriptionARN),
	})
	return err
}

func (l *Listener) destroyChunkSubscription() error {
	if l.nexradChunkSubscriptionARN == "" {
		return nil
	}
	_, err := l.awsSns.Unsubscribe(context.TODO(), &sns.UnsubscribeInput{
		SubscriptionArn: aws.String(l.nexradChunkSubscriptionARN),
	})
	return err
}

func (l *Listener) destroyArchiveQueue() error {
	if l.archiveQueueURL == "" {
		return nil
	}
	_, err := l.awsSqs.DeleteQueue(context.TODO(), &sqs.DeleteQueueInput{
		QueueUrl: aws.String(l.archiveQueueURL),
	})
	return err
}

func (l *Listener) destroyChunkQueue() error {
	if l.chunkQueueURL == "" {
		return nil
	}
	_, err := l.awsSqs.DeleteQueue(context.TODO(), &sqs.DeleteQueueInput{
		QueueUrl: aws.String(l.chunkQueueURL),
	})
	return err
}

func NewListener(eventChan chan events.Event) (*Listener, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion("us-east-1"), config.WithRetryMode(aws.RetryModeStandard), config.WithRetryMaxAttempts(10))
	if err != nil {
		return nil, err
	}
	svc := sqs.NewFromConfig(cfg)
	cfg, err = config.LoadDefaultConfig(context.TODO(), config.WithRegion("us-east-1"), config.WithRetryMode(aws.RetryModeStandard), config.WithRetryMaxAttempts(10))
	if err != nil {
		return nil, err
	}
	snsSvc := sns.NewFromConfig(cfg)
	cfg, err = config.LoadDefaultConfig(context.TODO(), config.WithRegion("us-east-1"), config.WithRetryMode(aws.RetryModeStandard), config.WithRetryMaxAttempts(10))
	if err != nil {
		return nil, err
	}
	stsSvc := sts.NewFromConfig(cfg)

	archiveQueueUUID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	chunkQueueUUID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}

	listener := &Listener{
		eventChan:        eventChan,
		archiveSites:     xsync.NewMapOf[string, uint](),
		chunkSites:       xsync.NewMapOf[string, uint](),
		awsSqs:           svc,
		awsSns:           snsSvc,
		awsSts:           stsSvc,
		archiveQueueName: fmt.Sprintf("nexrad-aws-notifier-events-archive-%s", archiveQueueUUID.String()),
		chunkQueueName:   fmt.Sprintf("nexrad-aws-notifier-events-chunk-%s", chunkQueueUUID.String()),
		running:          true,
	}

	err = listener.ensureArchiveQueue()
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

func (l *Listener) ListenChunk(ctx context.Context, station string) error {
	station = strings.ToUpper(station)
	num, loaded := l.chunkSites.LoadOrStore(station, 1)
	if loaded {
		l.chunkSites.Store(station, num+1)
	}
	return l.updateFilterPolicy(ctx)
}

func (l *Listener) ListenArchive(ctx context.Context, station string) error {
	station = strings.ToUpper(station)
	num, loaded := l.archiveSites.LoadOrStore(station, 1)
	if loaded {
		l.archiveSites.Store(station, num+1)
	}
	return l.updateFilterPolicy(ctx)
}

func (l *Listener) UnlistenArchive(ctx context.Context, station string) error {
	station = strings.ToUpper(station)
	num, loaded := l.archiveSites.LoadOrStore(station, 0)
	if loaded {
		l.archiveSites.Store(station, num-1)
	}
	if num-1 == 0 {
		l.archiveSites.Delete(station)
	}
	return l.updateFilterPolicy(ctx)
}

func (l *Listener) UnlistenChunk(ctx context.Context, station string) error {
	station = strings.ToUpper(station)
	num, loaded := l.chunkSites.LoadOrStore(station, 0)
	if loaded {
		l.chunkSites.Store(station, num-1)
	}
	if num-1 == 0 {
		l.chunkSites.Delete(station)
	}
	return l.updateFilterPolicy(ctx)
}

func (l *Listener) runArchive() {
	// Loop and poll the SQS queue
	for l.running {
		resp, err := l.awsSqs.ReceiveMessage(context.TODO(), &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(l.archiveQueueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     2,
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
			_, err := l.awsSqs.DeleteMessage(context.TODO(), &sqs.DeleteMessageInput{
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
		resp, err := l.awsSqs.ReceiveMessage(context.TODO(), &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(l.chunkQueueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     2,
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
			_, err := l.awsSqs.DeleteMessage(context.TODO(), &sqs.DeleteMessageInput{
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

func (l *Listener) onArchiveMessage(msg types.Message) {
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

		if l.running {
			l.eventChan <- events.NexradArchiveEvent{
				Station: station,
				Path:    record.S3.Object.Key,
			}
		}
	}
}

func (l *Listener) onChunkMessage(msg types.Message) {
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

	if l.running {
		l.eventChan <- events.NexradChunkEvent{
			Station:   site,
			Volume:    volume,
			Chunk:     chunk,
			ChunkType: chunkType,
			L2Version: l2Version,
		}
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
