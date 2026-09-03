// Package stream provides small Redis Stream helpers for background jobs.
package stream

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	DefaultBlock           = 5 * time.Second
	DefaultCount           = 10
	DefaultMaxMessageBytes = 1 << 20
)

// ReaderConfig describes a Redis Stream consumer group read.
type ReaderConfig struct {
	Stream   string
	Group    string
	Consumer string
	Count    int64
	Block    time.Duration
}

// ClaimConfig describes recovery of stale pending messages.
type ClaimConfig struct {
	Stream   string
	Group    string
	Consumer string
	MinIdle  time.Duration
	Start    string
	Count    int64
}

// Message is a normalized Redis Stream message.
type Message struct {
	Stream string
	ID     string
	Values map[string]any
}

// Publish appends a message to a stream.
func Publish(ctx context.Context, rdb redis.Cmdable, stream string, values map[string]any) (string, error) {
	return PublishMaxLen(ctx, rdb, stream, values, 0)
}

// PublishMaxLen appends a message with approximate max length trimming.
func PublishMaxLen(ctx context.Context, rdb redis.Cmdable, stream string, values map[string]any, maxLenApprox int64) (string, error) {
	if rdb == nil {
		return "", errors.New("stream: redis client is required")
	}
	if err := validateStreamName(stream); err != nil {
		return "", err
	}
	var id string
	err := withSpanKind(ctx, "redis.stream.publish", trace.SpanKindProducer, []attribute.KeyValue{attribute.String("messaging.system", "redis"), attribute.String("messaging.destination.name", stream)}, func(ctx context.Context) error {
		values = InjectMessageContext(ctx, values)
		if err := validateMessageValues(values); err != nil {
			return err
		}
		var err error
		id, err = rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: stream,
			Values: values,
			MaxLen: maxLenApprox,
			Approx: maxLenApprox > 0,
		}).Result()
		return err
	})
	return id, err
}

// EnsureGroup creates a consumer group and stream if needed.
// startID is usually "0" for replay or "$" for new messages only.
func EnsureGroup(ctx context.Context, rdb redis.Cmdable, stream, group, startID string) error {
	if rdb == nil {
		return errors.New("stream: redis client is required")
	}
	if err := validateStreamName(stream); err != nil {
		return err
	}
	if strings.TrimSpace(group) == "" || len(group) > 128 {
		return errors.New("stream: invalid consumer group")
	}
	if startID == "" {
		startID = "0"
	}
	err := rdb.XGroupCreateMkStream(ctx, stream, group, startID).Err()
	if isBusyGroup(err) {
		return nil
	}
	return err
}

// ReadGroup reads messages from a consumer group.
func ReadGroup(ctx context.Context, rdb redis.Cmdable, cfg ReaderConfig) ([]Message, error) {
	if rdb == nil {
		return nil, errors.New("stream: redis client is required")
	}
	if err := validateStreamName(cfg.Stream); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Group) == "" || strings.TrimSpace(cfg.Consumer) == "" {
		return nil, errors.New("stream: group and consumer are required")
	}
	count := cfg.Count
	if count <= 0 {
		count = DefaultCount
	}
	if count > 1000 {
		count = 1000
	}
	block := cfg.Block
	if block <= 0 {
		block = DefaultBlock
	}
	var result []redis.XStream
	err := withSpanKind(ctx, "redis.stream.read_group", trace.SpanKindConsumer, []attribute.KeyValue{attribute.String("messaging.system", "redis"), attribute.String("messaging.destination.name", cfg.Stream)}, func(ctx context.Context) error {
		var err error
		result, err = rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    cfg.Group,
			Consumer: cfg.Consumer,
			Streams:  []string{cfg.Stream, ">"},
			Count:    count,
			Block:    block,
		}).Result()
		return err
	})
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	messages := normalize(result)
	for _, message := range messages {
		if err := validateMessageValues(message.Values); err != nil {
			return nil, err
		}
	}
	return messages, nil
}

// ClaimStale transfers pending messages idle for at least MinIdle to Consumer.
// Pass the returned cursor back as Start until it returns "0-0".
func ClaimStale(ctx context.Context, rdb redis.Cmdable, cfg ClaimConfig) ([]Message, string, error) {
	if rdb == nil {
		return nil, "", errors.New("stream: redis client is required")
	}
	if err := validateStreamName(cfg.Stream); err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(cfg.Group) == "" || strings.TrimSpace(cfg.Consumer) == "" {
		return nil, "", errors.New("stream: group and consumer are required")
	}
	if cfg.MinIdle <= 0 {
		return nil, "", errors.New("stream: min idle must be positive")
	}
	if cfg.Start == "" {
		cfg.Start = "0-0"
	}
	if cfg.Count <= 0 {
		cfg.Count = DefaultCount
	}
	if cfg.Count > 1000 {
		cfg.Count = 1000
	}
	var messages []redis.XMessage
	var next string
	err := withSpanKind(ctx, "redis.stream.claim_stale", trace.SpanKindConsumer, []attribute.KeyValue{attribute.String("messaging.system", "redis"), attribute.String("messaging.destination.name", cfg.Stream)}, func(ctx context.Context) error {
		var err error
		messages, next, err = rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: cfg.Stream, Group: cfg.Group, Consumer: cfg.Consumer,
			MinIdle: cfg.MinIdle, Start: cfg.Start, Count: cfg.Count,
		}).Result()
		return err
	})
	if errors.Is(err, redis.Nil) {
		return nil, next, nil
	}
	if err != nil {
		return nil, next, err
	}
	normalized := normalize([]redis.XStream{{Stream: cfg.Stream, Messages: messages}})
	for _, message := range normalized {
		if err := validateMessageValues(message.Values); err != nil {
			return nil, next, err
		}
	}
	return normalized, next, nil
}

// Ack acknowledges messages in a consumer group.
func Ack(ctx context.Context, rdb redis.Cmdable, stream, group string, ids ...string) error {
	if rdb == nil {
		return errors.New("stream: redis client is required")
	}
	if err := validateStreamName(stream); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return withSpanKind(ctx, "redis.stream.ack", trace.SpanKindConsumer, []attribute.KeyValue{attribute.String("messaging.system", "redis"), attribute.String("messaging.destination.name", stream)}, func(ctx context.Context) error {
		return rdb.XAck(ctx, stream, group, ids...).Err()
	})
}

func withSpanKind(ctx context.Context, name string, kind trace.SpanKind, attrs []attribute.KeyValue, fn func(context.Context) error) error {
	if p := providerFromContext(ctx); p != nil {
		return p.InSpan(ctx, name, kind, attrs, fn)
	}
	return fn(ctx)
}

// ToDeadLetter copies a failed message to a dead-letter stream.
func ToDeadLetter(ctx context.Context, rdb redis.Cmdable, deadStream string, msg Message, extra map[string]any) (string, error) {
	return addDeadLetter(ctx, rdb, deadStream, msg, extra, false, "")
}

// DeadLetterAndAck atomically appends to the dead-letter stream and ACKs the
// source message. In Redis Cluster both stream keys must share a hash slot.
func DeadLetterAndAck(ctx context.Context, rdb redis.Cmdable, group, deadStream string, msg Message, extra map[string]any) (string, error) {
	return addDeadLetter(ctx, rdb, deadStream, msg, extra, true, group)
}

func addDeadLetter(ctx context.Context, rdb redis.Cmdable, deadStream string, msg Message, extra map[string]any, ack bool, group string) (string, error) {
	values := make(map[string]any, len(msg.Values)+len(extra)+2)
	for k, v := range msg.Values {
		values[k] = v
	}
	for k, v := range extra {
		values[k] = v
	}
	values["source_stream"] = msg.Stream
	values["source_id"] = msg.ID
	if err := validateMessageValues(values); err != nil {
		return "", err
	}
	if !ack {
		return Publish(ctx, rdb, deadStream, values)
	}
	if group == "" || msg.Stream == "" || msg.ID == "" {
		return "", errors.New("stream: group, source stream, and source ID are required")
	}
	var addCmd *redis.StringCmd
	_, err := rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		addCmd = pipe.XAdd(ctx, &redis.XAddArgs{Stream: deadStream, Values: values})
		pipe.XAck(ctx, msg.Stream, group, msg.ID)
		return nil
	})
	if err != nil {
		return "", err
	}
	return addCmd.Val(), nil
}

func normalize(streams []redis.XStream) []Message {
	out := make([]Message, 0)
	for _, xs := range streams {
		for _, xm := range xs.Messages {
			out = append(out, Message{
				Stream: xs.Stream,
				ID:     xm.ID,
				Values: cloneValues(xm.Values),
			})
		}
	}
	return out
}

func validateMessageValues(values map[string]any) error {
	if len(values) == 0 {
		return errors.New("stream: message values are required")
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	if len(encoded) > DefaultMaxMessageBytes {
		return errors.New("stream: message exceeds 1 MiB limit")
	}
	return nil
}

func cloneValues(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func isBusyGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), "BUSYGROUP")
}

func validateStreamName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 256 || strings.ContainsAny(name, "\r\n") {
		return errors.New("stream: invalid stream name")
	}
	return nil
}
