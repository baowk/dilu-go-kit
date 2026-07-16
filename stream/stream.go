// Package stream provides small Redis Stream helpers for background jobs.
package stream

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	DefaultBlock = 5 * time.Second
	DefaultCount = 10
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
	return rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: values,
		MaxLen: maxLenApprox,
		Approx: maxLenApprox > 0,
	}).Result()
}

// EnsureGroup creates a consumer group and stream if needed.
// startID is usually "0" for replay or "$" for new messages only.
func EnsureGroup(ctx context.Context, rdb redis.Cmdable, stream, group, startID string) error {
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
	count := cfg.Count
	if count <= 0 {
		count = DefaultCount
	}
	block := cfg.Block
	if block <= 0 {
		block = DefaultBlock
	}
	result, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    cfg.Group,
		Consumer: cfg.Consumer,
		Streams:  []string{cfg.Stream, ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return normalize(result), nil
}

// ClaimStale transfers pending messages idle for at least MinIdle to Consumer.
// Pass the returned cursor back as Start until it returns "0-0".
func ClaimStale(ctx context.Context, rdb redis.Cmdable, cfg ClaimConfig) ([]Message, string, error) {
	if cfg.MinIdle <= 0 {
		return nil, "", errors.New("stream: min idle must be positive")
	}
	if cfg.Start == "" {
		cfg.Start = "0-0"
	}
	if cfg.Count <= 0 {
		cfg.Count = DefaultCount
	}
	messages, next, err := rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: cfg.Stream, Group: cfg.Group, Consumer: cfg.Consumer,
		MinIdle: cfg.MinIdle, Start: cfg.Start, Count: cfg.Count,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, next, nil
	}
	if err != nil {
		return nil, next, err
	}
	return normalize([]redis.XStream{{Stream: cfg.Stream, Messages: messages}}), next, nil
}

// Ack acknowledges messages in a consumer group.
func Ack(ctx context.Context, rdb redis.Cmdable, stream, group string, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return rdb.XAck(ctx, stream, group, ids...).Err()
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
				Values: xm.Values,
			})
		}
	}
	return out
}

func isBusyGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), "BUSYGROUP")
}
