package natsbus

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bimross/agent-factory/internal/orchestratorevent"
	"github.com/nats-io/nats.go"
)

var (
	publishMu      sync.Mutex
	publishConn    *nats.Conn
	publishJS      nats.JetStreamContext
	publishURLUsed string
)

func publishJetStreamContext(natsURL, clientName string) (nats.JetStreamContext, error) {
	url := strings.TrimSpace(natsURL)
	if url == "" {
		return nil, fmt.Errorf("natsbus: NATS_URL is empty")
	}

	publishMu.Lock()
	defer publishMu.Unlock()

	if publishConn != nil && publishConn.IsConnected() && publishJS != nil && publishURLUsed == url {
		return publishJS, nil
	}
	if publishConn != nil {
		_ = publishConn.Drain()
		publishConn = nil
		publishJS = nil
		publishURLUsed = ""
	}

	nc, err := nats.Connect(url,
		nats.Name("agent-factory-"+strings.TrimSpace(clientName)),
		nats.Timeout(20*time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		_ = nc.Drain()
		return nil, fmt.Errorf("jetstream: %w", err)
	}
	publishConn = nc
	publishJS = js
	publishURLUsed = url
	return publishJS, nil
}

// PublishOrchestratorEvent publishes a pipeline continuation (or test event) to slack.work.<employee>.events.
func PublishOrchestratorEvent(natsURL, natsStream, employeeID string, payload *orchestratorevent.EventV1) error {
	if payload == nil {
		return fmt.Errorf("natsbus: nil payload")
	}
	js, err := publishJetStreamContext(natsURL, "pipeline-"+strings.TrimSpace(employeeID))
	if err != nil {
		return err
	}
	stream := strings.TrimSpace(natsStream)
	if stream == "" {
		stream = "SLACK_WORK"
	}
	if err := EnsureJetStreamStream(js, stream); err != nil {
		return err
	}
	emp := strings.ToLower(strings.TrimSpace(payload.TargetEmployee))
	if emp == "" {
		return fmt.Errorf("natsbus: empty target_employee")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	subject := fmt.Sprintf("slack.work.%s.events", emp)
	if _, err := js.Publish(subject, body); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}
