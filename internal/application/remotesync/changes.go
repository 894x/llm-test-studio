package remotesync

// SubscribeChanges signals committed local mirror/report changes. Notifications
// carry no remote payload; consumers re-read their bounded local projections.
// Observers must return promptly and may unsubscribe from their own callback.
func (watcher *Watcher) SubscribeChanges(observer func()) func() {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if observer == nil || watcher.closed {
		return func() {}
	}
	if watcher.listeners == nil {
		watcher.listeners = map[uint64]func(){}
	}
	watcher.nextListener++
	id := watcher.nextListener
	watcher.listeners[id] = observer
	return func() {
		watcher.mu.Lock()
		defer watcher.mu.Unlock()
		delete(watcher.listeners, id)
	}
}

func (watcher *Watcher) notifyChanged() {
	watcher.mu.Lock()
	listeners := []func(){}
	if !watcher.closed {
		for _, observer := range watcher.listeners {
			listeners = append(listeners, observer)
		}
	}
	watcher.mu.Unlock()
	for _, observer := range listeners {
		observer()
	}
}
