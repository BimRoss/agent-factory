package natsbus

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

type ConsumerConfig struct {
	EmployeeID      string
	NatsURL         string
	NatsStream      string
	NatsDurableName string
	FetchBatch      int
	FetchMaxWaitMS  int
	Workers         int
}

type OrchestratorPayloadHandler func(ctx context.Context, payload []byte) error

var ErrRetryable = errors.New("retryable payload handler error")

func RunOrchestratorConsumer(ctx context.Context, cfg ConsumerConfig, handle OrchestratorPayloadHandler) error {
	if strings.TrimSpace(cfg.EmployeeID) == "" {
		return fmt.Errorf("natsbus: employee id is required")
	}
	if strings.TrimSpace(cfg.NatsURL) == "" {
		return fmt.Errorf("natsbus: NATS_URL is empty")
	}
	if handle == nil {
		return fmt.Errorf("natsbus: payload handler is required")
	}

	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := runConsumerSession(ctx, cfg, handle)
		if errors.Is(err, context.Canceled) {
			return err
		}
		log.Printf("agent-factory nats consumer: %v; retry in %s", err, backoff)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func runConsumerSession(ctx context.Context, cfg ConsumerConfig, handle OrchestratorPayloadHandler) error {
	nc, err := nats.Connect(strings.TrimSpace(cfg.NatsURL),
		nats.Name("agent-factory-"+strings.TrimSpace(cfg.EmployeeID)),
		nats.Timeout(20*time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		return fmt.Errorf("nats connect: %w", err)
	}
	defer func() { _ = nc.Drain() }()

	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("jetstream: %w", err)
	}

	stream := strings.TrimSpace(cfg.NatsStream)
	if stream == "" {
		stream = "SLACK_WORK"
	}
	if err := EnsureJetStreamStream(js, stream); err != nil {
		return err
	}

	employeeID := strings.ToLower(strings.TrimSpace(cfg.EmployeeID))
	subject := fmt.Sprintf("slack.work.%s.events", employeeID)
	durable := strings.TrimSpace(cfg.NatsDurableName)
	if durable == "" {
		// Backward-compatible default: keep the same durable name used by employee-factory so
		// agent-factory continues from the existing cursor instead of replaying historical backlog.
		durable = "employee-factory-" + employeeID
	}

	sub, err := js.PullSubscribe(subject, durable, nats.BindStream(stream), nats.ManualAck())
	if err != nil {
		return fmt.Errorf("pull subscribe %s: %w", subject, err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	log.Printf("agent-factory consumer ready stream=%s subject=%s durable=%s", stream, subject, durable)

	batch := cfg.FetchBatch
	if batch <= 0 {
		batch = 8
	}
	maxWait := time.Duration(cfg.FetchMaxWaitMS) * time.Millisecond
	if maxWait <= 0 {
		maxWait = 5 * time.Second
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = 4
	}
	sem := make(chan struct{}, workers)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msgs, err := sub.Fetch(batch, nats.MaxWait(maxWait))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("agent-factory nats fetch: %v", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
			continue
		}

		var wg sync.WaitGroup
		for _, msg := range msgs {
			sem <- struct{}{}
			wg.Add(1)
			go func(msg *nats.Msg) {
				defer wg.Done()
				defer func() { <-sem }()

				if err := handle(ctx, msg.Data); err != nil {
					log.Printf("agent-factory payload handler error: %v", err)
					if errors.Is(err, ErrRetryable) {
						_ = msg.Nak()
						return
					}
					_ = msg.Ack()
					return
				}
				_ = msg.Ack()
			}(msg)
		}
		wg.Wait()
	}
}

// EnsureJetStreamStream creates the JetStream stream if missing (subjects slack.work.*.events).
func EnsureJetStreamStream(js nats.JetStreamContext, name string) error {
	if _, err := js.StreamInfo(name); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrStreamNotFound) {
		return fmt.Errorf("stream info %q: %w", name, err)
	}
	_, err := js.AddStream(&nats.StreamConfig{
		Name:     name,
		Subjects: []string{"slack.work.*.events"},
		Storage:  nats.FileStorage,
	})
	if err != nil {
		return fmt.Errorf("add stream %q: %w", name, err)
	}
	log.Printf("agent-factory nats created stream=%s subjects=slack.work.*.events", name)
	return nil
}
