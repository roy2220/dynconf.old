// Package dynconf implements a dynamic configuration.
package dynconf

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/hashicorp/consul/api"
	"github.com/rs/zerolog"
)

// Watcher presents a watcher for configurations.
type Watcher struct {
	client *api.Client
	logger *zerolog.Logger
}

// Init initialize the watcher and then returns the watcher.
func (w *Watcher) Init(client *api.Client, logger *zerolog.Logger) *Watcher {
	w.client = client
	w.logger = logger
	return w
}

// AddWatch adds a watch on the given key and then returns the watch.
func (w *Watcher) AddWatch(ctx context.Context, key string, valueFactory ValueFactory) (*Watch, error) {
	watch := Watch{
		client:       w.client,
		logger:       w.logger,
		key:          key,
		valueFactory: valueFactory,
	}

	if err := watch.populateValue(ctx); err != nil {
		return nil, err
	}

	watch.add()
	return &watch, nil
}

// Watch presents a watch on a key.
type Watch struct {
	client           *api.Client
	logger           *zerolog.Logger
	key              string
	valueFactory     ValueFactory
	latestValue      atomic.Value
	latestValueIndex uint64
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
}

// Remove removes the watch.
func (w *Watch) Remove() {
	w.cancel()
	w.wg.Wait()
}

// Key returns the key on which the watch is set.
func (w *Watch) Key() string {
	return w.key
}

// LatestValue returns the latest value of the key on which the watch is set.
func (w *Watch) LatestValue() Value {
	return w.latestValue.Load().(Value)
}

func (w *Watch) populateValue(ctx context.Context) error {
	queryOptions := (&api.QueryOptions{}).WithContext(w.ctx)
	kvPair, _, err := w.client.KV().Get(w.key, queryOptions)

	if err != nil {
		return fmt.Errorf("dynconf: kv get failed: err=%q key=%q", err, w.key)
	}

	if kvPair == nil {
		return fmt.Errorf("%w: key=%q", ErrKeyNotFound, w.key)
	}

	value := w.valueFactory()

	if err := value.Unmarshal(kvPair.Value); err != nil {
		return fmt.Errorf("dynconf: value unmarshal failed: err=%q key=%q data=%q", err, w.key, kvPair.Value)
	}

	w.setValue(value)
	w.latestValueIndex = kvPair.ModifyIndex
	return nil
}

func (w *Watch) add() {
	w.ctx, w.cancel = context.WithCancel(context.Background())
	w.wg.Add(1)

	go func() {
		w.keepValueUpToDate()
		defer w.wg.Done()
	}()
}

func (w *Watch) keepValueUpToDate() {
	retry := retry{
		BackoffJitter: 0.5,
	}

	for {
		queryOptions := (&api.QueryOptions{
			WaitIndex: w.latestValueIndex,
		}).WithContext(w.ctx)

		var kvPair *api.KVPair

		if _, err := retry.Do(w.ctx, func() bool {
			var err error
			kvPair, _, err = w.client.KV().Get(w.key, queryOptions)

			if err != nil {
				w.logger.Warn().
					Err(err).
					Str("key", w.key).
					Msg("dynconf_kv_get_failed")
				return false
			}

			if kvPair == nil {
				w.logger.Error().
					Str("key", w.key).
					Msg("dynconf_key_not_found")
				return false
			}

			return true
		}); err != nil {
			w.logger.Info().
				Str("key", w.key).
				Msg("dynconf_watch_removed")
			return
		}

		if kvPair.ModifyIndex == w.latestValueIndex {
			continue
		}

		newValue := w.valueFactory()

		if err := newValue.Unmarshal(kvPair.Value); err == nil {
			w.logger.Info().
				Str("key", w.key).
				Str("new_value", newValue.String()).
				Msg("dynconf_value_updated")
			oldValue := w.LatestValue()
			w.setValue(newValue)

			if callback, ok := oldValue.(ValueOutdatedCallback); ok {
				callback.OnOutdated()
			}
		} else {
			w.logger.Err(err).
				Str("key", w.key).
				Bytes("data", kvPair.Value).
				Msg("dynconf_value_unmarshal_failed")
		}

		if kvPair.ModifyIndex < w.latestValueIndex {
			kvPair.ModifyIndex = 0
		}

		w.latestValueIndex = kvPair.ModifyIndex
	}
}

func (w *Watch) setValue(value Value) {
	w.latestValue.Store(value)
}

// ValueFactory is the type of the function returning a new value.
type ValueFactory func() Value

// Value represents a structured value of a key.
type Value interface {
	// Unmarshal unmarshals the value from the given data.
	Unmarshal(data []byte) (err error)

	// String returns a string representing the value.
	String() string
}

// ValueOutdatedCallback represents an optional callback to Value.
type ValueOutdatedCallback interface {
	// OnOutdated is called when the value is out of date.
	OnOutdated()
}

// ErrKeyNotFound is returned when a key has not been found.
var ErrKeyNotFound = errors.New("dynconf: key not found")
