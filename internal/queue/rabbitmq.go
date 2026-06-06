package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type RabbitMQPublisher struct {
	cfg  RabbitMQConfig
	mu   sync.Mutex
	conn *amqp.Connection
	ch   *amqp.Channel
	acks <-chan amqp.Confirmation
}

func NewRabbitMQPublisher(ctx context.Context, cfg RabbitMQConfig) (*RabbitMQPublisher, error) {
	pub := &RabbitMQPublisher{cfg: cfg}
	_ = pub.connect(ctx)
	return pub, nil
}

func (p *RabbitMQPublisher) Publish(ctx context.Context, queueName string, msg Message) error {
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
		p.resetChannel()
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

func (p *RabbitMQPublisher) Ready(ctx context.Context) error {
	return p.ensure(ctx)
}

func (p *RabbitMQPublisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var err error
	if p.ch != nil {
		err = errors.Join(err, p.ch.Close())
	}
	if p.conn != nil {
		err = errors.Join(err, p.conn.Close())
	}
	return err
}

func (p *RabbitMQPublisher) ensure(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil && !p.conn.IsClosed() && p.ch != nil && !p.ch.IsClosed() {
		return nil
	}
	return p.connectLocked(ctx)
}

func (p *RabbitMQPublisher) connect(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connectLocked(ctx)
}

func (p *RabbitMQPublisher) connectLocked(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	conn, err := amqp.Dial(p.cfg.URL)
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return err
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return err
	}
	if err := p.declare(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return err
	}
	p.conn = conn
	p.ch = ch
	p.acks = ch.NotifyPublish(make(chan amqp.Confirmation, 1))
	if p.cfg.Metrics != nil {
		p.cfg.Metrics.RabbitMQReconnect()
	}
	return nil
}

func (p *RabbitMQPublisher) declare(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(p.cfg.Exchange, p.cfg.ExchangeType, p.cfg.Durable, false, false, false, nil); err != nil {
		return err
	}
	declarations := p.queueDeclarations()
	if len(declarations) == 0 {
		return nil
	}
	for _, declaration := range declarations {
		if declaration.Name == "" || !declaration.CreateQueue {
			continue
		}
		durable := p.queueDurable(declaration.Durable)
		args := amqp.Table{}
		if declaration.DLQ != nil {
			if declaration.DLQ.Exchange != "" {
				args["x-dead-letter-exchange"] = declaration.DLQ.Exchange
			}
			if declaration.DLQ.RoutingKey != "" {
				args["x-dead-letter-routing-key"] = declaration.DLQ.RoutingKey
			}
			if err := p.declareDeadLetter(ch, declaration.DLQ); err != nil {
				return err
			}
		}
		if _, err := ch.QueueDeclare(declaration.Name, durable, false, false, false, args); err != nil {
			return err
		}
		if err := ch.QueueBind(declaration.Name, declaration.Name, p.cfg.Exchange, false, nil); err != nil {
			return err
		}
	}
	return nil
}

func (p *RabbitMQPublisher) publishOnce(ctx context.Context, queueName string, msg Message, body []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil || p.conn.IsClosed() || p.ch == nil || p.ch.IsClosed() {
		if err := p.connectLocked(ctx); err != nil {
			return err
		}
	}
	if err := p.ch.PublishWithContext(ctx, p.cfg.Exchange, queueName, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    msg.ReceivedAt,
		MessageId:    msg.ID,
		Type:         msg.Endpoint,
		Body:         body,
	}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ack, ok := <-p.acks:
		if !ok {
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

func (p *RabbitMQPublisher) resetChannel() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ch != nil {
		_ = p.ch.Close()
	}
	p.ch = nil
	p.acks = nil
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
