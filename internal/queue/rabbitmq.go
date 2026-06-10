package queue

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const defaultPublisherChannels = 8

// RabbitMQPublisher publishes envelopes to RabbitMQ using a bounded pool of
// confirm channels so requests publish concurrently instead of serialising
// through a single channel. A dropped connection is recovered proactively in
// the background (NotifyClose) as well as lazily on the next publish.
type RabbitMQPublisher struct {
	cfg RabbitMQConfig

	tlsConfig *tls.Config

	mu   sync.RWMutex
	conn *amqp.Connection

	pool      chan *publishChannel
	closeOnce sync.Once
	closed    chan struct{}
}

// publishChannel is a confirm-enabled channel owned by exactly one in-flight
// publish at a time, which keeps confirm correlation trivial (one outstanding
// publish per channel).
type publishChannel struct {
	ch       *amqp.Channel
	conn     *amqp.Connection
	confirms <-chan amqp.Confirmation
}

func NewRabbitMQPublisher(ctx context.Context, cfg RabbitMQConfig) (*RabbitMQPublisher, error) {
	size := cfg.PublisherChannels
	if size <= 0 {
		size = defaultPublisherChannels
	}
	// Build TLS config eagerly so a bad CA/cert path fails fast at startup
	// rather than surfacing only on the first publish.
	tlsConfig, err := buildTLSConfig(cfg.TLS)
	if err != nil {
		return nil, err
	}
	pub := &RabbitMQPublisher{
		cfg:       cfg,
		tlsConfig: tlsConfig,
		pool:      make(chan *publishChannel, size),
		closed:    make(chan struct{}),
	}
	// Pre-fill the pool with empty slots; channels are created lazily on first
	// use so an unavailable broker at startup is tolerated.
	for i := 0; i < size; i++ {
		pub.pool <- nil
	}
	// Best-effort eager connect so the first request is fast; failures are fine
	// because publish and readiness both reconnect on demand.
	_, _ = pub.connection(ctx)
	return pub, nil
}

func (p *RabbitMQPublisher) Publish(ctx context.Context, destinationQueueID, queueName string, msg Message) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	timeout := p.cfg.PublishTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var last error
	attempts := p.retryAttempts()
	delay := p.initialBackoff()
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 && p.cfg.Metrics != nil {
			p.cfg.Metrics.RabbitMQPublishRetry()
		}
		last = p.publishOnce(ctx, queueName, msg, body)
		if last == nil {
			if p.isDeadLetterRoute(queueName) && p.cfg.Metrics != nil {
				p.cfg.Metrics.RabbitMQDeadLetterPublish()
			}
			return nil
		}
		if attempt == attempts {
			break
		}
		if err := sleepContext(ctx, delay); err != nil {
			return err
		}
		delay = nextBackoff(delay, p.maxBackoff())
	}
	return last
}

// Ready reports broker reachability without dialing under the publish path.
// It reflects the current connection state (kept fresh by the reconnect
// watcher) and only dials when no connection has been established yet, so
// readiness probes never contend with in-flight publish round-trips.
func (p *RabbitMQPublisher) Ready(ctx context.Context) error {
	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()
	if conn != nil && !conn.IsClosed() {
		return nil
	}
	_, err := p.connection(ctx)
	return err
}

func (p *RabbitMQPublisher) Close() error {
	p.closeOnce.Do(func() { close(p.closed) })
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		err := p.conn.Close()
		p.conn = nil
		return err
	}
	return nil
}

func (p *RabbitMQPublisher) publishOnce(ctx context.Context, queueName string, msg Message, body []byte) error {
	pc, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	broken := false
	defer func() { p.release(pc, broken) }()

	var headers amqp.Table
	if msg.IdempotencyKey != "" {
		headers = amqp.Table{"x-idempotency-key": msg.IdempotencyKey}
	}
	if err := pc.ch.PublishWithContext(ctx, p.cfg.Exchange, queueName, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    msg.ReceivedAt,
		MessageId:    msg.ID,
		Type:         msg.Endpoint,
		Headers:      headers,
		Body:         body,
	}); err != nil {
		broken = true
		return err
	}
	select {
	case <-ctx.Done():
		broken = true
		return ctx.Err()
	case ack, ok := <-pc.confirms:
		if !ok {
			broken = true
			return errors.New("rabbitmq publisher confirms closed")
		}
		if !ack.Ack {
			return fmt.Errorf("rabbitmq publish nack delivery_tag=%d", ack.DeliveryTag)
		}
		if p.cfg.Metrics != nil {
			p.cfg.Metrics.RabbitMQPublishConfirm()
		}
		return nil
	}
}

// acquire checks out a usable confirm channel, recreating it when the slot is
// empty or its channel/connection is stale. Blocking on an empty pool provides
// natural backpressure at PublisherChannels concurrency.
func (p *RabbitMQPublisher) acquire(ctx context.Context) (*publishChannel, error) {
	var pc *publishChannel
	select {
	case pc = <-p.pool:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	conn, err := p.connection(ctx)
	if err != nil {
		// Return the slot so capacity is preserved; the stale channel is
		// rebuilt on a later acquire once the connection is back.
		p.pool <- pc
		return nil, err
	}
	if pc != nil && pc.conn == conn && !pc.ch.IsClosed() {
		return pc, nil
	}
	if pc != nil && pc.ch != nil {
		_ = pc.ch.Close()
	}
	fresh, err := p.openChannel(conn)
	if err != nil {
		p.pool <- nil
		return nil, err
	}
	return fresh, nil
}

func (p *RabbitMQPublisher) release(pc *publishChannel, broken bool) {
	if broken {
		if pc != nil && pc.ch != nil {
			_ = pc.ch.Close()
		}
		p.pool <- nil
		return
	}
	p.pool <- pc
}

func (p *RabbitMQPublisher) openChannel(conn *amqp.Connection) (*publishChannel, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		return nil, err
	}
	confirms := ch.NotifyPublish(make(chan amqp.Confirmation, 1))
	return &publishChannel{ch: ch, conn: conn, confirms: confirms}, nil
}

// connection returns a live connection, dialing once under the write lock when
// needed. Concurrent callers coalesce: only the first dials, the rest observe
// the established connection.
func (p *RabbitMQPublisher) connection(ctx context.Context) (*amqp.Connection, error) {
	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()
	if conn != nil && !conn.IsClosed() {
		return conn, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil && !p.conn.IsClosed() {
		return p.conn, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.closed:
		return nil, errors.New("rabbitmq publisher closed")
	default:
	}
	dialed, err := p.dialBroker()
	if err != nil {
		return nil, err
	}
	if err := p.declareTopology(dialed); err != nil {
		_ = dialed.Close()
		return nil, err
	}
	p.conn = dialed
	if p.cfg.Metrics != nil {
		p.cfg.Metrics.RabbitMQReconnect()
	}
	go p.watchClose(dialed)
	return dialed, nil
}

// dialBroker opens the AMQP connection, using the custom TLS config when one
// was built. With no custom TLS options, amqp.Dial handles amqps:// using the
// system trust store.
func (p *RabbitMQPublisher) dialBroker() (*amqp.Connection, error) {
	if p.tlsConfig != nil {
		return amqp.DialTLS(p.cfg.URL, p.tlsConfig)
	}
	return amqp.Dial(p.cfg.URL)
}

// buildTLSConfig assembles a *tls.Config from the configured options, or returns
// nil when no TLS customisation is requested (amqp.Dial then uses system roots
// for amqps://). Unreadable or invalid CA/cert material is a hard error.
func buildTLSConfig(c RabbitMQTLSConfig) (*tls.Config, error) {
	if c.CACert == "" && c.ClientCert == "" && c.ClientKey == "" && c.ServerName == "" && !c.InsecureSkipVerify {
		return nil, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.ServerName != "" {
		cfg.ServerName = c.ServerName
	}
	if c.InsecureSkipVerify {
		cfg.InsecureSkipVerify = true
	}
	if c.CACert != "" {
		pem, err := os.ReadFile(c.CACert)
		if err != nil {
			return nil, fmt.Errorf("read rabbitmq tls ca_cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("rabbitmq tls ca_cert %q contained no valid certificates", c.CACert)
		}
		cfg.RootCAs = pool
	}
	if c.ClientCert != "" || c.ClientKey != "" {
		if c.ClientCert == "" || c.ClientKey == "" {
			return nil, errors.New("rabbitmq tls client_cert and client_key must both be set for mutual TLS")
		}
		cert, err := tls.LoadX509KeyPair(c.ClientCert, c.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load rabbitmq tls client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// watchClose proactively re-establishes the connection when the broker drops it,
// rather than waiting for the next publish to discover the failure.
func (p *RabbitMQPublisher) watchClose(conn *amqp.Connection) {
	notify := conn.NotifyClose(make(chan *amqp.Error, 1))
	select {
	case <-p.closed:
		return
	case _, ok := <-notify:
		if !ok {
			return
		}
	}
	delay := p.initialBackoff()
	for {
		select {
		case <-p.closed:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), p.dialTimeout())
		_, err := p.connection(ctx)
		cancel()
		if err == nil {
			return
		}
		if waitErr := sleepContextOrClosed(p.closed, delay); waitErr != nil {
			return
		}
		delay = nextBackoff(delay, p.maxBackoff())
	}
}

// declareTopology declares the exchange and any managed queues/DLX once per
// connection on a short-lived channel, independent of the publish channels.
func (p *RabbitMQPublisher) declareTopology(conn *amqp.Connection) error {
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err := ch.ExchangeDeclare(p.cfg.Exchange, p.cfg.ExchangeType, p.cfg.Durable, false, false, false, nil); err != nil {
		return err
	}
	for _, declaration := range p.queueDeclarations() {
		if declaration.Name == "" || !declaration.CreateQueue {
			continue
		}
		durable := p.queueDurable(declaration.Durable)
		args := amqp.Table{}
		if declaration.DLQ != nil {
			if declaration.DLQ.Exchange != "" {
				args["x-dead-letter-exchange"] = declaration.DLQ.Exchange
			}
			deadLetterRoutingKey := declaration.DLQ.RoutingKey
			if declaration.Options.DeadLetterRoutingKey != "" {
				deadLetterRoutingKey = declaration.Options.DeadLetterRoutingKey
			}
			if deadLetterRoutingKey != "" {
				args["x-dead-letter-routing-key"] = deadLetterRoutingKey
			}
			if err := p.declareDeadLetter(ch, declaration.DLQ); err != nil {
				return err
			}
		}
		addQueueOptionArgs(args, declaration.Options)
		if _, err := ch.QueueDeclare(declaration.Name, durable, false, false, false, args); err != nil {
			return err
		}
		if err := ch.QueueBind(declaration.Name, declaration.Name, p.cfg.Exchange, false, nil); err != nil {
			return err
		}
	}
	return nil
}

func addQueueOptionArgs(args amqp.Table, opts QueueOptions) {
	if opts.MessageTTL > 0 {
		args["x-message-ttl"] = amqpInt(opts.MessageTTL.Milliseconds())
	}
	if opts.Expires > 0 {
		args["x-expires"] = amqpInt(opts.Expires.Milliseconds())
	}
	if opts.MaxLength > 0 {
		args["x-max-length"] = amqpInt(opts.MaxLength)
	}
	if opts.Overflow != "" {
		args["x-overflow"] = opts.Overflow
	}
}

func amqpInt(value int64) any {
	if value <= int64(^uint32(0)>>1) {
		return int32(value)
	}
	return value
}

func (p *RabbitMQPublisher) declareDeadLetter(ch *amqp.Channel, dlq *DeadLetterDeclaration) error {
	if dlq.Exchange != "" {
		exchangeType := dlq.ExchangeType
		if exchangeType == "" {
			exchangeType = "direct"
		}
		if err := ch.ExchangeDeclare(dlq.Exchange, exchangeType, p.queueDurable(dlq.Durable), false, false, false, nil); err != nil {
			return err
		}
	}
	if !dlq.CreateQueue || dlq.Queue == "" {
		return nil
	}
	if _, err := ch.QueueDeclare(dlq.Queue, p.queueDurable(dlq.Durable), false, false, false, nil); err != nil {
		return err
	}
	if dlq.Exchange == "" {
		return nil
	}
	routingKey := dlq.RoutingKey
	if routingKey == "" {
		routingKey = dlq.Queue
	}
	return ch.QueueBind(dlq.Queue, routingKey, dlq.Exchange, false, nil)
}

func (p *RabbitMQPublisher) queueDeclarations() []QueueDeclaration {
	if len(p.cfg.QueueDeclarations) > 0 {
		return p.cfg.QueueDeclarations
	}
	if !p.cfg.DeclareQueues {
		return nil
	}
	declarations := make([]QueueDeclaration, 0, len(p.cfg.Queues))
	for _, queueName := range p.cfg.Queues {
		declarations = append(declarations, QueueDeclaration{Name: queueName, CreateQueue: true})
	}
	return declarations
}

func (p *RabbitMQPublisher) queueDurable(durable *bool) bool {
	if durable != nil {
		return *durable
	}
	return p.cfg.Durable
}

func (p *RabbitMQPublisher) isDeadLetterRoute(queueName string) bool {
	for _, declaration := range p.queueDeclarations() {
		if declaration.DLQ == nil {
			continue
		}
		if queueName == declaration.DLQ.Queue || queueName == declaration.DLQ.RoutingKey {
			return true
		}
	}
	return false
}

func (p *RabbitMQPublisher) retryAttempts() int {
	if p.cfg.Retry.MaxAttempts > 0 {
		return p.cfg.Retry.MaxAttempts
	}
	return 1
}

func (p *RabbitMQPublisher) initialBackoff() time.Duration {
	if p.cfg.Retry.InitialBackoff > 0 {
		return p.cfg.Retry.InitialBackoff
	}
	return 100 * time.Millisecond
}

func (p *RabbitMQPublisher) maxBackoff() time.Duration {
	if p.cfg.Retry.MaxBackoff > 0 {
		return p.cfg.Retry.MaxBackoff
	}
	return 2 * time.Second
}

func (p *RabbitMQPublisher) dialTimeout() time.Duration {
	if p.cfg.PublishTimeout > 0 {
		return p.cfg.PublishTimeout
	}
	return 5 * time.Second
}

func nextBackoff(delay, maxDelay time.Duration) time.Duration {
	if delay <= 0 {
		delay = 100 * time.Millisecond
	}
	delay *= 2
	if maxDelay > 0 && delay > maxDelay {
		return maxDelay
	}
	return delay
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func sleepContextOrClosed(closed <-chan struct{}, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-closed:
		return errors.New("closed")
	case <-timer.C:
		return nil
	}
}
