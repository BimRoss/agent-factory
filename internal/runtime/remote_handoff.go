package runtime

import (
	"context"
	"errors"

	"github.com/bimross/agent-factory/internal/orchestratorevent"
)

// ErrHandoffDispatched means the capability was forwarded to another employee's JetStream consumer;
// the caller must not treat this as a failure and must not expect local execution.
var ErrHandoffDispatched = errors.New("handoff: capability forwarded to remote worker")

// RemoteHandoffForwarder publishes a continuation-aware orchestrator event to the target employee's
// subject (slack.work.<to>.events) after persisting handoff state in Redis.
type RemoteHandoffForwarder interface {
	ForwardRemoteHandoff(ctx context.Context, task Task, fromEmp, toEmp, capabilityID string, source *orchestratorevent.EventV1) error
}
