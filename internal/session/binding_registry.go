package session

import "sync"

var threadBindingAdapters sync.Map

func RegisterThreadBindingAdapter(adapter ThreadBindingAdapter) {
	if adapter == nil {
		return
	}
	threadBindingAdapters.Store(
		normalizeThreadBindingAdapterKey(adapter.Channel(), adapter.AccountID()),
		adapter,
	)
}

func UnregisterThreadBindingAdapter(channel string, accountID string) {
	threadBindingAdapters.Delete(normalizeThreadBindingAdapterKey(channel, accountID))
}

func resolveThreadBindingAdapter(channel string, accountID string) (ThreadBindingAdapter, bool) {
	value, ok := threadBindingAdapters.Load(normalizeThreadBindingAdapterKey(channel, accountID))
	if !ok || value == nil {
		return nil, false
	}
	adapter, ok := value.(ThreadBindingAdapter)
	return adapter, ok && adapter != nil
}

