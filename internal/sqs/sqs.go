package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

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
	nexradArchiveTopicARN = "arn:aws:sns:us-east-1:684042711724:NewNEXRADLevel2Archive"
	nexradChunkTopicARN   = "arn:aws:sns:us-east-1:684042711724:NewNEXRADLevel2ObjectFilterable"
)

const (
	// SQS caps long polling at 20 seconds. Every second below that multiplies
	// the number of empty receives, and every empty receive is billed.
	receiveWaitSeconds = 20
	receiveBatchSize   = 10

	// Backoff between consecutive ReceiveMessage failures. Without it a queue
	// that has gone away, or credentials that have expired, spin the poll loop
	// at network speed.
	pollRetryBase = 1 * time.Second
	pollRetryMax  = 30 * time.Second

	// How long teardown may take after the poll context is cancelled.
	shutdownTimeout = 15 * time.Second

	// Neither topic publishes for this site, so it is what an empty
	// subscription list renders to: a filter that matches nothing.
	noSite = "nonsense"
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
	// Cancels the poll context, so a 20-second long poll does not hold
	// shutdown open for its full duration.
	cancel  context.CancelFunc
	running atomic.Bool
}

// ensureQueue finds or creates one queue, returning its URL.
func (l *Listener) ensureQueue(ctx context.Context, name string) (string, error) {
	resp, err := l.awsSqs.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{
		QueueName: aws.String(name),
	})
	if err == nil {
		return *resp.QueueUrl, nil
	}
	// Queue does not exist, create it. The queue-level wait matches the
	// per-request one so a future caller cannot silently short-poll.
	created, err := l.awsSqs.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(name),
		Attributes: map[string]string{
			"ReceiveMessageWaitTimeSeconds": strconv.Itoa(receiveWaitSeconds),
		},
	})
	if err != nil {
		return "", err
	}
	return *created.QueueUrl, nil
}

func (l *Listener) ensureChunkQueue(ctx context.Context) error {
	url, err := l.ensureQueue(ctx, l.chunkQueueName)
	if err != nil {
		return err
	}
	l.chunkQueueURL = url
	return nil
}

func (l *Listener) ensureArchiveQueue(ctx context.Context) error {
	url, err := l.ensureQueue(ctx, l.archiveQueueName)
	if err != nil {
		return err
	}
	l.archiveQueueURL = url
	return nil
}

// sendMessagePolicy lets one SNS topic, and nothing else, write to one queue.
func sendMessagePolicy(queueARN, topicARN string) string {
	return fmt.Sprintf(`{
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
	}`, queueARN, topicARN)
}

func (l *Listener) queueARN(ctx context.Context, queueName string) (string, error) {
	callerID, err := l.awsSts.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("arn:aws:sqs:us-east-1:%s:%s", *callerID.Account, queueName), nil
}

// subscribedSites lists the stations with at least one live websocket, sorted
// so an unchanged subscription set renders to an unchanged policy.
func subscribedSites(m *xsync.MapOf[string, uint]) []string {
	var sites []string
	m.Range(func(key string, val uint) bool {
		if val > 0 && !slices.Contains(sites, key) {
			sites = append(sites, key)
		}
		return true
	})
	slices.Sort(sites)
	return sites
}

// chunkFilterPolicy filters the chunk topic on the SiteID message attribute it
// publishes.
func chunkFilterPolicy(sites []string) (string, error) {
	if len(sites) == 0 {
		sites = []string{noSite}
	}
	jsonSites, err := json.Marshal(sites)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"SiteID": %s}`, jsonSites), nil
}

// archiveFilterPolicy filters the archive topic, which publishes raw S3 event
// notifications carrying no message attributes at all, so the match has to be
// made against the message body.
//
// The object key is `YYYY/MM/DD/SITE/SITE<timestamp>_V06`, so the leading date
// rules out a prefix match on the site; `wildcard` matches the site wherever
// the date puts it, and needs no rewrite when the date rolls over.
func archiveFilterPolicy(sites []string) (string, error) {
	if len(sites) == 0 {
		sites = []string{noSite}
	}
	patterns := make([]map[string]string, 0, len(sites))
	for _, site := range sites {
		patterns = append(patterns, map[string]string{"wildcard": "*/" + site + "/*"})
	}
	policy := map[string]any{
		"Records": map[string]any{
			"s3":        map[string]any{"object": map[string]any{"key": patterns}},
			"eventName": []map[string]string{{"prefix": "ObjectCreated:"}},
		},
	}
	out, err := json.Marshal(policy)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (l *Listener) updateChunkFilterPolicy(ctx context.Context) error {
	policy, err := chunkFilterPolicy(subscribedSites(l.chunkSites))
	if err != nil {
		return err
	}
	_, err = l.awsSns.SetSubscriptionAttributes(ctx, &sns.SetSubscriptionAttributesInput{
		SubscriptionArn: aws.String(l.nexradChunkSubscriptionARN),
		AttributeName:   aws.String("FilterPolicy"),
		AttributeValue:  aws.String(policy),
	})
	return err
}

func (l *Listener) updateArchiveFilterPolicy(ctx context.Context) error {
	policy, err := archiveFilterPolicy(subscribedSites(l.archiveSites))
	if err != nil {
		return err
	}
	_, err = l.awsSns.SetSubscriptionAttributes(ctx, &sns.SetSubscriptionAttributesInput{
		SubscriptionArn: aws.String(l.nexradArchiveSubscriptionARN),
		AttributeName:   aws.String("FilterPolicy"),
		AttributeValue:  aws.String(policy),
	})
	return err
}

func (l *Listener) ensureArchiveSubscription(ctx context.Context) error {
	sqsARN, err := l.queueARN(ctx, l.archiveQueueName)
	if err != nil {
		return err
	}

	policy, err := archiveFilterPolicy(nil)
	if err != nil {
		return err
	}
	subs, err := l.awsSns.Subscribe(ctx, &sns.SubscribeInput{
		Protocol:              aws.String("sqs"),
		TopicArn:              aws.String(nexradArchiveTopicARN),
		Endpoint:              aws.String(sqsARN),
		ReturnSubscriptionArn: true,
		Attributes: map[string]string{
			"FilterPolicyScope": "MessageBody",
			"FilterPolicy":      policy,
		},
	})
	if err != nil {
		return err
	}
	l.nexradArchiveSubscriptionARN = *subs.SubscriptionArn
	_, err = l.awsSqs.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(l.archiveQueueURL),
		Attributes: map[string]string{
			"Policy": sendMessagePolicy(sqsARN, nexradArchiveTopicARN),
		},
	})
	return err
}

func (l *Listener) ensureChunkSubscription(ctx context.Context) error {
	sqsARN, err := l.queueARN(ctx, l.chunkQueueName)
	if err != nil {
		return err
	}

	policy, err := chunkFilterPolicy(nil)
	if err != nil {
		return err
	}
	subs, err := l.awsSns.Subscribe(ctx, &sns.SubscribeInput{
		Protocol:              aws.String("sqs"),
		TopicArn:              aws.String(nexradChunkTopicARN),
		Endpoint:              aws.String(sqsARN),
		ReturnSubscriptionArn: true,
		Attributes: map[string]string{
			"FilterPolicy": policy,
		},
	})
	if err != nil {
		return err
	}
	l.nexradChunkSubscriptionARN = *subs.SubscriptionArn
	_, err = l.awsSqs.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(l.chunkQueueURL),
		Attributes: map[string]string{
			"Policy": sendMessagePolicy(sqsARN, nexradChunkTopicARN),
		},
	})
	return err
}

func (l *Listener) destroyArchiveSubscription(ctx context.Context) error {
	if l.nexradArchiveSubscriptionARN == "" {
		return nil
	}
	_, err := l.awsSns.Unsubscribe(ctx, &sns.UnsubscribeInput{
		SubscriptionArn: aws.String(l.nexradArchiveSubscriptionARN),
	})
	return err
}

func (l *Listener) destroyChunkSubscription(ctx context.Context) error {
	if l.nexradChunkSubscriptionARN == "" {
		return nil
	}
	_, err := l.awsSns.Unsubscribe(ctx, &sns.UnsubscribeInput{
		SubscriptionArn: aws.String(l.nexradChunkSubscriptionARN),
	})
	return err
}

func (l *Listener) destroyArchiveQueue(ctx context.Context) error {
	if l.archiveQueueURL == "" {
		return nil
	}
	_, err := l.awsSqs.DeleteQueue(ctx, &sqs.DeleteQueueInput{
		QueueUrl: aws.String(l.archiveQueueURL),
	})
	return err
}

func (l *Listener) destroyChunkQueue(ctx context.Context) error {
	if l.chunkQueueURL == "" {
		return nil
	}
	_, err := l.awsSqs.DeleteQueue(ctx, &sqs.DeleteQueueInput{
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
	snsSvc := sns.NewFromConfig(cfg)
	stsSvc := sts.NewFromConfig(cfg)

	archiveQueueUUID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	chunkQueueUUID, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}

	pollCtx, cancel := context.WithCancel(context.Background())
	listener := &Listener{
		eventChan:        eventChan,
		archiveSites:     xsync.NewMapOf[string, uint](),
		chunkSites:       xsync.NewMapOf[string, uint](),
		awsSqs:           svc,
		awsSns:           snsSvc,
		awsSts:           stsSvc,
		archiveQueueName: fmt.Sprintf("nexrad-aws-notifier-events-archive-%s", archiveQueueUUID.String()),
		chunkQueueName:   fmt.Sprintf("nexrad-aws-notifier-events-chunk-%s", chunkQueueUUID.String()),
		cancel:           cancel,
		running:          atomic.Bool{},
	}
	listener.running.Store(true)

	unwind := func(err error) (*Listener, error) {
		cancel()
		teardown, teardownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer teardownCancel()
		_ = listener.destroyArchiveSubscription(teardown)
		_ = listener.destroyChunkSubscription(teardown)
		_ = listener.destroyArchiveQueue(teardown)
		_ = listener.destroyChunkQueue(teardown)
		return nil, err
	}

	if err := listener.ensureArchiveQueue(pollCtx); err != nil {
		return unwind(err)
	}
	if err := listener.ensureChunkQueue(pollCtx); err != nil {
		return unwind(err)
	}
	if err := listener.ensureArchiveSubscription(pollCtx); err != nil {
		return unwind(err)
	}
	if err := listener.ensureChunkSubscription(pollCtx); err != nil {
		return unwind(err)
	}

	go listener.poll(pollCtx, "archive", listener.archiveQueueURL, listener.onArchiveMessage)
	go listener.poll(pollCtx, "chunk", listener.chunkQueueURL, listener.onChunkMessage)

	return listener, nil
}

// listen adds one reference to a station.
func listen(m *xsync.MapOf[string, uint], station string) {
	m.Compute(station, func(oldValue uint, _ bool) (uint, bool) {
		return oldValue + 1, false
	})
}

// unlisten drops one reference to a station, deleting the entry when the last
// one goes. Decrementing an absent key would underflow uint, so an entry that
// is not there is removed rather than written back.
func unlisten(m *xsync.MapOf[string, uint], station string) {
	m.Compute(station, func(oldValue uint, loaded bool) (uint, bool) {
		if !loaded || oldValue <= 1 {
			return 0, true
		}
		return oldValue - 1, false
	})
}

func (l *Listener) ListenChunk(ctx context.Context, station string) error {
	listen(l.chunkSites, strings.ToUpper(station))
	return l.updateChunkFilterPolicy(ctx)
}

func (l *Listener) ListenArchive(ctx context.Context, station string) error {
	listen(l.archiveSites, strings.ToUpper(station))
	return l.updateArchiveFilterPolicy(ctx)
}

func (l *Listener) UnlistenArchive(ctx context.Context, station string) error {
	unlisten(l.archiveSites, strings.ToUpper(station))
	return l.updateArchiveFilterPolicy(ctx)
}

func (l *Listener) UnlistenChunk(ctx context.Context, station string) error {
	unlisten(l.chunkSites, strings.ToUpper(station))
	return l.updateChunkFilterPolicy(ctx)
}

// pollBackoff grows with consecutive receive failures, to a ceiling.
func pollBackoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := pollRetryBase
	for i := 1; i < failures; i++ {
		delay *= 2
		if delay >= pollRetryMax {
			return pollRetryMax
		}
	}
	return delay
}

// wait sleeps for d, or returns false as soon as the listener is stopping.
func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// poll drains one queue until the listener stops. Both feeds have identical
// mechanics; only the queue and the per-message handler differ.
func (l *Listener) poll(ctx context.Context, name string, queueURL string, onMessage func(types.Message)) {
	failures := 0
	for l.running.Load() {
		resp, err := l.awsSqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: receiveBatchSize,
			WaitTimeSeconds:     receiveWaitSeconds,
		})
		// Break early since it's likely the listener will stop
		// while waiting for messages
		if !l.running.Load() {
			return
		}
		if err != nil {
			failures++
			delay := pollBackoff(failures)
			slog.Warn("Error receiving message:", "queue", name, "error", err, "retryIn", delay)
			if !wait(ctx, delay) {
				return
			}
			continue
		}
		failures = 0
		if len(resp.Messages) == 0 {
			continue
		}
		l.deleteMessages(ctx, name, queueURL, resp.Messages)
		for _, msg := range resp.Messages {
			go onMessage(msg)
		}
	}
}

// deleteMessages removes a whole receive in one request, where deleting each
// message on its own cost one billable request per message.
func (l *Listener) deleteMessages(ctx context.Context, name string, queueURL string, msgs []types.Message) {
	entries := make([]types.DeleteMessageBatchRequestEntry, 0, len(msgs))
	for i, msg := range msgs {
		entries = append(entries, types.DeleteMessageBatchRequestEntry{
			Id:            aws.String(strconv.Itoa(i)),
			ReceiptHandle: msg.ReceiptHandle,
		})
	}
	resp, err := l.awsSqs.DeleteMessageBatch(ctx, &sqs.DeleteMessageBatchInput{
		QueueUrl: aws.String(queueURL),
		Entries:  entries,
	})
	if err != nil {
		slog.Warn("Error deleting messages:", "queue", name, "count", len(entries), "error", err)
		return
	}
	for _, failed := range resp.Failed {
		slog.Warn("Error deleting message:", "queue", name, "code", aws.ToString(failed.Code), "reason", aws.ToString(failed.Message))
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

		if l.running.Load() {
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

	// The message attributes don't carry the volume start time, so the object
	// key can't be rebuilt from them. The message body has it verbatim.
	var message ChunkNotificationMessage
	if err := json.Unmarshal([]byte(notification.Message), &message); err != nil {
		slog.Warn("Error unmarshalling chunk message:", "error", err)
	}

	// Key is SITE/VOLUME/yyyymmdd-hhmmss-NNN-T
	name := message.Key
	if idx := strings.LastIndex(name, "/"); idx != -1 {
		name = name[idx+1:]
	}

	slog.Info("Received chunk record", "site", site, "volume", volume, "chunk", chunk, "chunkType", chunkType, "l2Version", l2Version, "path", message.Key)

	if l.running.Load() {
		l.eventChan <- events.NexradChunkEvent{
			Station:   site,
			Volume:    volume,
			Chunk:     chunk,
			ChunkType: chunkType,
			L2Version: l2Version,
			Name:      name,
			Path:      message.Key,
		}
	}
}

func (l *Listener) Stop() error {
	l.running.Store(false)
	// Cancels the in-flight long poll, which would otherwise hold shutdown open
	// for the rest of its 20 seconds.
	l.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	errGrp := errgroup.Group{}
	errGrp.SetLimit(4)
	errGrp.Go(func() error {
		return l.destroyChunkSubscription(ctx)
	})
	errGrp.Go(func() error {
		return l.destroyArchiveSubscription(ctx)
	})
	errGrp.Go(func() error {
		return l.destroyChunkQueue(ctx)
	})
	errGrp.Go(func() error {
		return l.destroyArchiveQueue(ctx)
	})
	return errGrp.Wait()
}
