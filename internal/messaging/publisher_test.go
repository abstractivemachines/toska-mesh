package messaging

import (
	"strings"
	"testing"
	"time"
)

func TestEventMeta(t *testing.T) {
	tests := []struct {
		name             string
		event            any
		wantTypeName     string
		wantExchangeName string
	}{
		{
			name:             "ServiceRegisteredEvent",
			event:            ServiceRegisteredEvent{},
			wantTypeName:     "urn:message:ToskaMesh.Common.Messaging:ServiceRegisteredEvent",
			wantExchangeName: "ToskaMesh.Common.Messaging:ServiceRegisteredEvent",
		},
		{
			name:             "ServiceDeregisteredEvent",
			event:            ServiceDeregisteredEvent{},
			wantTypeName:     "urn:message:ToskaMesh.Common.Messaging:ServiceDeregisteredEvent",
			wantExchangeName: "ToskaMesh.Common.Messaging:ServiceDeregisteredEvent",
		},
		{
			name:             "ServiceHealthChangedEvent",
			event:            ServiceHealthChangedEvent{},
			wantTypeName:     "urn:message:ToskaMesh.Common.Messaging:ServiceHealthChangedEvent",
			wantExchangeName: "ToskaMesh.Common.Messaging:ServiceHealthChangedEvent",
		},
		{
			name:             "unknown event type",
			event:            "not an event",
			wantTypeName:     "urn:message:Unknown",
			wantExchangeName: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typeName, exchangeName := eventMeta(tt.event)
			if typeName != tt.wantTypeName {
				t.Errorf("eventMeta() typeName = %q, want %q", typeName, tt.wantTypeName)
			}
			if exchangeName != tt.wantExchangeName {
				t.Errorf("eventMeta() exchangeName = %q, want %q", exchangeName, tt.wantExchangeName)
			}
		})
	}
}

func TestGenerateID_Unique(t *testing.T) {
	seen := make(map[string]struct{})
	for range 1000 {
		id := generateID()
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestGenerateID_Format(t *testing.T) {
	id := generateID()
	parts := strings.SplitN(id, "-", 2)
	if len(parts) != 2 {
		t.Fatalf("expected ID format 'timestamp-seq', got %q", id)
	}
}

func TestMassTransitEnvelope_Fields(t *testing.T) {
	event := ServiceRegisteredEvent{
		EventID:     "test-1",
		Timestamp:   time.Now().UTC(),
		ServiceID:   "svc-1",
		ServiceName: "test-service",
		Address:     "127.0.0.1",
		Port:        8080,
	}

	typeName, _ := eventMeta(event)
	if !strings.HasPrefix(typeName, "urn:message:") {
		t.Errorf("expected URN prefix, got %q", typeName)
	}
}
