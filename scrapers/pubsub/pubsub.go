package pubsub

import (
	gocontext "context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/duty/context"
	gocloudpubsub "gocloud.dev/pubsub"
)

func ListenToSubscription(ctx context.Context, subscription *gocloudpubsub.Subscription, messageCh chan PubSubMessage, timeout time.Duration, maxMessages int) error {
	defer func() { close(messageCh) }()

	var count int
	for {
		rctx, cancel := ctx.WithDeadline(time.Now().Add(timeout))
		msg, err := subscription.Receive(rctx)
		cancel()
		if err != nil {
			if errors.Is(err, gocontext.DeadlineExceeded) {
				return nil
			}
			return fmt.Errorf("error receiving message: %w", err)
		}

		count++

		// Send message to channel
		select {
		case messageCh <- processMessageBody(msg.Body, msg.Metadata):
			msg.Ack()
			if count >= maxMessages {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type PubSubMessage struct {
	Message  any               `json:"message"`
	Metadata map[string]string `json:"metadata"`
}

func processMessageBody(msgBody []byte, metadata map[string]string) PubSubMessage {
	p := PubSubMessage{
		Message:  string(msgBody),
		Metadata: metadata,
	}

	// See if json
	var m map[string]any
	if err := json.Unmarshal(msgBody, &m); err == nil && m != nil {
		p.Message = m
	}
	return p
}
