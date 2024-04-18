package sqs

type ArchiveNotification struct {
	Type             string `json:"Type"`
	MessageID        string `json:"MessageId"`
	TopicArn         string `json:"TopicArn"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	UnsubscribeURL   string `json:"UnsubscribeURL"`
}

type ArchiveNotificationMessage struct {
	Records []ArchiveNotificationRecord `json:"Records"`
}

type ArchiveNotificationRecord struct {
	EventVersion      string                `json:"eventVersion"`
	EventSource       string                `json:"eventSource"`
	AwsRegion         string                `json:"awsRegion"`
	EventTimee        string                `json:"eventTime"`
	EventName         string                `json:"eventName"`
	UserIdentity      map[string]string     `json:"userIdentity"`
	RequestParameters map[string]string     `json:"requestParameters"`
	ResponseElements  map[string]string     `json:"responseElements"`
	S3                ArchiveNotificationS3 `json:"s3"`
}

type ArchiveNotificationS3 struct {
	S3SchemaVersion string `json:"s3SchemaVersion"`
	ConfigurationID string `json:"configurationId"`
	Bucket          struct {
		Name          string            `json:"name"`
		OwnerIdentity map[string]string `json:"ownerIdentity"`
		Arn           string            `json:"arn"`
	} `json:"bucket"`
	Object struct {
		Key       string `json:"key"`
		Size      uint   `json:"size"`
		ETag      string `json:"eTag"`
		Sequencer string `json:"sequencer"`
	} `json:"object"`
}

type ChunkNotification struct {
	ArchiveNotification
	UnsubscribeURL    string `json:"UnsubscribeURL"`
	MessageAttributes map[string]struct {
		Type  string `json:"Type"`
		Value string `json:"Value"`
	}
}
