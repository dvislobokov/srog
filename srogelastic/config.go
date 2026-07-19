package srogelastic

import (
	"fmt"
	"io"
	"time"

	"github.com/dvislobokov/srog"
)

// init plugs the sink into srog's declarative config: importing this package
// (even blank: _ "github.com/dvislobokov/srog/srogelastic") enables
//
//	{"sinks": [{
//	    "type": "elasticsearch",
//	    "options": {
//	        "addresses":     ["http://es:9200"],
//	        "index":         "app-logs-%{2006.01.02}",
//	        "username":      "elastic",
//	        "password":      "secret",
//	        "apiKey":        "…",            // takes precedence over basic auth
//	        "dataStream":    false,
//	        "gzip":          true,
//	        "batchSize":     500,
//	        "flushInterval": "5s",            // Go duration string
//	        "queueSize":     10000,
//	        "maxRetries":    3,
//	        "timeout":       "30s"
//	    }
//	}]}
//
// Events default to ECS formatting (Kibana-ready); set the sink's "format"
// field to override. The logger's Close flushes and closes the sink.
func init() {
	srog.RegisterSinkType("elasticsearch", factory)
	srog.RegisterSinkType("elastic", factory)
}

// sinkOptions is the serializable form of Config for SinkSpec.Options.
// Durations are Go duration strings so the same value works from JSON and YAML.
type sinkOptions struct {
	Addresses     []string `json:"addresses"`
	Index         string   `json:"index"`
	DataStream    bool     `json:"dataStream"`
	Gzip          bool     `json:"gzip"`
	Username      string   `json:"username"`
	Password      string   `json:"password"`
	APIKey        string   `json:"apiKey"`
	BatchSize     int      `json:"batchSize"`
	FlushInterval string   `json:"flushInterval"`
	QueueSize     int      `json:"queueSize"`
	MaxRetries    int      `json:"maxRetries"`
	Timeout       string   `json:"timeout"`
}

func factory(_ srog.Config, spec srog.SinkSpec) (io.Writer, srog.Format, error) {
	var o sinkOptions
	if err := spec.DecodeOptions(&o); err != nil {
		return nil, 0, err
	}
	cfg := Config{
		Addresses:  o.Addresses,
		Index:      o.Index,
		DataStream: o.DataStream,
		Gzip:       o.Gzip,
		Username:   o.Username,
		Password:   o.Password,
		APIKey:     o.APIKey,
		BatchSize:  o.BatchSize,
		QueueSize:  o.QueueSize,
		MaxRetries: o.MaxRetries,
	}
	var err error
	if cfg.FlushInterval, err = parseDuration("flushInterval", o.FlushInterval); err != nil {
		return nil, 0, err
	}
	if cfg.Timeout, err = parseDuration("timeout", o.Timeout); err != nil {
		return nil, 0, err
	}
	sink, err := New(cfg)
	if err != nil {
		return nil, 0, err
	}
	// The sink implements io.Closer, so Logger.Close flushes and releases it.
	return sink, srog.FormatECS, nil
}

func parseDuration(field, s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("srogelastic: option %q: %w", field, err)
	}
	return d, nil
}
