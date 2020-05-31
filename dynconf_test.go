package dynconf_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	"github.com/roy2220/dynconf"
)

func TestWatcherAddWatcher(t *testing.T) {
	wr, c := makeWatcher(t)
	_, err := wr.AddWatch(context.Background(), "hello", newValue)
	assert.EqualError(t, err, "dynconf: key not found: key=\"hello\"")

	_, err = c.KV().Put(&api.KVPair{
		Key:   "hello",
		Value: []byte(`bad json`),
	}, &api.WriteOptions{})
	assert.NoError(t, err)

	_, err = wr.AddWatch(context.Background(), "hello", newValue)
	assert.EqualError(t, err, "dynconf: value unmarshal failed: err=\"invalid character 'b' looking for beginning of value\" key=\"hello\" data=\"bad json\"")

	_, err = c.KV().Put(&api.KVPair{
		Key:   "hello",
		Value: []byte(`{}`),
	}, &api.WriteOptions{})
	assert.NoError(t, err)

	w, err := wr.AddWatch(context.Background(), "hello", newValue)
	if assert.NoError(t, err) {
		defer w.Remove()
	}

	assert.Equal(t, "hello", w.Key())
}

func TestWatchLatestValue(t *testing.T) {
	wr, c := makeWatcher(t)
	_, err := c.KV().Put(&api.KVPair{
		Key:   "hello2",
		Value: []byte(`{"Foo": 99, "Bar": "world"}`),
	}, &api.WriteOptions{})
	assert.NoError(t, err)
	w, err := wr.AddWatch(context.Background(), "hello2", newValue)
	if assert.NoError(t, err) {
		defer w.Remove()
	}

	cfg := w.LatestValue().(*config)
	cfg.Equals(t, &config{
		Foo: 99,
		Bar: "world",
	})

	_, err = c.KV().Put(&api.KVPair{
		Key:   "hello2",
		Value: []byte(`{"Foo": 108, "Bar": "haha"}`),
	}, &api.WriteOptions{})
	assert.NoError(t, err)

	<-cfg.OutdatedEvent()

	cfg = w.LatestValue().(*config)
	cfg.Equals(t, &config{
		Foo: 108,
		Bar: "haha",
	})

	_, err = c.KV().Put(&api.KVPair{
		Key:   "hello2",
		Value: []byte(`{"Foo": 233, "Bar": "bad json`),
	}, &api.WriteOptions{})
	assert.NoError(t, err)

	time.Sleep(1 * time.Second)
	select {
	case <-cfg.OutdatedEvent():
		assert.Fail(t, "unreachable")
	default:
	}

	cfg = w.LatestValue().(*config)
	cfg.Equals(t, &config{
		Foo: 108,
		Bar: "haha",
	})

	_, err = c.KV().Delete("hello2", &api.WriteOptions{})
	assert.NoError(t, err)

	time.Sleep(1 * time.Second)
	select {
	case <-cfg.OutdatedEvent():
		assert.Fail(t, "unreachable")
	default:
	}

	cfg = w.LatestValue().(*config)
	cfg.Equals(t, &config{
		Foo: 108,
		Bar: "haha",
	})

	_, err = c.KV().Put(&api.KVPair{
		Key:   "hello2",
		Value: []byte(`{"Foo": 666, "Bar": "sixsixsix"}`),
	}, &api.WriteOptions{})
	assert.NoError(t, err)

	<-cfg.OutdatedEvent()

	cfg = w.LatestValue().(*config)
	cfg.Equals(t, &config{
		Foo: 666,
		Bar: "sixsixsix",
	})
}

type config struct {
	Foo int
	Bar string

	outdatedEvent chan struct{}
}

func (c *config) Init() *config {
	c.outdatedEvent = make(chan struct{})
	return c
}

func (c *config) Unmarshal(data []byte) error {
	return json.Unmarshal(data, c)
}

func (c *config) String() string {
	data, _ := json.Marshal(c)
	return string(data)
}

func (c *config) OnOutdated() {
	close(c.outdatedEvent)
}

func (c *config) OutdatedEvent() <-chan struct{} {
	return c.outdatedEvent
}

func (c *config) Equals(t *testing.T, other *config) bool {
	f1 := assert.Equal(t, other.Foo, c.Foo)
	f2 := assert.Equal(t, other.Bar, c.Bar)
	return f1 && f2
}

func makeWatcher(t *testing.T) (*dynconf.Watcher, *api.Client) {
	client := makeClient(t)
	watcher := new(dynconf.Watcher).Init(client, makeLogger(t))
	return watcher, client
}

func makeClient(t *testing.T) *api.Client {
	client, err := api.NewClient(&api.Config{
		Scheme:  os.Getenv("TEST_CONSUL_SCHEME"),
		Address: os.Getenv("TEST_CONSUL_ADDRESS"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type tWritter struct {
	T *testing.T
}

func (tw tWritter) Write(p []byte) (int, error) {
	tw.T.Log(string(p[:len(p)-1]))
	return len(p), nil
}

func makeLogger(t *testing.T) *zerolog.Logger {
	logger := zerolog.New(tWritter{t})
	return &logger
}

func newValue() dynconf.Value {
	return new(config).Init()
}
