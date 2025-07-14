package pubsub

import (
	gocontext "context"
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/duty/context"
	gocloudpubsub "gocloud.dev/pubsub"
)

func ListenToSubscription(ctx context.Context, subscription *gocloudpubsub.Subscription, messageCh chan string, timeout time.Duration, maxMessages int) error {
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
		case messageCh <- string(msg.Body):
			msg.Ack()
			if count >= maxMessages {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
