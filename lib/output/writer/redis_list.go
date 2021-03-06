package writer

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/Jeffail/benthos/v3/lib/log"
	"github.com/Jeffail/benthos/v3/lib/metrics"
	"github.com/Jeffail/benthos/v3/lib/types"
	"github.com/go-redis/redis/v7"
)

//------------------------------------------------------------------------------

// RedisListConfig contains configuration fields for the RedisList output type.
type RedisListConfig struct {
	URL         string `json:"url" yaml:"url"`
	Key         string `json:"key" yaml:"key"`
	MaxInFlight int    `json:"max_in_flight" yaml:"max_in_flight"`
}

// NewRedisListConfig creates a new RedisListConfig with default values.
func NewRedisListConfig() RedisListConfig {
	return RedisListConfig{
		URL:         "tcp://localhost:6379",
		Key:         "benthos_list",
		MaxInFlight: 1,
	}
}

//------------------------------------------------------------------------------

// RedisList is an output type that serves RedisList messages.
type RedisList struct {
	log   log.Modular
	stats metrics.Type

	url  *url.URL
	conf RedisListConfig

	client  *redis.Client
	connMut sync.RWMutex
}

// NewRedisList creates a new RedisList output type.
func NewRedisList(
	conf RedisListConfig,
	log log.Modular,
	stats metrics.Type,
) (*RedisList, error) {

	r := &RedisList{
		log:   log,
		stats: stats,
		conf:  conf,
	}

	var err error
	r.url, err = url.Parse(conf.URL)
	if err != nil {
		return nil, err
	}

	return r, nil
}

//------------------------------------------------------------------------------

// ConnectWithContext establishes a connection to an RedisList server.
func (r *RedisList) ConnectWithContext(ctx context.Context) error {
	return r.Connect()
}

// Connect establishes a connection to an RedisList server.
func (r *RedisList) Connect() error {
	r.connMut.Lock()
	defer r.connMut.Unlock()

	var pass string
	if r.url.User != nil {
		pass, _ = r.url.User.Password()
	}

	// We default to Redis DB 0 for backward compatibilitiy, but if it's
	// specified in the URL, we'll use the specified one instead.
	var redisDB int
	if len(r.url.Path) > 1 {
		var err error
		// We'll strip the leading '/'
		redisDB, err = strconv.Atoi(r.url.Path[1:])
		if err != nil {
			return fmt.Errorf("invalid Redis DB, can't parse '%s'", r.url.Path)
		}
	}

	client := redis.NewClient(&redis.Options{
		Addr:     r.url.Host,
		Network:  r.url.Scheme,
		DB:       redisDB,
		Password: pass,
	})

	if _, err := client.Ping().Result(); err != nil {
		return err
	}

	r.log.Infof("Pushing messages to Redis list: %v\n", r.conf.Key)

	r.client = client
	return nil
}

//------------------------------------------------------------------------------

// WriteWithContext attempts to write a message by pushing it to the end of a
// Redis list.
func (r *RedisList) WriteWithContext(ctx context.Context, msg types.Message) error {
	return r.Write(msg)
}

// Write attempts to write a message by pushing it to the end of a Redis list.
func (r *RedisList) Write(msg types.Message) error {
	r.connMut.RLock()
	client := r.client
	r.connMut.RUnlock()

	if client == nil {
		return types.ErrNotConnected
	}

	return msg.Iter(func(i int, p types.Part) error {
		if err := client.RPush(r.conf.Key, p.Get()).Err(); err != nil {
			r.disconnect()
			r.log.Errorf("Error from redis: %v\n", err)
			return types.ErrNotConnected
		}
		return nil
	})
}

// disconnect safely closes a connection to an RedisList server.
func (r *RedisList) disconnect() error {
	r.connMut.Lock()
	defer r.connMut.Unlock()
	if r.client != nil {
		err := r.client.Close()
		r.client = nil
		return err
	}
	return nil
}

// CloseAsync shuts down the RedisList output and stops processing messages.
func (r *RedisList) CloseAsync() {
	go r.disconnect()
}

// WaitForClose blocks until the RedisList output has closed down.
func (r *RedisList) WaitForClose(timeout time.Duration) error {
	return nil
}

//------------------------------------------------------------------------------
