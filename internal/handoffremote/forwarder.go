package handoffremote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bimross/agent-factory/internal/natsbus"
	"github.com/bimross/agent-factory/internal/orchestratorevent"
	"github.com/bimross/agent-factory/internal/runtime"
	"github.com/nats-io/nats.go"
)

// Forwarder implements runtime.RemoteHandoffForwarder.
type Forwarder struct {
	Store *Store
	nc    *nats.Conn
}

// NewForwarder connects to NATS and ensures the JetStream stream exists.
func NewForwarder(store *Store, natsURL, stream string) (*Forwarder, error) {
	if store == nil {
		return nil, fmt.Errorf("handoffremote: store is required")
	}
	natsURL = strings.TrimSpace(natsURL)
	if natsURL == "" {
		return nil, fmt.Errorf("handoffremote: nats url is required")
	}
	if stream == "" {
		stream = "SLACK_WORK"
	}
	nc, err := nats.Connect(natsURL,
		nats.Name("agent-factory-handoff-forwarder"),
		nats.Timeout(20*time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("handoffremote: nats connect: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		_ = nc.Drain()
		return nil, fmt.Errorf("handoffremote: jetstream: %w", err)
	}
	if err := natsbus.EnsureJetStreamStream(js, stream); err != nil {
		_ = nc.Drain()
		return nil, err
	}
	return &Forwarder{Store: store, nc: nc}, nil
}

func (f *Forwarder) Close() {
	if f != nil && f.nc != nil {
		_ = f.nc.Drain()
	}
}

// ForwardRemoteHandoff writes Redis state then publishes to slack.work.<to>.events.
func (f *Forwarder) ForwardRemoteHandoff(ctx context.Context, task runtime.Task, fromEmp, toEmp, capabilityID string, source *orchestratorevent.EventV1) error {
	if f == nil || f.Store == nil {
		return fmt.Errorf("handoffremote: forwarder not configured")
	}
	if source == nil {
		return fmt.Errorf("handoffremote: source event is required for remote handoff")
	}
	fromEmp = strings.ToLower(strings.TrimSpace(fromEmp))
	toEmp = strings.ToLower(strings.TrimSpace(toEmp))
	capabilityID = strings.ToLower(strings.TrimSpace(capabilityID))
	if fromEmp == "" || toEmp == "" || capabilityID == "" {
		return fmt.Errorf("handoffremote: from, to, and capability are required")
	}

	handoffID, err := newHandoffID()
	if err != nil {
		return err
	}

	decision := source.Decision
	if strings.TrimSpace(decision.ToolID) == "" {
		decision.ToolID = capabilityID
	}

	rec := &Record{
		SchemaVersion:      RecordSchemaVersion,
		HandoffID:          handoffID,
		FromEmployee:       fromEmp,
		ToEmployee:         toEmp,
		CapabilityID:       capabilityID,
		TraceID:            strings.TrimSpace(source.TraceID),
		RunID:              strings.TrimSpace(source.RunID),
		SlackEventID:       strings.TrimSpace(source.SlackEventID),
		TriggerSource:      strings.TrimSpace(source.TriggerSource),
		OriginatingTaskID:  strings.TrimSpace(task.ID),
		Message:            source.Message,
		Decision:           decision,
		EventSchemaVersion: strings.TrimSpace(source.SchemaVersion),
	}

	if err := f.Store.Put(ctx, handoffID, rec); err != nil {
		return fmt.Errorf("handoffremote: redis put: %w", err)
	}

	out := orchestratorevent.EventV1{
		SchemaVersion:  firstNonEmpty(strings.TrimSpace(source.SchemaVersion), "3"),
		TraceID:        source.TraceID,
		RunID:          source.RunID,
		TriggerSource:  firstNonEmpty(strings.TrimSpace(source.TriggerSource), "handoff"),
		SlackEventID:   source.SlackEventID,
		TargetEmployee: toEmp,
		Decision:       decision,
		Message:        source.Message,
		Continuation:   &orchestratorevent.ContinuationV1{HandoffID: handoffID},
	}

	body, err := json.Marshal(out)
	if err != nil {
		_ = f.Store.Delete(ctx, handoffID)
		return err
	}

	subject := fmt.Sprintf("slack.work.%s.events", toEmp)
	js, err := f.nc.JetStream()
	if err != nil {
		_ = f.Store.Delete(ctx, handoffID)
		return fmt.Errorf("handoffremote: jetstream: %w", err)
	}
	if _, err := js.Publish(subject, body); err != nil {
		_ = f.Store.Delete(ctx, handoffID)
		return fmt.Errorf("handoffremote: publish %s: %w", subject, err)
	}
	return nil
}

var _ runtime.RemoteHandoffForwarder = (*Forwarder)(nil)

func newHandoffID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "hf_" + hex.EncodeToString(b[:]), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
