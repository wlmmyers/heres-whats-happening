// Package queue wraps the AWS SQS SDK with the minimal Send/Receive/Delete
// surface our ingest pipeline needs. Knows nothing about event semantics.
package queue

import (
	"context"
	"fmt"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type Client struct {
	sqs *sqs.Client
}

// Message is a received SQS message in our internal shape.
type Message struct {
	Body          []byte
	ReceiptHandle string
}

// NewClient builds an SQS client. If endpointURL is non-empty, it overrides
// the default AWS endpoint (used for ElasticMQ in dev). Region must be set.
func NewClient(ctx context.Context, region, endpointURL string) (*Client, error) {
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	var clientOpts []func(*sqs.Options)
	if endpointURL != "" {
		clientOpts = append(clientOpts, func(o *sqs.Options) {
			o.BaseEndpoint = &endpointURL
		})
	}
	return &Client{sqs: sqs.NewFromConfig(cfg, clientOpts...)}, nil
}

func (c *Client) Send(ctx context.Context, queueURL string, body []byte) error {
	s := string(body)
	_, err := c.sqs.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    &queueURL,
		MessageBody: &s,
	})
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return nil
}

// Receive pulls up to maxMessages with long-polling for waitTime (max 20s).
func (c *Client) Receive(ctx context.Context, queueURL string, maxMessages int32, waitTime time.Duration) ([]Message, error) {
	waitSec := int32(waitTime / time.Second)
	if waitSec > 20 {
		waitSec = 20
	}
	out, err := c.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: maxMessages,
		WaitTimeSeconds:     waitSec,
	})
	if err != nil {
		return nil, fmt.Errorf("receive: %w", err)
	}
	msgs := make([]Message, 0, len(out.Messages))
	for _, m := range out.Messages {
		msgs = append(msgs, Message{
			Body:          []byte(*m.Body),
			ReceiptHandle: *m.ReceiptHandle,
		})
	}
	return msgs, nil
}

func (c *Client) Delete(ctx context.Context, queueURL, receiptHandle string) error {
	_, err := c.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueURL,
		ReceiptHandle: &receiptHandle,
	})
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// Purge empties the queue. Test-only; production callers should not use it.
func (c *Client) Purge(ctx context.Context, queueURL string) error {
	_, err := c.sqs.PurgeQueue(ctx, &sqs.PurgeQueueInput{QueueUrl: &queueURL})
	if err != nil {
		return fmt.Errorf("purge: %w", err)
	}
	// PurgeQueue is async; sleep briefly so subsequent operations see the cleared state.
	time.Sleep(500 * time.Millisecond)
	return nil
}
