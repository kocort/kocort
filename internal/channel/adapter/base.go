package adapter

import (
	"context"
	"net/http"

	"github.com/kocort/kocort/internal/infra"
)

// BaseAdapter provides safe no-op defaults for every ChannelAdapter method.
// Embed it in concrete adapters and override only the methods you support.
//
// Example:
//
//	type MyAdapter struct {
//	    adapter.BaseAdapter
//	}
//
//	func NewMyAdapter() *MyAdapter {
//	    return &MyAdapter{BaseAdapter: adapter.NewBaseAdapter("my-channel")}
//	}
type BaseAdapter struct {
	id string
}

// NewBaseAdapter returns a BaseAdapter with the given driver ID.
func NewBaseAdapter(id string) BaseAdapter {
	return BaseAdapter{id: id}
}

func (b BaseAdapter) ID() string { return b.id }

func (b BaseAdapter) Schema() ChannelDriverSchema {
	return ChannelDriverSchema{ID: b.id, Name: b.id}
}

func (b BaseAdapter) StartBackground(_ context.Context, _ string, _ ChannelConfig, _ *infra.DynamicHTTPClient, _ Callbacks) error {
	return ErrNotImplemented
}

func (b BaseAdapter) StopBackground() {}

func (b BaseAdapter) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, ErrNotImplemented.Error(), http.StatusNotImplemented)
}

func (b BaseAdapter) SendText(_ context.Context, _ OutboundMessage, _ ChannelConfig) (DeliveryResult, error) {
	return DeliveryResult{}, ErrNotImplemented
}

func (b BaseAdapter) SendMedia(_ context.Context, _ OutboundMessage, _ ChannelConfig) (DeliveryResult, error) {
	return DeliveryResult{}, ErrNotImplemented
}
