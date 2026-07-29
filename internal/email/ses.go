package email

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// sesSender sends through SES v2. The task role carries ses:SendEmail scoped
// to the apex identity (terraform/prod/iam.tf).
type sesSender struct {
	client *sesv2.Client
	from   string
}

// NewSESSender builds a Sender backed by SES in the given region. from must be
// an address on a verified identity.
func NewSESSender(ctx context.Context, region, from string) (Sender, error) {
	if from == "" {
		return nil, fmt.Errorf("email: SES sender requires a From address")
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("email: load aws config: %w", err)
	}
	return &sesSender{client: sesv2.NewFromConfig(cfg), from: from}, nil
}

func (s *sesSender) buildInput(msg Message) *sesv2.SendEmailInput {
	return &sesv2.SendEmailInput{
		FromEmailAddress: &s.from,
		Destination:      &types.Destination{ToAddresses: []string{msg.To}},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{Data: &msg.Subject},
				Body: &types.Body{
					Html: &types.Content{Data: &msg.HTML},
					Text: &types.Content{Data: &msg.Text},
				},
			},
		},
	}
}

func (s *sesSender) Send(ctx context.Context, msg Message) error {
	if _, err := s.client.SendEmail(ctx, s.buildInput(msg)); err != nil {
		return fmt.Errorf("email: ses send: %w", err)
	}
	return nil
}
