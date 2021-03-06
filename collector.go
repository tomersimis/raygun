package raygun

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"time"
)

type Collector interface {
	Capture(Ray)
	CaptureError(error, ...CaptureOption)
	CaptureMessage(string, ...CaptureOption)
	CapturePanic()
}

type RaygunCollector struct {
	AppName   string
	ApiKey    string
	Workers   int
	QueueSize int
	Logger    *log.Logger

	queue  chan Ray
	client *http.Client
	wg     sync.WaitGroup
}

type RaygunCollectorConfig func(*RaygunCollector)

func Workers(n int) RaygunCollectorConfig {
	return func(c *RaygunCollector) {
		c.Workers = n
	}
}

func QueueSize(n int) RaygunCollectorConfig {
	return func(c *RaygunCollector) {
		c.QueueSize = n
	}
}

func Logger(logger *log.Logger) RaygunCollectorConfig {
	return func(c *RaygunCollector) {
		c.Logger = logger
	}
}

func NewCollector(appName, apiKey string, options ...RaygunCollectorConfig) Collector {

	collector := &RaygunCollector{
		AppName:   appName,
		ApiKey:    apiKey,
		Workers:   1,
		QueueSize: 10000,
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:       10,
				IdleConnTimeout:    30 * time.Second,
				DisableCompression: true,
			},
			Timeout: 5 * time.Second,
		},
	}

	for _, f := range options {
		f(collector)
	}

	if collector.Logger == nil {
		collector.Logger = log.New(ioutil.Discard, "raygun", 0)
	}

	collector.queue = make(chan Ray, collector.QueueSize)
	collector.start()
	return collector
}

type CaptureOption func(*Ray)

func WithUser(id string) CaptureOption {
	return func(ray *Ray) {
		ray.Details.User.Identifier = id
	}
}

func WithTags(tags []string) CaptureOption {
	return func(ray *Ray) {
		ray.Details.Tags = tags
	}
}

func WithCustomData(data interface{}) CaptureOption {
	return func(ray *Ray) {
		ray.Details.UserCustomData = data
	}
}

func (c *RaygunCollector) CaptureMessage(msg string, opts ...CaptureOption) {
	ray := NewRay(msg)
	for _, f := range opts {
		f(&ray)
	}
	c.queue <- ray
	c.wg.Add(1)
}

func (c *RaygunCollector) CaptureError(err error, opts ...CaptureOption) {
	c.CaptureMessage(err.Error(), opts...)
}

func (c *RaygunCollector) CapturePanic() {
	if rec := recover(); rec != nil {
		if err, ok := rec.(error); ok {
			c.CaptureError(err)
		} else {
			c.CaptureMessage(rec.(string))
		}
	}
}

func (c *RaygunCollector) Capture(ray Ray) {
	c.queue <- ray
	c.wg.Add(1)
}

func (c *RaygunCollector) start() {
	for i := 0; i < c.Workers; i++ {
		go func() {
			for {
				ray := <-c.queue

				json, err := json.Marshal(ray)
				if err != nil {
					c.Logger.Printf("raygun: failed to marshal raygun error: %s", err.Error())
				}

				req, err := http.NewRequest("POST", RaygunEndpoint+"/entries", bytes.NewBuffer(json))
				if err != nil {
					c.Logger.Printf("raygun: failed to create error request: %s", err.Error())
				}
				req.Header.Add("X-ApiKey", c.ApiKey)
				res, err := c.client.Do(req)
				if err != nil {
					c.Logger.Printf("raygun: request failed: %s", err.Error())
				}

				if res != nil {
					res.Body.Close()
				}

				c.wg.Done()
			}
		}()
	}
}

func (c *RaygunCollector) Wait() {
	c.wg.Wait()
}

type NoopCollector struct{}

func (c *NoopCollector) CaptureMessage(_ string, _ ...CaptureOption) {}

func (c *NoopCollector) CaptureError(_ error, _ ...CaptureOption) {}

func (c *NoopCollector) CapturePanic() {}

func (c *NoopCollector) Capture(_ Ray) {}
