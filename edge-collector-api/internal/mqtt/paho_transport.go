package mqtt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	autopaho "github.com/eclipse/paho.golang/autopaho"
	paho5 "github.com/eclipse/paho.golang/paho"
	paho311 "github.com/eclipse/paho.mqtt.golang"
)

func NewPahoTransportFactory() TransportFactory {
	return func(ctx context.Context, config RuntimeConfig, callbacks TransportCallbacks) (Transport, error) {
		switch config.ProtocolVersion {
		case ProtocolMQTT311:
			return newPaho311Transport(ctx, config, callbacks)
		case ProtocolMQTT5:
			return newPaho5Transport(ctx, config, callbacks)
		default:
			return nil, fmt.Errorf("unsupported MQTT protocol %q", config.ProtocolVersion)
		}
	}
}

type paho5Transport struct {
	mu      sync.RWMutex
	manager *autopaho.ConnectionManager
	cancel  context.CancelFunc
}

func newPaho5Transport(parent context.Context, config RuntimeConfig, callbacks TransportCallbacks) (Transport, error) {
	brokerURL, err := normalizePaho5URL(config.BrokerURL)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := buildTLSConfig(config, brokerURL)
	if err != nil {
		return nil, err
	}
	commandTopic, err := NewTopicBuilder(config.TopicPrefix, config.EdgeID)
	if err != nil {
		return nil, err
	}
	transportContext, cancel := context.WithCancel(parent)
	transport := &paho5Transport{cancel: cancel}
	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     uint16(config.KeepAliveSeconds),
		CleanStartOnInitialConnection: false,
		ConnectTimeout:                time.Duration(config.ConnectTimeoutMS) * time.Millisecond,
		ReconnectBackoff: func(attempt int) time.Duration {
			return defaultBackoff(attempt, time.Duration(config.ReconnectMinMS)*time.Millisecond, time.Duration(config.ReconnectMaxMS)*time.Millisecond)
		},
		ConnectUsername: config.Username,
		ConnectPassword: []byte(config.Password),
		OnConnectionUp: func(manager *autopaho.ConnectionManager, _ *paho5.Connack) {
			transport.setManager(manager)
			if callbacks.OnConnected != nil {
				callbacks.OnConnected()
			}
		},
		OnConnectionDown: func() bool {
			if callbacks.OnDisconnected != nil {
				callbacks.OnDisconnected(nil)
			}
			return true
		},
		OnConnectError: func(err error) {
			if callbacks.OnDisconnected != nil {
				callbacks.OnDisconnected(err)
			}
		},
		ConnectPacketBuilder: func(connect *paho5.Connect, _ *url.URL) (*paho5.Connect, error) {
			will, err := BuildEdgeStatus(config.EdgeID, NewMessageID(time.Now().UTC()), time.Now().UTC(), false, "last_will")
			if err != nil {
				return nil, err
			}
			connect.WillMessage = &paho5.WillMessage{Topic: commandTopic.EdgeStatus(), QoS: 1, Retain: true, Payload: will}
			return connect, nil
		},
		ClientConfig: paho5.ClientConfig{
			OnPublishReceived: []func(paho5.PublishReceived) (bool, error){func(received paho5.PublishReceived) (bool, error) {
				if callbacks.OnMessage != nil && received.Packet != nil {
					callbacks.OnMessage(received.Packet.Topic, append([]byte(nil), received.Packet.Payload...))
				}
				return false, nil
			}},
		},
	}
	manager, err := autopaho.NewConnection(transportContext, clientConfig)
	if err != nil {
		cancel()
		return nil, err
	}
	transport.setManager(manager)
	return transport, nil
}

func (t *paho5Transport) setManager(manager *autopaho.ConnectionManager) {
	t.mu.Lock()
	t.manager = manager
	t.mu.Unlock()
}

func (t *paho5Transport) getManager() *autopaho.ConnectionManager {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.manager
}

func (t *paho5Transport) WaitConnected(ctx context.Context) error {
	manager := t.getManager()
	if manager == nil {
		return ErrMQTTNotConnected
	}
	return manager.AwaitConnection(ctx)
}

func (t *paho5Transport) Publish(ctx context.Context, publication Publication) error {
	manager := t.getManager()
	if manager == nil {
		return ErrMQTTNotConnected
	}
	_, err := manager.Publish(ctx, &paho5.Publish{Topic: publication.Topic, QoS: publication.QoS, Retain: publication.Retain, Payload: append([]byte(nil), publication.Payload...)})
	return err
}

func (t *paho5Transport) Subscribe(ctx context.Context, topic string, qos byte) error {
	manager := t.getManager()
	if manager == nil {
		return ErrMQTTNotConnected
	}
	_, err := manager.Subscribe(ctx, &paho5.Subscribe{Subscriptions: []paho5.SubscribeOptions{{Topic: topic, QoS: qos}}})
	return err
}

func (t *paho5Transport) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	manager := t.getManager()
	if manager == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return manager.Disconnect(ctx)
}

type paho311Transport struct {
	mu     sync.RWMutex
	client paho311.Client
	cancel context.CancelFunc
}

func newPaho311Transport(parent context.Context, config RuntimeConfig, callbacks TransportCallbacks) (Transport, error) {
	tlsConfig, err := buildTLSConfig(config, mustParseURL(config.BrokerURL))
	if err != nil {
		return nil, err
	}
	builder, err := NewTopicBuilder(config.TopicPrefix, config.EdgeID)
	if err != nil {
		return nil, err
	}
	transportContext, cancel := context.WithCancel(parent)
	transport := &paho311Transport{cancel: cancel}
	options := paho311.NewClientOptions().
		AddBroker(config.BrokerURL).
		SetClientID(config.ClientID).
		SetProtocolVersion(4).
		SetKeepAlive(time.Duration(config.KeepAliveSeconds) * time.Second).
		SetConnectTimeout(time.Duration(config.ConnectTimeoutMS) * time.Millisecond).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(time.Duration(config.ReconnectMaxMS) * time.Millisecond).
		SetOrderMatters(false).
		SetCleanSession(false)
	if config.Username != "" {
		options.SetUsername(config.Username)
	}
	if config.Password != "" {
		options.SetPassword(config.Password)
	}
	if tlsConfig != nil {
		options.SetTLSConfig(tlsConfig)
	}
	will, err := BuildEdgeStatus(config.EdgeID, NewMessageID(time.Now().UTC()), time.Now().UTC(), false, "last_will")
	if err != nil {
		cancel()
		return nil, err
	}
	options.SetBinaryWill(builder.EdgeStatus(), will, 1, true)
	options.SetDefaultPublishHandler(func(_ paho311.Client, message paho311.Message) {
		if callbacks.OnMessage != nil {
			callbacks.OnMessage(message.Topic(), append([]byte(nil), message.Payload()...))
		}
	})
	options.SetOnConnectHandler(func(client paho311.Client) {
		transport.setClient(client)
		if callbacks.OnConnected != nil {
			callbacks.OnConnected()
		}
	})
	options.SetConnectionLostHandler(func(_ paho311.Client, err error) {
		if callbacks.OnDisconnected != nil {
			callbacks.OnDisconnected(err)
		}
	})
	client := paho311.NewClient(options)
	transport.setClient(client)
	token := client.Connect()
	if !waitToken(transportContext, token, time.Duration(config.ConnectTimeoutMS)*time.Millisecond) {
		cancel()
		return nil, context.DeadlineExceeded
	}
	if err := token.Error(); err != nil {
		cancel()
		return nil, err
	}
	return transport, nil
}

func (t *paho311Transport) setClient(client paho311.Client) {
	t.mu.Lock()
	t.client = client
	t.mu.Unlock()
}

func (t *paho311Transport) getClient() paho311.Client {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.client
}

func (t *paho311Transport) WaitConnected(ctx context.Context) error {
	if client := t.getClient(); client != nil && client.IsConnectionOpen() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if client := t.getClient(); client != nil && client.IsConnectionOpen() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (t *paho311Transport) Publish(ctx context.Context, publication Publication) error {
	client := t.getClient()
	if client == nil || !client.IsConnectionOpen() {
		return ErrMQTTNotConnected
	}
	token := client.Publish(publication.Topic, publication.QoS, publication.Retain, publication.Payload)
	if !waitToken(ctx, token, time.Until(contextDeadline(ctx, 30*time.Second))) {
		return ctx.Err()
	}
	return token.Error()
}

func (t *paho311Transport) Subscribe(ctx context.Context, topic string, qos byte) error {
	client := t.getClient()
	if client == nil || !client.IsConnectionOpen() {
		return ErrMQTTNotConnected
	}
	token := client.Subscribe(topic, qos, nil)
	if !waitToken(ctx, token, time.Until(contextDeadline(ctx, 30*time.Second))) {
		return ctx.Err()
	}
	return token.Error()
}

func (t *paho311Transport) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	if client := t.getClient(); client != nil && client.IsConnectionOpen() {
		client.Disconnect(1000)
	}
	return nil
}

func waitToken(ctx context.Context, token paho311.Token, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	done := make(chan struct{})
	go func() {
		token.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func contextDeadline(ctx context.Context, fallback time.Duration) time.Time {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline
	}
	return time.Now().Add(fallback)
}

func normalizePaho5URL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid MQTT broker URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "mqtt", "ws":
		return parsed, nil
	case "mqtts", "tls", "ssl":
		parsed.Scheme = "tls"
		return parsed, nil
	case "wss":
		parsed.Scheme = "wss"
		return parsed, nil
	case "tcp":
		parsed.Scheme = "mqtt"
		return parsed, nil
	default:
		return nil, fmt.Errorf("unsupported MQTT broker URL scheme %q", parsed.Scheme)
	}
}

func mustParseURL(raw string) *url.URL {
	parsed, _ := url.Parse(raw)
	return parsed
}

func buildTLSConfig(config RuntimeConfig, brokerURL *url.URL) (*tls.Config, error) {
	parsedTLS := brokerURL != nil && (strings.EqualFold(brokerURL.Scheme, "mqtts") || strings.EqualFold(brokerURL.Scheme, "tls") || strings.EqualFold(brokerURL.Scheme, "ssl") || strings.EqualFold(brokerURL.Scheme, "wss"))
	if config.TLSEnabled == 0 && !parsedTLS && config.CACertificate == "" && config.ClientCertificate == "" && config.ClientPrivateKey == "" {
		return nil, nil
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if brokerURL != nil {
		tlsConfig.ServerName = brokerURL.Hostname()
	}
	if config.CACertificate != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(config.CACertificate)) {
			return nil, errors.New("invalid MQTT CA certificate")
		}
		tlsConfig.RootCAs = pool
	}
	if (config.ClientCertificate == "") != (config.ClientPrivateKey == "") {
		return nil, errors.New("MQTT client certificate and private key must be supplied together")
	}
	if config.ClientCertificate != "" {
		if block, _ := pem.Decode([]byte(config.ClientCertificate)); block == nil {
			return nil, errors.New("invalid MQTT client certificate PEM")
		}
		certificate, err := tls.X509KeyPair([]byte(config.ClientCertificate), []byte(config.ClientPrivateKey))
		if err != nil {
			return nil, errors.New("invalid MQTT client certificate or private key")
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	return tlsConfig, nil
}
