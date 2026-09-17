package mqtt

import (
	"context"
	"errors"
	"time"
)

var ErrMQTTNotConnected = errors.New("MQTT_NOT_CONNECTED")

type Publication struct {
	Topic   string
	QoS     byte
	Retain  bool
	Payload []byte
}

type TransportCallbacks struct {
	OnConnected    func()
	OnDisconnected func(error)
	OnMessage      func(string, []byte)
}

// Transport.Publish returns only after the underlying MQTT client has
// completed the broker acknowledgement for QoS1. The application outbox
// deletes a row only after this method succeeds.
type Transport interface {
	Publish(context.Context, Publication) error
	Subscribe(context.Context, string, byte) error
	Close() error
}

// ConnectedWaiter is implemented by transports whose factory starts an
// asynchronous connection. It lets the management "test connection" path
// report the broker handshake result instead of only reporting that a client
// object was constructed.
type ConnectedWaiter interface {
	WaitConnected(context.Context) error
}

type TransportFactory func(context.Context, RuntimeConfig, TransportCallbacks) (Transport, error)

func defaultBackoff(attempt int, minimum, maximum time.Duration) time.Duration {
	if minimum <= 0 {
		minimum = time.Second
	}
	if maximum < minimum {
		maximum = minimum
	}
	result := minimum
	for index := 0; index < attempt && result < maximum; index++ {
		if result > maximum/2 {
			return maximum
		}
		result *= 2
	}
	if result > maximum {
		return maximum
	}
	return result
}
