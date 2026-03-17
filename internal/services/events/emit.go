package events

import (
	"encoding/json"
	"time"

	"github.com/syndg/deck/internal/domain"
)

// Emit constructs an event with a JSON payload from key-value pairs and publishes it.
// Simplifies the common pattern of json.Marshal(map) + bus.Publish(domain.Event{...}).
//
// Usage:
//
//	bus.Emit(domain.EventAgentSpawned, objectiveID, streamID, agentID,
//	    "session_id", session.ID,
//	    "role", "builder",
//	)
func (pb *PersistentBus) Emit(typ domain.EventType, objectiveID, streamID, agentID string, kv ...any) {
	var payload string
	if len(kv) > 0 {
		m := make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			if key, ok := kv[i].(string); ok {
				m[key] = kv[i+1]
			}
		}
		data, _ := json.Marshal(m)
		payload = string(data)
	}
	pb.Publish(domain.Event{
		Type:      typ,
		Objective: objectiveID,
		Stream:    streamID,
		Agent:     agentID,
		Payload:   payload,
		CreatedAt: time.Now(),
	})
}
