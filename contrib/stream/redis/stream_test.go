package stream

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestNormalize(t *testing.T) {
	got := normalize([]redis.XStream{
		{
			Stream: "jobs",
			Messages: []redis.XMessage{
				{ID: "1-0", Values: map[string]any{"type": "sync"}},
				{ID: "2-0", Values: map[string]any{"type": "webhook"}},
			},
		},
	})

	want := []Message{
		{Stream: "jobs", ID: "1-0", Values: map[string]any{"type": "sync"}},
		{Stream: "jobs", ID: "2-0", Values: map[string]any{"type": "webhook"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalize mismatch\nwant=%#v\ngot=%#v", want, got)
	}
}

func TestClaimStaleRequiresPositiveMinIdle(t *testing.T) {
	_, _, err := ClaimStale(context.Background(), nil, ClaimConfig{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestIsBusyGroup(t *testing.T) {
	if !isBusyGroup(errors.New("BUSYGROUP Consumer Group name already exists")) {
		t.Fatalf("expected BUSYGROUP error to be ignored")
	}
	if isBusyGroup(errors.New("ERR unknown command")) {
		t.Fatalf("unexpected busy group")
	}
	if isBusyGroup(nil) {
		t.Fatalf("nil is not busy group")
	}
}
