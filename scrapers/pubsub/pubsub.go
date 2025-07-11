package pubsub

import (
	"fmt"
	"time"

	"github.com/flanksource/duty/context"
	gocloudpubsub "gocloud.dev/pubsub"
)

func ListenToSubscription(ctx context.Context, subscription *gocloudpubsub.Subscription, messageCh chan string, timeout time.Duration, maxMessages int) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	defer func() { close(messageCh) }()

	var count int
	for {
		msg, err := subscription.Receive(ctx)
		if err != nil {
			return fmt.Errorf("error receiving message: %w", err)
		}

		// Reset the timer since we received a message
		if !timer.Stop() {
			<-timer.C
		}
		timer.Reset(timeout)

		count++

		// Send message to channel
		select {
		case messageCh <- string(msg.Body):
			msg.Ack()
			if count >= maxMessages {
				return nil
			}
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
