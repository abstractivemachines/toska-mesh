package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// MassTransit wraps messages in an envelope for compatibility with C# MassTransit consumers.
// See: https://masstransit.io/documentation/concepts/messages#message-headers
type massTransitEnvelope struct {
	MessageID   string            `json:"messageId"`
	MessageType []string          `json:"messageType"`
	Headers     map[string]string `json:"headers"`
	Message     any               `json:"message"`
	SentTime    time.Time         `json:"sentTime"`
	Host        massTransitHost   `json:"host"`
}

type massTransitHost struct {
	MachineName    string `json:"machineName"`
	ProcessName    string `json:"processName"`
	ProcessID      int    `json:"processId"`
	Assembly       string `json:"assembly"`
	AssemblyVersion string `json:"assemblyVersion"`
	FrameworkVersion string `json:"frameworkVersion"`
	MassTransitVersion string `json:"massTransitVersion"`
	OperatingSystemVersion string `json:"operatingSystemVersion"`
}

// Publisher sends events to RabbitMQ in MassTransit-compatible envelope format.
type Publisher struct {
	conn    *amqp.Connection
	ch      *amqp.Channel
	logger  *slog.Logger
}

// NewPublisher creates a Publisher connected to the given AMQP URL.
// If url is empty, returns a no-op publisher that logs events instead of sending them.
func NewPublisher(url string, logger *slog.Logger) (*Publisher, error) {
	if url == "" {
		logger.Info("RabbitMQ URL not configured, using no-op publisher")
		return &Publisher{logger: logger}, nil
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("amqp dial: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("amqp channel: %w", err)
	}

	return &Publisher{
		conn:   conn,
		ch:     ch,
		logger: logger,
	}, nil
}

// Publish sends an event message to the appropriate RabbitMQ exchange.
// The exchange name and message type URN are derived from the event type.
func (p *Publisher) Publish(ctx context.Context, event any) error {
	typeName, exchangeName := eventMeta(event)

	envelope := massTransitEnvelope{
		MessageID:   generateID(),
		MessageType: []string{typeName},
		Headers:     map[string]string{},
		Message:     event,
		SentTime:    time.Now().UTC(),
		Host: massTransitHost{
			MachineName:    "toska-mesh",
			ProcessName:    "discovery",
			Assembly:       "toska-mesh",
			AssemblyVersion: "1.0.0",
		},
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	// No-op mode: just log.
	if p.ch == nil {
		p.logger.Info("event published (no-op)", "type", typeName, "exchange", exchangeName)
		return nil
	}

	// Declare a fanout exchange matching MassTransit convention.
	if err := p.ch.ExchangeDeclare(exchangeName, "fanout", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", exchangeName, err)
	}

	return p.ch.PublishWithContext(ctx, exchangeName, "", false, false, amqp.Publishing{
		ContentType: "application/vnd.masstransit+json",
		Body:        body,
	})
}

// Close cleanly shuts down the AMQP connection.
func (p *Publisher) Close() error {
	if p.ch != nil {
		p.ch.Close()
	}
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

func eventMeta(event any) (typeName, exchangeName string) {
	switch event.(type) {
	case ServiceRegisteredEvent:
		return "urn:message:ToskaMesh.Common.Messaging:ServiceRegisteredEvent",
			"ToskaMesh.Common.Messaging:ServiceRegisteredEvent"
	case ServiceDeregisteredEvent:
		return "urn:message:ToskaMesh.Common.Messaging:ServiceDeregisteredEvent",
			"ToskaMesh.Common.Messaging:ServiceDeregisteredEvent"
	case ServiceHealthChangedEvent:
		return "urn:message:ToskaMesh.Common.Messaging:ServiceHealthChangedEvent",
			"ToskaMesh.Common.Messaging:ServiceHealthChangedEvent"
	default:
		return "urn:message:Unknown", "Unknown"
	}
}

func generateID() string {
	// Use timestamp + random suffix for simplicity; can switch to UUID later.
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
